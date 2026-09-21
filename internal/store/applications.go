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
    gmail_message_id TEXT,
    gmail_thread_id TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    draft_created_at TEXT,
    reviewed_at TEXT,
    review_note TEXT,
    sent_at TEXT,
    FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_applications_state ON applications(state);
CREATE INDEX IF NOT EXISTS idx_applications_job_id ON applications(job_id);
`

func migrateApplications(db *sql.DB) error {
	if _, err := db.Exec(applicationSchema); err != nil {
		return err
	}

	rows, err := db.Query(`PRAGMA table_info(applications)`)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range []struct {
		name string
		ddl  string
	}{
		{"reviewed_at", "TEXT"},
		{"review_note", "TEXT"},
		{"gmail_message_id", "TEXT"},
		{"gmail_thread_id", "TEXT"},
	} {
		if existing[c.name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE applications ADD COLUMN %s %s", c.name, c.ddl)); err != nil {
			return err
		}
	}
	return nil
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
    WHEN applications.state IN ('DRAFT_CREATED','APPROVED','SENT') THEN applications.state
    ELSE excluded.state
  END,
  recipient=CASE
    WHEN applications.state IN ('DRAFT_CREATED','APPROVED','SENT') THEN applications.recipient
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
	switch existing.State {
	case models.ApplicationStateDraftCreated:
		return nil, fmt.Errorf("application for job %s already has a Gmail draft; revise the existing draft explicitly instead of re-preparing", jobID)
	case models.ApplicationStateApproved:
		return nil, fmt.Errorf("application for job %s is already APPROVED; unapprove it before making changes", jobID)
	case models.ApplicationStateSent:
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
	if existing.State == models.ApplicationStateDraftCreated ||
		existing.State == models.ApplicationStateApproved ||
		existing.State == models.ApplicationStateSent {
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

// ApproveApplication records an explicit human review gate. It can only
// advance a real Gmail draft from DRAFT_CREATED to APPROVED.
func (s *Store) ApproveApplication(jobID, note string) (*models.JobApplication, error) {
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
	if existing.State == models.ApplicationStateApproved {
		return existing, nil
	}
	if existing.State != models.ApplicationStateDraftCreated {
		return nil, fmt.Errorf("application state is %s; expected DRAFT_CREATED", existing.State)
	}
	if strings.TrimSpace(existing.GmailDraftID) == "" {
		return nil, fmt.Errorf("application has no Gmail draft id")
	}

	now := NowISO()
	if _, err := s.db.Exec(`
UPDATE applications
SET state=?, reviewed_at=?, review_note=?, updated_at=?, last_error=''
WHERE job_id=?
`,
		models.ApplicationStateApproved, now, strings.TrimSpace(note), now, jobID); err != nil {
		return nil, err
	}
	return s.GetApplicationByJobID(jobID)
}

// UnapproveApplication re-opens an approved draft for further human review.
// The Gmail draft remains attached; no message is sent or recreated.
func (s *Store) UnapproveApplication(jobID, note string) (*models.JobApplication, error) {
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
	if existing.State != models.ApplicationStateApproved {
		return nil, fmt.Errorf("application state is %s; expected APPROVED", existing.State)
	}

	now := NowISO()
	if _, err := s.db.Exec(`
UPDATE applications
SET state=?, reviewed_at='', review_note=?, updated_at=?
WHERE job_id=?
`,
		models.ApplicationStateDraftCreated, strings.TrimSpace(note), now, jobID); err != nil {
		return nil, err
	}
	return s.GetApplicationByJobID(jobID)
}

// MarkApplicationSent records the Gmail provider identifiers only after the
// approved draft has been explicitly sent by the external provider.
func (s *Store) MarkApplicationSent(jobID, messageID, threadID string) (*models.JobApplication, error) {
	jobID = strings.TrimSpace(jobID)
	messageID = strings.TrimSpace(messageID)
	threadID = strings.TrimSpace(threadID)
	if jobID == "" {
		return nil, fmt.Errorf("empty job id")
	}
	if messageID == "" {
		return nil, fmt.Errorf("empty Gmail message id")
	}

	existing, err := s.GetApplicationByJobID(jobID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("job %s is not queued for application", jobID)
	}
	if existing.State == models.ApplicationStateSent {
		if existing.GmailMessageID == messageID &&
			(threadID == "" || existing.GmailThreadID == threadID) {
			return existing, nil
		}
		return nil, fmt.Errorf("application for job %s is already SENT as Gmail message %s", jobID, existing.GmailMessageID)
	}
	if existing.State != models.ApplicationStateApproved {
		return nil, fmt.Errorf("application state is %s; expected APPROVED", existing.State)
	}
	if strings.TrimSpace(existing.GmailDraftID) == "" {
		return nil, fmt.Errorf("application has no Gmail draft id")
	}

	now := NowISO()
	if _, err := s.db.Exec(`
UPDATE applications
SET state=?, gmail_message_id=?, gmail_thread_id=?, sent_at=?, updated_at=?, last_error=''
WHERE job_id=?
`,
		models.ApplicationStateSent, messageID, threadID, now, now, jobID); err != nil {
		return nil, err
	}
	return s.GetApplicationByJobID(jobID)
}

func (s *Store) GetApplicationByJobID(jobID string) (*models.JobApplication, error) {
	row := s.db.QueryRow(`
SELECT id,job_id,state,COALESCE(recipient,''),COALESCE(subject,''),
       COALESCE(body,''),COALESCE(cv_profile,''),COALESCE(gmail_draft_id,''),
       COALESCE(gmail_message_id,''),COALESCE(gmail_thread_id,''),
       COALESCE(last_error,''),created_at,updated_at,
       COALESCE(draft_created_at,''),COALESCE(reviewed_at,''),
       COALESCE(review_note,''),COALESCE(sent_at,'')
FROM applications WHERE job_id=? LIMIT 1
`, jobID)
	return scanApplication(row)
}

func (s *Store) ListApplications(state string, limit int) ([]models.JobApplication, error) {
	q := `
SELECT id,job_id,state,COALESCE(recipient,''),COALESCE(subject,''),
       COALESCE(body,''),COALESCE(cv_profile,''),COALESCE(gmail_draft_id,''),
       COALESCE(gmail_message_id,''),COALESCE(gmail_thread_id,''),
       COALESCE(last_error,''),created_at,updated_at,
       COALESCE(draft_created_at,''),COALESCE(reviewed_at,''),
       COALESCE(review_note,''),COALESCE(sent_at,'')
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
		&a.Body, &a.CVProfile, &a.GmailDraftID, &a.GmailMessageID,
		&a.GmailThreadID, &a.LastError, &a.CreatedAt, &a.UpdatedAt,
		&a.DraftCreatedAt, &a.ReviewedAt,
		&a.ReviewNote, &a.SentAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}
