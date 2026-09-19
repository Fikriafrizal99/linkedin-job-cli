package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"linkedin-jobs/internal/models"
)

func fptr(f float64) *float64 { return &f }

func tmpDB(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func sampleJob(id string) *models.JobPosting {
	return &models.JobPosting{
		ID:         id,
		Title:      "Staff Engineer",
		Company:    "Acme",
		Location:   "Remote",
		URL:        "http://x/" + id,
		SalaryHigh: fptr(200000),
		SearchedAt: "2026-06-28T00:00:00Z",
	}
}

// TestOpen_FreshSchema confirms a brand-new DB has the new columns and a
// round-trip insert/read works.
func TestOpen_FreshSchema(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("a")
	j.ContentHash = "hash-a"
	j.PostedAt = "2026-09-19"
	j.ApplyEmail = "recruitment@example.com"
	j.ApplyEmails = []string{"recruitment@example.com", "hr@example.com"}
	j.ApplyURL = "https://careers.example.com/jobs/1"
	j.ApplicationMethod = "EMAIL"
	j.ApplicationInstruction = "Send CV to recruitment@example.com"
	j.DetailStatus = "EMAIL_FOUND"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := st.Get("a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("nil job")
	}
	if got.ContentHash != "hash-a" {
		t.Errorf("content_hash = %q, want hash-a", got.ContentHash)
	}
	if got.PostedAt != "2026-09-19" {
		t.Errorf("posted_at = %q", got.PostedAt)
	}
	if got.ApplyEmail != "recruitment@example.com" || len(got.ApplyEmails) != 2 {
		t.Errorf("application emails not round-tripped: primary=%q all=%v", got.ApplyEmail, got.ApplyEmails)
	}
	if got.ApplicationMethod != "EMAIL" || got.ApplyURL == "" || got.DetailStatus != "EMAIL_FOUND" {
		t.Errorf("collector metadata not round-tripped: method=%q url=%q detail=%q", got.ApplicationMethod, got.ApplyURL, got.DetailStatus)
	}
	if got.FirstSeen == "" || got.LastSeen == "" || got.ScrapedAt == "" {
		t.Errorf("collector timestamps missing: first=%q last=%q scraped=%q", got.FirstSeen, got.LastSeen, got.ScrapedAt)
	}
}

// TestMigrate_OldSchemaDB verifies that a DB created with the pre-fit-engine
// schema is migrated on Open: new columns exist and pre-existing rows are
// readable with empty enrichment fields.
func TestMigrate_OldSchemaDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	// Build a DB that looks like the legacy schema (no new columns, no profile).
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	_, err = raw.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, title TEXT NOT NULL, company TEXT, location TEXT, url TEXT NOT NULL, salary_raw TEXT, salary_low REAL, salary_high REAL, salary_currency TEXT, description TEXT, summary TEXT, llm_summary TEXT, remote_type TEXT, status TEXT DEFAULT 'new', notes TEXT, source TEXT, listed_at INTEGER, searched_at TEXT NOT NULL, fetched_at TEXT)`)
	if err != nil {
		t.Fatalf("create old table: %v", err)
	}
	_, err = raw.Exec(`INSERT INTO jobs (id,title,url,searched_at,description) VALUES ('old','Legacy','http://legacy','2020','desc')`)
	if err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	raw.Close()

	// Open with new code -> migration runs.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open migrated: %v", err)
	}
	defer st.Close()
	got, err := st.Get("old")
	if err != nil {
		t.Fatalf("Get after migrate: %v", err)
	}
	if got == nil || got.Title != "Legacy" {
		t.Fatalf("legacy row not readable after migration: %+v", got)
	}
	if got.CompanyOverview != "" || got.EnrichedAt != "" {
		t.Errorf("new columns should be empty for legacy row, got overview=%q enriched=%q", got.CompanyOverview, got.EnrichedAt)
	}
	// Re-open should be a no-op idempotent migration (no error).
	if err := migrate(st.db); err != nil {
		t.Errorf("re-migrate not idempotent: %v", err)
	}
}

// TestSearchFTS_Parity is the regression for the P1 review finding: SearchFTS
// had its own inline column list separate from jobCols. After adding columns it
// must still Scan without a destination-count mismatch and return enriched data.
func TestSearchFTS_Parity(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("fts1")
	j.Description = "Go Kubernetes distributed systems"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	yp := 8
	if err := st.SetEnrichmentAndScore("fts1", models.Enrichment{
		CompanyOverview: "Makes widgets", TechStack: "Go,K8s", Seniority: "staff",
		YearsExperience: &yp,
	}); err != nil {
		t.Fatalf("SetEnrichmentAndScore: %v", err)
	}
	if err := st.SetScore("fts1", 82, "Strong Go fit", ""); err != nil {
		t.Fatalf("SetScore: %v", err)
	}
	got, err := st.SearchFTS("Go", 10)
	if err != nil {
		t.Fatalf("SearchFTS errored (column-count drift?): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 FTS hit, got %d", len(got))
	}
	if got[0].CompanyOverview != "Makes widgets" || got[0].TechStack != "Go,K8s" {
		t.Errorf("enrichment not returned via SearchFTS: overview=%q stack=%q", got[0].CompanyOverview, got[0].TechStack)
	}
	if got[0].FitScore == nil || *got[0].FitScore != 82 {
		t.Errorf("fit_score not returned via SearchFTS: %+v", got[0].FitScore)
	}
}

func TestSetEnrichmentAndScore(t *testing.T) {
	st := tmpDB(t)
	st.Upsert(sampleJob("e1"))
	yp := 7
	st.SetEnrichmentAndScore("e1", models.Enrichment{
		Industry: "Fintech", Seniority: "senior", YearsExperience: &yp,
		WorkArrangement: "remote",
	})
	got, _ := st.Get("e1")
	if got.Industry != "Fintech" || got.Seniority != "senior" {
		t.Errorf("fields not stored: %+v", got)
	}
	if got.RemoteType != "remote" {
		t.Errorf("remote_type not refined: %q", got.RemoteType)
	}
	if got.EnrichedAt == "" || got.ScoredAt == "" {
		t.Errorf("timestamps not set: enriched=%q scored=%q", got.EnrichedAt, got.ScoredAt)
	}
	if got.YearsExperience == nil || *got.YearsExperience != 7 {
		t.Errorf("years_experience not stored: %+v", got.YearsExperience)
	}
}

func TestUpsert_PreservesEnrichment(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("p1")
	j.RemoteType = "remote"
	st.Upsert(j)
	st.SetEnrichmentAndScore("p1", models.Enrichment{Industry: "AI"})
	// Re-fetch the same job with empty enrichment fields.
	refetch := sampleJob("p1")
	refetch.RemoteType = ""
	if err := st.Upsert(refetch); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	got, _ := st.Get("p1")
	if got.Industry != "AI" {
		t.Errorf("enrichment lost on re-Upsert: industry=%q", got.Industry)
	}
	if got.RemoteType != "remote" {
		t.Errorf("remote_type lost on re-Upsert: %q", got.RemoteType)
	}
}

func TestTouchSeenPreservesFirstSeen(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("seen1")
	j.FirstSeen = "2026-09-18T00:00:00Z"
	j.LastSeen = "2026-09-18T00:00:00Z"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := st.TouchSeen("seen1", "2026-09-19T00:00:00Z"); err != nil {
		t.Fatalf("TouchSeen: %v", err)
	}
	got, err := st.Get("seen1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FirstSeen != "2026-09-18T00:00:00Z" {
		t.Errorf("first_seen changed: %q", got.FirstSeen)
	}
	if got.LastSeen != "2026-09-19T00:00:00Z" {
		t.Errorf("last_seen=%q", got.LastSeen)
	}
}

func TestFindByContentHash(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("h1")
	j.ContentHash = "dup-123"
	st.Upsert(j)
	got, err := st.FindByContentHash("dup-123")
	if err != nil || got == nil {
		t.Fatalf("FindByContentHash: %v %+v", err, got)
	}
	if got.ID != "h1" {
		t.Errorf("id=%q want h1", got.ID)
	}
	miss, _ := st.FindByContentHash("nope")
	if miss != nil {
		t.Errorf("missing hash should return nil")
	}
}

func TestMarkViewed(t *testing.T) {
	st := tmpDB(t)
	st.Upsert(sampleJob("v1")) // default status "new"
	st.Upsert(sampleJob("v2"))
	st.SetTag("v2", "saved", "") // non-new status

	if err := st.MarkViewed("v1"); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	if err := st.MarkViewed("v2"); err != nil {
		t.Fatalf("MarkViewed non-new: %v", err)
	}

	g1, _ := st.Get("v1")
	if g1.Status != "viewed" {
		t.Errorf("v1 status=%q want viewed", g1.Status)
	}
	g2, _ := st.Get("v2")
	if g2.Status != "saved" {
		t.Errorf("v2 status=%q want saved (MarkViewed must not touch non-new)", g2.Status)
	}
}

func TestDelete(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("d1")
	j.Description = "searchable text"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := st.Delete("d1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, _ := st.Get("d1"); got != nil {
		t.Errorf("job still present after delete")
	}
	// FTS entry must be gone too.
	hits, _ := st.SearchFTS("searchable", 10)
	if len(hits) != 0 {
		t.Errorf("FTS entry still present after delete: %d hits", len(hits))
	}
	// Deleting a missing id is not an error.
	if err := st.Delete("missing"); err != nil {
		t.Errorf("deleting missing id should not error: %v", err)
	}
}

// TestList_SinceSearched covers the "stored since" floor: only jobs whose
// searched_at is >= the cutoff are returned. The comparison is a string match
// against the ISO 8601 column, so a date-only cutoff floors to start-of-day.
func TestList_SinceSearched(t *testing.T) {
	st := tmpDB(t)
	old := sampleJob("old")
	old.SearchedAt = "2026-06-01T10:00:00Z"
	recent := sampleJob("recent")
	recent.SearchedAt = "2026-07-03T09:00:00Z"
	st.Upsert(old)
	st.Upsert(recent)

	// Floor at 2026-07-03 00:00:00 UTC → only the recent job qualifies.
	got, err := st.List(Filters{SinceSearched: "2026-07-03T00:00:00Z"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "recent" {
		t.Fatalf("want only [recent], got %v", jobIDs(got))
	}

	// Empty floor returns both.
	got, _ = st.List(Filters{})
	if len(got) != 2 {
		t.Errorf("want 2 jobs with no floor, got %d", len(got))
	}
}

func jobIDs(jobs []*models.JobPosting) []string {
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ID)
	}
	return out
}

// TestList_OnsiteFilter verifies the Onsite flag matches both the normalized
// remote_type vocabulary ("onsite") and the hyphenated "On-site" form that
// appears in raw location text, and ORs with Remote/Hybrid when combined.
func TestList_OnsiteFilter(t *testing.T) {
	st := tmpDB(t)
	remote := sampleJob("remote")
	remote.RemoteType = "remote"
	onsite := sampleJob("onsite")
	onsite.RemoteType = "onsite"
	hyphenated := sampleJob("hyphenated")
	hyphenated.RemoteType = ""
	hyphenated.Location = "New York, NY (On-site)"
	for _, j := range []*models.JobPosting{remote, onsite, hyphenated} {
		if err := st.Upsert(j); err != nil {
			t.Fatalf("Upsert %s: %v", j.ID, err)
		}
	}

	got, err := st.List(Filters{Onsite: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]bool{"onsite": true, "hyphenated": true}
	if len(got) != 2 {
		t.Fatalf("want 2 on-site jobs, got %v", jobIDs(got))
	}
	for _, j := range got {
		if !want[j.ID] {
			t.Errorf("unexpected job %q in on-site filter", j.ID)
		}
	}

	// OR: --remote --onsite keeps all three.
	got, _ = st.List(Filters{Remote: true, Onsite: true})
	if len(got) != 3 {
		t.Errorf("remote+onsite OR: want 3 jobs, got %v", jobIDs(got))
	}
}
