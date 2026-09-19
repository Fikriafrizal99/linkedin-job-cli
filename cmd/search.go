package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/linkedin"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

var (
	searchTop           int
	searchForceOW       bool
	searchLocation      string
	searchRemote        bool
	searchHybrid        bool
	searchOnsite        bool
	searchPostedWithin  string
)

var searchCmd = &cobra.Command{
	Use:   "search <keywords>",
	Short: "Search LinkedIn's public job board (anonymous, no session required)",
	Args:  cobra.MinimumNArgs(1),
	Long: `Searches LinkedIn's public (logged-out) job board and ingests results through
the same pipeline as 'recommended'. Works without a session; no login needed.

The first positional argument is the keyword search (e.g. "Senior Software
Engineer"). Use --location and --remote/--hybrid/--onsite to narrow results;
these are passed directly to LinkedIn as structured filters:

  --location <text>     Location filter; LinkedIn geocodes it to a region
                         (e.g. "Toronto" covers the GTA including Mississauga,
                         Markham, etc.). Omit for a global keywords-only search.
  --remote               Only remote-eligible roles (f_WT=2)
  --hybrid               Only hybrid roles (f_WT=3)
  --onsite               Only on-site roles (f_WT=1)
  Combine --remote/--hybrid/--onsite for OR (e.g. --remote --hybrid).
  --posted-within Nd     Only jobs posted in the last N days (f_TPR), e.g.
                         --posted-within 7d, --posted-within 30d, --posted-within 365d.

--top N caps the number of jobs processed end-to-end (detail fetch + LLM score).
Jobs already in the DB (by LinkedIn ID) are skipped entirely — only brand-new
jobs are detail-fetched, scored, and displayed. Pass --force-overwrite to
re-process existing jobs (bypasses the new-only pre-filter and content-hash
dedup; overwrites stored values). Every fetched job is persisted and scored;
no ingest-time filters — use 'list --remote --min-salary ...' or the 'serve'
filters to exclude at view time.

Examples:
  linkedin-jobs search "Senior Software Engineer" --location Toronto --remote
  linkedin-jobs search "Staff Engineer" --location "Mississauga, ON" --hybrid --top 50
  linkedin-jobs search "Backend Developer" --location "San Francisco" --remote --hybrid --posted-within 7d
  linkedin-jobs search "Go Engineer" --posted-within 30d
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := mustResolveProvider()
		keywords := strings.TrimSpace(strings.Join(args, " "))
		c, err := newClient(false)
		if err != nil {
			die("%v", err)
		}
		postedWithin, err := resolvePostedWithin(searchPostedWithin)
		if err != nil {
			die("%v", err)
		}
		fmt.Fprintf(os.Stderr, "Searching LinkedIn Jobs: %q", keywords)
		if searchLocation != "" {
			fmt.Fprintf(os.Stderr, " @ %q", searchLocation)
		}
		if wt := resolveWorkType(searchRemote, searchHybrid, searchOnsite); wt != "" {
			fmt.Fprintf(os.Stderr, " [%s]", workTypeLabel(searchRemote, searchHybrid, searchOnsite))
		}
		if searchPostedWithin != "" {
			fmt.Fprintf(os.Stderr, " [last %s]", searchPostedWithin)
		}
		fmt.Fprintln(os.Stderr, "…")
		jobs, err := c.Search(linkedin.SearchParams{
			Keywords:     keywords,
			Location:     searchLocation,
			WorkType:     resolveWorkType(searchRemote, searchHybrid, searchOnsite),
			PostedWithin: postedWithin,
			MaxJobs:      searchTop,
		})
		if err != nil {
			die("search failed: %v", err)
		}
		if searchTop > 0 && len(jobs) > searchTop {
			jobs = jobs[:searchTop]
		}

		// New-only pre-filter: drop jobs whose LinkedIn ID is already stored so
		// we skip detail-fetch, scoring, and display for jobs seen before. The
		// content-hash dedup inside ingest still guards against structural
		// duplicates that slip in under new IDs. --force-overwrite bypasses
		// this pre-filter (and the ingest dedup) to re-process everything.
		target := jobs
		if !searchForceOW {
			target = filterNewIDs(jobs)
		}

		ingest(target, provider, ingestOptions{
			forceOverwrite: searchForceOW,
			detailDelay:    resolveDetailDelay(),
			scoreDelay:     resolveLLMDelay(),
			jsonOut:        jsonOut,
		})
		return nil
	},
}

func init() {
	searchCmd.Flags().IntVar(&searchTop, "top", 20, "cap on number of jobs to fetch + process end-to-end (each is LLM-scored; raise to burn more tokens)")
	searchCmd.Flags().BoolVar(&searchForceOW, "force-overwrite", false, "re-parse and re-score jobs already in the DB (bypass the new-only pre-filter and dedup; overwrites existing values)")
	searchCmd.Flags().StringVar(&searchLocation, "location", "", "location filter (e.g. Toronto, \"Remote, Ontario, Canada\"); LinkedIn geocodes it server-side")
	searchCmd.Flags().BoolVar(&searchRemote, "remote", false, "only remote jobs (f_WT=2)")
	searchCmd.Flags().BoolVar(&searchHybrid, "hybrid", false, "only hybrid jobs (f_WT=3); combine with --remote/--onsite for OR")
	searchCmd.Flags().BoolVar(&searchOnsite, "onsite", false, "only on-site jobs (f_WT=1); combine with --remote/--hybrid for OR")
	searchCmd.Flags().StringVar(&searchPostedWithin, "posted-within", "", "only jobs posted in the last N days, e.g. --posted-within 7d (30d, 365d, …)")
	rootCmd.AddCommand(searchCmd)
}

// filterNewIDs returns only the jobs whose LinkedIn ID is not already stored.
// Used by 'search' to skip existing jobs entirely — no detail fetch, no score,
// no display — so re-running a query shows only what's new since the last run.
func filterNewIDs(jobs []*models.JobPosting) []*models.JobPosting {
	st, err := openStore()
	if err != nil {
		die("failed to open DB: %v", err)
	}
	defer st.Close()
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
	}
	existing, err := st.ExistingIDs(ids)
	if err != nil {
		die("lookup failed: %v", err)
	}
	var fresh []*models.JobPosting
	seenAt := store.NowISO()
	for _, j := range jobs {
		if existing[j.ID] {
			if err := st.TouchSeen(j.ID, seenAt); err != nil {
				fmt.Fprintf(os.Stderr, "  ! touch %s: %v\n", j.ID, err)
			}
			continue
		}
		fresh = append(fresh, j)
	}
	fmt.Fprintf(os.Stderr, "Found %d jobs, %d new since last run.\n", len(jobs), len(fresh))
	return fresh
}

// resolveWorkType maps the boolean work-arrangement flags to the LinkedIn f_WT
// query parameter value. Multiple flags combine into a comma-separated OR list
// (e.g. --remote --hybrid → "2,3"). Returns "" when no flag is set.
func resolveWorkType(remote, hybrid, onsite bool) string {
	var parts []string
	if onsite {
		parts = append(parts, "1")
	}
	if remote {
		parts = append(parts, "2")
	}
	if hybrid {
		parts = append(parts, "3")
	}
	return strings.Join(parts, ",")
}

// resolvePostedWithin maps a "--posted-within Nd" flag value to LinkedIn's
// f_TPR query parameter value. Accepts only the form "<N>d" (days), e.g.
// "1d", "7d", "30d", "365d"; any other shape is rejected with an error so the
// user gets a clear message instead of a silent no-op. Returns "" when the flag
// is empty (filter disabled). LinkedIn encodes "past N seconds" as "r<secs>".
func resolvePostedWithin(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) < 2 || s[len(s)-1] != 'd' {
		return "", fmt.Errorf(`--posted-within must be "<N>d" (e.g. 7d, 30d), got %q`, s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return "", fmt.Errorf(`--posted-within must be a positive number of days (e.g. 7d), got %q`, s)
	}
	return "r" + strconv.Itoa(n*86400), nil
}

// workTypeLabel produces a human-readable label for the progress message.
func workTypeLabel(remote, hybrid, onsite bool) string {
	var parts []string
	if onsite {
		parts = append(parts, "onsite")
	}
	if remote {
		parts = append(parts, "remote")
	}
	if hybrid {
		parts = append(parts, "hybrid")
	}
	return strings.Join(parts, "/")
}
