package hr

import (
	"strings"
	"testing"

	"linkedin-jobs/internal/linkedin"
	"linkedin-jobs/internal/models"
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

func TestBestPeopleCandidateMatchesRoleAndSkipsUsed(t *testing.T) {
	target := collectorTarget{Title: "Sales Manager / Hiring Manager", SearchTerms: "Sales Manager"}
	candidates := []linkedin.PeopleSearchCandidate{
		{Name: "Wrong Person", Headline: "Finance Manager", ProfileURL: "https://www.linkedin.com/in/wrong/"},
		{Name: "Right Person", Headline: "Regional Sales Manager", ProfileURL: "https://www.linkedin.com/in/right/"},
	}
	best, ok := bestPeopleCandidate(candidates, target, map[string]bool{})
	if !ok || best.Name != "Right Person" {
		t.Fatalf("best=%+v ok=%v", best, ok)
	}

	_, ok = bestPeopleCandidate(candidates, target, map[string]bool{"https://www.linkedin.com/in/right/": true})
	if ok {
		t.Fatal("used profile should not be selected again")
	}
}

func TestRoleMatchScoreRejectsGenericOnlyMatch(t *testing.T) {
	if got := roleMatchScore("Finance Manager", "Sales Manager"); got != 0 {
		t.Fatalf("generic manager-only match should be rejected, got %d", got)
	}
	if got := roleMatchScore("Regional Sales Manager", "Sales Manager"); got == 0 {
		t.Fatal("sales manager should match")
	}
	if got := roleMatchScore("Senior Talent Acquisition Partner", "Talent Acquisition"); got == 0 {
		t.Fatal("talent acquisition should match")
	}
}

func TestBestPeopleCandidateRejectsLinkedInMemberPlaceholder(t *testing.T) {
	target := collectorTarget{Title: "Talent Acquisition / Recruiter", SearchTerms: "Talent Acquisition"}
	candidates := []linkedin.PeopleSearchCandidate{
		{Name: "LinkedIn Member", Headline: "Talent Acquisition", ProfileURL: "https://www.linkedin.com/in/hidden/"},
	}
	if _, ok := bestPeopleCandidate(candidates, target, map[string]bool{}); ok {
		t.Fatal("placeholder profile should not resolve")
	}
}

func TestPreserveResolvedContactsKeepsExistingProfile(t *testing.T) {
	fresh := []models.JobContact{
		{JobID: "1", Title: "Talent Acquisition / Recruiter", ContactType: ContactTypeTalentAcquisition, Source: "heuristic"},
	}
	existing := []models.JobContact{
		{JobID: "1", Name: "Jane Doe", Title: "Senior Talent Acquisition Partner", ContactType: ContactTypeTalentAcquisition, LinkedInURL: "https://www.linkedin.com/in/jane-doe/", Source: "linkedin_voyager"},
	}
	got := PreserveResolvedContacts(fresh, existing)
	if got[0].Name != "Jane Doe" || got[0].LinkedInURL == "" || got[0].Source != "linkedin_voyager" {
		t.Fatalf("resolved profile not preserved: %+v", got[0])
	}
}

func TestPreserveResolvedContactsFreshResolutionWins(t *testing.T) {
	fresh := []models.JobContact{
		{JobID: "1", Name: "New Person", Title: "Talent Acquisition Manager", ContactType: ContactTypeTalentAcquisition, LinkedInURL: "https://www.linkedin.com/in/new/", Source: "linkedin_voyager"},
	}
	existing := []models.JobContact{
		{JobID: "1", Name: "Old Person", Title: "Recruiter", ContactType: ContactTypeTalentAcquisition, LinkedInURL: "https://www.linkedin.com/in/old/", Source: "linkedin_voyager"},
	}
	got := PreserveResolvedContacts(fresh, existing)
	if got[0].Name != "New Person" {
		t.Fatalf("fresh resolution should win: %+v", got[0])
	}
}
