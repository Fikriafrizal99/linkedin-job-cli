package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"linkedin-jobs/internal/models"
)

func TestQueueApplicationReadyEmail(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-email")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	a, err := st.QueueApplication(j.ID)
	if err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if a.State != models.ApplicationStateReadyEmail {
		t.Fatalf("state=%q want READY_EMAIL", a.State)
	}
	if a.Recipient != "jobs@example.com" {
		t.Fatalf("recipient=%q", a.Recipient)
	}

	again, err := st.QueueApplication(j.ID)
	if err != nil {
		t.Fatalf("QueueApplication second: %v", err)
	}
	if again.ID != a.ID {
		t.Fatalf("queue must be idempotent: first=%d second=%d", a.ID, again.ID)
	}
}

func TestQueueApplicationNeedReviewWithoutEmail(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-review")
	j.ApplicationMethod = "UNKNOWN"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	a, err := st.QueueApplication(j.ID)
	if err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if a.State != models.ApplicationStateNeedReview {
		t.Fatalf("state=%q want NEED_REVIEW", a.State)
	}
	if a.Recipient != "" {
		t.Fatalf("recipient=%q want empty", a.Recipient)
	}
}

func TestListApplicationsByState(t *testing.T) {
	st := tmpDB(t)
	emailJob := sampleJob("app-list-email")
	emailJob.ApplicationMethod = "EMAIL"
	emailJob.ApplyEmail = "jobs@example.com"
	reviewJob := sampleJob("app-list-review")
	reviewJob.ApplicationMethod = "LINKEDIN"

	for _, j := range []*models.JobPosting{emailJob, reviewJob} {
		if err := st.Upsert(j); err != nil {
			t.Fatalf("Upsert %s: %v", j.ID, err)
		}
		if _, err := st.QueueApplication(j.ID); err != nil {
			t.Fatalf("QueueApplication %s: %v", j.ID, err)
		}
	}

	got, err := st.ListApplications(models.ApplicationStateReadyEmail, 10)
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	if len(got) != 1 || got[0].JobID != emailJob.ID {
		t.Fatalf("got %+v", got)
	}
}


func TestSaveApplicationPreparation(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-prepare")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}

	got, err := st.SaveApplicationPreparation(
		j.ID,
		"Application - Staff Engineer",
		"Dear Hiring Team...",
		"general",
	)
	if err != nil {
		t.Fatalf("SaveApplicationPreparation: %v", err)
	}
	if got.State != models.ApplicationStateReadyEmail {
		t.Fatalf("state changed unexpectedly: %q", got.State)
	}
	if got.Subject != "Application - Staff Engineer" || got.Body == "" || got.CVProfile != "general" {
		t.Fatalf("prepared data not persisted: %+v", got)
	}
}


func TestMarkApplicationDraftCreated(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-draft")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.SaveApplicationPreparation(j.ID, "Subject", "Body", "general"); err != nil {
		t.Fatalf("SaveApplicationPreparation: %v", err)
	}

	got, err := st.MarkApplicationDraftCreated(j.ID, "draft-123")
	if err != nil {
		t.Fatalf("MarkApplicationDraftCreated: %v", err)
	}
	if got.State != models.ApplicationStateDraftCreated || got.GmailDraftID != "draft-123" || got.DraftCreatedAt == "" {
		t.Fatalf("draft transition failed: %+v", got)
	}

	again, err := st.MarkApplicationDraftCreated(j.ID, "draft-123")
	if err != nil {
		t.Fatalf("idempotent MarkApplicationDraftCreated: %v", err)
	}
	if again.GmailDraftID != "draft-123" {
		t.Fatalf("draft id changed: %+v", again)
	}
	if _, err := st.MarkApplicationDraftCreated(j.ID, "other-draft"); err == nil {
		t.Fatal("expected conflicting draft id error")
	}
}

