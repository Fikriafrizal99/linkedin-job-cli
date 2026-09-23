package application

import (
	"strings"
	"testing"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
)

func TestSelectCVProfileByTitleKeywords(t *testing.T) {
	settings := config.ApplicationSettings{
		DefaultCVProfile: "general",
		CVProfiles: []config.CVProfileSettings{
			{ID: "general", Path: "/cv/general.pdf"},
			{ID: "sales", Path: "/cv/sales.pdf", Keywords: []string{"sales", "account executive"}, Priority: 10},
		},
	}
	job := &models.JobPosting{Title: "Industrial Account Executive - SaaS"}
	got, err := SelectCVProfile(job, settings, "")
	if err != nil {
		t.Fatalf("SelectCVProfile: %v", err)
	}
	if got == nil || got.ID != "sales" {
		t.Fatalf("got %+v want sales", got)
	}
}

func TestSelectCVProfileFallsBackToDefault(t *testing.T) {
	settings := config.ApplicationSettings{
		DefaultCVProfile: "general",
		CVProfiles: []config.CVProfileSettings{
			{ID: "general", Path: "/cv/general.pdf"},
		},
	}
	got, err := SelectCVProfile(&models.JobPosting{Title: "Unknown Role"}, settings, "")
	if err != nil {
		t.Fatalf("SelectCVProfile: %v", err)
	}
	if got == nil || got.ID != "general" {
		t.Fatalf("got %+v want general", got)
	}
}

func TestSelectCVProfileOverride(t *testing.T) {
	settings := config.ApplicationSettings{
		CVProfiles: []config.CVProfileSettings{{ID: "sales"}, {ID: "finance"}},
	}
	got, err := SelectCVProfile(&models.JobPosting{Title: "Sales"}, settings, "finance")
	if err != nil {
		t.Fatalf("SelectCVProfile: %v", err)
	}
	if got == nil || got.ID != "finance" {
		t.Fatalf("got %+v want finance", got)
	}
}

func TestPrepareApplication(t *testing.T) {
	settings := config.ApplicationSettings{
		CandidateName:    "Example Candidate",
		DefaultCVProfile: "sales",
		CVProfiles: []config.CVProfileSettings{
			{ID: "sales", Path: "/cv/sales.pdf", Keywords: []string{"sales"}},
		},
	}
	job := &models.JobPosting{Title: "Sales Executive", Company: "Acme"}
	got, err := Prepare(job, settings, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got.CVProfile != "sales" || got.CVPath != "/cv/sales.pdf" {
		t.Fatalf("cv=%q path=%q", got.CVProfile, got.CVPath)
	}
	if got.Subject != "Application - Sales Executive - Example Candidate" {
		t.Fatalf("subject=%q", got.Subject)
	}
	if !strings.Contains(got.Body, "Sales Executive position at Acme") {
		t.Fatalf("body=%q", got.Body)
	}
}

func TestPrepareApplicationUsesRoleRelevantExperience(t *testing.T) {
	settings := config.ApplicationSettings{CandidateName: "Mochamad Fikri Afrizal"}
	job := &models.JobPosting{Title: "Business Development Executive", Company: "Example Company", Description: "Own sales pipeline, client relationships, and commercial growth."}
	got, err := Prepare(job, settings, "")
	if err != nil { t.Fatalf("Prepare: %v", err) }
	for _, want := range []string{"more than three years of experience across consumer finance", "sales execution", "pipeline", "Example Company", "Mochamad Fikri Afrizal"} {
		if !strings.Contains(got.Body, want) { t.Fatalf("generated body missing %q: %s", want, got.Body) }
	}
}
