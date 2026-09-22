package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

func TestJobTriageSkippedLeavesInbox(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "triage-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for _, j := range []*models.JobPosting{
		{ID: "keep", Title: "Keep Reviewing", URL: "https://www.linkedin.com/jobs/view/keep", SearchedAt: "2026-09-22T00:00:00Z"},
		{ID: "skip", Title: "Not Interesting", URL: "https://www.linkedin.com/jobs/view/skip", SearchedAt: "2026-09-22T00:00:00Z"},
	} {
		if err := st.Upsert(j); err != nil {
			t.Fatal(err)
		}
	}

	ws := &webServer{st: st, csrf: "token"}
	form := url.Values{
		"csrf":          {"token"},
		"job_id":        {"skip"},
		"review_state":  {models.JobReviewSkipped},
		"filter_review": {models.JobReviewUnreviewed},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/review-state", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppBulkReviewState(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "review=UNREVIEWED") || !strings.Contains(loc, "triage=SKIPPED") {
		t.Fatalf("unexpected redirect: %s", loc)
	}

	skipped, err := st.Get("skip")
	if err != nil {
		t.Fatal(err)
	}
	if skipped.ReviewState != models.JobReviewSkipped {
		t.Fatalf("review_state=%q want SKIPPED", skipped.ReviewState)
	}

	inbox, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	if inbox.ReviewFilter != models.JobReviewUnreviewed || inbox.TotalRows != 1 || len(inbox.Jobs) != 1 || inbox.Jobs[0].ID != "keep" {
		t.Fatalf("skipped job leaked into Inbox: filter=%q total=%d jobs=%+v", inbox.ReviewFilter, inbox.TotalRows, inbox.Jobs)
	}

	skippedPage, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs?review=SKIPPED", nil))
	if err != nil {
		t.Fatal(err)
	}
	if skippedPage.TotalRows != 1 || len(skippedPage.Jobs) != 1 || skippedPage.Jobs[0].ID != "skip" {
		t.Fatalf("Skipped view missing decision: total=%d jobs=%+v", skippedPage.TotalRows, skippedPage.Jobs)
	}
}

func TestJobTriageDetailRequiresShortlistForExecution(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	job := &models.JobPosting{
		ID: "skip-detail", Title: "No Thanks", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/skip-detail",
		ApplicationMethod: "EMAIL", ApplyEmail: "jobs@example.com",
		ReviewState: models.JobReviewSkipped,
	}
	var out strings.Builder
	if err := tpl.Execute(&out, appPageData{
		Title: "Job Detail", Active: "jobs", CSRF: "token",
		CandidateName: "Candidate", CandidateInitials: "C",
		SelectedJob: job, ListURL: "/app/jobs?review=SKIPPED",
	}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, "Queue Email Application") {
		t.Fatal("SKIPPED job must not expose application execution")
	}
	if !strings.Contains(html, "Shortlist this job first") {
		t.Fatal("detail should explain the shortlist gate")
	}
}