func TestMarkApplicationDraftCreatedRequiresPreparedReadyEmail(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-draft-unprepared")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.MarkApplicationDraftCreated(j.ID, "draft-1"); err == nil {
		t.Fatal("expected unprepared error")
	}
}


func TestApproveAndUnapproveApplication(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-review-gate")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.SaveApplicationPreparation(j.ID, "Subject", "Body", "general"); err != nil {
		t.Fatalf("SaveApplicationPreparation: %v", err)
	}
	if _, err := st.MarkApplicationDraftCreated(j.ID, "draft-review"); err != nil {
		t.Fatalf("MarkApplicationDraftCreated: %v", err)
	}

	approved, err := st.ApproveApplication(j.ID, "Reviewed in Gmail")
	if err != nil {
		t.Fatalf("ApproveApplication: %v", err)
	}
	if approved.State != models.ApplicationStateApproved {
		t.Fatalf("state=%q want APPROVED", approved.State)
	}
	if approved.ReviewedAt == "" || approved.ReviewNote != "Reviewed in Gmail" {
		t.Fatalf("review metadata missing: %+v", approved)
	}
	if approved.GmailDraftID != "draft-review" {
		t.Fatalf("draft lost: %+v", approved)
	}

	reopened, err := st.UnapproveApplication(j.ID, "Need wording changes")
	if err != nil {
		t.Fatalf("UnapproveApplication: %v", err)
	}
	if reopened.State != models.ApplicationStateDraftCreated {
		t.Fatalf("state=%q want DRAFT_CREATED", reopened.State)
	}
	if reopened.ReviewedAt != "" || reopened.ReviewNote != "Need wording changes" {
		t.Fatalf("unexpected reopened review metadata: %+v", reopened)
	}
	if reopened.GmailDraftID != "draft-review" {
		t.Fatalf("draft should remain attached: %+v", reopened)
	}
}

func TestApproveApplicationRequiresDraftCreated(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-approve-too-early")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.ApproveApplication(j.ID, "too early"); err == nil {
		t.Fatal("expected approval to reject READY_EMAIL")
	}
}

func TestPreparationRejectedAfterDraftCreated(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-no-reprepare")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.SaveApplicationPreparation(j.ID, "Subject", "Body", "general"); err != nil {
		t.Fatalf("SaveApplicationPreparation: %v", err)
	}
	if _, err := st.MarkApplicationDraftCreated(j.ID, "draft-1"); err != nil {
		t.Fatalf("MarkApplicationDraftCreated: %v", err)
	}
	if _, err := st.SaveApplicationPreparation(j.ID, "Changed", "Changed body", "general"); err == nil {
		t.Fatal("expected preparation to reject DRAFT_CREATED")
	}
}


func TestMigrateApplicationsAddsReviewColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-applications.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer raw.Close()

	_, err = raw.Exec(`
CREATE TABLE applications (
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
	sent_at TEXT
);
`)
	if err != nil {
		t.Fatalf("create legacy applications schema: %v", err)
	}

	// Exercise only the migration under test. Store.Open also runs unrelated
	// jobs-table migrations/backfills whose schema is intentionally outside
	// this fixture.
	if err := migrateApplications(raw); err != nil {
		t.Fatalf("migrateApplications: %v", err)
	}

	rows, err := raw.Query(`PRAGMA table_info(applications)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	if !cols["reviewed_at"] || !cols["review_note"] ||
		!cols["gmail_message_id"] || !cols["gmail_thread_id"] {
		t.Fatalf("application lifecycle columns missing after migration: %+v", cols)
	}

	// Idempotency: running the migration again must be a no-op.
	if err := migrateApplications(raw); err != nil {
		t.Fatalf("second migrateApplications: %v", err)
	}
}


func TestMarkApplicationSentRequiresApproved(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-send-gate")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.SaveApplicationPreparation(j.ID, "Subject", "Body", "general"); err != nil {
		t.Fatalf("SaveApplicationPreparation: %v", err)
	}
	if _, err := st.MarkApplicationDraftCreated(j.ID, "draft-send"); err != nil {
		t.Fatalf("MarkApplicationDraftCreated: %v", err)
	}
	if _, err := st.MarkApplicationSent(j.ID, "msg-1", "thread-1"); err == nil {
		t.Fatal("expected DRAFT_CREATED send transition to be rejected")
	}
}

func TestMarkApplicationSent(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-send")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatalf("QueueApplication: %v", err)
	}
	if _, err := st.SaveApplicationPreparation(j.ID, "Subject", "Body", "general"); err != nil {
		t.Fatalf("SaveApplicationPreparation: %v", err)
	}
	if _, err := st.MarkApplicationDraftCreated(j.ID, "draft-send"); err != nil {
		t.Fatalf("MarkApplicationDraftCreated: %v", err)
	}
	if _, err := st.ApproveApplication(j.ID, "reviewed"); err != nil {
		t.Fatalf("ApproveApplication: %v", err)
	}

	got, err := st.MarkApplicationSent(j.ID, "msg-123", "thread-123")
	if err != nil {
		t.Fatalf("MarkApplicationSent: %v", err)
	}
	if got.State != models.ApplicationStateSent {
		t.Fatalf("state=%q want SENT", got.State)
	}
	if got.GmailMessageID != "msg-123" || got.GmailThreadID != "thread-123" || got.SentAt == "" {
		t.Fatalf("sent metadata missing: %+v", got)
	}

	again, err := st.MarkApplicationSent(j.ID, "msg-123", "thread-123")
	if err != nil {
		t.Fatalf("idempotent MarkApplicationSent: %v", err)
	}
	if again.GmailMessageID != "msg-123" {
		t.Fatalf("unexpected sent record: %+v", again)
	}

	if _, err := st.MarkApplicationSent(j.ID, "other-msg", "thread-123"); err == nil {
		t.Fatal("expected conflicting sent message id error")
	}
}


func TestReplaceApplicationDraftResetsApproval(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-recreate-draft")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueApplication(j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveApplicationPreparation(j.ID, "Subject", "Body", "general"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkApplicationDraftCreated(j.ID, "old-draft"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveApplication(j.ID, "reviewed old draft"); err != nil {
		t.Fatal(err)
	}

	got, err := st.ReplaceApplicationDraft(j.ID, "new-draft")
	if err != nil {
		t.Fatalf("ReplaceApplicationDraft: %v", err)
	}
	if got.State != models.ApplicationStateDraftCreated {
		t.Fatalf("state=%q want DRAFT_CREATED", got.State)
	}
	if got.GmailDraftID != "new-draft" {
		t.Fatalf("draft=%q", got.GmailDraftID)
	}
	if got.ReviewedAt != "" || got.ReviewNote != "" {
		t.Fatalf("old approval was not cleared: %+v", got)
	}
	if got.Subject != "Subject" || got.Body != "Body" || got.CVProfile != "general" {
		t.Fatalf("prepared content changed: %+v", got)
	}
}

func TestReplaceApplicationDraftRejectsSent(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("app-recreate-sent")
	j.ApplicationMethod = "EMAIL"
	j.ApplyEmail = "jobs@example.com"
	if err := st.Upsert(j); err != nil { t.Fatal(err) }
	if _, err := st.QueueApplication(j.ID); err != nil { t.Fatal(err) }
	if _, err := st.SaveApplicationPreparation(j.ID, "Subject", "Body", "general"); err != nil { t.Fatal(err) }
	if _, err := st.MarkApplicationDraftCreated(j.ID, "draft"); err != nil { t.Fatal(err) }
	if _, err := st.ApproveApplication(j.ID, "reviewed"); err != nil { t.Fatal(err) }
	if _, err := st.MarkApplicationSent(j.ID, "msg", "thread"); err != nil { t.Fatal(err) }

	if _, err := st.ReplaceApplicationDraft(j.ID, "replacement"); err == nil {
		t.Fatal("expected SENT draft recreation to be rejected")
	}
}
