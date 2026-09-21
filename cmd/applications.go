package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/render"
	"linkedin-jobs/internal/store"
)

var (
	applicationsAll           bool
	applicationsLimit         int
	applicationsIncludeReview bool
	applicationsState         string
)

var applicationsCmd = &cobra.Command{
	Use:   "applications",
	Short: "Application Engine queue and lifecycle commands",
	Long:  "Manage application execution separately from job collection. These commands do not send email or submit applications.",
}

var applicationsQueueCmd = &cobra.Command{
	Use:   "queue [job_id]",
	Short: "Queue one job or a bounded batch for application processing",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && !applicationsAll {
			return fmt.Errorf("provide a job_id or use --all")
		}
		if len(args) == 1 && applicationsAll {
			return fmt.Errorf("job_id and --all are mutually exclusive")
		}
		if applicationsLimit < 1 {
			return fmt.Errorf("--limit must be at least 1")
		}

		st, err := openStore()
		if err != nil {
			return fmt.Errorf("open DB: %w", err)
		}
		defer st.Close()

		var jobs []*models.JobPosting
		if len(args) == 1 {
			j, err := st.Get(args[0])
			if err != nil {
				return err
			}
			if j == nil {
				return fmt.Errorf("job %s not found", args[0])
			}
			jobs = []*models.JobPosting{j}
		} else {
			all, err := st.List(store.Filters{SortBySearched: true})
			if err != nil {
				return err
			}
			for _, j := range all {
				readyEmail := strings.EqualFold(strings.TrimSpace(j.ApplicationMethod), "EMAIL") &&
					strings.TrimSpace(j.ApplyEmail) != ""
				if !readyEmail && !applicationsIncludeReview {
					continue
				}
				jobs = append(jobs, j)
				if len(jobs) >= applicationsLimit {
					break
				}
			}
		}

		if len(jobs) == 0 {
			fmt.Fprintln(os.Stdout, "No jobs matched application-queue filters.")
			return nil
		}

		queued := 0
		ready := 0
		review := 0
		var queuedApps []models.JobApplication
		for _, j := range jobs {
			a, err := st.QueueApplication(j.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "! %s: %v\n", j.ID, err)
				continue
			}
			queued++
			queuedApps = append(queuedApps, *a)
			switch a.State {
			case models.ApplicationStateReadyEmail:
				ready++
			case models.ApplicationStateNeedReview:
				review++
			}
			if !jsonOut {
				fmt.Fprintf(os.Stdout, "%s  %-12s  %s @ %s", a.State, j.ID, j.Title, j.Company)
				if a.Recipient != "" {
					fmt.Fprintf(os.Stdout, " -> %s", a.Recipient)
				}
				fmt.Fprintln(os.Stdout)
			}
		}
		if jsonOut {
			return render.AsJSON(os.Stdout, queuedApps)
		}
		fmt.Fprintf(os.Stdout, "Queued %d application(s): %d ready email, %d need review.\n", queued, ready, review)
		return nil
	},
}

var applicationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List application queue records",
	RunE: func(cmd *cobra.Command, args []string) error {
		if applicationsLimit < 1 {
			return fmt.Errorf("--limit must be at least 1")
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		apps, err := st.ListApplications(strings.ToUpper(strings.TrimSpace(applicationsState)), applicationsLimit)
		if err != nil {
			return err
		}
		if jsonOut {
			return render.AsJSON(os.Stdout, apps)
		}
		if len(apps) == 0 {
			fmt.Fprintln(os.Stdout, "No application records found.")
			return nil
		}
		for _, a := range apps {
			j, _ := st.Get(a.JobID)
			title, company := "", ""
			if j != nil {
				title, company = j.Title, j.Company
			}
			fmt.Fprintf(os.Stdout, "%-13s  %s  %s @ %s", a.State, a.JobID, title, company)
			if a.Recipient != "" {
				fmt.Fprintf(os.Stdout, " -> %s", a.Recipient)
			}
			if a.GmailDraftID != "" {
				fmt.Fprintf(os.Stdout, " [draft:%s]", a.GmailDraftID)
			}
			fmt.Fprintln(os.Stdout)
		}
		return nil
	},
}

var applicationsShowCmd = &cobra.Command{
	Use:   "show <job_id>",
	Short: "Show one application lifecycle record",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		a, err := st.GetApplicationByJobID(args[0])
		if err != nil {
			return err
		}
		if a == nil {
			return fmt.Errorf("no application record for job %s", args[0])
		}
		if jsonOut {
			return render.AsJSON(os.Stdout, a)
		}

		j, _ := st.Get(a.JobID)
		if j != nil {
			fmt.Fprintf(os.Stdout, "%s @ %s\n", j.Title, j.Company)
		}
		fmt.Fprintf(os.Stdout, "Job ID:      %s\n", a.JobID)
		fmt.Fprintf(os.Stdout, "State:       %s\n", a.State)
		if a.Recipient != "" {
			fmt.Fprintf(os.Stdout, "Recipient:   %s\n", a.Recipient)
		}
		if a.Subject != "" {
			fmt.Fprintf(os.Stdout, "Subject:     %s\n", a.Subject)
		}
		if a.CVProfile != "" {
			fmt.Fprintf(os.Stdout, "CV profile:  %s\n", a.CVProfile)
		}
		if a.GmailDraftID != "" {
			fmt.Fprintf(os.Stdout, "Gmail draft: %s\n", a.GmailDraftID)
		}
		if a.LastError != "" {
			fmt.Fprintf(os.Stdout, "Last error:  %s\n", a.LastError)
		}
		fmt.Fprintf(os.Stdout, "Created:     %s\n", a.CreatedAt)
		fmt.Fprintf(os.Stdout, "Updated:     %s\n", a.UpdatedAt)
		return nil
	},
}

func init() {
	applicationsQueueCmd.Flags().BoolVar(&applicationsAll, "all", false, "queue a bounded batch of stored jobs")
	applicationsQueueCmd.Flags().IntVar(&applicationsLimit, "limit", 50, "maximum application records to queue/list")
	applicationsQueueCmd.Flags().BoolVar(&applicationsIncludeReview, "include-review", false, "with --all, also queue non-email jobs as NEED_REVIEW")

	applicationsListCmd.Flags().IntVar(&applicationsLimit, "limit", 50, "maximum application records to list")
	applicationsListCmd.Flags().StringVar(&applicationsState, "state", "", "filter by lifecycle state, e.g. READY_EMAIL")

	applicationsCmd.AddCommand(applicationsQueueCmd)
	applicationsCmd.AddCommand(applicationsListCmd)
	applicationsCmd.AddCommand(applicationsShowCmd)
	rootCmd.AddCommand(applicationsCmd)
}
