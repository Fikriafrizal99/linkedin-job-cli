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


func TestBuildCollectPlanExpandsAndDeduplicates(t *testing.T) {
	base := collectRequest{PostedWithin: "7d", Top: 25}
	plan, err := buildCollectPlan(base,
		[]string{"Sales Operations", "Business Development", "sales operations"},
		[]string{"Indonesia", "Jakarta", "indonesia"},
	)
	if err != nil {
		t.Fatalf("buildCollectPlan: %v", err)
	}
	if len(plan.Queries) != 2 || len(plan.Locations) != 2 || len(plan.Requests) != 4 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	want := [][2]string{
		{"Sales Operations", "Indonesia"},
		{"Sales Operations", "Jakarta"},
		{"Business Development", "Indonesia"},
		{"Business Development", "Jakarta"},
	}
	for i, pair := range want {
		if plan.Requests[i].Keywords != pair[0] || plan.Requests[i].Location != pair[1] {
			t.Fatalf("request %d=%+v want %q @ %q", i, plan.Requests[i], pair[0], pair[1])
		}
	}
}

func TestBuildCollectPlanAllowsGlobalLocationAndBoundsCombinations(t *testing.T) {
	plan, err := buildCollectPlan(collectRequest{Top: 10}, []string{"Sales"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Requests) != 1 || plan.Requests[0].Location != "" {
		t.Fatalf("expected unrestricted location: %+v", plan)
	}

	queries := []string{"q1", "q2", "q3", "q4", "q5", "q6"}
	locations := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}
	if _, err := buildCollectPlan(collectRequest{Top: 10}, queries, locations); err == nil {
		t.Fatal("expected maximum-combination error")
	}
}
