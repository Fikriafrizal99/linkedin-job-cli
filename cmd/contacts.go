package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/hr"
	"linkedin-jobs/internal/linkedin"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/render"
	"linkedin-jobs/internal/store"
)

var (
	contactsAll         bool
	contactsUnknownOnly bool
	contactsLimit       int
	contactsDelay       float64
)

var contactsCmd = &cobra.Command{
	Use:   "contacts",
	Short: "Optional HR/hiring contact enrichment for collected jobs",
	Long:  "Research and persist role-level hiring contacts. This command never sends outreach or applications.",
}

var contactsEnrichCmd = &cobra.Command{
	Use:   "enrich [job_id]",
	Short: "Enrich one stored job or a bounded batch with hiring-contact targets",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && !contactsAll {
			return fmt.Errorf("provide a job_id or use --all")
		}
		if len(args) == 1 && contactsAll {
			return fmt.Errorf("job_id and --all are mutually exclusive")
		}
		if contactsLimit < 1 {
			return fmt.Errorf("--limit must be at least 1")
		}
		if contactsDelay < 0 {
			return fmt.Errorf("--delay must be zero or greater")
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
				if contactsUnknownOnly && j.ApplicationMethod != "UNKNOWN" {
					continue
				}
				jobs = append(jobs, j)
				if len(jobs) >= contactsLimit {
					break
				}
			}
		}
		if len(jobs) == 0 {
			fmt.Fprintln(os.Stderr, "No jobs matched contact-enrichment filters.")
			return nil
		}

		client, err := newClient(false)
		if err != nil {
			return err
		}

		enriched := 0
		failed := 0
		for i, j := range jobs {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s @ %s\n", i+1, len(jobs), j.Title, j.Company)
			jobRef := j.URL
			if j.ID != "" {
				jobRef = "https://www.linkedin.com/jobs/view/" + j.ID + "/"
			}
			ctx, err := client.FetchJobContext(jobRef)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ! context: %v\n", err)
				failed++
				continue
			}

			var co *linkedin.CompanyProfile
			if ctx.CompanySlug != "" {
				if p, err := client.FetchCompanyProfile(ctx.CompanySlug); err == nil {
					co = p
				}
			}

			contacts := hr.CollectorContacts(ctx, co)
			if err := st.ReplaceJobContacts(j.ID, contacts); err != nil {
				fmt.Fprintf(os.Stderr, "  ! persist: %v\n", err)
				failed++
				continue
			}
			enriched++
			fmt.Fprintf(os.Stderr, "  stored %d contact target(s)\n", len(contacts))

			if i < len(jobs)-1 && contactsDelay > 0 {
				time.Sleep(time.Duration(contactsDelay * float64(time.Second)))
			}
		}

		fmt.Fprintf(os.Stderr, "Contact enrichment complete: %d enriched, %d failed.\n", enriched, failed)
		return nil
	},
}

var contactsListCmd = &cobra.Command{
	Use:   "list <job_id>",
	Short: "List persisted contact enrichment for a job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		contacts, err := st.ListJobContacts(args[0])
		if err != nil {
			return err
		}
		if jsonOut {
			return render.AsJSON(os.Stdout, contacts)
		}
		if len(contacts) == 0 {
			fmt.Fprintln(os.Stdout, "No contact enrichment stored for this job.")
			return nil
		}
		for _, c := range contacts {
			fmt.Fprintf(os.Stdout, "%d. [%s] %s\n", c.Priority, c.ContactType, c.Title)
			if c.Name != "" {
				fmt.Fprintf(os.Stdout, "   Name:   %s\n", c.Name)
			}
			if c.Why != "" {
				fmt.Fprintf(os.Stdout, "   Why:    %s\n", c.Why)
			}
			if c.LinkedInURL != "" {
				fmt.Fprintf(os.Stdout, "   Profile:%s\n", " "+c.LinkedInURL)
			}
			if c.SearchURL != "" {
				fmt.Fprintf(os.Stdout, "   Search: %s\n", c.SearchURL)
			}
		}
		return nil
	},
}

func init() {
	contactsEnrichCmd.Flags().BoolVar(&contactsAll, "all", false, "enrich a bounded batch of stored jobs")
	contactsEnrichCmd.Flags().BoolVar(&contactsUnknownOnly, "unknown-only", false, "with --all, only enrich jobs whose application method is UNKNOWN")
	contactsEnrichCmd.Flags().IntVar(&contactsLimit, "limit", 20, "maximum jobs in a batch enrichment run")
	contactsEnrichCmd.Flags().Float64Var(&contactsDelay, "delay", 0.8, "seconds to wait between batch jobs")

	contactsCmd.AddCommand(contactsEnrichCmd)
	contactsCmd.AddCommand(contactsListCmd)
	rootCmd.AddCommand(contactsCmd)
}
