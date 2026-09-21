package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

func setupDraftCreatedApplication(t *testing.T) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	job := &models.JobPosting{
		ID:                "60001",
		Title:             "Account Executive",
		Company:           "Example Co",
		URL:               "https://www.linkedin.com/jobs/view/60001",
		ApplicationMethod: "EMAIL",
		ApplyEmail:        "jobs@example.com",
	}
	if err := st.Upsert(job); err != nil {
		st.Close()
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(job.ID); err != nil {
		st.Close()
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.SaveApplicationPreparation(job.ID, "Application", "Dear Hiring Team", "general"); err != nil {
		st.Close()
		t.Fatalf("SaveApplicationPreparation: %v", err)
	}
	if _, err := st.MarkApplicationDraftCreated(job.ID, "draft-60001"); err != nil {
		st.Close()
		t.Fatalf("MarkApplicationDraftCreated: %v", err)
	}
	return st, job.ID
}

func TestApproveApplicationUIRequiresExplicitReviewConfirmation(t *testing.T) {
	st, jobID := setupDraftCreatedApplication(t)
	defer st.Close()

	ws := &webServer{st: st, csrf: "csrf-test"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/applications/{id}/approve", ws.handleAppApproveApplication)

	form := url.Values{
		"csrf":        {"csrf-test"},
		"review_note": {"Reviewed in Gmail"},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/applications/"+jobID+"/approve", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "action_error=") {
		t.Fatalf("expected review confirmation error, redirect=%q", rec.Header().Get("Location"))
	}
	app, err := st.GetApplicationByJobID(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if app.State != models.ApplicationStateDraftCreated {
		t.Fatalf("state=%q want DRAFT_CREATED", app.State)
	}
}

func TestApproveAndUnapproveApplicationUI(t *testing.T) {
	st, jobID := setupDraftCreatedApplication(t)
	defer st.Close()

	ws := &webServer{st: st, csrf: "csrf-test"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/applications/{id}/approve", ws.handleAppApproveApplication)
	mux.HandleFunc("POST /app/applications/{id}/unapprove", ws.handleAppUnapproveApplication)

	approve := url.Values{
		"csrf":           {"csrf-test"},
		"review_confirm": {"1"},
		"review_note":    {"Recipient, body, CV and portfolio checked in Gmail"},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/applications/"+jobID+"/approve", strings.NewReader(approve.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "review=approved") {
		t.Fatalf("approve status=%d redirect=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	app, err := st.GetApplicationByJobID(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if app.State != models.ApplicationStateApproved || app.ReviewedAt == "" {
		t.Fatalf("approved app=%+v", app)
	}
	if !strings.Contains(app.ReviewNote, "portfolio") {
		t.Fatalf("review note=%q", app.ReviewNote)
	}

	unapprove := url.Values{
		"csrf":        {"csrf-test"},
		"review_note": {"Need to revise the Gmail draft"},
	}
	req = httptest.NewRequest(http.MethodPost, "/app/applications/"+jobID+"/unapprove", strings.NewReader(unapprove.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "review=unapproved") {
		t.Fatalf("unapprove status=%d redirect=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	app, err = st.GetApplicationByJobID(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if app.State != models.ApplicationStateDraftCreated || app.ReviewedAt != "" {
		t.Fatalf("reopened app=%+v", app)
	}
	if app.GmailDraftID != "draft-60001" {
		t.Fatalf("draft id changed=%q", app.GmailDraftID)
	}
}

func TestApplicationDetailRendersManualReviewActions(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	render := func(app *models.JobApplication) string {
		var out bytes.Buffer
		if err := tpl.Execute(&out, appPageData{
			Title:             "Application Detail",
			Active:            "applications",
			CSRF:              "csrf-test",
			CandidateName:     "Candidate",
			CandidateInitials: "C",
			SelectedApplication: app,
		}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	draft := render(&models.JobApplication{
		JobID:        "60001",
		State:        models.ApplicationStateDraftCreated,
		GmailDraftID: "draft-60001",
	})
	for _, want := range []string{
		"action=\"/app/applications/60001/approve\"",
		"name=\"review_confirm\"",
		"name=\"review_note\"",
		"Approve Application",
		"It does not send the email.",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("DRAFT_CREATED UI missing %q", want)
		}
	}

	approved := render(&models.JobApplication{
		JobID:        "60001",
		State:        models.ApplicationStateApproved,
		GmailDraftID: "draft-60001",
		ReviewedAt:   "2026-09-21T14:00:00Z",
		ReviewNote:   "Reviewed",
	})
	for _, want := range []string{
		"action=\"/app/applications/60001/unapprove\"",
		"Unapprove &amp; Reopen Review",
		"Send (separate phase)",
		"2026-09-21T14:00:00Z",
	} {
		if !strings.Contains(approved, want) {
			t.Errorf("APPROVED UI missing %q", want)
		}
	}
	if strings.Contains(approved, "action=\"/app/applications/60001/send\"") {
		t.Fatal("manual review phase must not expose a send POST action")
	}
}
