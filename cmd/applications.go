package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	appengine "linkedin-jobs/internal/application"
	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/render"
	"linkedin-jobs/internal/store"
)

var (
	applicationsAll           bool
	applicationsLimit         int
	applicationsIncludeReview bool
	applicationsState         string
	applicationsPrepareAll    bool
	applicationsCVProfile     string
	applicationsDraftID       string
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

var applicationsPrepareCmd = &cobra.Command{
	Use:   "prepare [job_id]",
	Short: "Select a CV profile and generate deterministic email subject/body",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && !applicationsPrepareAll {
			return fmt.Errorf("provide a job_id or use --all")
		}
		if len(args) == 1 && applicationsPrepareAll {
			return fmt.Errorf("job_id and --all are mutually exclusive")
		}
		if applicationsLimit < 1 {
			return fmt.Errorf("--limit must be at least 1")
		}

		settings, err := config.LoadSettings()
		if err != nil {
			return fmt.Errorf("load settings: %w", err)
		}
		st, err := openStore()
		if err != nil {
			return fmt.Errorf("open DB: %w", err)
		}
		defer st.Close()

		var targets []models.JobApplication
		if len(args) == 1 {
			a, err := st.GetApplicationByJobID(args[0])
			if err != nil {
				return err
			}
			if a == nil {
				return fmt.Errorf("job %s is not in the application queue; run applications queue %s first", args[0], args[0])
			}
			targets = []models.JobApplication{*a}
		} else {
			targets, err = st.ListApplications(models.ApplicationStateReadyEmail, applicationsLimit)
			if err != nil {
				return err
			}
		}

		if len(targets) == 0 {
			fmt.Fprintln(os.Stdout, "No READY_EMAIL application records found.")
			return nil
		}

		var preparedApps []models.JobApplication
		for _, target := range targets {
			if target.State != models.ApplicationStateReadyEmail && target.State != models.ApplicationStateNeedReview {
				fmt.Fprintf(os.Stderr, "! %s: state %s is not preparable\n", target.JobID, target.State)
				continue
			}
			job, err := st.Get(target.JobID)
			if err != nil || job == nil {
				fmt.Fprintf(os.Stderr, "! %s: collector job not found\n", target.JobID)
				continue
			}

			prepared, err := appengine.Prepare(job, settings.Application, applicationsCVProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "! %s: %v\n", target.JobID, err)
				continue
			}
			a, err := st.SaveApplicationPreparation(
				target.JobID,
				prepared.Subject,
				prepared.Body,
				prepared.CVProfile,
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "! %s: %v\n", target.JobID, err)
				continue
			}
			preparedApps = append(preparedApps, *a)

			if !jsonOut {
				fmt.Fprintf(os.Stdout, "PREPARED      %s  %s @ %s\n", target.JobID, job.Title, job.Company)
				fmt.Fprintf(os.Stdout, "  To:      %s\n", a.Recipient)
				fmt.Fprintf(os.Stdout, "  Subject: %s\n", a.Subject)
				if prepared.CVProfile != "" {
					fmt.Fprintf(os.Stdout, "  CV:      %s", prepared.CVProfile)
					if prepared.CVPath != "" {
						fmt.Fprintf(os.Stdout, " (%s)", prepared.CVPath)
					}
					fmt.Fprintln(os.Stdout)
				} else {
					fmt.Fprintln(os.Stdout, "  CV:      not configured")
				}
			}
		}

		if jsonOut {
			return render.AsJSON(os.Stdout, preparedApps)
		}
		if strings.TrimSpace(settings.Application.CandidateName) == "" {
			fmt.Fprintln(os.Stderr, "Note: application.candidate_name is empty; generated email body has no candidate name.")
		}
		fmt.Fprintf(os.Stdout, "Prepared %d application(s). No email was sent.\n", len(preparedApps))
		return nil
	},
}

var applicationsProfilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "Show configured Application Engine CV profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := config.LoadSettings()
		if err != nil {
			return err
		}
		if jsonOut {
			return render.AsJSON(os.Stdout, settings.Application)
		}
		if strings.TrimSpace(settings.Application.CandidateName) == "" {
			fmt.Fprintln(os.Stdout, "Candidate name: (not configured)")
		} else {
			fmt.Fprintf(os.Stdout, "Candidate name: %s\n", settings.Application.CandidateName)
		}
		if strings.TrimSpace(settings.Application.DefaultCVProfile) == "" {
			fmt.Fprintln(os.Stdout, "Default CV:     (not configured)")
		} else {
			fmt.Fprintf(os.Stdout, "Default CV:     %s\n", settings.Application.DefaultCVProfile)
		}
		if len(settings.Application.CVProfiles) == 0 {
			fmt.Fprintf(os.Stdout, "\nNo CV profiles configured. Edit %s and add application.cv_profiles.\n", config.SettingsPath())
			return nil
		}
		fmt.Fprintln(os.Stdout, "\nCV profiles:")
		for _, p := range settings.Application.CVProfiles {
			fmt.Fprintf(os.Stdout, "- %s", p.ID)
			if p.Path != "" {
				fmt.Fprintf(os.Stdout, " -> %s", p.Path)
			}
			if len(p.Keywords) > 0 {
				fmt.Fprintf(os.Stdout, " | keywords: %s", strings.Join(p.Keywords, ", "))
			}
			fmt.Fprintln(os.Stdout)
		}
		return nil
	},
}

