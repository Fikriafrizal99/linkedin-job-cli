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
	collectLocation     string
	collectRemote       bool
	collectHybrid       bool
	collectOnsite       bool
	collectPostedWithin string
	collectWithSession  bool
)

var collectCmd = &cobra.Command{
	Use:   "collect <keywords>",
	Short: "Collect LinkedIn jobs into SQLite without LLM scoring",
	Args:  cobra.MinimumNArgs(1),
	Long: `Collect searches LinkedIn's public job board, fetches full job details,
and persists new jobs to SQLite without requiring an LLM provider.

By default collection is anonymous. Use --with-session only when you explicitly
want the existing LinkedIn session to be available as a detail fallback.

Examples:
  linkedin-jobs collect "Sales Executive" --location Indonesia --posted-within 7d
  linkedin-jobs collect "Relationship Manager" --location "West Java" --top 50
  linkedin-jobs collect "SAP ABAP" --location Indonesia --with-session
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if collectTop < 0 {
			return fmt.Errorf("--top must be 0 or greater")
		}

		keywords := strings.TrimSpace(strings.Join(args, " "))
		fmt.Fprintf(os.Stderr, "Collecting LinkedIn Jobs: %q", keywords)
		if collectLocation != "" {
			fmt.Fprintf(os.Stderr, " @ %q", collectLocation)
		}
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

		result, err := runCollect(collectRequest{
			Keywords:       keywords,
			Location:       collectLocation,
			PostedWithin:   collectPostedWithin,
			Top:            collectTop,
			Remote:         collectRemote,
			Hybrid:         collectHybrid,
			Onsite:         collectOnsite,
			ForceOverwrite: collectForceOW,
			WithSession:    collectWithSession,
		}, st, func(stage string, done, total int) {
			if stage != "details" {
				return
			}
			if done == 0 {
				fmt.Fprintf(os.Stderr, "Fetching details for %d new job(s)…\n", total)
				return
			}
			fmt.Fprintf(os.Stderr, "\r  %d/%d", done, total)
			if done == total {
				fmt.Fprintln(os.Stderr)
			}
		})
		if err != nil {
			return err
		}

		if result.NewCandidates == 0 {
			fmt.Fprintln(os.Stderr, "No new jobs to collect.")
		} else {
			fmt.Fprintf(os.Stderr, "Collected %d job(s) into SQLite", result.Persisted)
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
	collectCmd.Flags().IntVar(&collectTop, "top", 50, "maximum unique jobs to collect; 0 means no explicit cap")
	collectCmd.Flags().BoolVar(&collectForceOW, "force-overwrite", false, "re-fetch and overwrite jobs already present by LinkedIn ID")
	collectCmd.Flags().StringVar(&collectLocation, "location", "", "LinkedIn location filter, e.g. Indonesia or West Java")
	collectCmd.Flags().BoolVar(&collectRemote, "remote", false, "only remote jobs (f_WT=2)")
	collectCmd.Flags().BoolVar(&collectHybrid, "hybrid", false, "only hybrid jobs (f_WT=3)")
	collectCmd.Flags().BoolVar(&collectOnsite, "onsite", false, "only on-site jobs (f_WT=1)")
	collectCmd.Flags().StringVar(&collectPostedWithin, "posted-within", "", "only jobs posted in the last N days, e.g. 7d")
	collectCmd.Flags().BoolVar(&collectWithSession, "with-session", false, "attach an existing LinkedIn session for detail fallback")
	rootCmd.AddCommand(collectCmd)
}
