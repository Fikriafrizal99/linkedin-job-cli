package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

func TestAppTemplateParses(t *testing.T) {
	if _, err := newAppTemplate(); err != nil {
		t.Fatalf("app template parse: %v", err)
	}
}

func TestAppTemplateHasCanonicalNavigation(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pd := appPageData{
		Title: "Dashboard",
		Subtitle: "Overview",
		Active: "dashboard",
		CSRF: "csrf",
		CandidateName: "Mochamad Fikri Afrizal",
		CandidateInitials: "MA",
		Stats: appStats{JobsTotal: 124, EmailTotal: 28, PipelineTotal: 8, SentTotal: 1},
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, pd); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"href=\"/app/dashboard\"",
		"href=\"/app/jobs\"",
		"href=\"/app/applications\"",
		"href=\"/app/cv-profiles\"",
		"href=\"/app/collect\"",
		"href=\"/app/settings\"",
		"New to Review",
		"Shortlisted",
		"In Application Pipeline",
		"Completed Applications",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered app missing %q", want)
		}
	}
}


func TestJobsNavigationExposesPersistentViewHooks(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, appPageData{
		Title: "Jobs", Active: "jobs", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C",
		ReviewFilter: models.JobReviewUnreviewed,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := out.String()
	if !strings.Contains(html, "data-jobs-return") {
		t.Fatal("Jobs navigation must expose a last-view persistence hook")
	}
	if !strings.Contains(html, "data-jobs-reset") {
		t.Fatal("Jobs Reset must clear the persisted last view")
	}
}


func TestApplicationsSeparateActiveAndCompletedScopes(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "missing-settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "application-scopes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	activeJob := &models.JobPosting{
		ID: "active-1", Title: "Account Executive", Company: "Active Co",
		URL: "https://www.linkedin.com/jobs/view/100001/", ApplicationMethod: "LINKEDIN",
	}
	doneJob := &models.JobPosting{
		ID: "done-1", Title: "Business Development", Company: "Done Co",
		URL: "https://www.linkedin.com/jobs/view/100002/", ApplicationMethod: "LINKEDIN",
	}
	for _, job := range []*models.JobPosting{activeJob, doneJob} {
		if err := st.Upsert(job); err != nil {
			t.Fatal(err)
		}
		if _, err := st.QueueApplication(job.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.MarkEasyApplyOpened(doneJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkEasyApplyApplied(doneJob.ID, ""); err != nil {
		t.Fatal(err)
	}

	ws := &webServer{st: st, csrf: "csrf-test"}
	activeReq := httptest.NewRequest(http.MethodGet, "/app/applications", nil)
	active, err := ws.buildAppPage(activeReq)
	if err != nil {
		t.Fatal(err)
	}
	if active.ApplicationScope != "active" {
		t.Fatalf("active scope=%q", active.ApplicationScope)
	}
	if len(active.Applications) != 1 || active.Applications[0].JobID != activeJob.ID {
		t.Fatalf("active applications=%+v", active.Applications)
	}
	if active.Stats.PipelineTotal != 1 || active.Stats.CompletedTotal != 1 {
		t.Fatalf("stats active=%d completed=%d", active.Stats.PipelineTotal, active.Stats.CompletedTotal)
	}

	doneReq := httptest.NewRequest(http.MethodGet, "/app/applications?scope=completed", nil)
	done, err := ws.buildAppPage(doneReq)
	if err != nil {
		t.Fatal(err)
	}
	if done.ApplicationScope != "completed" {
		t.Fatalf("completed scope=%q", done.ApplicationScope)
	}
	if len(done.Applications) != 1 || done.Applications[0].JobID != doneJob.ID {
		t.Fatalf("completed applications=%+v", done.Applications)
	}
}

func TestCompletedApplicationsRenderAsHistoryWithoutQueueControls(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = tpl.Execute(&out, appPageData{
		Title: "Applications", Active: "applications", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C",
		ApplicationScope: "completed",
		ApplicationStates: []string{models.ApplicationStateApplied, models.ApplicationStateSent},
		Stats: appStats{PipelineTotal: 2, CompletedTotal: 3, AppliedTotal: 2, SentTotal: 1},
		Applications: []appApplicationRow{{
			JobID: "done-1", Title: "Account Executive", Company: "Example",
			Method: "EASY_APPLY", State: models.ApplicationStateApplied,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		"Completed <span>3</span>",
		"Applied on LinkedIn",
		"Email sent",
		"Completed applications are kept here as history.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("completed applications UI missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"id=\"select-all-apps\"",
		"Prepare Selected",
		"Create Drafts",
		"Review Selected",
		"Confirm Send",
	} {
		if strings.Contains(html, unwanted) {
			t.Errorf("completed applications UI should not expose %q", unwanted)
		}
	}
}

func TestAppTemplateApplicationDetailIsReadOnlyUntilActionWiring(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	app := &models.JobApplication{
		JobID: "4467841092",
		State: models.ApplicationStateApproved,
		Recipient: "jobs@example.com",
		Subject: "Application - Sales Executive",
		Body: "Dear Hiring Team",
		GmailDraftID: "draft-123",
	}
	job := &models.JobPosting{ID: app.JobID, Title: "Sales Executive", Company: "CeoJetset"}
	pd := appPageData{
		Title: "Application Detail",
		Subtitle: "Review",
		Active: "applications",
		CSRF: "csrf",
		CandidateName: "Candidate",
		CandidateInitials: "C",
		SelectedApplication: app,
		SelectedApplicationJob: job,
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, pd); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"APPROVED", "draft-123", "Review &amp; Send Application", "action=\"/app/applications/send-confirm\"", "Unapprove &amp; Reopen Review"} {
		if !strings.Contains(out, want) {
			t.Errorf("application detail missing %q", want)
		}
	}
}


func TestSaveApplicationEmailContentBeforeDraft(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "editable-email.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()

	job := &models.JobPosting{
		ID: "email-edit-1", Title: "Sales Executive", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/123001", ApplicationMethod: "EMAIL",
		ApplyEmail: "jobs@example.com", ReviewState: models.JobReviewShortlisted,
	}
	if err := st.Upsert(job); err != nil { t.Fatal(err) }
	if _, err := st.QueueApplication(job.ID); err != nil { t.Fatal(err) }
	if _, err := st.SaveApplicationPreparation(job.ID, "Old subject", "Old body", "general"); err != nil { t.Fatal(err) }

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{
		"csrf": {"csrf-test"},
		"subject": {"Application - Sales Executive - Custom"},
		"body": {"Dear Hiring Team,\n\nThis is my edited application email."},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/applications/"+job.ID+"/content", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", job.ID)
	rec := httptest.NewRecorder()
	ws.handleAppSaveApplicationContent(rec, req)
	if rec.Code != http.StatusSeeOther { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
	if !strings.Contains(rec.Header().Get("Location"), "content_saved=1") { t.Fatalf("redirect=%q", rec.Header().Get("Location")) }
	app, err := st.GetApplicationByJobID(job.ID)
	if err != nil { t.Fatal(err) }
	if app.Subject != "Application - Sales Executive - Custom" || !strings.Contains(app.Body, "edited application email") || app.CVProfile != "general" {
		t.Fatalf("saved application=%+v", app)
	}
}

func TestSaveApplicationEmailContentLockedAfterDraft(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "locked-email.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()
	job := &models.JobPosting{
		ID: "email-edit-locked", Title: "Sales Executive", Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/123002", ApplicationMethod: "EMAIL",
		ApplyEmail: "jobs@example.com", ReviewState: models.JobReviewShortlisted,
	}
	if err := st.Upsert(job); err != nil { t.Fatal(err) }
	if _, err := st.QueueApplication(job.ID); err != nil { t.Fatal(err) }
	if _, err := st.SaveApplicationPreparation(job.ID, "Original subject", "Original body", "general"); err != nil { t.Fatal(err) }
	if _, err := st.MarkApplicationDraftCreated(job.ID, "draft-locked"); err != nil { t.Fatal(err) }

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{"csrf": {"csrf-test"}, "subject": {"Changed subject"}, "body": {"Changed body"}}
	req := httptest.NewRequest(http.MethodPost, "/app/applications/"+job.ID+"/content", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", job.ID)
	rec := httptest.NewRecorder()
	ws.handleAppSaveApplicationContent(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action_error=") {
		t.Fatalf("locked edit redirect=%q status=%d", rec.Header().Get("Location"), rec.Code)
	}
	app, _ := st.GetApplicationByJobID(job.ID)
	if app.Subject != "Original subject" || app.Body != "Original body" { t.Fatalf("locked content changed: %+v", app) }
}

func TestReadyEmailDetailRendersEditableEmailComposer(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil { t.Fatal(err) }
	app := &models.JobApplication{
		JobID: "editable-ui", State: models.ApplicationStateReadyEmail, Recipient: "jobs@example.com",
		Subject: "Application - Sales Executive", Body: "Dear Hiring Team,\n\nEditable body.", CVProfile: "general",
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, appPageData{
		Title: "Application Detail", Active: "applications", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C", SelectedApplication: app,
		SelectedApplicationJob: &models.JobPosting{ID: app.JobID, Title: "Sales Executive", Company: "Example"},
		CVProfiles: []appCVProfile{{ID: "general", Default: true, Exists: true}},
	}); err != nil { t.Fatal(err) }
	html := out.String()
	for _, want := range []string{
		"action=\"/app/applications/editable-ui/content\"",
		"name=\"subject\"",
		"name=\"body\"",
		"Save Changes",
		"Regenerate Default",
		"overwrite your saved subject and body",
	} {
		if !strings.Contains(html, want) { t.Errorf("editable email UI missing %q", want) }
	}
}

func TestMatchesUIJobFilters(t *testing.T) {
	j := &models.JobPosting{
		Title: "Sales Executive",
		Company: "CeoJetset",
		Location: "Jakarta, Indonesia",
		ApplicationMethod: "EMAIL",
		ApplyEmail: "jobs@example.com",
		ReviewState: models.JobReviewShortlisted,
	}
	if !matchesUIJob(j, models.ApplicationStateApproved, "sales", "Jakarta, Indonesia", "EMAIL", models.ApplicationStateApproved) {
		t.Fatal("expected matching job filters to pass")
	}
	if matchesUIJob(j, models.ApplicationStateApproved, "engineer", "", "", "") {
		t.Fatal("unexpected query match")
	}
	if matchesUIJob(j, models.ApplicationStateApproved, "", "", "LINKEDIN", "") {
		t.Fatal("unexpected method match")
	}
	if matchesUIJob(j, models.ApplicationStateApproved, "", "", "", models.ApplicationStateDraftCreated) {
		t.Fatal("unexpected state match")
	}
}

func TestMatchesUIApplicationFilters(t *testing.T) {
	app := &models.JobApplication{
		JobID: "4467841092",
		State: models.ApplicationStateApproved,
		Recipient: "jobs@example.com",
		Subject: "Application - Sales Executive",
	}
	job := &models.JobPosting{
		Title: "Sales Executive",
		Company: "CeoJetset",
		ApplicationMethod: "EMAIL",
	}
	if !matchesUIApplication(app, job, "ceojetset", "EMAIL", models.ApplicationStateApproved) {
		t.Fatal("expected matching application filters to pass")
	}
	if matchesUIApplication(app, job, "traveloka", "", "") {
		t.Fatal("unexpected application query match")
	}
}

func TestSettingsAndCollectUIInteractionsRender(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, pd := range []appPageData{
		{Title: "Settings", Active: "settings", CandidateName: "Candidate", CandidateInitials: "C"},
		{Title: "Collect LinkedIn Jobs", Active: "collect", CandidateName: "Candidate", CandidateInitials: "C"},
	} {
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, pd); err != nil {
			t.Fatalf("execute %s: %v", pd.Active, err)
		}
		out := buf.String()
		switch pd.Active {
		case "settings":
			for _, want := range []string{"settings-general", "settings-collector", "Email &amp; Gmail", "Database &amp; Files"} {
				if !strings.Contains(out, want) {
					t.Errorf("settings UI missing %q", want)
				}
			}
		case "collect":
			for _, want := range []string{"collect-keywords", "collect-locations", "collect-plan", "collect-preview", "--posted-within"} {
				if !strings.Contains(out, want) {
					t.Errorf("collect UI missing %q", want)
				}
			}
		}
	}
}


func TestParseUICollectFormBounds(t *testing.T) {
	good, err := parseUICollectForm(url.Values{
		"keywords": {"Sales Executive"},
		"location": {"Indonesia"},
		"posted_within": {"7d"},
		"top": {"50"},
	})
	if err != nil {
		t.Fatalf("valid collector form: %v", err)
	}
	if good.Top != 50 || good.WithSession || good.ForceOverwrite {
		t.Fatalf("unexpected safe collector request: %+v", good)
	}

	for name, values := range map[string]url.Values{
		"missing keywords": {"top": {"50"}},
		"too many": {"keywords": {"Sales"}, "top": {"101"}},
		"zero": {"keywords": {"Sales"}, "top": {"0"}},
		"bad top": {"keywords": {"Sales"}, "top": {"abc"}},
		"bad posted window": {"keywords": {"Sales"}, "top": {"50"}, "posted_within": {"forever"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseUICollectForm(values); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseUICollectPlanSupportsMultipleQueriesAndLocations(t *testing.T) {
	plan, err := parseUICollectPlan(url.Values{
		"keywords":      {"Sales Operations\nBusiness Development\nSales Operations"},
		"locations":     {"Indonesia\nJakarta"},
		"posted_within": {"7d"},
		"top":           {"30"},
	})
	if err != nil {
		t.Fatalf("parseUICollectPlan: %v", err)
	}
	if len(plan.Queries) != 2 || len(plan.Locations) != 2 || len(plan.Requests) != 4 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Requests[0].Keywords != "Sales Operations" || plan.Requests[0].Location != "Indonesia" || plan.Requests[0].Top != 30 {
		t.Fatalf("unexpected first request: %+v", plan.Requests[0])
	}
	if _, err := parseUICollectPlan(url.Values{
		"keywords":  {"A\nB\nC\nD\nE\nF"},
		"locations": {"1\n2\n3\n4\n5\n6\n7\n8\n9"},
		"top":       {"10"},
	}); err == nil {
		t.Fatal("expected combination limit error")
	}
}

func TestCollectUIRendersRealPostAction(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pd := appPageData{
		Title: "Collect LinkedIn Jobs",
		Active: "collect",
		CSRF: "csrf-real",
		CandidateName: "Candidate",
		CandidateInitials: "C",
		CollectKeywords: "Sales Executive\nBusiness Development",
		CollectLocation: "Indonesia",
		CollectLocations: "Indonesia\nJakarta",
		CollectPostedWithin: "7d",
		CollectTop: 50,
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, pd); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`method="post" action="/app/collect/run"`,
		`name="csrf" value="csrf-real"`,
		`name="keywords"`,
		`name="locations"`,
		"Maximum Results / Search",
		`name="top"`,
		`id="collect-submit"`,
		"maximum 50 combinations",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("collect UI missing %q", want)
		}
	}
	if strings.Contains(out, `id="collect-submit" type="submit" disabled`) {
		t.Fatal("collector submit must be enabled")
	}
}


func TestQueueApplicationWebActionReadyEmail(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "queue-ready.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	job := &models.JobPosting{
		ID: "4467841092",
		Title: "Sales Executive",
		Company: "CeoJetset",
		URL: "https://www.linkedin.com/jobs/view/4467841092",
		ApplicationMethod: "EMAIL",
		ApplyEmail: "jobs@example.com",
		ReviewState: models.JobReviewShortlisted,
	}
	if err := st.Upsert(job); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{"csrf": {"csrf-test"}}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/4467841092/queue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "4467841092")
	rec := httptest.NewRecorder()

	ws.handleAppQueueApplication(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/app/applications/4467841092?queued=1" {
		t.Fatalf("redirect=%q", loc)
	}

	app, err := st.GetApplicationByJobID("4467841092")
	if err != nil {
		t.Fatalf("GetApplicationByJobID: %v", err)
	}
	if app == nil || app.State != models.ApplicationStateReadyEmail || app.Recipient != "jobs@example.com" {
		t.Fatalf("unexpected queued application: %+v", app)
	}
}

func TestQueueApplicationWebActionNeedReview(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "queue-review.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	job := &models.JobPosting{
		ID: "4468614708",
		Title: "Industrial Account Executive",
		Company: "Michael Page",
		URL: "https://www.linkedin.com/jobs/view/4468614708",
		ApplicationMethod: "UNKNOWN",
	}
	if err := st.Upsert(job); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	ws := &webServer{st: st, csrf: "csrf-test"}
	form := url.Values{"csrf": {"csrf-test"}}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/4468614708/queue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "4468614708")
	rec := httptest.NewRecorder()

	ws.handleAppQueueApplication(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	app, err := st.GetApplicationByJobID("4468614708")
	if err != nil {
		t.Fatalf("GetApplicationByJobID: %v", err)
	}
	if app == nil || app.State != models.ApplicationStateNeedReview || app.Recipient != "" {
		t.Fatalf("unexpected review application: %+v", app)
	}
}

func TestQueueApplicationWebActionRejectsBadCSRF(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "queue-csrf.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	ws := &webServer{st: st, csrf: "expected"}
	form := url.Values{"csrf": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/12345/queue", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "12345")
	rec := httptest.NewRecorder()

	ws.handleAppQueueApplication(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestJobDetailRendersQueueOrExistingApplication(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	job := &models.JobPosting{
		ID: "12345",
		Title: "Sales Executive",
		Company: "Example Co",
		ApplicationMethod: "EMAIL",
		ApplyEmail: "jobs@example.com",
		ReviewState: models.JobReviewShortlisted,
	}

	var fresh bytes.Buffer
	if err := tpl.Execute(&fresh, appPageData{
		Title: "Job Detail", Active: "jobs", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C",
		SelectedJob: job,
	}); err != nil {
		t.Fatalf("execute fresh detail: %v", err)
	}
	if !strings.Contains(fresh.String(), `action="/app/jobs/12345/start-application"`) ||
		!strings.Contains(fresh.String(), "Start Application") ||
		!strings.Contains(fresh.String(), "Shortlisted") {
		t.Fatalf("shortlisted job detail missing Start Application UI: %s", fresh.String())
	}

	var queued bytes.Buffer
	if err := tpl.Execute(&queued, appPageData{
		Title: "Job Detail", Active: "jobs", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C",
		SelectedJob: job,
		SelectedApplication: &models.JobApplication{JobID: "12345", State: models.ApplicationStateReadyEmail},
	}); err != nil {
		t.Fatalf("execute queued detail: %v", err)
	}
	if !strings.Contains(queued.String(), "View Application") ||
		strings.Contains(queued.String(), `action="/app/jobs/12345/start-application"`) {
		t.Fatalf("queued job detail should show View Application and no start-application action")
	}
}


func TestPrepareApplicationForUIReadyEmail(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "prepare-ui.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	job := &models.JobPosting{
		ID: "50001",
		Title: "Sales Executive",
		Company: "Example Co",
		URL: "https://www.linkedin.com/jobs/view/50001",
		ApplicationMethod: "EMAIL",
		ApplyEmail: "jobs@example.com",
		Description: "Sales operations and account management",
	}
	if err := st.Upsert(job); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(job.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}

	settings := config.ApplicationSettings{
		CandidateName: "Mochamad Fikri Afrizal",
		DefaultCVProfile: "general",
		CVProfiles: []config.CVProfileSettings{
			{ID: "general", Path: "/tmp/general.pdf", Priority: 1},
			{ID: "sales", Path: "/tmp/sales.pdf", Keywords: []string{"sales"}, Priority: 2},
		},
	}
	got, err := prepareApplicationForUI(st, job.ID, settings, "")
	if err != nil {
		t.Fatalf("prepareApplicationForUI: %v", err)
	}
	if got.State != models.ApplicationStateReadyEmail {
		t.Fatalf("state=%q want READY_EMAIL", got.State)
	}
	if got.CVProfile != "sales" {
		t.Fatalf("CV profile=%q want sales", got.CVProfile)
	}
	if !strings.Contains(got.Subject, "Sales Executive") || !strings.Contains(got.Subject, "Mochamad Fikri Afrizal") {
		t.Fatalf("unexpected subject: %q", got.Subject)
	}
	if !strings.Contains(got.Body, "Example Co") {
		t.Fatalf("unexpected body: %q", got.Body)
	}
}

func TestPrepareApplicationForUIRejectsNeedReview(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "prepare-review.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	job := &models.JobPosting{
		ID: "50002",
		Title: "Senior Sales Marketing",
		Company: "Example Co",
		URL: "https://www.linkedin.com/jobs/view/50002",
		ApplicationMethod: "LINKEDIN",
	}
	if err := st.Upsert(job); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(job.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}

	_, err = prepareApplicationForUI(st, job.ID, config.ApplicationSettings{}, "")
	if err == nil || !strings.Contains(err.Error(), "expected READY_EMAIL") {
		t.Fatalf("expected READY_EMAIL guard, got %v", err)
	}
}

func TestApplicationDetailRendersPrepareFormForReadyEmail(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	app := &models.JobApplication{
		JobID: "50003",
		State: models.ApplicationStateReadyEmail,
		Recipient: "jobs@example.com",
	}
	job := &models.JobPosting{ID: app.JobID, Title: "Sales Specialist", Company: "Example Co"}
	pd := appPageData{
		Title: "Application Detail",
		Active: "applications",
		CSRF: "csrf-prepare",
		CandidateName: "Candidate",
		CandidateInitials: "C",
		SelectedApplication: app,
		SelectedApplicationJob: job,
		CVProfiles: []appCVProfile{{ID: "general", Default: true}, {ID: "sales"}},
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, pd); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`action="/app/applications/50003/prepare"`,
		`name="csrf" value="csrf-prepare"`,
		`name="cv_profile"`,
		"Auto · match by keywords",
		"Generate Default Email",
		"does not create a Gmail draft",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prepare UI missing %q", want)
		}
	}
}

func TestApplicationDetailBlocksPrepareForNeedReview(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	app := &models.JobApplication{JobID: "50004", State: models.ApplicationStateNeedReview}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, appPageData{
		Title: "Application Detail",
		Active: "applications",
		CandidateName: "Candidate",
		CandidateInitials: "C",
		SelectedApplication: app,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "/prepare") || !strings.Contains(out, "No verified email recipient is available") {
		t.Fatalf("NEED_REVIEW should block prepare: %s", out)
	}
}


func TestJobDetailRendersEasyApplyStartApplicationAction(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	job := &models.JobPosting{
		ID: "easy-job",
		Title: "Account Executive",
		Company: "Example",
		URL: "https://www.linkedin.com/jobs/view/123456/",
		ApplicationMethod: "LINKEDIN",
		ReviewState: models.JobReviewShortlisted,
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, appPageData{
		Title: "Job Detail", Active: "jobs", CSRF: "csrf",
		CandidateName: "Candidate", CandidateInitials: "C",
		SelectedJob: job,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := out.String()
	for _, want := range []string{
		"EASY_APPLY",
		"Shortlisted",
		"Start Application",
		`action="/app/jobs/easy-job/start-application"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Easy Apply shortlisted job detail missing %q", want)
		}
	}
	for _, unwanted := range []string{"Queue Easy Apply", "Queue as NEED_REVIEW"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("pre-handoff Easy Apply job detail should not contain legacy action %q", unwanted)
		}
	}
}

func TestApplicationMethodLabelMapsLinkedInToEasyApply(t *testing.T) {
	if got := applicationMethodLabel("LINKEDIN"); got != "EASY_APPLY" {
		t.Fatalf("label=%q", got)
	}
	if got := applicationMethodLabel("EMAIL"); got != "EMAIL" {
		t.Fatalf("email label=%q", got)
	}
	j := &models.JobPosting{ApplicationMethod: "LINKEDIN"}
	if !matchesUIJob(j, "NOT_APPLIED", "", "", "EASY_APPLY", "") {
		t.Fatal("EASY_APPLY UI filter should match stored LINKEDIN method")
	}
}
