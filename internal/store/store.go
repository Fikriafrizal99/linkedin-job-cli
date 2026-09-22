package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/salary"
)

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    company TEXT,
    location TEXT,
    url TEXT NOT NULL,
    salary_raw TEXT,
    salary_low REAL,
    salary_high REAL,
    salary_currency TEXT,
    salary_source TEXT,
    description TEXT,
    short_description TEXT,
    summary TEXT,
    llm_summary TEXT,
    remote_type TEXT,
    status TEXT DEFAULT 'new',
    notes TEXT,
    source TEXT,
    listed_at INTEGER,
    searched_at TEXT NOT NULL,
    fetched_at TEXT,
    posted_at TEXT,
    posted_at_estimated INTEGER DEFAULT 0,
    first_seen TEXT,
    last_seen TEXT,
    scraped_at TEXT,
    apply_email TEXT,
    apply_emails TEXT,
    apply_url TEXT,
    application_method TEXT,
    application_instruction TEXT,
    detail_status TEXT,
    review_state TEXT NOT NULL DEFAULT 'UNREVIEWED',
    review_reason TEXT,
    reviewed_at TEXT,
    company_overview TEXT,
    industry TEXT,
    tech_stack TEXT,
    seniority TEXT,
    employment_type TEXT,
    years_experience INTEGER,
    company_size_band TEXT,
    company_stage TEXT,
    is_founding_role INTEGER DEFAULT 0,
    fit_score INTEGER,
    fit_reason TEXT,
    content_hash TEXT,
    structural_hash TEXT,
    duplicate_classification TEXT,
    duplicate_of_job_id TEXT,
    enriched_at TEXT,
    scored_at TEXT,
    rubric_scores TEXT
);
CREATE INDEX IF NOT EXISTS idx_jobs_company ON jobs(company);
CREATE INDEX IF NOT EXISTS idx_jobs_salary_high ON jobs(salary_high);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_source ON jobs(source);
CREATE VIRTUAL TABLE IF NOT EXISTS jobs_fts USING fts5(id UNINDEXED, title, company, description);
`

// addColumns lists columns added by the fit-engine migration that must be
// ALTER-TABLE-added onto pre-existing databases. Fresh databases get them from
// the schema above; this list heals DBs created by older binary versions.
// (typeDDL must be valid after "ADD COLUMN".)
var addColumns = []struct {
	name    string
	typeDDL string
}{
	{"company_overview", "TEXT"},
	{"industry", "TEXT"},
	{"tech_stack", "TEXT"},
	{"seniority", "TEXT"},
	{"employment_type", "TEXT"},
	{"years_experience", "INTEGER"},
	{"company_size_band", "TEXT"},
	{"company_stage", "TEXT"},
	{"is_founding_role", "INTEGER DEFAULT 0"},
	{"fit_score", "INTEGER"},
	{"fit_reason", "TEXT"},
	{"content_hash", "TEXT"},
	{"structural_hash", "TEXT"},
	{"duplicate_classification", "TEXT"},
	{"duplicate_of_job_id", "TEXT"},
	{"enriched_at", "TEXT"},
	{"scored_at", "TEXT"},
	{"salary_source", "TEXT"},
	{"rubric_scores", "TEXT"},
	{"short_description", "TEXT"},
	{"posted_at", "TEXT"},
	{"posted_at_estimated", "INTEGER DEFAULT 0"},
	{"first_seen", "TEXT"},
	{"last_seen", "TEXT"},
	{"scraped_at", "TEXT"},
	{"apply_email", "TEXT"},
	{"apply_emails", "TEXT"},
	{"apply_url", "TEXT"},
	{"application_method", "TEXT"},
	{"application_instruction", "TEXT"},
	{"detail_status", "TEXT"},
	{"review_state", "TEXT NOT NULL DEFAULT 'UNREVIEWED'"},
	{"review_reason", "TEXT"},
	{"reviewed_at", "TEXT"},
}

// Store is the SQLite persistence layer.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // avoid SQLITE_BUSY under concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateContacts(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateApplications(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := backfillJobReviewState(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := backfillSalarySource(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := backfillStructuralMetadata(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate adds any fit-engine columns missing from a pre-existing database.
// Idempotent: queries PRAGMA table_info once and only ALTER-TABLE-adds columns
// that are absent, so re-opening an already-migrated DB is a no-op.
func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(jobs)`)
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
	for _, c := range addColumns {
		if existing[c.name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE jobs ADD COLUMN %s %s", c.name, c.typeDDL)); err != nil {
			return err
		}
	}
	// Indexes on migrated columns (created here so they only run once the
	// columns exist on pre-existing databases; on fresh DBs the columns already
	// exist, so these are no-ops via IF NOT EXISTS).
	for _, idx := range []string{
		`CREATE INDEX IF NOT EXISTS idx_jobs_content_hash ON jobs(content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_structural_hash ON jobs(structural_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_fit_score ON jobs(fit_score)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_application_method ON jobs(application_method)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_apply_email ON jobs(apply_email)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_review_state ON jobs(review_state)`,
	} {
		if _, err := db.Exec(idx); err != nil {
			return err
		}
	}
	return nil
}

