package hr

import (
	"fmt"
	"strings"
	"time"

	"linkedin-jobs/internal/linkedin"
	"linkedin-jobs/internal/models"
)

const (
	ContactTypeRecruiter        = "RECRUITER"
	ContactTypeTalentAcquisition = "TALENT_ACQUISITION"
	ContactTypeHR               = "HR"
	ContactTypeHiringManager    = "HIRING_MANAGER"
	ContactTypeDepartmentLeader = "DEPARTMENT_LEADER"
	ContactTypeExecutive        = "EXECUTIVE"
	ContactTypeFounder          = "FOUNDER"
)

// CollectorContacts returns deterministic, role-level contact enrichment for a
// stored job. It does not guess person names and does not perform outreach.
func CollectorContacts(ctx *linkedin.JobContext, co *linkedin.CompanyProfile) []models.JobContact {
	if ctx == nil {
		return nil
	}

	targets := collectorTargets(ctx)
	out := make([]models.JobContact, 0, len(targets))
	for i, t := range targets {
		out = append(out, models.JobContact{
			JobID:       ctx.JobID,
			Title:       t.Title,
			ContactType: t.Type,
			SearchURL:   peopleSearchURL(ctx.CompanyID, ctx.CompanySlug, t.SearchTerms, t.Title),
			Source:      "heuristic",
			Priority:    i + 1,
			Why:         t.Why,
		})
	}
	return out
}

type collectorTarget struct {
	Title       string
	Type        string
	SearchTerms string
	Why         string
}

// ContactResolution is the outcome of authenticated contact resolution.
// Contacts always contains the role-level fallbacks; resolved profiles replace
// those rows only when LinkedIn returns a sufficiently relevant current-company
// person.
type ContactResolution struct {
	Contacts []models.JobContact
	Resolved int
	Warnings []string
}

// ResolveCollectorContacts upgrades deterministic role targets into concrete
// LinkedIn profiles using an authenticated, company-scoped people search.
// It never guesses names: unresolved targets stay as role-level heuristic rows.
func ResolveCollectorContacts(client *linkedin.Client, ctx *linkedin.JobContext, co *linkedin.CompanyProfile, maxResults int, delaySeconds float64) ContactResolution {
	base := CollectorContacts(ctx, co)
	result := ContactResolution{Contacts: base}
	if client == nil || ctx == nil {
		return result
	}
	if strings.TrimSpace(ctx.CompanyID) == "" {
		result.Warnings = append(result.Warnings, "company has no LinkedIn company id; keeping role-level contacts")
		return result
	}
	if maxResults < 1 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}
	if delaySeconds < 0 {
		delaySeconds = 0
	}

	targets := collectorTargets(ctx)
	usedProfiles := map[string]bool{}
	for i, target := range targets {
		candidates, err := client.SearchPeopleAtCompany(ctx.CompanyID, target.SearchTerms, maxResults)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", target.Title, err))
		} else if best, ok := bestPeopleCandidate(candidates, target, usedProfiles); ok {
			resolvedTitle := strings.TrimSpace(best.Headline)
			if resolvedTitle == "" {
				resolvedTitle = target.Title
			}
			result.Contacts[i].Name = best.Name
			result.Contacts[i].Title = resolvedTitle
			result.Contacts[i].LinkedInURL = best.ProfileURL
			result.Contacts[i].Source = "linkedin_voyager"
			usedProfiles[best.ProfileURL] = true
			result.Resolved++
		}
		if i < len(targets)-1 && delaySeconds > 0 {
			time.Sleep(time.Duration(delaySeconds * float64(time.Second)))
		}
	}
	return result
}

func bestPeopleCandidate(candidates []linkedin.PeopleSearchCandidate, target collectorTarget, used map[string]bool) (linkedin.PeopleSearchCandidate, bool) {
	bestScore := 0
	var best linkedin.PeopleSearchCandidate
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Name) == "" || strings.EqualFold(strings.TrimSpace(candidate.Name), "LinkedIn Member") {
			continue
		}
		if candidate.ProfileURL == "" || used[candidate.ProfileURL] {
			continue
		}
		score := roleMatchScore(candidate.Headline, target.SearchTerms)
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}
	return best, bestScore > 0
}

func roleMatchScore(headline, searchTerms string) int {
	h := strings.ToLower(strings.TrimSpace(headline))
	q := strings.ToLower(strings.TrimSpace(searchTerms))
	if h == "" || q == "" {
		return 0
	}
	score := 0
	if strings.Contains(h, q) {
		score += 10
	}
	generic := map[string]bool{
		"manager": true, "head": true, "director": true, "vp": true,
		"lead": true, "hiring": true, "team": true,
	}
	meaningful := 0
	matchedMeaningful := 0
	for _, token := range strings.Fields(q) {
		token = strings.Trim(token, " /-&.,()")
		if len(token) < 3 {
			continue
		}
		if !generic[token] {
			meaningful++
		}
		if strings.Contains(h, token) {
			score++
			if !generic[token] {
				matchedMeaningful++
			}
		}
	}
	if meaningful > 0 && matchedMeaningful == 0 && score < 10 {
		return 0
	}
	return score
}

