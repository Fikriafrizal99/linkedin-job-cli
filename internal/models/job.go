package models

import (
	"fmt"
	"strings"
)

// JobPosting mirrors the LinkedIn job record persisted in SQLite. Field names
// line up with the database columns (snake_case via explicit SQL).
type JobPosting struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Company        string   `json:"company,omitempty"`
	Location       string   `json:"location,omitempty"`
	URL            string   `json:"url"`
	SalaryRaw      string   `json:"salary_raw,omitempty"`
	SalaryLow      *float64 `json:"salary_low,omitempty"`
	SalaryHigh     *float64 `json:"salary_high,omitempty"`
	SalaryCurrency string   `json:"salary_currency,omitempty"`
	// SalarySource records where the parsed salary came from, driving display
	// confidence: "description" (authoritative, green) vs "badge"/"estimated"
	// (low confidence, amber). "" means unknown/pre-feature.
	SalarySource string `json:"salary_source,omitempty"`
	Description  string `json:"description,omitempty"`
	// ShortDescription is the LLM-generated tight summary of Description, shown
	// in the UI instead of the full body. Empty until enrichment runs.
	ShortDescription string `json:"short_description,omitempty"`
	Summary      string `json:"summary,omitempty"`
	LLMSummary   string `json:"llm_summary,omitempty"`
	RemoteType   string `json:"remote_type,omitempty"`
	Status       string `json:"status,omitempty"`
	Notes        string `json:"notes,omitempty"`
	Source       string `json:"source,omitempty"`    // "recommended" | "search"
	ListedAt     int64  `json:"listed_at,omitempty"` // epoch ms (legacy/upstream)
	PostedAt     string `json:"posted_at,omitempty"`
	PostedAtEstimated bool `json:"posted_at_estimated,omitempty"`
	SearchedAt   string `json:"searched_at,omitempty"`
	FetchedAt    string `json:"fetched_at,omitempty"`
	FirstSeen    string `json:"first_seen,omitempty"`
	LastSeen     string `json:"last_seen,omitempty"`
	ScrapedAt    string `json:"scraped_at,omitempty"`

	// Collector-owned application metadata extracted deterministically from the
	// posting. Empty values mean the posting did not explicitly provide them.
	ApplyEmail             string   `json:"apply_email,omitempty"`
	ApplyEmails            []string `json:"apply_emails,omitempty"`
	ApplyURL               string   `json:"apply_url,omitempty"`
	ApplicationMethod      string   `json:"application_method,omitempty"`
	ApplicationInstruction string   `json:"application_instruction,omitempty"`
	DetailStatus           string   `json:"detail_status,omitempty"`

	// Persistent user triage decision. This is intentionally independent from
	// application lifecycle state: Jobs owns pursue/do-not-pursue intent while
	// applications owns execution progress.
	ReviewState  string `json:"review_state,omitempty"`
	ReviewReason string `json:"review_reason,omitempty"`
	ReviewedAt   string `json:"reviewed_at,omitempty"`

	// Structured enrichment (LLM-extracted). Zero values mean "not enriched."
	CompanyOverview string `json:"company_overview,omitempty"`
	Industry        string `json:"industry,omitempty"`
	TechStack       string `json:"tech_stack,omitempty"`
	Seniority       string `json:"seniority,omitempty"`
	EmploymentType  string `json:"employment_type,omitempty"`
	YearsExperience *int   `json:"years_experience,omitempty"`
	CompanySizeBand string `json:"company_size_band,omitempty"`
	CompanyStage    string `json:"company_stage,omitempty"`
	IsFoundingRole bool   `json:"is_founding_role,omitempty"`
	EnrichedAt     string `json:"enriched_at,omitempty"`

	// Fit scoring against the user's rubric set.
	FitScore  *int   `json:"fit_score,omitempty"` // 0-100, nil = unscored
	FitReason string `json:"fit_reason,omitempty"`
	ScoredAt  string `json:"scored_at,omitempty"`
	// RubricScores holds a JSON array of per-rubric {id, kind, rating, weight,
	// reason} for this job. The fit_score column holds the normalized weighted
	// average; this column carries the breakdown. Loose JSON by design — the
	// rubric set is user-generated, so there is no fixed column per rubric.
	RubricScores string `json:"rubric_scores,omitempty"`

	// LLM-free dedup fingerprints and structural duplicate classification.
	ContentHash             string `json:"content_hash,omitempty"`
	StructuralHash          string `json:"structural_hash,omitempty"`
	DuplicateClassification string `json:"duplicate_classification,omitempty"`
	DuplicateOfJobID        string `json:"duplicate_of_job_id,omitempty"`
}

