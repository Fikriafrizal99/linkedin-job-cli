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

func TestSafeLinkedInApplyURL(t *testing.T) {
	good, err := safeLinkedInApplyURL("https://www.linkedin.com/jobs/view/123456/?trk=test#fragment")
	if err != nil { t.Fatalf("safeLinkedInApplyURL: %v", err) }
	if strings.Contains(good, "#") { t.Fatalf("fragment should be stripped: %q", good) }
	for _, bad := range []string{
		"https://example.com/jobs/view/123",
		"javascript:alert(1)",
		"https://www.linkedin.com/in/example",
	} {
		if _, err := safeLinkedInApplyURL(bad); err == nil {
			t.Fatalf("expected invalid Easy Apply URL: %q", bad)
		}
	}
}

func TestOpenEasyApplyMarksInProgressAndRedirects(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "easy-open.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()
	job := &models.JobPosting{
		ID: "easy-open", Title: "Account Executive", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/777777/", ApplicationMethod: "LINKEDIN",
	}
	if err := st.Upsert(job); err != nil { t.Fatal(err) }
	if _, err := st.QueueApplication(job.ID); err != nil { t.Fatal(err) }

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{"csrf": {"csrf-test"}}
	req := httptest.NewRequest(http.MethodPost, "/app/applications/"+job.ID+"/easy-apply/open", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", job.ID)
	rec := httptest.NewRecorder()
	ws.handleAppOpenEasyApply(rec, req)
	if rec.Code != http.StatusSeeOther { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
	if rec.Header().Get("Location") != job.URL { t.Fatalf("redirect=%q", rec.Header().Get("Location")) }
	app, err := st.GetApplicationByJobID(job.ID)
	if err != nil { t.Fatal(err) }
	if app == nil || app.State != models.ApplicationStateInProgress || app.OpenedAt == "" {
		t.Fatalf("application=%+v", app)
	}
}

func TestMarkEasyApplyAppliedRequiresHumanConfirmation(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "missing-settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "easy-applied.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()
	job := &models.JobPosting{
		ID: "easy-applied", Title: "Sales", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/888888/", ApplicationMethod: "LINKEDIN",
	}
	if err := st.Upsert(job); err != nil { t.Fatal(err) }
	if _, err := st.QueueApplication(job.ID); err != nil { t.Fatal(err) }
	if _, err := st.MarkEasyApplyOpened(job.ID); err != nil { t.Fatal(err) }

	ws := &webServer{st: st, csrf: "csrf-test"}
	post := func(confirm bool) *httptest.ResponseRecorder {
		form := url.Values{"csrf": {"csrf-test"}, "return_to": {"/app/applications/easy-apply?ids="+job.ID+"&pos=0"}}
		if confirm { form.Set("apply_confirm", "1") }
		req := httptest.NewRequest(http.MethodPost, "/app/applications/"+job.ID+"/easy-apply/applied", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", job.ID)
		rec := httptest.NewRecorder()
		ws.handleAppMarkEasyApplyApplied(rec, req)
		return rec
	}
	rec := post(false)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action_error=") {
		t.Fatalf("missing confirmation redirect=%q status=%d", rec.Header().Get("Location"), rec.Code)
	}
	app, _ := st.GetApplicationByJobID(job.ID)
	if app.State != models.ApplicationStateInProgress { t.Fatalf("state changed without confirmation: %+v", app) }

	rec = post(true)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "easy_applied=1") {
		t.Fatalf("confirmed redirect=%q status=%d", rec.Header().Get("Location"), rec.Code)
	}
	app, _ = st.GetApplicationByJobID(job.ID)
	if app.State != models.ApplicationStateApplied || app.AppliedAt == "" {
		t.Fatalf("application=%+v", app)
	}
}

func TestEasyApplyQueueRendersManualControls(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil { t.Fatal(err) }
	var out strings.Builder
	pd := appPageData{
		Title: "Easy Apply Queue", Active: "applications", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C",
		EasyApplyMode: true, EasyApplyPosition: 1, EasyApplyTotal: 3,
		EasyApplyPrevURL: "/app/applications/easy-apply?pos=2",
		EasyApplyNextURL: "/app/applications/easy-apply?pos=1",
		EasyApplyStayURL: "/app/applications/easy-apply?pos=0",
		EasyApplyCurrentURL: "https://www.linkedin.com/jobs/view/999999/",
		EasyApplyRecommendedCV: "sales", EasyApplyRecommendedCVPath: "/tmp/sales.pdf", EasyApplyRecommendedCVReady: true,
		SelectedApplication: &models.JobApplication{
			JobID: "easy-ui", State: models.ApplicationStateReadyEasyApply, ApplyURL: "https://www.linkedin.com/jobs/view/999999/",
		},
		SelectedApplicationJob: &models.JobPosting{
			ID: "easy-ui", Title: "Account Executive", Company: "Example", ApplicationMethod: "LINKEDIN",
			Location: "Jakarta", PostedAt: "2026-09-20", RemoteType: "Hybrid",
			EmploymentType: "Full-time", Seniority: "Mid-Senior level", SalaryRaw: "IDR 12,000,000 - 18,000,000",
			ShortDescription: "Own the sales pipeline and grow key accounts.",
			Description: "Lead prospecting, pipeline reviews, account growth, and cross-functional coordination.",
			ApplicationInstruction: "Complete the LinkedIn Easy Apply form and attach an updated CV.",
			CompanyOverview: "Example builds business software for growing teams.",
		},
		CVProfiles: []appCVProfile{{ID: "sales", Default: true, Exists: true}},
		EasyApplyOpenTargets: []easyApplyOpenTarget{{
			JobID: "easy-ui", URL: "https://www.linkedin.com/jobs/view/999999/", MarkURL: "/app/applications/easy-ui/easy-apply/open",
		}},
	}
	if err := tpl.Execute(&out, pd); err != nil { t.Fatal(err) }
	html := out.String()
	for _, want := range []string{
		"Easy Apply 1 of 3",
		"Open LinkedIn Easy Apply ↗",
		"Open Next 3",
		"Mark Applied &amp; Next",
		"name=\"apply_confirm\" value=\"1\" required",
		"target=\"_blank\"",
		"Submission remains human-controlled",
		"Job Context",
		"Jakarta",
		"Mid-Senior level",
		"Own the sales pipeline and grow key accounts.",
		"Application instructions",
		"Complete the LinkedIn Easy Apply form and attach an updated CV.",
		"Job description",
		"Lead prospecting, pipeline reviews, account growth, and cross-functional coordination.",
		"Company overview",
		"data-mark=\"/app/applications/easy-ui/easy-apply/open\"",
	} {
		if !strings.Contains(html, want) { t.Errorf("Easy Apply UI missing %q", want) }
	}
	if strings.Contains(html, "auto-submit") {
		t.Fatal("Easy Apply UI must not claim or expose auto-submit")
	}
}


func TestEasyApplyReturnAfterAppliedAdvancesToNextOriginalItem(t *testing.T) {
	got := easyApplyReturnAfterApplied("/app/applications/easy-apply?ids=a%2Cb%2Cc&pos=1", "b")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("ids") != "a,c" || u.Query().Get("pos") != "1" {
		t.Fatalf("got %q; expected remaining a,c at c position", got)
	}

	got = easyApplyReturnAfterApplied("/app/applications/easy-apply?ids=a%2Cb%2Cc&pos=2", "c")
	u, err = url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("ids") != "a,b" || u.Query().Get("pos") != "0" {
		t.Fatalf("got %q; expected wrap to a", got)
	}
}
