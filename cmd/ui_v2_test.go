package cmd

import (
	"bytes"
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