const (
	JobReviewUnreviewed = "UNREVIEWED"
	JobReviewShortlisted = "SHORTLISTED"
	JobReviewLater = "LATER"
	JobReviewSkipped = "SKIPPED"
)

// NormalizeJobReviewState canonicalizes and validates a persisted job triage
// state. The boolean is false for unknown values.
func NormalizeJobReviewState(state string) (string, bool) {
	state = strings.ToUpper(strings.TrimSpace(state))
	switch state {
	case JobReviewUnreviewed, JobReviewShortlisted, JobReviewLater, JobReviewSkipped:
		return state, true
	default:
		return "", false
	}
}

// IsEnriched reports whether structured enrichment has run for this job.
func (j *JobPosting) IsEnriched() bool { return j.EnrichedAt != "" }

// DetectArrangement determines the job's work arrangement from its location and
// remote_type fields. Returns one of "remote", "hybrid", "onsite", or "" (unknown).
// Detection priority is hybrid > remote > onsite — a blob containing both
// "remote" and "hybrid" resolves to "hybrid" because hybrid is the more specific
// arrangement mode. Matches "on-site" and "on site" as well as "onsite".
func (j *JobPosting) DetectArrangement() string {
	blob := strings.ToLower(j.Location + " " + j.RemoteType)
	if strings.Contains(blob, "hybrid") {
		return "hybrid"
	}
	if strings.Contains(blob, "remote") {
		return "remote"
	}
	if strings.Contains(blob, "onsite") || strings.Contains(blob, "on-site") || strings.Contains(blob, "on site") {
		return "onsite"
	}
	return ""
}

// IsFiltered reports whether the hard preference filter marked this job a mismatch.
func (j *JobPosting) IsFiltered() bool { return j.Status == "filtered" }

// HasSalary reports whether any numeric salary was parsed.
func (j *JobPosting) HasSalary() bool {
	return j.SalaryHigh != nil
}

// SalarySourceDescription marks a salary parsed from the job description body —
// the authoritative, localized band. All other origins are treated as estimates.
const SalarySourceDescription = "description"

// SalarySourceBadge marks a salary scraped from LinkedIn's page-chrome salary
// badge. It is a low-confidence, frequently rounded/generic band the employer
// may not have stated directly, so it is surfaced in the UI as "est. salary".
const SalarySourceBadge = "badge"

// IsSalaryEstimated reports whether the salary is low-confidence: it came from
// the page badge or another heuristic rather than the description body.
func (j *JobPosting) IsSalaryEstimated() bool {
	return j.HasSalary() && j.SalarySource != SalarySourceDescription
}

// SalaryMax returns the highest parsed salary figure, or 0 if none.
func (j *JobPosting) SalaryMax() float64 {
	if j.SalaryHigh != nil {
		return *j.SalaryHigh
	}
	if j.SalaryLow != nil {
		return *j.SalaryLow
	}
	return 0
}

// SalaryDisplay renders a human-readable salary string.
func (j *JobPosting) SalaryDisplay() string {
	cur := j.SalaryCurrency
	if cur == "" {
		cur = "USD"
	}
	if j.SalaryLow != nil && j.SalaryHigh != nil {
		return fmt.Sprintf("%s$%s – $%s", cur, comma(*j.SalaryLow), comma(*j.SalaryHigh))
	}
	if j.SalaryHigh != nil {
		return fmt.Sprintf("%s$%s", cur, comma(*j.SalaryHigh))
	}
	if j.SalaryLow != nil {
		return fmt.Sprintf("%s$%s", cur, comma(*j.SalaryLow))
	}
	if j.SalaryRaw != "" {
		return j.SalaryRaw
	}
	return "N/A"
}

func comma(f float64) string {
	n := int64(f)
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		s = s[1:]
	}
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += ","
		}
		out += string(c)
	}
	if n < 0 {
		out = "-" + out
	}
	return out
}
