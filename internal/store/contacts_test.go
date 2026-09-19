package store

import (
	"testing"

	"linkedin-jobs/internal/models"
)

func TestReplaceAndListJobContacts(t *testing.T) {
	st := tmpDB(t)
	if err := st.Upsert(sampleJob("contact-job")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	first := []models.JobContact{
		{JobID: "contact-job", Title: "Talent Acquisition", ContactType: "TALENT_ACQUISITION", SearchURL: "https://linkedin.example/ta", Source: "heuristic", Priority: 1},
		{JobID: "contact-job", Title: "Sales Manager", ContactType: "HIRING_MANAGER", SearchURL: "https://linkedin.example/sales", Source: "heuristic", Priority: 2},
	}
	if err := st.ReplaceJobContacts("contact-job", first); err != nil {
		t.Fatalf("ReplaceJobContacts: %v", err)
	}
	got, err := st.ListJobContacts("contact-job")
	if err != nil {
		t.Fatalf("ListJobContacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d contacts, want 2", len(got))
	}
	if got[0].Priority != 1 || got[0].ContactType != "TALENT_ACQUISITION" {
		t.Fatalf("unexpected first contact: %+v", got[0])
	}
	if got[0].CreatedAt == "" {
		t.Fatal("created_at not set")
	}

	replacement := []models.JobContact{
		{JobID: "contact-job", Title: "Head of Sales", ContactType: "DEPARTMENT_LEADER", Source: "heuristic", Priority: 1},
	}
	if err := st.ReplaceJobContacts("contact-job", replacement); err != nil {
		t.Fatalf("replace second time: %v", err)
	}
	got, _ = st.ListJobContacts("contact-job")
	if len(got) != 1 || got[0].Title != "Head of Sales" {
		t.Fatalf("replacement did not remove stale contacts: %+v", got)
	}
}

func TestReplaceJobContactsRejectsEmptyJobID(t *testing.T) {
	st := tmpDB(t)
	if err := st.ReplaceJobContacts("", nil); err == nil {
		t.Fatal("expected empty job id error")
	}
}
