package store

import (
	"database/sql"
	"fmt"

	"linkedin-jobs/internal/models"
)

const contactSchema = `
CREATE TABLE IF NOT EXISTS job_contacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    name TEXT,
    title TEXT NOT NULL,
    contact_type TEXT NOT NULL,
    linkedin_url TEXT,
    search_url TEXT,
    source TEXT NOT NULL,
    priority INTEGER DEFAULT 0,
    why TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_contacts_job_id ON job_contacts(job_id);
CREATE INDEX IF NOT EXISTS idx_job_contacts_type ON job_contacts(contact_type);
`

func migrateContacts(db *sql.DB) error {
	_, err := db.Exec(contactSchema)
	return err
}

// ReplaceJobContacts atomically replaces enrichment contacts for one job.
// Collection data in jobs is never modified.
func (s *Store) ReplaceJobContacts(jobID string, contacts []models.JobContact) error {
	if jobID == "" {
		return fmt.Errorf("empty job id")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM job_contacts WHERE job_id=?`, jobID); err != nil {
		return err
	}
	for i := range contacts {
		c := contacts[i]
		if c.Title == "" || c.ContactType == "" {
			continue
		}
		createdAt := c.CreatedAt
		if createdAt == "" {
			createdAt = NowISO()
		}
		if c.Source == "" {
			c.Source = "heuristic"
		}
		if _, err := tx.Exec(`
INSERT INTO job_contacts
  (job_id,name,title,contact_type,linkedin_url,search_url,source,priority,why,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
			jobID, c.Name, c.Title, c.ContactType, c.LinkedInURL, c.SearchURL,
			c.Source, c.Priority, c.Why, createdAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListJobContacts returns persisted enrichment ordered by priority.
func (s *Store) ListJobContacts(jobID string) ([]models.JobContact, error) {
	rows, err := s.db.Query(`
SELECT id,job_id,COALESCE(name,''),title,contact_type,
       COALESCE(linkedin_url,''),COALESCE(search_url,''),source,
       COALESCE(priority,0),COALESCE(why,''),created_at
FROM job_contacts
WHERE job_id=?
ORDER BY CASE WHEN priority=0 THEN 999999 ELSE priority END, id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.JobContact
	for rows.Next() {
		var c models.JobContact
		if err := rows.Scan(&c.ID, &c.JobID, &c.Name, &c.Title, &c.ContactType,
			&c.LinkedInURL, &c.SearchURL, &c.Source, &c.Priority, &c.Why, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountJobContacts returns the enrichment count for one job.
func (s *Store) CountJobContacts(jobID string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM job_contacts WHERE job_id=?`, jobID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
