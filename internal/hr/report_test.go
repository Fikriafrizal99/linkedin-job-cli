package hr

import (
	"strings"
	"testing"

	"linkedin-jobs/internal/linkedin"
)

func TestPeopleSearchURL(t *testing.T) {
	cases := []struct {
		name        string
		companyID   string
		companySlug string
		titleTerms  string
		role        string
		wantSubstr  string
	}{
		{"id+terms", "105863333", "getclera", "CTO", "", "facetCurrentCompany=105863333"},
		{"id+terms encodes title", "105863333", "getclera", "Head of Talent", "", "title=Head+of+Talent"},
		{"id falls back to role when no terms", "105863333", "getclera", "", "Recruiter", "title=Recruiter"},
		{"no id uses company page", "", "getclera", "CTO", "", "/company/getclera/"},
		{"no id no slug uses home", "", "", "CTO", "", "linkedin.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := peopleSearchURL(tc.companyID, tc.companySlug, tc.titleTerms, tc.role)
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("got %q, want it to contain %q", got, tc.wantSubstr)
			}
		})
	}
}

func TestParseReport_HappyPath(t *testing.T) {
	content := `{
		"target_role": "Co-Founder & CTO",
		"reasoning": "Founding eng role; you'd work directly with the CTO.",
		"outreach_angle": "I shipped an agentic TypeScript system end to end.",
		"contacts": [
			{"name":"Daniel Wintermeyer","role":"Co-Founder & CTO","search_terms":"CTO","priority":1,"why":"You report to him."},
			{"name":"","role":"Co-Founder & CEO","search_terms":"Founder","priority":2,"why":"Co-founders decide hires."}
		]
	}`
	pr := parseReport(content)
	if pr == nil {
		t.Fatal("expected parsed report, got nil")
	}
	if pr.TargetRole != "Co-Founder & CTO" {
		t.Errorf("target_role=%q", pr.TargetRole)
	}
	if len(pr.Contacts) != 2 {
		t.Fatalf("contacts=%d want 2", len(pr.Contacts))
	}
	if pr.Contacts[0].Name != "Daniel Wintermeyer" {
		t.Errorf("first contact name=%q", pr.Contacts[0].Name)
	}
}

func TestParseReport_RejectsNonJSON(t *testing.T) {
	if pr := parseReport("just some prose with no json at all"); pr != nil {
		t.Errorf("expected nil for non-JSON, got %+v", pr)
	}
}

func TestParseReport_FencedJSON(t *testing.T) {
	content := "```json\n{\"target_role\":\"Recruiter\",\"contacts\":[{\"role\":\"Recruiter\",\"priority\":1}]}\n```"
	pr := parseReport(content)
	if pr == nil || pr.TargetRole != "Recruiter" {
		t.Fatalf("expected fenced json to parse, got %+v", pr)
	}
}

func TestApplyParsed_AttachesURLsAndOrdersByPriority(t *testing.T) {
	r := &Report{CompanyID: "105863333", CompanySlug: "getclera"}
	pr := &parsedReport{
		TargetRole: "CTO",
		Contacts: []struct {
			Name        string `json:"name"`
			Role        string `json:"role"`
			SearchTerms string `json:"search_terms"`
			Priority    int    `json:"priority"`
			Why         string `json:"why"`
		}{
			{Role: "CEO", SearchTerms: "Founder", Priority: 2},
			{Role: "CTO", SearchTerms: "CTO", Priority: 1},
		},
	}
	applyParsed(r, pr)
	if len(r.Contacts) != 2 {
		t.Fatalf("contacts=%d", len(r.Contacts))
	}
	if r.Contacts[0].Role != "CTO" {
		t.Errorf("expected CTO first, got %q", r.Contacts[0].Role)
	}
	for _, c := range r.Contacts {
		if !strings.Contains(c.SearchURL, "facetCurrentCompany=105863333") {
			t.Errorf("contact %q missing company facet in %q", c.Role, c.SearchURL)
		}
	}
}

func TestHeuristic_FoundingRole(t *testing.T) {
	ctx := &linkedin.JobContext{Title: "Founding Engineer", Description: "be engineer #4 on the founding team"}
	r := Heuristic(ctx, nil)
	if !r.LLMUsed {
		// expected
	}
	if len(r.Contacts) == 0 {
		t.Fatal("no contacts")
	}
	if !strings.Contains(strings.ToLower(r.Contacts[0].Role), "cto") && !strings.Contains(strings.ToLower(r.TargetRole), "cto") {
		t.Errorf("founding role should target CTO/founder first, got target=%q first=%q", r.TargetRole, r.Contacts[0].Role)
	}
	for _, c := range r.Contacts {
		if c.SearchURL == "" {
			t.Errorf("contact %q has no search url", c.Role)
		}
	}
}

func TestHeuristic_ManagerRole(t *testing.T) {
	ctx := &linkedin.JobContext{Title: "Engineering Manager"}
	r := Heuristic(ctx, nil)
	if !strings.Contains(strings.ToLower(r.TargetRole), "vp") && !strings.Contains(strings.ToLower(r.TargetRole), "director") && !strings.Contains(strings.ToLower(r.TargetRole), "manager") {
		t.Errorf("manager role target unexpected: %q", r.TargetRole)
	}
}

func TestHeuristic_DefaultRecruiter(t *testing.T) {
	ctx := &linkedin.JobContext{Title: "Software Engineer"}
	r := Heuristic(ctx, nil)
	if !strings.Contains(strings.ToLower(r.TargetRole), "recruiter") {
		t.Errorf("default IC role should lead with recruiter, got %q", r.TargetRole)
	}
}

func TestBaseReport_CompanyURLAndAbout(t *testing.T) {
	ctx := &linkedin.JobContext{JobID: "123", Title: "Eng", Company: "Clera", CompanySlug: "getclera", CompanyID: "105863333"}
	co := &linkedin.CompanyProfile{Slug: "getclera", Name: "Clera", Tagline: "AI talent agent"}
	r := baseReport(ctx, co)
	if r.CompanyURL != "https://www.linkedin.com/company/getclera/" {
		t.Errorf("company url=%q", r.CompanyURL)
	}
	if r.CompanyAbout != "AI talent agent" {
		t.Errorf("about=%q", r.CompanyAbout)
	}
}

func TestResolveJobID(t *testing.T) {
	cases := map[string]string{
		"https://www.linkedin.com/jobs/view/4435820129/":                                           "4435820129",
		"https://id.linkedin.com/jobs/view/industrial-account-executive-saas-at-michael-page-4469119347": "4469119347",
		"https://id.linkedin.com/jobs/view/field-sales-executive-at-t%C3%BCv-rheinland-group-4468614708":   "4468614708",
		"https://www.linkedin.com/jobs/search/?currentJobId=4435820129":                            "4435820129",
		"https://www.linkedin.com/jobs/search/?originToLandingJobPostings=4435820129%2C4435813285": "4435820129",
		"https://www.linkedin.com/jobs/view/4435820129/?currentJobId=999":                          "4435820129", // path wins
		"https://www.linkedin.com/feed/":                                                           "",
		"https://www.linkedin.com/jobs/search/?\\currentJobId\\=4435820129":                        "4435820129", // shell-escaped backslashes stripped
	}
	for in, want := range cases {
		got := linkedin.ResolveJobID(in)
		if got != want {
			t.Errorf("ResolveJobID(%q)=%q want %q", in, got, want)
		}
	}
}
