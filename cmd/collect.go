package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/render"
)

var (
	collectTop          int
	collectForceOW      bool
	collectQueries      []string
	collectLocations    []string
	collectRemote       bool
	collectHybrid       bool
	collectOnsite       bool
	collectPostedWithin string
	collectWithSession  bool
)

var collectCmd = &cobra.Command{
	Use:   "collect [keywords]",
	Short: "Collect LinkedIn jobs into SQLite without LLM scoring",
	Long: `Collect searches LinkedIn's public job board, fetches full job details,
and persists new jobs to SQLite without requiring an LLM provider.

A positional keyword phrase remains supported for backward compatibility.
Repeat --query and --location for multi-query / multi-location collection.
Every query is combined with every location. --top applies per search
combination. Duplicate LinkedIn job IDs are skipped by the existing store logic.

By default collection is anonymous. Use --with-session only when you explicitly
want the existing LinkedIn session to be available as a detail fallback.

Examples:
  linkedin-jobs collect "Sales Executive" --location Indonesia --posted-within 7d
  linkedin-jobs collect --query "Sales Operations" --query "Business Development" --location Indonesia --top 30
  linkedin-jobs collect --query "Account Executive" --location Jakarta --location Bandung --posted-within 7d
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if collectTop < 0 {
			return fmt.Errorf("--top must be 0 or greater")
		}

		queryValues := append([]string(nil), collectQueries...)
		if positional := strings.TrimSpace(strings.Join(args, " ")); positional != "" {
			queryValues = append([]string{positional}, queryValues...)
		}
		base := collectRequest{
			PostedWithin:   collectPostedWithin,
			Top:            collectTop,
			Remote:         collectRemote,
			Hybrid:         collectHybrid,
			Onsite:         collectOnsite,
			ForceOverwrite: collectForceOW,
			WithSession:    collectWithSession,
		}
		plan, err := buildCollectPlan(base, queryValues, collectLocations)
		if err != nil {
			return err
		}

		locationCount := len(plan.Locations)
		locationLabel := fmt.Sprintf("%d location(s)", locationCount)
		if locationCount == 1 && strings.TrimSpace(plan.Locations[0]) == "" {
			locationLabel = "all locations"
		}
		fmt.Fprintf(os.Stderr, "Collecting %d query(s) across %s = %d search combination(s)", len(plan.Queries), locationLabel, len(plan.Requests))
		if wt := resolveWorkType(collectRemote, collectHybrid, collectOnsite); wt != "" {
			fmt.Fprintf(os.Stderr, " [%s]", workTypeLabel(collectRemote, collectHybrid, collectOnsite))
		}
		if collectPostedWithin != "" {
			fmt.Fprintf(os.Stderr, " [last %s]", collectPostedWithin)
		}
		fmt.Fprintln(os.Stderr, "…")

		st, err := openStore()
		if err != nil {
			return fmt.Errorf("failed to open DB: %w", err)
		}
		defer st.Close()

		lastBatchDone := -1
		result, err := runCollectBatch(plan, st, func(stage string, done, total int) {
			switch stage {
			case "batch":
				if done < total && done != lastBatchDone {
					fmt.Fprintf(os.Stderr, "Search combination %d/%d…\n", done+1, total)
					lastBatchDone = done
				}
			case "details":
				if done == 0 {
					fmt.Fprintf(os.Stderr, "  Fetching details for %d new job(s)…\n", total)
					return
				}
				fmt.Fprintf(os.Stderr, "\r    %d/%d", done, total)
				if done == total {
					fmt.Fprintln(os.Stderr)
				}
			}
		})
		if err != nil {
			return err
		}

		if result.NewCandidates == 0 {
			fmt.Fprintf(os.Stderr, "No new jobs to collect across %d search(es).\n", result.SearchRuns)
		} else {
			fmt.Fprintf(os.Stderr, "Collected %d job(s) into SQLite across %d search(es)", result.Persisted, result.SearchRuns)
			if result.ExactDuplicates > 0 || result.LikelyReposts > 0 {
				fmt.Fprintf(os.Stderr, " [%d exact duplicate(s), %d likely repost(s)]", result.ExactDuplicates, result.LikelyReposts)
			}
			fmt.Fprintln(os.Stderr, ".")
		}

		if jsonOut {
			if err := render.AsJSON(os.Stdout, result.Jobs); err != nil {
				return fmt.Errorf("json output failed: %w", err)
			}
		} else {
			render.Table(os.Stdout, result.Jobs)
		}
		return nil
	},
}

func init() {
	collectCmd.Flags().IntVar(&collectTop, "top", 50, "maximum LinkedIn results per query/location search; 0 means no explicit cap")
	collectCmd.Flags().BoolVar(&collectForceOW, "force-overwrite", false, "re-fetch and overwrite jobs already present by LinkedIn ID")
	collectCmd.Flags().StringArrayVar(&collectQueries, "query", nil, "search query/job title; repeat for multiple queries")
	collectCmd.Flags().StringArrayVar(&collectLocations, "location", nil, "LinkedIn location filter; repeat for multiple locations")
	collectCmd.Flags().BoolVar(&collectRemote, "remote", false, "only remote jobs (f_WT=2)")
	collectCmd.Flags().BoolVar(&collectHybrid, "hybrid", false, "only hybrid jobs (f_WT=3)")
	collectCmd.Flags().BoolVar(&collectOnsite, "onsite", false, "only on-site jobs (f_WT=1)")
	collectCmd.Flags().StringVar(&collectPostedWithin, "posted-within", "", "only jobs posted in the last N days, e.g. 7d")
	collectCmd.Flags().BoolVar(&collectWithSession, "with-session", false, "attach an existing LinkedIn session for detail fallback")
	rootCmd.AddCommand(collectCmd)
}