// backfillJobReviewState initializes the decision layer after the applications
// table exists. Jobs already in the application lifecycle are treated as
// SHORTLISTED; every other legacy job remains UNREVIEWED. The update is
// idempotent and never overwrites an explicit non-UNREVIEWED decision.
func backfillJobReviewState(db *sql.DB) error {
	if _, err := db.Exec(`UPDATE jobs SET review_state=?
		WHERE review_state IS NULL OR TRIM(review_state)=''`, models.JobReviewUnreviewed); err != nil {
		return err
	}
	_, err := db.Exec(`
UPDATE jobs
SET review_state=?,
    reviewed_at=COALESCE(NULLIF(reviewed_at,''),
        (SELECT created_at FROM applications WHERE applications.job_id=jobs.id LIMIT 1))
WHERE review_state=?
  AND EXISTS (SELECT 1 FROM applications WHERE applications.job_id=jobs.id)
`, models.JobReviewShortlisted, models.JobReviewUnreviewed)
	return err
}

// backfillSalarySource infers the origin of pre-existing parsed salaries so the
// UI can show confidence (description = authoritative, else estimated). Runs
// once per DB: only rows with an empty salary_source are considered. A salary
// is tagged "description" when its stored low/high/currency exactly match the
// authoritative range parsed from the stored description; otherwise it is tagged
// "badge" (low confidence). Rows with no salary are left untouched.
func backfillSalarySource(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, salary_low, salary_high, COALESCE(salary_currency,''), COALESCE(description,'') FROM jobs WHERE salary_source IS NULL OR salary_source=''`)
	if err != nil {
		return err
	}
	type rec struct {
		id        string
		low, high sql.NullFloat64
		cur, desc string
	}
	var recs []rec
	for rows.Next() {
		var r rec
		var cur, desc sql.NullString
		if err := rows.Scan(&r.id, &r.low, &r.high, &cur, &desc); err != nil {
			rows.Close()
			return err
		}
		r.cur, r.desc = cur.String, desc.String
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range recs {
		if !r.high.Valid {
			continue // no salary -> nothing to attribute
		}
		source := models.SalarySourceBadge // default for pre-feature data: low confidence
		if r.low.Valid {
			if s := salary.InDescription(r.desc); s != nil && s.Low != nil && s.High != nil &&
				*s.Low == r.low.Float64 && *s.High == r.high.Float64 &&
				strings.EqualFold(s.Currency, r.cur) {
				source = models.SalarySourceDescription
			}
		}
		if _, err := db.Exec(`UPDATE jobs SET salary_source=? WHERE id=?`, source, r.id); err != nil {
			return err
		}
	}
	return nil
}

// backfillStructuralMetadata prepares databases created before structural
// duplicate tracking existed. It is idempotent and does not require re-fetching
// LinkedIn details.
func backfillStructuralMetadata(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, COALESCE(company,''), COALESCE(title,''), COALESCE(description,'')
		FROM jobs WHERE structural_hash IS NULL OR structural_hash=''`)
	if err != nil {
		return err
	}
	type rec struct{ id, company, title, description string }
	var pending []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.company, &r.title, &r.description); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range pending {
		h := StructuralHash(r.company, r.title, r.description)
		if _, err := db.Exec(`UPDATE jobs SET structural_hash=?,
			duplicate_classification=COALESCE(NULLIF(duplicate_classification,''), ?)
			WHERE id=?`, h, DuplicateNew, r.id); err != nil {
			return err
		}
	}
	_, err = db.Exec(`UPDATE jobs SET duplicate_classification=?
		WHERE duplicate_classification IS NULL OR duplicate_classification=''`, DuplicateNew)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Upsert inserts or updates a job, preserving llm_summary/status/notes when
