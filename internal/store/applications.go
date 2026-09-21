package store

import (
	"database/sql"
	"fmt"
	"strings"

	"linkedin-jobs/internal/models"
)

const applicationSchema = `
CREATE TABLE IF NOT EXISTS applications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL,
    recipient TEXT,
    subject TEXT,
    body TEXT,
    cv_profile TEXT,
    gmail_draft_id TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    draft_created_at TEXT,
    sent_at TEXT,
    FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_applications_state ON applications(state);
CREATE INDEX IF NOT EXISTS idx_applications_job_id ON applications(job_id);
`

func migrateApplications(db *sql.DB) error {
	_, err := db.Exec(applicationSchema)
	return err
}

// QueueApplication creates or refreshes one application lifecycle record from
// the collector-owned job metadata. Existing draft/sent states are preserved.
func (s *Store) QueueApplication(jobID string) (*models.JobApplication, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("empty job id")
	}
	j, err := s.Get(jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, fmt.Errorf("job %s not found", jobID)
	}

	state := models.ApplicationStateNeedReview
	recipient := ""
	if strings.EqualFold(strings.TrimSpace(j.ApplicationMethod), "EMAIL") && strings.TrimSpace(j.ApplyEmail) != "" {
		state = models.ApplicationStateReadyEmail
		recipient = strings.TrimSpace(j.ApplyEmail)
	}

	now := NowISO()
	_, err = s.db.Exec(`
INSERT INTO applications
  (job_id,state,recipient,created_at,updated_at)
VALUES (?,?,?,?,?)
ON CONFLICT(job_id) DO UPDATE SET
  state=CASE
    WHEN applications.state IN ('DRAFT_CREATED','SENT') THEN applications.state
    ELSE excluded.state
  END,
  recipient=CASE
    WHEN applications.state IN ('DRAFT_CREATED','SENT') THEN applications.recipient
    ELSE excluded.recipient
  END,
  updated_at=excluded.updated_at
`, jobID, state, recipient, now, now)
	if err != nil {
		return nil, err
	}
	return s.GetApplicationByJobID(jobID)
}

// SaveApplicationPreparation stores deterministic draft content while keeping
// lifecycle state unchanged. Only queued applications may be prepared.
func (s *Store) SaveApplicationPreparation(jobID, subject, body, cvProfile string) (*models.JobApplication, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("empty job id")
	}
	existing, err := s.GetApplicationByJobID(jobID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("job %s is not queued for application", jobID)
	}
	if existing.State == models.ApplicationStateSent {
		return nil, fmt.Errorf("application for job %s is already SENT", jobID)
	}

	now := NowISO()
	if _, err := s.db.Exec(`
UPDATE applications
SET subject=?, body=?, cv_profile=?, last_error='', updated_at=?
WHERE job_id=?
`, strings.TrimSpace(subject), strings.TrimSpace(body), strings.TrimSpace(cvProfile), now, jobID); err != nil {
		return nil, err
	}
	return s.GetApplicationByJobID(jobID)
}

// MarkApplicationDraftCreated records the provider draft id and advances the
// lifecycle only after an external Gmail draft creation succeeded.
func (s *Store) MarkApplicationDraftCreated(jobID, draftID string) (*models.JobApplication, error) {
	jobID = strings.TrimSpace(jobID)
	draftID = strings.TrimSpace(draftID)
	if jobID == "" {
		return nil, fmt.Errorf("empty job id")
	}
	if draftID == "" {
		return nil, fmt.Errorf("empty Gmail draft id")
	}

	existing, err := s.GetApplicationByJobID(jobID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("job %s is not queued for application", jobID)
	}
	if existing.State == models.ApplicationStateSent {
		return nil, fmt.Errorf("application for job %s is already SENT", jobID)
	}
	if existing.State == models.ApplicationStateDraftCreated {
		if existing.GmailDraftID == draftID {
			return existing, nil
		}
		return nil, fmt.Errorf("application for job %s already references Gmail draft %s", jobID, existing.GmailDraftID)
	}
	if existing.State != models.ApplicationStateReadyEmail {
		return nil, fmt.Errorf("application state is %s; expected READY_EMAIL", existing.State)
	}
	if strings.TrimSpace(existing.Recipient) == "" ||
		strings.TrimSpace(existing.Subject) == "" ||
		strings.TrimSpace(existing.Body) == "" ||
		strings.TrimSpace(existing.CVProfile) == "" {
		return nil, fmt.Errorf("application is not fully prepared")
	}

	now := NowISO()
	if _, err := s.db.Exec(`
UPDATE applications
SET state=?, gmail_draft_id=?, draft_created_at=?, updated_at=?, last_error=''
WHERE job_id=?
`,
		models.ApplicationStateDraftCreated, draftID, now, now, jobID); err != nil {
		return nil, err
	}
	return s.GetApplicationByJobID(jobID)
}

// RecordApplicationDraftError stores a provider error without advancing state.
func (s *Store) RecordApplicationDraftError(jobID, message string) (*models.JobApplication, error) {
	existing, err := s.GetApplicationByJobID(strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("job %s is not queued for application", jobID)
	}
	if existing.State == models.ApplicationStateDraftCreated || existing.State == models.ApplicationStateSent {
		return existing, nil
	}
	now := NowISO()
	if _, err := s.db.Exec(`
UPDATE applications SET last_error=?, updated_at=? WHERE job_id=?
`, strings.TrimSpace(message), now, strings.TrimSpace(jobID)); err != nil {
		return nil, err
	}
	return s.GetApplicationByJobID(strings.TrimSpace(jobID))
}

func (s *Store) GetApplicationByJobID(jobID string) (*models.JobApplication, error) {
	row := s.db.QueryRow(`
SELECT id,job_id,state,COALESCE(recipient,''),COALESCE(subject,''),
       COALESCE(body,''),COALESCE(cv_profile,''),COALESCE(gmail_draft_id,''),
       COALESCE(last_error,''),created_at,updated_at,
       COALESCE(draft_created_at,''),COALESCE(sent_at,'')
FROM applications WHERE job_id=? LIMIT 1
`, jobID)
	return scanApplication(row)
}

func (s *Store) ListApplications(state string, limit int) ([]models.JobApplication, error) {
	q := `
SELECT id,job_id,state,COALESCE(recipient,''),COALESCE(subject,''),
       COALESCE(body,''),COALESCE(cv_profile,''),COALESCE(gmail_draft_id,''),
       COALESCE(last_error,''),created_at,updated_at,
       COALESCE(draft_created_at,''),COALESCE(sent_at,'')
FROM applications WHERE 1=1
`
	var args []interface{}
	if strings.TrimSpace(state) != "" {
		q += " AND state=?"
		args = append(args, strings.TrimSpace(state))
	}
	q += " ORDER BY updated_at DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.JobApplication
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		if a != nil {
			out = append(out, *a)
		}
	}
	return out, rows.Err()
}

func scanApplication(row scanner) (*models.JobApplication, error) {
	var a models.JobApplication
	if err := row.Scan(
		&a.ID, &a.JobID, &a.State, &a.Recipient, &a.Subject,
		&a.Body, &a.CVProfile, &a.GmailDraftID, &a.LastError,
		&a.CreatedAt, &a.UpdatedAt, &a.DraftCreatedAt, &a.SentAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}
