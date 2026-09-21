package store

import (
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
