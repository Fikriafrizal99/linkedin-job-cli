package models

// JobContact is optional enrichment attached to a collected job. A row may
// represent a concrete person when Name/LinkedInURL are known, or a role-level
// target when only Title/SearchURL are available.
type JobContact struct {
	ID          int64  `json:"id,omitempty"`
	JobID       string `json:"job_id"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title"`
	ContactType string `json:"contact_type"`
	LinkedInURL string `json:"linkedin_url,omitempty"`
	SearchURL   string `json:"search_url,omitempty"`
	Source      string `json:"source"`
	Priority    int    `json:"priority,omitempty"`
	Why         string `json:"why,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}
