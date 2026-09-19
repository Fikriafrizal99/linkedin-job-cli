package hr

import (
	"strings"
	"testing"

	"linkedin-jobs/internal/linkedin"
)

func TestCollectorContactsSalesRole(t *testing.T) {
	ctx := &linkedin.JobContext{
		JobID: "1",
		Title: "Senior Account Executive",
		Company: "Acme",
		CompanyID: "123",
		CompanySlug: "acme",
		Description: "Own enterprise sales pipeline and close new business.",
	}
	got := CollectorContacts(ctx, nil)
	if len(got) != 3 {
		t.Fatalf("got %d contacts, want 3", len(got))
	}
	if got[0].ContactType != ContactTypeTalentAcquisition {
		t.Fatalf("first contact type=%q", got[0].ContactType)
	}
	if !strings.Contains(strings.ToLower(got[1].Title), "sales manager") {
		t.Fatalf("sales role should target sales manager, got %q", got[1].Title)
	}
	if strings.Contains(strings.ToLower(got[1].Title), "engineering") {
		t.Fatalf("sales role incorrectly got engineering target: %q", got[1].Title)
	}
	for _, c := range got {
		if c.SearchURL == "" || !strings.Contains(c.SearchURL, "facetCurrentCompany=123") {
			t.Fatalf("missing company-scoped search URL: %+v", c)
		}
	}
}

func TestCollectorContactsTechnicalRole(t *testing.T) {
	ctx := &linkedin.JobContext{
		JobID: "2",
		Title: "SAP ABAP Developer",
		Description: "Develop SAP ABAP reports and integrations.",
	}
	got := CollectorContacts(ctx, nil)
	if len(got) != 3 {
		t.Fatalf("got %d contacts", len(got))
	}
	if got[0].ContactType != ContactTypeTalentAcquisition {
		t.Fatalf("first contact=%+v", got[0])
	}
	if got[1].ContactType != ContactTypeHiringManager {
		t.Fatalf("second contact=%+v", got[1])
	}
}

func TestCollectorContactsManagerRoleTargetsDepartmentLeaderFirst(t *testing.T) {
	ctx := &linkedin.JobContext{JobID: "3", Title: "Sales Manager"}
	got := CollectorContacts(ctx, nil)
	if len(got) == 0 || got[0].ContactType != ContactTypeDepartmentLeader {
		t.Fatalf("manager role should target department leader first: %+v", got)
	}
}

func TestCollectorContactsDoesNotGuessNames(t *testing.T) {
	ctx := &linkedin.JobContext{JobID: "4", Title: "Sales Executive"}
	for _, c := range CollectorContacts(ctx, nil) {
		if c.Name != "" {
			t.Fatalf("collector heuristic guessed a person name: %+v", c)
		}
		if c.LinkedInURL != "" {
			t.Fatalf("role-level enrichment should not invent a profile URL: %+v", c)
		}
	}
}