func collectorTargets(ctx *linkedin.JobContext) []collectorTarget {
	blob := strings.ToLower(ctx.Title + " " + ctx.Description + " " + ctx.Seniority)

	if isFounding(ctx) {
		return []collectorTarget{
			{Title: "Founder / Functional Leader", Type: ContactTypeFounder, SearchTerms: "Founder", Why: "Founding roles are commonly owned directly by company founders or the functional leader."},
			{Title: "Hiring Manager", Type: ContactTypeHiringManager, SearchTerms: "Hiring Manager", Why: "The direct hiring owner can validate role fit and team needs."},
			{Title: "Talent Acquisition / Recruiter", Type: ContactTypeTalentAcquisition, SearchTerms: "Talent Acquisition", Why: "Talent teams coordinate the formal recruiting process when one exists."},
		}
	}

	if isManagerLevel(ctx) {
		dept, terms := departmentLeaderFor(blob)
		return []collectorTarget{
			{Title: dept, Type: ContactTypeDepartmentLeader, SearchTerms: terms, Why: "Management roles are typically owned by the next-level functional leader."},
			{Title: "Talent Acquisition / Recruiter", Type: ContactTypeTalentAcquisition, SearchTerms: "Talent Acquisition", Why: "Talent acquisition can confirm process ownership and route the application."},
			{Title: "HR / HRBP", Type: ContactTypeHR, SearchTerms: "HRBP", Why: "HR or HRBP may support the hiring process for management-level roles."},
		}
	}

	switch {
	case containsAny(blob, "sales", "account executive", "business development", "relationship manager", "funding", "commercial"):
		return []collectorTarget{
			{Title: "Talent Acquisition / Recruiter", Type: ContactTypeTalentAcquisition, SearchTerms: "Talent Acquisition", Why: "Recruiting teams are a reliable first contact for commercial hiring processes."},
			{Title: "Sales Manager / Hiring Manager", Type: ContactTypeHiringManager, SearchTerms: "Sales Manager", Why: "The sales manager is likely to own day-to-day performance expectations for the role."},
			{Title: "Head of Sales / Sales Director", Type: ContactTypeDepartmentLeader, SearchTerms: "Head of Sales", Why: "The functional sales leader can be relevant for team fit and headcount ownership."},
		}
	case containsAny(blob, "sap", "abap", "software", "developer", "engineer", "technical", "data", "cloud", "devops"):
		return []collectorTarget{
			{Title: "Talent Acquisition / Technical Recruiter", Type: ContactTypeTalentAcquisition, SearchTerms: "Technical Recruiter", Why: "Technical recruiting commonly coordinates the first stage for specialist roles."},
			{Title: "Technical Hiring Manager / Team Lead", Type: ContactTypeHiringManager, SearchTerms: "Engineering Manager", Why: "The technical hiring manager owns team-level requirements and practical fit."},
			{Title: "Department Head / Director", Type: ContactTypeDepartmentLeader, SearchTerms: "Director", Why: "The functional leader may own headcount and hiring priorities for the team."},
		}
	case containsAny(blob, "finance", "credit", "collection", "risk", "banking", "operations"):
		return []collectorTarget{
			{Title: "Talent Acquisition / Recruiter", Type: ContactTypeTalentAcquisition, SearchTerms: "Talent Acquisition", Why: "Recruiting teams usually coordinate the formal hiring process."},
			{Title: "Functional Hiring Manager", Type: ContactTypeHiringManager, SearchTerms: "Manager", Why: "The relevant manager is likely to own daily responsibilities and candidate fit."},
			{Title: "Department Head / Regional Leader", Type: ContactTypeDepartmentLeader, SearchTerms: "Head", Why: "The department or regional leader may own headcount and final functional alignment."},
		}
	default:
		return []collectorTarget{
			{Title: "Talent Acquisition / Recruiter", Type: ContactTypeTalentAcquisition, SearchTerms: "Talent Acquisition", Why: "Recruiting teams are the safest general first contact for a published vacancy."},
			{Title: "Hiring Manager", Type: ContactTypeHiringManager, SearchTerms: "Manager", Why: "The hiring manager owns role requirements and team fit."},
			{Title: "Department Head / Director", Type: ContactTypeDepartmentLeader, SearchTerms: "Director", Why: "The functional leader may own headcount and hiring priorities."},
		}
	}
}

func departmentLeaderFor(blob string) (title, terms string) {
	switch {
	case containsAny(blob, "sales", "account", "business development", "commercial", "relationship manager"):
		return "Sales Director / VP Sales", "Sales Director"
	case containsAny(blob, "sap", "abap", "software", "engineering", "developer", "technical", "data"):
		return "Director / VP Technology", "Director Engineering"
	case containsAny(blob, "finance", "credit", "collection", "risk", "banking"):
		return "Department Head / Regional Leader", "Head"
	default:
		return "Department Head / Director", "Director"
	}
}

func containsAny(s string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}
