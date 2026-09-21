package models

const (
	ApplicationStateReadyEmail   = "READY_EMAIL"
	ApplicationStateNeedReview   = "NEED_REVIEW"
	ApplicationStateDraftCreated = "DRAFT_CREATED"
	ApplicationStateApproved     = "APPROVED"
	ApplicationStateSent         = "SENT"
	ApplicationStateFailed       = "FAILED"
)

// JobApplication is the Application Engine's lifecycle record for one collected
// job. Collector fields remain in jobs; execution state lives here.
type JobApplication struct {
	ID             int64  `json:"id,omitempty"`
	JobID          string `json:"job_id"`
	State          string `json:"state"`
	Recipient      string `json:"recipient,omitempty"`
	Subject        string `json:"subject,omitempty"`
	Body           string `json:"body,omitempty"`
	CVProfile      string `json:"cv_profile,omitempty"`
	GmailDraftID   string `json:"gmail_draft_id,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	DraftCreatedAt string `json:"draft_created_at,omitempty"`
	ReviewedAt     string `json:"reviewed_at,omitempty"`
	ReviewNote     string `json:"review_note,omitempty"`
	SentAt         string `json:"sent_at,omitempty"`
}
