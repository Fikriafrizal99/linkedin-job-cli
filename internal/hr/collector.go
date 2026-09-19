package hr

import (
	"strings"

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
