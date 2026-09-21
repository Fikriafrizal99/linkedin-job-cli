package cmd

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"linkedin-jobs/internal/models"
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
		"Jobs Collected",
		"With Email Contact",
		"In Application Pipeline",
		"Applications Sent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered app missing %q", want)
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
	for _, want := range []string{"APPROVED", "draft-123", "Send (Optional)", "disabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("application detail missing %q", want)
		}
	}
}


func TestMatchesUIJobFilters(t *testing.T) {
	j := &models.JobPosting{
		Title: "Sales Executive",
		Company: "CeoJetset",
		Location: "Jakarta, Indonesia",
		ApplicationMethod: "EMAIL",
		ApplyEmail: "jobs@example.com",
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
			for _, want := range []string{"collect-keywords", "collect-location", "collect-preview", "--posted-within"} {
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
		CollectKeywords: "Sales Executive",
		CollectLocation: "Indonesia",
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
		`name="top"`,
		`id="collect-submit"`,
		"Anonymous public collection only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("collect UI missing %q", want)
		}
	}
	if strings.Contains(out, `id="collect-submit" type="submit" disabled`) {
		t.Fatal("collector submit must be enabled")
	}
}
