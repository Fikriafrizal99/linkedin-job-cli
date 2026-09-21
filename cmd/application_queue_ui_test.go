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

func TestRemoveFromQueueHandlerKeepsCollectedJob(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "remove-queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	job := &models.JobPosting{
		ID: "remove-1", Title: "Sales Executive", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/remove-1",
		ApplicationMethod: "UNKNOWN",
	}
	if err := st.Upsert(job); err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueApplication(job.ID); err != nil {
		t.Fatal(err)
	}

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{"csrf": {"csrf-test"}}
	req := httptest.NewRequest(http.MethodPost, "/app/applications/"+job.ID+"/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", job.ID)
	rec := httptest.NewRecorder()

	ws.handleAppRemoveFromQueue(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "/app/applications?removed=1" {
		t.Fatalf("redirect=%q", rec.Header().Get("Location"))
	}
	app, err := st.GetApplicationByJobID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if app != nil {
		t.Fatalf("application still queued: %+v", app)
	}
	kept, err := st.Get(job.ID)
	if err != nil || kept == nil {
		t.Fatalf("collected job should remain, job=%+v err=%v", kept, err)
	}
}

func TestRemoveFromQueueUIOnlyForPreDraftStates(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	render := func(state string) string {
		var out strings.Builder
		if err := tpl.Execute(&out, appPageData{
			Title: "Application Detail", Active: "applications", CSRF: "csrf",
			CandidateName: "Candidate", CandidateInitials: "C",
			SelectedApplication: &models.JobApplication{
				JobID: "remove-ui", State: state, Recipient: "jobs@example.com",
			},
		}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	for _, state := range []string{models.ApplicationStateReadyEmail, models.ApplicationStateNeedReview} {
		html := render(state)
		if !strings.Contains(html, "action=\"/app/applications/remove-ui/remove\"") ||
			!strings.Contains(html, "Remove from Queue") {
			t.Fatalf("%s missing remove action", state)
		}
	}
	for _, state := range []string{models.ApplicationStateDraftCreated, models.ApplicationStateApproved, models.ApplicationStateSent} {
		html := render(state)
		if strings.Contains(html, "/app/applications/remove-ui/remove") {
			t.Fatalf("%s must not expose remove-from-queue", state)
		}
	}
}

func TestApplicationsWorkbenchRendersBulkRemove(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tpl.Execute(&out, appPageData{
		Title: "Applications", Active: "applications", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C",
		Applications: []appApplicationRow{{
			JobID: "remove-bulk", Title: "Sales", Company: "Example",
			Method: "EMAIL", State: models.ApplicationStateReadyEmail,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "formaction=\"/app/applications/bulk/remove\"") {
		t.Fatal("Applications workbench missing bulk remove")
	}
}
