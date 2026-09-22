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

func TestStartApplicationsRoutesShortlistedJobsByMethod(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "start-apps.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	jobs := []*models.JobPosting{
		{ID: "email", Title: "Email Role", URL: "https://www.linkedin.com/jobs/view/email", SearchedAt: "2026-09-22T00:00:00Z", ApplicationMethod: "EMAIL", ApplyEmail: "jobs@example.com"},
		{ID: "easy", Title: "Easy Role", URL: "https://www.linkedin.com/jobs/view/easy", SearchedAt: "2026-09-22T00:00:00Z", ApplicationMethod: "EASY_APPLY"},
		{ID: "review", Title: "Review Role", URL: "https://www.linkedin.com/jobs/view/review", SearchedAt: "2026-09-22T00:00:00Z", ApplicationMethod: "UNKNOWN"},
	}
	for _, j := range jobs {
		if err := st.Upsert(j); err != nil {
			t.Fatal(err)
		}
		if err := st.SetJobReviewState(j.ID, models.JobReviewShortlisted, ""); err != nil {
			t.Fatal(err)
		}
	}

	ws := &webServer{st: st, csrf: "token"}
	scope := normalizedJobSelectionScope(url.Values{"review": {models.JobReviewShortlisted}})
	ws.replaceSelectedJobs(scope, []string{"email", "easy", "review"})

	form := url.Values{
		"csrf":          {"token"},
		"filter_review": {models.JobReviewShortlisted},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/start-applications", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppStartApplications(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	for _, want := range []string{"applications_started=1", "started_count=3", "email_count=1", "easy_count=1", "need_review_count=1"} {
		if !strings.Contains(loc, want) {
			t.Fatalf("redirect %q missing %q", loc, want)
		}
	}

	emailApp, _ := st.GetApplicationByJobID("email")
	easyApp, _ := st.GetApplicationByJobID("easy")
	reviewApp, _ := st.GetApplicationByJobID("review")
	if emailApp == nil || emailApp.State != models.ApplicationStateReadyEmail {
		t.Fatalf("email app=%+v", emailApp)
	}
	if easyApp == nil || easyApp.State != models.ApplicationStateReadyEasyApply {
		t.Fatalf("easy app=%+v", easyApp)
	}
	if reviewApp == nil || reviewApp.State != models.ApplicationStateNeedReview {
		t.Fatalf("review app=%+v", reviewApp)
	}
	if got := len(ws.selectedJobsForScope(scope)); got != 0 {
		t.Fatalf("completed handoff should clear selection, got %d", got)
	}
}

func TestStartApplicationsRejectsNonShortlistedJob(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "start-single.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j := &models.JobPosting{ID: "inbox", Title: "Inbox Role", URL: "https://www.linkedin.com/jobs/view/inbox", SearchedAt: "2026-09-22T00:00:00Z", ApplicationMethod: "EMAIL", ApplyEmail: "jobs@example.com"}
	if err := st.Upsert(j); err != nil {
		t.Fatal(err)
	}
	ws := &webServer{st: st, csrf: "token"}

	form := url.Values{"csrf": {"token"}}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/inbox/start-application", strings.NewReader(form.Encode()))
	req.SetPathValue("id", "inbox")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppStartApplication(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	if app, _ := st.GetApplicationByJobID("inbox"); app != nil {
		t.Fatalf("non-shortlisted job entered Applications: %+v", app)
	}
}

func TestStartApplicationsIsIdempotent(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "start-idempotent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j := &models.JobPosting{ID: "same", Title: "Same Role", URL: "https://www.linkedin.com/jobs/view/same", SearchedAt: "2026-09-22T00:00:00Z", ApplicationMethod: "EMAIL", ApplyEmail: "jobs@example.com"}
	if err := st.Upsert(j); err != nil {
		t.Fatal(err)
	}
	if err := st.SetJobReviewState(j.ID, models.JobReviewShortlisted, ""); err != nil {
		t.Fatal(err)
	}
	first, err := st.QueueApplication(j.ID)
	if err != nil {
		t.Fatal(err)
	}

	ws := &webServer{st: st, csrf: "token"}
	scope := normalizedJobSelectionScope(url.Values{"review": {models.JobReviewShortlisted}})
	ws.replaceSelectedJobs(scope, []string{j.ID})
	form := url.Values{"csrf": {"token"}, "filter_review": {models.JobReviewShortlisted}}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/start-applications", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppStartApplications(rec, req)

	again, err := st.GetApplicationByJobID(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again == nil || again.ID != first.ID {
		t.Fatalf("application duplicated: first=%+v again=%+v", first, again)
	}
	if !strings.Contains(rec.Header().Get("Location"), "existing_count=1") {
		t.Fatalf("existing application was not reported: %s", rec.Header().Get("Location"))
	}
}
