package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/render"
	"linkedin-jobs/internal/store"
)

var (
	listMinSalary        string
	listSalaryCurrency   string
	listCompany          string
	listTitle            string
	listLocation         string
	listRemote           bool
	listHybrid           bool
	listOnsite           bool
	listStatus           string
	listSource           string
	listHasEmail         bool
	listNoEmail          bool
	listLimit            int
	listMinScore         int
	listSortScore        bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved jobs from the local database",
	RunE: func(cmd *cobra.Command, args []string) error {
		if listHasEmail && listNoEmail {
			die("--has-email and --no-email cannot be used together")
		}
		st, err := openStore()
		if err != nil {
			die("failed to open DB: %v", err)
		}
		defer st.Close()
		minSal := parseMinSalary(listMinSalary)
		currency := validateSalaryCurrency(listSalaryCurrency)
		f := store.Filters{
			MinSalary:         minSal,
			MinSalaryCurrency: currency,
			Company:           listCompany,
			Title:             listTitle,
			Location:          listLocation,
			Remote:            listRemote,
			Hybrid:            listHybrid,
			Onsite:            listOnsite,
			Status:            listStatus,
			Source:            listSource,
			HasEmail:          listHasEmail,
			NoEmail:           listNoEmail,
			MinScore:          listMinScore,
			SortByScore:       listSortScore,
		}
		// FX-aware salary filtering can't be expressed in SQL: fetch a broader
		// pool (no limit) when a currency is set, then trim after the Go filter.
		if currency != "" && minSal > 0 {
			f.MinSalary = 0
			f.Limit = 0
		} else {
			f.Limit = listLimit
		}
		jobs, err := st.List(f)
		if err != nil {
			die("query failed: %v", err)
		}
		if currency != "" && minSal > 0 {
			jobs = filterByMinSalary(jobs, minSal, currency)
			if listLimit > 0 && len(jobs) > listLimit {
				jobs = jobs[:listLimit]
			}
		}
		if jsonOut {
			render.AsJSON(os.Stdout, jobs)
		} else {
			render.Table(os.Stdout, jobs)
		}
		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&listMinSalary, "min-salary", "", "filter by minimum salary (e.g. 200k)")
	listCmd.Flags().StringVar(&listSalaryCurrency, "salary-currency", "", "currency for --min-salary (ISO 4217, e.g. CAD); enables FX-aware filtering")
	listCmd.Flags().StringVar(&listCompany, "company", "", "filter by company name (substring)")
	listCmd.Flags().StringVar(&listTitle, "title", "", "filter by title (substring)")
	listCmd.Flags().StringVar(&listLocation, "location", "", "filter by location (substring)")
	listCmd.Flags().BoolVar(&listRemote, "remote", false, "only remote-friendly jobs")
	listCmd.Flags().BoolVar(&listHybrid, "hybrid", false, "only hybrid-friendly jobs (combine with --remote/--onsite for OR)")
	listCmd.Flags().BoolVar(&listOnsite, "onsite", false, "only on-site jobs (combine with --remote/--hybrid for OR)")
	listCmd.Flags().StringVar(&listStatus, "status", "", "filter by status (new/viewed/saved/applied/rejected/filtered)")
	listCmd.Flags().StringVar(&listSource, "source", "", "filter by source (recommended/search)")
	listCmd.Flags().BoolVar(&listHasEmail, "has-email", false, "only jobs with an explicit application email")
	listCmd.Flags().BoolVar(&listNoEmail, "no-email", false, "only jobs without an explicit application email")
	listCmd.Flags().IntVar(&listLimit, "limit", 50, "max results")
	listCmd.Flags().IntVar(&listMinScore, "min-score", 0, "only jobs with fit_score >= N")
	listCmd.Flags().BoolVar(&listSortScore, "sort-score", false, "sort by fit_score descending (default: salary)")
	rootCmd.AddCommand(listCmd)
}
