package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const collectionRunsSchema = `
CREATE TABLE IF NOT EXISTS collection_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at TEXT NOT NULL,
	finished_at TEXT,
	queries_json TEXT NOT NULL DEFAULT '[]',
	locations_json TEXT NOT NULL DEFAULT '[]',
	posted_within TEXT,
	search_runs INTEGER NOT NULL DEFAULT 0,
	searched_count INTEGER NOT NULL DEFAULT 0,
	new_count INTEGER NOT NULL DEFAULT 0,
	persisted_count INTEGER NOT NULL DEFAULT 0,
	exact_duplicate_count INTEGER NOT NULL DEFAULT 0,
	likely_repost_count INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'RUNNING',
	error TEXT
);
CREATE TABLE IF NOT EXISTS collection_run_jobs (
	run_id INTEGER NOT NULL,
	job_id TEXT NOT NULL,
	classification TEXT,
	is_new INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	PRIMARY KEY(run_id, job_id)
);
CREATE INDEX IF NOT EXISTS idx_collection_runs_started_at ON collection_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_collection_run_jobs_job_id ON collection_run_jobs(job_id);
`

type CollectionRun struct {
	ID                   int64    `json:"id"`
	StartedAt            string   `json:"started_at"`
	FinishedAt           string   `json:"finished_at,omitempty"`
	Queries              []string `json:"queries,omitempty"`
	Locations            []string `json:"locations,omitempty"`
	PostedWithin         string   `json:"posted_within,omitempty"`
	SearchRuns           int      `json:"search_runs"`
	SearchedCount        int      `json:"searched_count"`
	NewCount             int      `json:"new_count"`
	PersistedCount       int      `json:"persisted_count"`
	ExactDuplicateCount  int      `json:"exact_duplicate_count"`
	LikelyRepostCount    int      `json:"likely_repost_count"`
	Status               string   `json:"status"`
	Error                string   `json:"error,omitempty"`
}

type CollectionRunFinish struct {
	SearchRuns          int
	SearchedCount       int
	NewCount            int
	PersistedCount      int
	ExactDuplicateCount int
	LikelyRepostCount   int
	Status              string
	Error               string
}

func migrateCollectionRuns(db *sql.DB) error {
	_, err := db.Exec(collectionRunsSchema)
	return err
}

func (s *Store) CreateCollectionRun(queries, locations []string, postedWithin string) (int64, error) {
	qb, err := json.Marshal(queries)
	if err != nil {
		return 0, err
	}
	lb, err := json.Marshal(locations)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`
INSERT INTO collection_runs(started_at,queries_json,locations_json,posted_within,status)
VALUES(?,?,?,?, 'RUNNING')`, NowISO(), string(qb), string(lb), strings.TrimSpace(postedWithin))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) AddCollectionRunJob(runID int64, jobID, classification string, isNew bool) error {
	if runID <= 0 {
		return fmt.Errorf("invalid collection run id")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("empty job id")
	}
	newValue := 0
	if isNew {
		newValue = 1
	}
	_, err := s.db.Exec(`
INSERT INTO collection_run_jobs(run_id,job_id,classification,is_new,created_at)
VALUES(?,?,?,?,?)
ON CONFLICT(run_id,job_id) DO UPDATE SET
	classification=COALESCE(NULLIF(excluded.classification,''),collection_run_jobs.classification),
	is_new=MAX(collection_run_jobs.is_new,excluded.is_new)
`, runID, jobID, strings.TrimSpace(classification), newValue, NowISO())
	return err
}

func (s *Store) FinishCollectionRun(runID int64, finish CollectionRunFinish) error {
	if runID <= 0 {
		return fmt.Errorf("invalid collection run id")
	}
	status := strings.ToUpper(strings.TrimSpace(finish.Status))
	if status == "" {
		status = "COMPLETED"
	}
	if status != "COMPLETED" && status != "FAILED" {
		return fmt.Errorf("invalid collection run status %q", status)
	}
	_, err := s.db.Exec(`
UPDATE collection_runs SET
	finished_at=?,
	search_runs=?,
	searched_count=?,
	new_count=?,
	persisted_count=?,
	exact_duplicate_count=?,
	likely_repost_count=?,
	status=?,
	error=?
WHERE id=?
`, NowISO(), finish.SearchRuns, finish.SearchedCount, finish.NewCount, finish.PersistedCount,
		finish.ExactDuplicateCount, finish.LikelyRepostCount, status, strings.TrimSpace(finish.Error), runID)
	return err
}

func (s *Store) CollectionRunJobIDs(runID int64, onlyNew bool) (map[string]bool, error) {
	out := map[string]bool{}
	if runID <= 0 {
		return out, fmt.Errorf("invalid collection run id")
	}
	q := `SELECT job_id FROM collection_run_jobs WHERE run_id=?`
	args := []interface{}{runID}
	if onlyNew {
		q += ` AND is_new=1`
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) GetCollectionRun(id int64) (*CollectionRun, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid collection run id")
	}
	row := s.db.QueryRow(`
SELECT id,started_at,COALESCE(finished_at,''),queries_json,locations_json,
	COALESCE(posted_within,''),search_runs,searched_count,new_count,persisted_count,
	exact_duplicate_count,likely_repost_count,status,COALESCE(error,'')
FROM collection_runs WHERE id=?`, id)
	return scanCollectionRun(row)
}

func (s *Store) ListCollectionRuns(limit int) ([]CollectionRun, error) {
	q := `
SELECT id,started_at,COALESCE(finished_at,''),queries_json,locations_json,
	COALESCE(posted_within,''),search_runs,searched_count,new_count,persisted_count,
	exact_duplicate_count,likely_repost_count,status,COALESCE(error,'')
FROM collection_runs ORDER BY id DESC`
	args := []interface{}{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectionRun
	for rows.Next() {
		run, err := scanCollectionRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

type collectionRunScanner interface {
	Scan(dest ...interface{}) error
}

func scanCollectionRun(row collectionRunScanner) (*CollectionRun, error) {
	var run CollectionRun
	var queriesJSON, locationsJSON string
	if err := row.Scan(
		&run.ID, &run.StartedAt, &run.FinishedAt, &queriesJSON, &locationsJSON,
		&run.PostedWithin, &run.SearchRuns, &run.SearchedCount, &run.NewCount, &run.PersistedCount,
		&run.ExactDuplicateCount, &run.LikelyRepostCount, &run.Status, &run.Error,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(queriesJSON), &run.Queries)
	_ = json.Unmarshal([]byte(locationsJSON), &run.Locations)
	return &run, nil
}
