package cmd

import (
	"path/filepath"
	"testing"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

func TestFilterNewJobsWithStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	existing := &models.JobPosting{
		ID: "12345",
		Title: "Sales Executive",
		URL: "https://www.linkedin.com/jobs/view/12345",
		Company: "Existing Co",
	}
	if err := st.Upsert(existing); err != nil {
		t.Fatalf("Upsert existing: %v", err)
	}

	jobs := []*models.JobPosting{
		existing,
		{ID: "67890", Title: "Account Executive", URL: "https://www.linkedin.com/jobs/view/67890"},
	}
	got, err := filterNewJobsWithStore(st, jobs)
	if err != nil {
		t.Fatalf("filterNewJobsWithStore: %v", err)
	}
	if len(got) != 1 || got[0].ID != "67890" {
		t.Fatalf("unexpected filtered jobs: %+v", got)
	}
}

func TestRunCollectRejectsInvalidInputBeforeNetwork(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	for name, req := range map[string]collectRequest{
		"empty keywords": {Top: 10},
		"negative top": {Keywords: "Sales", Top: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runCollect(req, st, nil); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
