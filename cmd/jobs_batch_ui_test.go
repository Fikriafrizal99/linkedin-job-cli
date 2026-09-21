package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"linkedin-jobs/internal/application"
	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/gmailclient"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

func TestProcessJobsToDraftQueuesPreparesAndBuildsReviewQueue(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs-batch.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()

	cv := filepath.Join(t.TempDir(), "CV.pdf")
	if err := os.WriteFile(cv, []byte("pdf"), 0o600); err != nil { t.Fatal(err) }
	settings := config.ApplicationSettings{
		CandidateName: "Candidate",
		DefaultCVProfile: "general",
		CVProfiles: []config.CVProfileSettings{{ID: "general", Path: cv, Priority: 1}},
	}

	emailJob := &models.JobPosting{
		ID: "job-email", Title: "Sales Executive", Company: "Example Co",
		URL: "https://www.linkedin.com/jobs/view/job-email",
		ApplicationMethod: "EMAIL", ApplyEmail: "jobs@example.com",
	}
	reviewJob := &models.JobPosting{
		ID: "job-review", Title: "Account Executive", Company: "Review Co",
		URL: "https://www.linkedin.com/jobs/view/job-review",
		ApplicationMethod: "UNKNOWN",
	}
	for _, job := range []*models.JobPosting{emailJob, reviewJob} {
		if err := st.Upsert(job); err != nil { t.Fatalf("Upsert %s: %v", job.ID, err) }
	}

	var payloads []application.DraftPayload
	creator := func(_ context.Context, payload application.DraftPayload) (gmailclient.DraftResult, error) {
		payloads = append(payloads, payload)
		return gmailclient.DraftResult{DraftID: "draft-" + payload.JobID}, nil
	}

	got := processJobsToDraft(context.Background(), st, settings, nil, []string{emailJob.ID, reviewJob.ID}, creator)
	if got.Selected != 2 || got.Queued != 2 || got.Prepared != 1 || got.DraftCreated != 1 || got.NeedReview != 1 || got.Failed != 0 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.ReviewIDs) != 1 || got.ReviewIDs[0] != emailJob.ID {
		t.Fatalf("review ids=%v", got.ReviewIDs)
	}
	if len(payloads) != 1 || payloads[0].To != "jobs@example.com" || len(payloads[0].AttachmentFiles) != 1 {
		t.Fatalf("draft payloads=%+v", payloads)
	}

	emailApp, err := st.GetApplicationByJobID(emailJob.ID)
	if err != nil { t.Fatal(err) }
	if emailApp == nil || emailApp.State != models.ApplicationStateDraftCreated || emailApp.GmailDraftID != "draft-job-email" {
		t.Fatalf("email application=%+v", emailApp)
	}
	reviewApp, err := st.GetApplicationByJobID(reviewJob.ID)
	if err != nil { t.Fatal(err) }
	if reviewApp == nil || reviewApp.State != models.ApplicationStateNeedReview || reviewApp.Recipient != "" {
		t.Fatalf("review application=%+v", reviewApp)
	}

	again := processJobsToDraft(context.Background(), st, settings, nil, []string{emailJob.ID}, creator)
	if again.ExistingDrafts != 1 || again.DraftCreated != 0 || len(again.ReviewIDs) != 1 {
		t.Fatalf("existing draft should route to review without recreation: %+v", again)
	}
}

func TestBulkQueueJobsHandlerPreservesFilters(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs-queue.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()
	for _, id := range []string{"job-1", "job-2"} {
		job := &models.JobPosting{
			ID: id, Title: "Sales", Company: "Example", URL: "https://www.linkedin.com/jobs/view/" + id,
			ApplicationMethod: "EMAIL", ApplyEmail: id + "@example.com",
		}
		if err := st.Upsert(job); err != nil { t.Fatal(err) }
	}

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{
		"csrf": {"csrf-test"},
		"job_id": {"job-1", "job-2"},
		"filter_q": {"sales"},
		"filter_location": {"Jakarta"},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/queue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppBulkQueueJobs(rec, req)
	if rec.Code != http.StatusSeeOther { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "job_bulk=queue") || !strings.Contains(loc, "queued_count=2") || !strings.Contains(loc, "q=sales") || !strings.Contains(loc, "location=Jakarta") {
		t.Fatalf("redirect=%q", loc)
	}
}

func TestJobsDatabaseRendersBulkWorkflow(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil { t.Fatal(err) }
	pd := appPageData{
		Title: "Jobs", Subtitle: "Database workbench", Active: "jobs", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C", GmailConnected: true,
		Query: "sales", LocationFilter: "Jakarta",
		Jobs: []appJobRow{
			{ID: "101", Title: "Sales Executive", Company: "Example", Location: "Jakarta", Method: "EMAIL", State: "NOT_APPLIED", Email: "jobs@example.com"},
			{ID: "102", Title: "Account Executive", Company: "Review", Location: "Bogor", Method: "UNKNOWN", State: "NOT_APPLIED"},
		},
		Attachments: []appAttachment{{ID: "portfolio-1", Label: "Portfolio", Kind: "portfolio", Exists: true}},
	}
	var b strings.Builder
	if err := tpl.Execute(&b, pd); err != nil { t.Fatal(err) }
	html := b.String()
	for _, want := range []string{
		"id=\"select-all-jobs\"",
		"name=\"job_id\" value=\"101\"",
		"formaction=\"/app/jobs/bulk/queue\"",
		"formaction=\"/app/jobs/bulk/process-to-draft\"",
		"Process Selected to Draft",
		"name=\"filter_q\" value=\"sales\"",
		"name=\"filter_location\" value=\"Jakarta\"",
		"name=\"attachment\" value=\"portfolio-1\" checked",
		"No explicit email · will need review",
		"Queue → deterministic Prepare → Gmail Draft",
	} {
		if !strings.Contains(html, want) { t.Errorf("Jobs bulk UI missing %q", want) }
	}
}

func TestJobsProcessButtonDisabledWithoutGmail(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil { t.Fatal(err) }
	var b strings.Builder
	if err := tpl.Execute(&b, appPageData{
		Title: "Jobs", Active: "jobs", CandidateName: "Candidate", CandidateInitials: "C",
		Jobs: []appJobRow{{ID: "101", Title: "Sales", Method: "EMAIL", State: "NOT_APPLIED"}},
	}); err != nil { t.Fatal(err) }
	html := b.String()
	if !strings.Contains(html, `formaction="/app/jobs/bulk/process-to-draft" disabled title="Connect Gmail first"`) {
		t.Fatal("Process Selected to Draft should be disabled when Gmail is disconnected")
	}
}
