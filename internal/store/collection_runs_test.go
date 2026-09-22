package store

import (
	"reflect"
	"testing"

	"linkedin-jobs/internal/models"
)

func TestCollectionRunPersistenceAndMembership(t *testing.T) {
	st := tmpDB(t)
	for _, id := range []string{"run-a", "run-b"} {
		if err := st.Upsert(&models.JobPosting{ID: id, Title: id, URL: "https://example.com/" + id, SearchedAt: NowISO()}); err != nil {
			t.Fatal(err)
		}
	}

	runID, err := st.CreateCollectionRun(
		[]string{"Sales Operations", "Business Development"},
		[]string{"Jakarta", "Bandung"},
		"7d",
	)
	if err != nil {
		t.Fatal(err)
	}
	if runID <= 0 {
		t.Fatalf("run id=%d", runID)
	}
	if err := st.AddCollectionRunJob(runID, "run-a", DuplicateNew, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddCollectionRunJob(runID, "run-b", DuplicateLikelyRepost, true); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishCollectionRun(runID, CollectionRunFinish{
		SearchRuns: 4, SearchedCount: 120, NewCount: 2, PersistedCount: 2,
		LikelyRepostCount: 1, Status: "COMPLETED",
	}); err != nil {
		t.Fatal(err)
	}

	run, err := st.GetCollectionRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != "COMPLETED" || run.SearchRuns != 4 || run.SearchedCount != 120 || run.NewCount != 2 || run.PersistedCount != 2 {
		t.Fatalf("unexpected run: %+v", run)
	}
	if !reflect.DeepEqual(run.Queries, []string{"Sales Operations", "Business Development"}) {
		t.Fatalf("queries=%v", run.Queries)
	}
	if !reflect.DeepEqual(run.Locations, []string{"Jakarta", "Bandung"}) {
		t.Fatalf("locations=%v", run.Locations)
	}
	ids, err := st.CollectionRunJobIDs(runID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || !ids["run-a"] || !ids["run-b"] {
		t.Fatalf("run ids=%v", ids)
	}

	runs, err := st.ListCollectionRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != runID {
		t.Fatalf("runs=%+v", runs)
	}
}

func TestCollectionRunFailedStatus(t *testing.T) {
	st := tmpDB(t)
	runID, err := st.CreateCollectionRun([]string{"Sales"}, nil, "1d")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishCollectionRun(runID, CollectionRunFinish{Status: "FAILED", Error: "search failed"}); err != nil {
		t.Fatal(err)
	}
	run, err := st.GetCollectionRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != "FAILED" || run.Error != "search failed" || run.FinishedAt == "" {
		t.Fatalf("failed run=%+v", run)
	}
}
