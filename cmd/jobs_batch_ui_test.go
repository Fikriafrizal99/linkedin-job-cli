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
	if got.Selected != 2 || got.Queued != 1 || got.Prepared != 1 || got.DraftCreated != 1 || got.NonEmailSkipped != 1 || got.NeedReview != 0 || got.Failed != 0 {
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
	if reviewApp != nil {
		t.Fatalf("non-email job should remain outside application queue, got %+v", reviewApp)
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
		Query: "sales", LocationFilter: "Jakarta", ReviewFilter: models.JobReviewShortlisted,
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
		"formaction=\"/app/jobs/bulk/start-applications\"",
		"Start Applications",
		"Select all",
		"Clear selection",
		"name=\"filter_q\" value=\"sales\"",
		"name=\"filter_location\" value=\"Jakarta\"",
		"Unsupported destination · skipped by default",
	} {
		if !strings.Contains(html, want) { t.Errorf("Jobs bulk UI missing %q", want) }
	}
}

func TestJobsInboxRendersTriageActions(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil { t.Fatal(err) }
	var b strings.Builder
	if err := tpl.Execute(&b, appPageData{
		Title: "Jobs Inbox", Active: "jobs", CandidateName: "Candidate", CandidateInitials: "C",
		ReviewFilter: models.JobReviewUnreviewed,
		Jobs: []appJobRow{{ID: "101", Title: "Sales", Method: "EMAIL", State: "NOT_APPLIED", ReviewState: models.JobReviewUnreviewed}},
	}); err != nil { t.Fatal(err) }
	html := b.String()
	for _, want := range []string{
		"Shortlist", "Later", "Skip", "formaction=\"/app/jobs/bulk/review-state\"",
		"Inbox", "Shortlisted", "All Jobs",
	} {
		if !strings.Contains(html, want) { t.Errorf("Inbox triage UI missing %q", want) }
	}
	if strings.Contains(html, "Process Selected") || strings.Contains(html, "Queue Selected") {
		t.Fatal("Inbox must not expose application execution actions")
	}
}

func TestShortlistedStartApplicationsDoesNotRequireGmail(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil { t.Fatal(err) }
	var b strings.Builder
	if err := tpl.Execute(&b, appPageData{
		Title: "Shortlisted", Active: "jobs", CandidateName: "Candidate", CandidateInitials: "C",
		ReviewFilter: models.JobReviewShortlisted, SelectedJobsCount: 1, SelectedEasyApplyCount: 1,
		Jobs: []appJobRow{{ID: "101", Title: "Sales", Method: "EASY_APPLY", State: "NOT_APPLIED", ReviewState: models.JobReviewShortlisted, Selected: true}},
	}); err != nil { t.Fatal(err) }
	html := b.String()
	if !strings.Contains(html, `formaction="/app/jobs/bulk/start-applications"`) ||
		!strings.Contains(html, "Start Applications") {
		t.Fatal("Shortlisted must expose Start Applications")
	}
	if strings.Contains(html, "Process Selected") || strings.Contains(html, "Queue Selected") {
		t.Fatal("legacy execution actions should not remain in the Shortlisted UI")
	}
	if strings.Contains(html, "Connect Gmail") {
		t.Fatal("Start Applications must not require Gmail")
	}
}


func TestBulkQueueJobsSkipsNonEmailUnlessExplicitlyIncluded(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs-queue-nonemail.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()

	job := &models.JobPosting{
		ID: "job-review", Title: "Account Executive", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/job-review",
		ApplicationMethod: "UNKNOWN",
	}
	if err := st.Upsert(job); err != nil { t.Fatal(err) }

	ws := &webServer{st: st, csrf: "csrf-test"}
	post := func(include bool) string {
		form := url.Values{"csrf": {"csrf-test"}, "job_id": {job.ID}}
		if include { form.Set("include_need_review", "1") }
		req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/queue", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		ws.handleAppBulkQueueJobs(rec, req)
		if rec.Code != http.StatusSeeOther { t.Fatalf("status=%d", rec.Code) }
		return rec.Header().Get("Location")
	}

	loc := post(false)
	if !strings.Contains(loc, "non_email_skipped_count=1") || !strings.Contains(loc, "queued_count=0") {
		t.Fatalf("default non-email redirect=%q", loc)
	}
	app, err := st.GetApplicationByJobID(job.ID)
	if err != nil { t.Fatal(err) }
	if app != nil {
		t.Fatalf("non-email job should not be queued by default: %+v", app)
	}

	loc = post(true)
	if !strings.Contains(loc, "queued_count=1") || !strings.Contains(loc, "need_review_count=1") {
		t.Fatalf("explicit NEED_REVIEW redirect=%q", loc)
	}
	app, err = st.GetApplicationByJobID(job.ID)
	if err != nil { t.Fatal(err) }
	if app == nil || app.State != models.ApplicationStateNeedReview {
		t.Fatalf("expected explicit NEED_REVIEW queue, got %+v", app)
	}
}


func TestProcessJobsToDraftRoutesEasyApplyWithoutCreatingEmailDraft(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs-easy-apply.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()

	job := &models.JobPosting{
		ID: "easy-1", Title: "Account Executive", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/555555/",
		ApplicationMethod: "LINKEDIN",
	}
	if err := st.Upsert(job); err != nil { t.Fatal(err) }

	draftCalls := 0
	creator := func(_ context.Context, payload application.DraftPayload) (gmailclient.DraftResult, error) {
		draftCalls++
		return gmailclient.DraftResult{DraftID: "unexpected"}, nil
	}
	got := processJobsToDraft(context.Background(), st, config.ApplicationSettings{}, nil, []string{job.ID}, creator)
	if got.Queued != 1 || got.EasyApplyQueued != 1 || got.DraftCreated != 0 || got.Failed != 0 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if draftCalls != 0 {
		t.Fatalf("Easy Apply must not create Gmail drafts, calls=%d", draftCalls)
	}
	if len(got.EasyApplyIDs) != 1 || got.EasyApplyIDs[0] != job.ID {
		t.Fatalf("easy apply ids=%v", got.EasyApplyIDs)
	}
	app, err := st.GetApplicationByJobID(job.ID)
	if err != nil { t.Fatal(err) }
	if app == nil || app.State != models.ApplicationStateReadyEasyApply {
		t.Fatalf("application=%+v", app)
	}
	if app.ApplyURL != job.URL {
		t.Fatalf("apply url=%q want %q", app.ApplyURL, job.URL)
	}
}

func TestBulkQueueJobsIncludesEasyApplyByDefault(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs-queue-easy.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()

	job := &models.JobPosting{
		ID: "easy-queue", Title: "Sales", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/666666/",
		ApplicationMethod: "LINKEDIN",
	}
	if err := st.Upsert(job); err != nil { t.Fatal(err) }

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{"csrf": {"csrf-test"}, "job_id": {job.ID}}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/queue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppBulkQueueJobs(rec, req)

	if rec.Code != http.StatusSeeOther { t.Fatalf("status=%d", rec.Code) }
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "easy_apply_queued_count=1") || !strings.Contains(loc, "queued_count=1") {
		t.Fatalf("redirect=%q", loc)
	}
	app, err := st.GetApplicationByJobID(job.ID)
	if err != nil { t.Fatal(err) }
	if app == nil || app.State != models.ApplicationStateReadyEasyApply {
		t.Fatalf("application=%+v", app)
	}
}