// the incoming fields are empty.
func (s *Store) Upsert(j *models.JobPosting) error {
	now := NowISO()
	if j.SearchedAt == "" {
		j.SearchedAt = now
	}
	if j.FirstSeen == "" {
		j.FirstSeen = j.SearchedAt
	}
	if j.LastSeen == "" {
		j.LastSeen = now
	}
	if j.ScrapedAt == "" {
		j.ScrapedAt = now
	}

	applyEmails := ""
	if len(j.ApplyEmails) > 0 {
		if b, err := json.Marshal(j.ApplyEmails); err == nil {
			applyEmails = string(b)
		}
	}
	_, err := s.db.Exec(`
INSERT INTO jobs (id,title,company,location,url,salary_raw,salary_low,salary_high,
  salary_currency,salary_source,description,summary,remote_type,status,notes,source,listed_at,
  searched_at,fetched_at,posted_at,posted_at_estimated,first_seen,last_seen,scraped_at,
  apply_email,apply_emails,apply_url,application_method,application_instruction,detail_status,
  llm_summary,content_hash,structural_hash,duplicate_classification,duplicate_of_job_id)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  title=excluded.title,
  company=COALESCE(NULLIF(excluded.company,''), jobs.company),
  location=COALESCE(NULLIF(excluded.location,''), jobs.location),
  url=excluded.url,
  salary_raw=COALESCE(NULLIF(excluded.salary_raw,''), jobs.salary_raw),
  salary_low=COALESCE(excluded.salary_low, jobs.salary_low),
  salary_high=COALESCE(excluded.salary_high, jobs.salary_high),
  salary_currency=COALESCE(NULLIF(excluded.salary_currency,''), jobs.salary_currency),
  salary_source=COALESCE(NULLIF(excluded.salary_source,''), jobs.salary_source),
  description=COALESCE(NULLIF(excluded.description,''), jobs.description),
  remote_type=COALESCE(NULLIF(excluded.remote_type,''), jobs.remote_type),
  source=COALESCE(NULLIF(excluded.source,''), jobs.source),
  listed_at=COALESCE(NULLIF(excluded.listed_at,0), jobs.listed_at),
  searched_at=excluded.searched_at,
  fetched_at=COALESCE(NULLIF(excluded.fetched_at,''), jobs.fetched_at),
  posted_at=COALESCE(NULLIF(excluded.posted_at,''), jobs.posted_at),
  posted_at_estimated=CASE WHEN excluded.posted_at_estimated!=0 THEN excluded.posted_at_estimated ELSE jobs.posted_at_estimated END,
  first_seen=COALESCE(NULLIF(jobs.first_seen,''), excluded.first_seen),
  last_seen=COALESCE(NULLIF(excluded.last_seen,''), jobs.last_seen),
  scraped_at=COALESCE(NULLIF(excluded.scraped_at,''), jobs.scraped_at),
  apply_email=COALESCE(NULLIF(excluded.apply_email,''), jobs.apply_email),
  apply_emails=CASE WHEN excluded.apply_emails IS NOT NULL AND excluded.apply_emails!='' AND excluded.apply_emails!='[]' THEN excluded.apply_emails ELSE jobs.apply_emails END,
  apply_url=COALESCE(NULLIF(excluded.apply_url,''), jobs.apply_url),
  application_method=COALESCE(NULLIF(excluded.application_method,''), jobs.application_method),
  application_instruction=COALESCE(NULLIF(excluded.application_instruction,''), jobs.application_instruction),
  detail_status=COALESCE(NULLIF(excluded.detail_status,''), jobs.detail_status),
  llm_summary=COALESCE(NULLIF(excluded.llm_summary,''), jobs.llm_summary),
  content_hash=COALESCE(NULLIF(excluded.content_hash,''), jobs.content_hash),
  structural_hash=COALESCE(NULLIF(excluded.structural_hash,''), jobs.structural_hash),
  duplicate_classification=COALESCE(NULLIF(excluded.duplicate_classification,''), jobs.duplicate_classification),
  duplicate_of_job_id=COALESCE(NULLIF(excluded.duplicate_of_job_id,''), jobs.duplicate_of_job_id)`,
		j.ID, j.Title, j.Company, j.Location, j.URL, j.SalaryRaw,
		nullFloat(j.SalaryLow), nullFloat(j.SalaryHigh), j.SalaryCurrency, j.SalarySource,
		j.Description, j.Summary, j.RemoteType, statusOrDefault(j.Status), j.Notes,
		j.Source, j.ListedAt, j.SearchedAt, j.FetchedAt, j.PostedAt, boolInt(j.PostedAtEstimated),
		j.FirstSeen, j.LastSeen, j.ScrapedAt, j.ApplyEmail, applyEmails, j.ApplyURL,
		j.ApplicationMethod, j.ApplicationInstruction, j.DetailStatus, j.LLMSummary, j.ContentHash,
		j.StructuralHash, j.DuplicateClassification, j.DuplicateOfJobID)
	if err != nil {
		return err
	}
	// keep FTS in sync
	if _, err := s.db.Exec(`DELETE FROM jobs_fts WHERE id=?`, j.ID); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT INTO jobs_fts(id,title,company,description) VALUES (?,?,?,?)`,
		j.ID, j.Title, j.Company, j.Description); err != nil {
		return err
	}
	return nil
}

// UpdateDetail sets salary + description + fetched_at for a job.
func (s *Store) UpdateDetail(id, salaryRaw, cur, desc, fetchedAt string, low, high *float64) error {
	_, err := s.db.Exec(`
UPDATE jobs SET salary_raw=COALESCE(NULLIF(?, ''), salary_raw),
  salary_low=COALESCE(?, salary_low), salary_high=COALESCE(?, salary_high),
  salary_currency=COALESCE(NULLIF(?, ''), salary_currency),
  description=COALESCE(NULLIF(?, ''), description),
  fetched_at=COALESCE(NULLIF(?, ''), fetched_at)
WHERE id=?`,
		salaryRaw, nullFloat(low), nullFloat(high), cur, desc, fetchedAt, id)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM jobs_fts WHERE id=?`, id); err == nil {
		var title, company, description string
		s.db.QueryRow(`SELECT title,company,description FROM jobs WHERE id=?`, id).Scan(&title, &company, &description)
		s.db.Exec(`INSERT INTO jobs_fts(id,title,company,description) VALUES (?,?,?,?)`, id, title, company, description)
	}
	return nil
}