var applicationsDraftPayloadCmd = &cobra.Command{
	Use:   "draft-payload <job_id>",
	Short: "Validate and emit the Gmail draft payload for a prepared application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		settings, err := config.LoadSettings()
		if err != nil {
			return fmt.Errorf("load settings: %w", err)
		}
		st, err := openStore()
		if err != nil {
			return fmt.Errorf("open DB: %w", err)
		}
		defer st.Close()

		a, err := st.GetApplicationByJobID(args[0])
		if err != nil {
			return err
		}
		if a == nil {
			return fmt.Errorf("no application record for job %s", args[0])
		}
		payload, err := appengine.BuildDraftPayload(a, settings.Application)
		if err != nil {
			return err
		}

		if jsonOut {
			return render.AsJSON(os.Stdout, payload)
		}
		fmt.Fprintf(os.Stdout, "Gmail draft payload ready for job %s\n", payload.JobID)
		fmt.Fprintf(os.Stdout, "To:      %s\n", payload.To)
		fmt.Fprintf(os.Stdout, "Subject: %s\n", payload.Subject)
		fmt.Fprintf(os.Stdout, "CV:      %s\n", payload.CVProfile)
		for _, path := range payload.AttachmentFiles {
			fmt.Fprintf(os.Stdout, "Attach:  %s\n", path)
		}
		fmt.Fprintln(os.Stdout, "\nBody:")
		fmt.Fprintln(os.Stdout, payload.Body)
		fmt.Fprintln(os.Stdout, "\nNo Gmail draft was created by this command.")
		return nil
	},
}

var applicationsRecordDraftCmd = &cobra.Command{
	Use:   "record-draft <job_id>",
	Short: "Record a successfully created Gmail draft and advance lifecycle state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(applicationsDraftID) == "" {
			return fmt.Errorf("--draft-id is required")
		}
		st, err := openStore()
		if err != nil {
			return fmt.Errorf("open DB: %w", err)
		}
		defer st.Close()

		a, err := st.MarkApplicationDraftCreated(args[0], applicationsDraftID)
		if err != nil {
			return err
		}
		if jsonOut {
			return render.AsJSON(os.Stdout, a)
		}
		fmt.Fprintf(os.Stdout, "DRAFT_CREATED  %s  Gmail draft %s\n", a.JobID, a.GmailDraftID)
		fmt.Fprintln(os.Stdout, "Draft recorded. No email was sent.")
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
		if a.Body != "" {
			fmt.Fprintf(os.Stdout, "\nEmail body:\n%s\n", a.Body)
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

	applicationsPrepareCmd.Flags().BoolVar(&applicationsPrepareAll, "all", false, "prepare a bounded batch of READY_EMAIL applications")
	applicationsPrepareCmd.Flags().IntVar(&applicationsLimit, "limit", 50, "maximum application records to prepare")
	applicationsPrepareCmd.Flags().StringVar(&applicationsCVProfile, "cv-profile", "", "override deterministic CV selection with a configured profile id")

	applicationsRecordDraftCmd.Flags().StringVar(&applicationsDraftID, "draft-id", "", "Gmail draft id returned by the draft provider")

	applicationsListCmd.Flags().IntVar(&applicationsLimit, "limit", 50, "maximum application records to list")
	applicationsListCmd.Flags().StringVar(&applicationsState, "state", "", "filter by lifecycle state, e.g. READY_EMAIL")

	applicationsCmd.AddCommand(applicationsQueueCmd)
	applicationsCmd.AddCommand(applicationsPrepareCmd)
	applicationsCmd.AddCommand(applicationsProfilesCmd)
	applicationsCmd.AddCommand(applicationsDraftPayloadCmd)
	applicationsCmd.AddCommand(applicationsRecordDraftCmd)
	applicationsCmd.AddCommand(applicationsListCmd)
	applicationsCmd.AddCommand(applicationsShowCmd)
	rootCmd.AddCommand(applicationsCmd)
}