// SetTag updates status and notes for a job.
func (s *Store) SetTag(id, status, notes string) error {
	if status == "" && notes == "" {
		return nil
	}
	if status != "" {
		if _, err := s.db.Exec(`UPDATE jobs SET status=? WHERE id=?`, status, id); err != nil {
			return err
		}
	}
	if notes != "" {
		if _, err := s.db.Exec(`UPDATE jobs SET notes=? WHERE id=?`, notes, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureReviewStateCompatibleWithApplication(id, state string) error {
	if state == models.JobReviewShortlisted {
		return nil
	}
	var appState string
	err := s.db.QueryRow(`SELECT state FROM applications WHERE job_id=?`, id).Scan(&appState)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("job %s has application state %s; remove the application before moving the job to %s", id, appState, state)
}

// SetJobReviewState persists one deliberate user triage decision. Re-collection
// never calls this method, so subsequent Upsert operations preserve the choice.
func (s *Store) SetJobReviewState(id, state, reason string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("empty job id")
	}
	state, ok := models.NormalizeJobReviewState(state)
	if !ok {
		return fmt.Errorf("invalid job review state %q", state)
	}
	if err := s.ensureReviewStateCompatibleWithApplication(id, state); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	reviewedAt := NowISO()
	if state == models.JobReviewUnreviewed {
		reason = ""
		reviewedAt = ""
	}
	res, err := s.db.Exec(`UPDATE jobs SET review_state=?, review_reason=?, reviewed_at=? WHERE id=?`,
		state, reason, reviewedAt, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("job %s not found", id)
	}
	return nil
}

// BulkSetJobReviewState applies one triage decision to a set of jobs in a
// single transaction. IDs are trimmed and deduplicated. Any missing ID rolls
// back the batch so the caller never gets a partially-applied decision.
func (s *Store) BulkSetJobReviewState(ids []string, state, reason string) (int, error) {
	state, ok := models.NormalizeJobReviewState(state)
	if !ok {
		return 0, fmt.Errorf("invalid job review state %q", state)
	}
	reason = strings.TrimSpace(reason)
	reviewedAt := NowISO()
	if state == models.JobReviewUnreviewed {
		reason = ""
		reviewedAt = ""
	}

	seen := map[string]bool{}
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return 0, nil
	}
	for _, id := range clean {
		if err := s.ensureReviewStateCompatibleWithApplication(id, state); err != nil {
			return 0, err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, id := range clean {
		res, err := tx.Exec(`UPDATE jobs SET review_state=?, review_reason=?, reviewed_at=? WHERE id=?`,
			state, reason, reviewedAt, id)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return 0, fmt.Errorf("job %s not found", id)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(clean), nil
}

// CountJobsByReviewState returns the number of jobs in one validated triage
// state. It is used by the upcoming Inbox/Shortlisted/Later/Skipped counters.
func (s *Store) CountJobsByReviewState(state string) (int64, error) {
	state, ok := models.NormalizeJobReviewState(state)
	if !ok {
		return 0, fmt.Errorf("invalid job review state %q", state)
	}
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE review_state=?`, state).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// SetEnrichmentAndScore persists the LLM-extracted structured fields for a job,
// stamping enriched_at and scored_at. remote_type is refined only when the LLM
// returned a non-empty work arrangement (so it never clobbers an existing
// DetectRemote value with empty). The fit score + per-rubric breakdown is
// written separately by SetScore after the rubric composer runs.
func (s *Store) SetEnrichmentAndScore(id string, e models.Enrichment) error {
	now := NowISO()
	var years interface{}
	if e.YearsExperience != nil {
		years = *e.YearsExperience
	}
	founding := 0
	if e.IsFoundingRole {
		founding = 1
	}
	_, err := s.db.Exec(`
UPDATE jobs SET
  short_description=COALESCE(NULLIF(?, ''), short_description),
  company_overview=?, industry=?, tech_stack=?, seniority=?, employment_type=?,
  years_experience=COALESCE(?, years_experience), company_size_band=?, company_stage=?,
  is_founding_role=?,
  remote_type=COALESCE(NULLIF(?, ''), remote_type),
  enriched_at=?, scored_at=?
WHERE id=?`,
		e.ShortDescription,
		e.CompanyOverview, e.Industry, e.TechStack, e.Seniority, e.EmploymentType,
		years, e.CompanySizeBand, e.CompanyStage, founding,
		e.WorkArrangement, now, now, id)
	return err
}

// SetSalaryFromDescription hard-overrides the salary columns with a
// description-sourced range. Used by the pipeline when the LLM extracted a
// salary from the description body that the strict text-extraction regex
// missed — description data is authoritative over the LinkedIn page-chrome
// badge, so the override is unconditional on the columns it writes. Currency
// is preserved when cur is empty (the caller inherited nothing), so an
// existing non-empty currency (e.g. a USD default) is not blanked.
func (s *Store) SetSalaryFromDescription(id string, low, high *float64, cur, raw string) error {
	_, err := s.db.Exec(`
UPDATE jobs SET
  salary_low=?, salary_high=?,
  salary_currency=COALESCE(NULLIF(?, ''), salary_currency),
  salary_raw=COALESCE(NULLIF(?, ''), salary_raw),
  salary_source=?
WHERE id=?`,
		nullFloat(low), nullFloat(high), cur, raw, models.SalarySourceDescription, id)
	return err
}

// SetScore writes the rubric-computed fit_score, machine-generated fit_reason,
// and the per-rubric breakdown JSON (rubricScores) for a job. Stamps scored_at.
// Called by the pipeline after the rubric composer runs.
func (s *Store) SetScore(id string, score int, fitReason, rubricScores string) error {
	_, err := s.db.Exec(`UPDATE jobs SET fit_score=?, fit_reason=?, rubric_scores=?, scored_at=? WHERE id=?`,
		score, fitReason, rubricScores, NowISO(), id)
	return err
}

// MarkViewed transitions a job from "new" to "viewed" only. Any other status is
// left untouched. Used when a user opens a job's posting from the web UI.
func (s *Store) MarkViewed(id string) error {
	_, err := s.db.Exec(`UPDATE jobs SET status='viewed' WHERE id=? AND status='new'`, id)
	return err
}

// SetFetchedTimes refreshes fetch metadata for a job already known (used when a
// re-fetched job is recognized as a duplicate and we skip re-processing).
func (s *Store) SetFetchedTimes(id, searchedAt, fetchedAt string) error {
	_, err := s.db.Exec(`UPDATE jobs SET searched_at=?, fetched_at=COALESCE(NULLIF(?, ''), fetched_at) WHERE id=?`,
		searchedAt, fetchedAt, id)
	return err
}

// TouchSeen records that an already-known LinkedIn job appeared in a new
// collection run without forcing an expensive detail re-fetch.
func (s *Store) TouchSeen(id, seenAt string) error {
	if seenAt == "" {
		seenAt = NowISO()
	}
	_, err := s.db.Exec(`UPDATE jobs SET last_seen=?, searched_at=? WHERE id=?`, seenAt, seenAt, id)
	return err
}

// FindByContentHash returns a job matching the dedup fingerprint, or nil.
func (s *Store) FindByContentHash(hash string) (*models.JobPosting, error) {
	if hash == "" {
		return nil, nil
	}
	row := s.db.QueryRow(jobCols+` FROM jobs WHERE content_hash=? LIMIT 1`, hash)
	return scanJob(row)
}

// FindByStructuralHash returns an earlier job with the same structural
// fingerprint, excluding excludeID when provided.
func (s *Store) FindByStructuralHash(hash, excludeID string) (*models.JobPosting, error) {
	if hash == "" {
		return nil, nil
	}
	if excludeID != "" {
		row := s.db.QueryRow(jobCols+` FROM jobs WHERE structural_hash=? AND id<>? ORDER BY COALESCE(first_seen,searched_at) ASC LIMIT 1`, hash, excludeID)
		return scanJob(row)
	}
	row := s.db.QueryRow(jobCols+` FROM jobs WHERE structural_hash=? ORDER BY COALESCE(first_seen,searched_at) ASC LIMIT 1`, hash)
	return scanJob(row)
}

// Unscored returns non-filtered jobs that have not been scored, for re-scoring
// after a profile edit. (Enriched jobs that predate scoring, plus new jobs.)
func (s *Store) Unscored() ([]*models.JobPosting, error) {
	rows, err := s.db.Query(jobCols + ` FROM jobs WHERE scored_at IS NULL AND status!='filtered'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// Get returns one job by id.
func (s *Store) Get(id string) (*models.JobPosting, error) {
	row := s.db.QueryRow(jobCols+` FROM jobs WHERE id=?`, id)
	return scanJob(row)
}

// Exists returns the set of job ids already stored (for the search new-only pre-filter).
func (s *Store) ExistingIDs(ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	q, args := inQuery(ids)
	rows, err := s.db.Query(`SELECT id FROM jobs WHERE id IN (`+q+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Filters selects jobs from the local DB.
type Filters struct {
	MinSalary         float64 // 0 = no filter (raw numeric, applied in SQL)
	MinSalaryCurrency string  // "" = use MinSalary as a raw SQL floor; else skip SQL floor and filter in Go (FX-aware)
	HasSalary         bool
	Company           string
	Title             string
	Location          string
	Remote            bool
	Hybrid            bool
	Onsite            bool
	Status            string
	Source            string
	HasEmail          bool
	NoEmail           bool
	ReviewState       string
	MinScore          int  // 0 = no score filter
	SortByScore       bool // order by fit_score desc instead of salary
	SortBySearched    bool // order by searched_at desc (newest first); overrides SortByScore
	Limit             int
	// SinceSearched filters to jobs first stored (searched_at) on or after the
	// given RFC3339 instant. Compared as a string because searched_at is stored
	// as ISO 8601 UTC, which sorts chronologically.
	SinceSearched string
}

// List returns jobs matching the filters, newest salary first.
func (s *Store) List(f Filters) ([]*models.JobPosting, error) {
	q := jobCols + ` FROM jobs WHERE 1=1`
	var args []interface{}
	// When a currency is set the salary floor is FX-converted in Go (the DB
	// can't do cross-currency math), so don't apply a raw SQL predicate here.
	if f.MinSalary > 0 && f.MinSalaryCurrency == "" {
		q += ` AND salary_high >= ?`
		args = append(args, f.MinSalary)
	}
	if f.HasSalary {
		q += ` AND salary_high IS NOT NULL`
	}
	if f.Company != "" {
		q += ` AND LOWER(company) LIKE ?`
		args = append(args, "%"+toLowerCase(f.Company)+"%")
	}
	if f.Title != "" {
		q += ` AND LOWER(title) LIKE ?`
		args = append(args, "%"+toLowerCase(f.Title)+"%")
	}
	if f.Location != "" {
		q += ` AND LOWER(location) LIKE ?`
		args = append(args, "%"+toLowerCase(f.Location)+"%")
	}
	if f.Remote || f.Hybrid || f.Onsite {
		// OR: when multiple flags are set, a job matching any token passes.
		// On-site is normalized to "onsite" in remote_type, but raw location
		// text often carries the hyphenated "On-site", so both forms are matched.
		var ors []string
		if f.Remote {
			ors = append(ors, `LOWER(COALESCE(location,'') || ' ' || COALESCE(remote_type,'')) LIKE '%remote%'`)
		}
		if f.Hybrid {
			ors = append(ors, `LOWER(COALESCE(location,'') || ' ' || COALESCE(remote_type,'')) LIKE '%hybrid%'`)
		}
		if f.Onsite {
			ors = append(ors, `LOWER(COALESCE(location,'') || ' ' || COALESCE(remote_type,'')) LIKE '%on-site%' OR LOWER(COALESCE(location,'') || ' ' || COALESCE(remote_type,'')) LIKE '%onsite%'`)
		}
		q += " AND (" + strings.Join(ors, " OR ") + ")"
	}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	if f.Source != "" {
		q += ` AND source=?`
		args = append(args, f.Source)
	}
	if f.HasEmail {
		q += ` AND COALESCE(apply_email,'')!=''`
	}
	if f.NoEmail {
		q += ` AND COALESCE(apply_email,'')=''`
	}
	if f.ReviewState != "" {
		state, ok := models.NormalizeJobReviewState(f.ReviewState)
		if !ok {
			return nil, fmt.Errorf("invalid job review state %q", f.ReviewState)
		}
		q += ` AND review_state=?`
		args = append(args, state)
	}
	if f.MinScore > 0 {
		q += ` AND fit_score>=?`
		args = append(args, f.MinScore)
	}
	if f.SinceSearched != "" {
		q += ` AND searched_at>=?`
		args = append(args, f.SinceSearched)
	}
	switch {
	case f.SortBySearched:
		q += ` ORDER BY searched_at DESC`
	case f.SortByScore:
		q += ` ORDER BY fit_score DESC`
	default:
		q += ` ORDER BY salary_high DESC`
	}
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// SearchFTS runs an offline full-text query over stored jobs.
func (s *Store) SearchFTS(expr string, limit int) ([]*models.JobPosting, error) {
	q := `SELECT j.id,j.title,j.company,j.location,j.url,j.salary_raw,j.salary_low,j.salary_high,
  j.salary_currency,j.salary_source,j.description,j.short_description,j.summary,j.llm_summary,j.remote_type,j.status,j.notes,j.source,j.listed_at,
  j.searched_at,j.fetched_at,j.posted_at,j.posted_at_estimated,j.first_seen,j.last_seen,j.scraped_at,
  j.apply_email,j.apply_emails,j.apply_url,j.application_method,j.application_instruction,j.detail_status,
  j.review_state,j.review_reason,j.reviewed_at,
  j.company_overview,j.industry,j.tech_stack,j.seniority,j.employment_type,
  j.years_experience,j.company_size_band,j.company_stage,j.is_founding_role,j.fit_score,
  j.fit_reason,j.content_hash,j.structural_hash,j.duplicate_classification,j.duplicate_of_job_id,j.enriched_at,j.scored_at,
  j.rubric_scores FROM jobs j JOIN (SELECT id, rank FROM jobs_fts WHERE jobs_fts MATCH ?`
	args := []interface{}{expr}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	q += `) f ON f.id = j.id ORDER BY f.rank`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// Stats holds aggregate stats over the DB.
type Stats struct {
	Total       int            `json:"total"`
	WithSalary  int            `json:"with_salary"`
	ByStatus    map[string]int `json:"by_status"`
	BySource    map[string]int `json:"by_source"`
	ByCompany   []CountItem    `json:"top_companies"`
	SalaryBands map[string]int `json:"salary_bands"`
	RemoteCount int            `json:"remote"`
}

// CountItem is a label/count pair.
type CountItem struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Stats computes aggregate stats. topCompaniesLimit bounds the top-companies
// ranking (0 falls back to a sane default).
func (s *Store) Stats(topCompaniesLimit int) (*Stats, error) {
	if topCompaniesLimit <= 0 {
		topCompaniesLimit = 50
	}
	st := &Stats{ByStatus: map[string]int{}, BySource: map[string]int{}, SalaryBands: map[string]int{}}
	s.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&st.Total)
	s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE salary_high IS NOT NULL`).Scan(&st.WithSalary)
	s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE LOWER(COALESCE(location,'')||' '||COALESCE(remote_type,'')) LIKE '%remote%'`).Scan(&st.RemoteCount)

	sr, _ := s.db.Query(`SELECT COALESCE(status,'new'), COUNT(*) FROM jobs GROUP BY status`)
	if sr != nil {
		for sr.Next() {
			var k string
			var c int
			sr.Scan(&k, &c)
			st.ByStatus[k] = c
		}
		sr.Close()
	}
	sr, _ = s.db.Query(`SELECT COALESCE(source,''), COUNT(*) FROM jobs GROUP BY source`)
	if sr != nil {
		for sr.Next() {
			var k string
			var c int
			sr.Scan(&k, &c)
			st.BySource[k] = c
		}
		sr.Close()
	}
	sr, _ = s.db.Query(`SELECT COALESCE(company,'Unknown'), COUNT(*) c FROM jobs GROUP BY company ORDER BY c DESC LIMIT ?`, topCompaniesLimit)
	if sr != nil {
		for sr.Next() {
			var k string
			var c int
			sr.Scan(&k, &c)
			st.ByCompany = append(st.ByCompany, CountItem{k, c})
		}
		sr.Close()
	}
	sr, _ = s.db.Query(`SELECT CAST(salary_high/50000 AS INT)*50000 AS band, COUNT(*) FROM jobs WHERE salary_high IS NOT NULL GROUP BY band ORDER BY band`)
	if sr != nil {
		for sr.Next() {
			var band, c int
			sr.Scan(&band, &c)
			st.SalaryBands[fmt.Sprintf("%d-%d", band, band+49999)] = c
		}
		sr.Close()
	}
	return st, nil
}

// Count returns the total number of jobs in the database.
func (s *Store) Count() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CountFiltered returns the number of jobs tagged status='filtered'.
func (s *Store) CountFiltered() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status='filtered'`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// DeleteAll removes all jobs (and FTS). Returns count removed.
func (s *Store) DeleteAll() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM jobs`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	s.db.Exec(`DELETE FROM jobs_fts`)
	return n, nil
}

// DeleteFiltered removes every job tagged status='filtered' (and its FTS
// entry). FTS rows are deleted first, while the jobs table still holds the IDs
// to select against. Returns count removed.
func (s *Store) DeleteFiltered() (int64, error) {
	if _, err := s.db.Exec(`DELETE FROM jobs_fts WHERE id IN (SELECT id FROM jobs WHERE status='filtered')`); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`DELETE FROM jobs WHERE status='filtered'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Delete removes a single job and its FTS entry by id.
func (s *Store) Delete(id string) error {
	if _, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM jobs_fts WHERE id=?`, id); err != nil {
		return err
	}
	return nil
}

// --- helpers ---

const jobCols = `SELECT id,title,company,location,url,salary_raw,salary_low,salary_high,
  salary_currency,salary_source,description,short_description,summary,llm_summary,remote_type,status,notes,source,listed_at,
  searched_at,fetched_at,posted_at,posted_at_estimated,first_seen,last_seen,scraped_at,
  apply_email,apply_emails,apply_url,application_method,application_instruction,detail_status,
  review_state,review_reason,reviewed_at,
  company_overview,industry,tech_stack,seniority,employment_type,
  years_experience,company_size_band,company_stage,is_founding_role,fit_score,
  fit_reason,content_hash,structural_hash,duplicate_classification,duplicate_of_job_id,enriched_at,scored_at,
  rubric_scores`

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanJob(row scanner) (*models.JobPosting, error) {
	j := &models.JobPosting{}
	var sl, sh sql.NullFloat64
	var company, location, salaryRaw, cur, salarySource, desc, shortDesc, summary, llm, remote, status, notes, source, fetched sql.NullString
	var postedAt, firstSeen, lastSeen, scrapedAt, applyEmail, applyEmails, applyURL, applicationMethod, applicationInstruction, detailStatus sql.NullString
	var reviewState, reviewReason, reviewedAt sql.NullString
	var listed, postedAtEstimated sql.NullInt64
	var companyOverview, industry, techStack, seniority, employmentType, companySizeBand, companyStage, fitReason, contentHash, structuralHash, duplicateClassification, duplicateOfJobID, enrichedAt, scoredAt sql.NullString
	var rubricScores sql.NullString
	var yearsExp, isFounding, fitScore sql.NullInt64
	if err := row.Scan(&j.ID, &j.Title, &company, &location, &j.URL, &salaryRaw, &sl, &sh,
		&cur, &salarySource, &desc, &shortDesc, &summary, &llm, &remote, &status, &notes, &source, &listed, &j.SearchedAt, &fetched,
		&postedAt, &postedAtEstimated, &firstSeen, &lastSeen, &scrapedAt,
		&applyEmail, &applyEmails, &applyURL, &applicationMethod, &applicationInstruction, &detailStatus,
		&reviewState, &reviewReason, &reviewedAt,
		&companyOverview, &industry, &techStack, &seniority, &employmentType, &yearsExp,
		&companySizeBand, &companyStage, &isFounding, &fitScore,
		&fitReason, &contentHash, &structuralHash, &duplicateClassification, &duplicateOfJobID, &enrichedAt, &scoredAt,
		&rubricScores); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	j.Company = company.String
	j.Location = location.String
	j.SalaryRaw = salaryRaw.String
	if sl.Valid {
		v := sl.Float64
		j.SalaryLow = &v
	}
	if sh.Valid {
		v := sh.Float64
		j.SalaryHigh = &v
	}
	j.SalaryCurrency = cur.String
	j.SalarySource = salarySource.String
	j.Description = desc.String
	j.ShortDescription = shortDesc.String
	j.Summary = summary.String
	j.LLMSummary = llm.String
	j.RemoteType = remote.String
	j.Status = status.String
	j.Notes = notes.String
	j.Source = source.String
	j.ListedAt = listed.Int64
	j.FetchedAt = fetched.String
	j.PostedAt = postedAt.String
	j.PostedAtEstimated = postedAtEstimated.Valid && postedAtEstimated.Int64 != 0
	j.FirstSeen = firstSeen.String
	j.LastSeen = lastSeen.String
	j.ScrapedAt = scrapedAt.String
	j.ApplyEmail = applyEmail.String
	if applyEmails.String != "" {
		_ = json.Unmarshal([]byte(applyEmails.String), &j.ApplyEmails)
	}
	j.ApplyURL = applyURL.String
	j.ApplicationMethod = applicationMethod.String
	j.ApplicationInstruction = applicationInstruction.String
	j.DetailStatus = detailStatus.String
	j.ReviewState = reviewState.String
	if j.ReviewState == "" {
		j.ReviewState = models.JobReviewUnreviewed
	}
	j.ReviewReason = reviewReason.String
	j.ReviewedAt = reviewedAt.String
	if j.Status == "" {
		j.Status = "new"
	}
	j.CompanyOverview = companyOverview.String
	j.Industry = industry.String
	j.TechStack = techStack.String
	j.Seniority = seniority.String
	j.EmploymentType = employmentType.String
	if yearsExp.Valid {
		v := int(yearsExp.Int64)
		j.YearsExperience = &v
	}
	j.CompanySizeBand = companySizeBand.String
	j.CompanyStage = companyStage.String
	j.IsFoundingRole = isFounding.Valid && isFounding.Int64 != 0
	if fitScore.Valid {
		v := int(fitScore.Int64)
		j.FitScore = &v
	}
	j.FitReason = fitReason.String
	j.ContentHash = contentHash.String
	j.StructuralHash = structuralHash.String
	j.DuplicateClassification = duplicateClassification.String
	j.DuplicateOfJobID = duplicateOfJobID.String
	j.EnrichedAt = enrichedAt.String
	j.ScoredAt = scoredAt.String
	j.RubricScores = rubricScores.String
	return j, nil
}

func scanJobs(rows *sql.Rows) ([]*models.JobPosting, error) {
	var out []*models.JobPosting
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullFloat(p *float64) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

func statusOrDefault(s string) string {
	if s == "" {
		return "new"
	}
	return s
}

func toLowerCase(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func inQuery(ids []string) (string, []interface{}) {
	q := ""
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		if i > 0 {
			q += ","
		}
		q += "?"
		args[i] = id
	}
	return q, args
}

// NowISO returns the current UTC time in ISO format.
func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
