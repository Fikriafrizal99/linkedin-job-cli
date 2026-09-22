package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

func selectionTestServer(t *testing.T, count int) *webServer {
	t.Helper()
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "selection.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i := 0; i < count; i++ {
		j := &models.JobPosting{
			ID: fmt.Sprintf("sel-%03d", i),
			Title: "Sales",
			Company: "Example",
			URL: fmt.Sprintf("https://www.linkedin.com/jobs/view/sel-%03d", i),
			SearchedAt: "2026-09-22T00:00:00Z",
		}
		if err := st.Upsert(j); err != nil {
			t.Fatal(err)
		}
	}
	return &webServer{st: st, csrf: "token"}
}

func postSelection(t *testing.T, ws *webServer, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	values.Set("csrf", "token")
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/selection", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppJobSelection(rec, req)
	return rec
}

func TestJobSelectionPersistsAcrossPages(t *testing.T) {
	ws := selectionTestServer(t, 55)

	page1, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Jobs) != 50 {
		t.Fatalf("page 1 rows=%d want 50", len(page1.Jobs))
	}

	values := url.Values{
		"selection_action": {"visible"},
		"selected":         {"1"},
		"filter_review":    {models.JobReviewUnreviewed},
	}
	for _, row := range page1.Jobs {
		values.Add("job_id", row.ID)
	}
	rec := postSelection(t, ws, values)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("selection status=%d want 303", rec.Code)
	}

	page2, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs?page=2", nil))
	if err != nil {
		t.Fatal(err)
	}
	if page2.SelectedJobsCount != 50 {
		t.Fatalf("selected across pages=%d want 50", page2.SelectedJobsCount)
	}
	if len(page2.Jobs) != 5 {
		t.Fatalf("page 2 rows=%d want 5", len(page2.Jobs))
	}
	for _, row := range page2.Jobs {
		if row.Selected {
			t.Fatalf("page 2 row %s should not yet be selected", row.ID)
		}
	}

	values = url.Values{
		"selection_action": {"toggle"},
		"selected":         {"1"},
		"job_id":           {page2.Jobs[0].ID},
		"filter_review":    {models.JobReviewUnreviewed},
	}
	postSelection(t, ws, values)

	page1Again, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	if page1Again.SelectedJobsCount != 51 {
		t.Fatalf("selection lost across navigation: got %d want 51", page1Again.SelectedJobsCount)
	}
	for _, row := range page1Again.Jobs {
		if !row.Selected {
			t.Fatalf("page 1 row %s lost persisted selection", row.ID)
		}
	}
}

func TestSelectAllMatchingIsNotLimitedToVisiblePage(t *testing.T) {
	ws := selectionTestServer(t, 73)

	rec := postSelection(t, ws, url.Values{
		"selection_action": {"all"},
		"filter_review":    {models.JobReviewUnreviewed},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303", rec.Code)
	}
	page2, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs?page=2", nil))
	if err != nil {
		t.Fatal(err)
	}
	if page2.SelectedJobsCount != 73 {
		t.Fatalf("Select all matching selected %d want 73", page2.SelectedJobsCount)
	}
	for _, row := range page2.Jobs {
		if !row.Selected {
			t.Fatalf("matching row %s not restored as selected", row.ID)
		}
	}
}

func TestChangingJobsFilterClearsCrossPageSelection(t *testing.T) {
	ws := selectionTestServer(t, 55)
	postSelection(t, ws, url.Values{
		"selection_action": {"all"},
		"filter_review":    {models.JobReviewUnreviewed},
	})

	filtered, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs?q=Sales", nil))
	if err != nil {
		t.Fatal(err)
	}
	if filtered.SelectedJobsCount != 0 {
		t.Fatalf("selection should clear after filter change, got %d", filtered.SelectedJobsCount)
	}
	if filtered.SelectionNotice == "" {
		t.Fatal("filter change should explain that selection was cleared")
	}
}

func TestCrossPageBulkSkipUsesWholeSelection(t *testing.T) {
	ws := selectionTestServer(t, 73)
	postSelection(t, ws, url.Values{
		"selection_action": {"all"},
		"filter_review":    {models.JobReviewUnreviewed},
	})

	form := url.Values{
		"csrf":          {"token"},
		"review_state":  {models.JobReviewSkipped},
		"filter_review": {models.JobReviewUnreviewed},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/review-state", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppBulkReviewState(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("bulk triage status=%d want 303", rec.Code)
	}

	inbox, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	if inbox.TotalRows != 0 || inbox.SelectedJobsCount != 0 {
		t.Fatalf("Inbox should be empty after cross-page skip: rows=%d selected=%d", inbox.TotalRows, inbox.SelectedJobsCount)
	}
	skipped, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs?review=SKIPPED", nil))
	if err != nil {
		t.Fatal(err)
	}
	if skipped.TotalRows != 73 {
		t.Fatalf("skipped rows=%d want 73", skipped.TotalRows)
	}
}

func TestVisibleCheckboxStateWinsOverPersistedSelection(t *testing.T) {
	ws := selectionTestServer(t, 55)
	postSelection(t, ws, url.Values{
		"selection_action": {"all"},
		"filter_review":    {models.JobReviewUnreviewed},
	})

	page1, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"csrf":          {"token"},
		"review_state":  {models.JobReviewSkipped},
		"filter_review": {models.JobReviewUnreviewed},
	}
	for _, row := range page1.Jobs {
		form.Add("visible_job_id", row.ID)
	}
	// Simulate one visible checkbox being unchecked immediately before submit.
	for _, row := range page1.Jobs[1:] {
		form.Add("job_id", row.ID)
	}
	req := httptest.NewRequest(http.MethodPost, "/app/jobs/bulk/review-state", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ws.handleAppBulkReviewState(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303", rec.Code)
	}

	kept, err := ws.st.Get(page1.Jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.ReviewState != models.JobReviewUnreviewed {
		t.Fatalf("unchecked visible job was mutated: %q", kept.ReviewState)
	}
	skippedCount, err := ws.st.CountJobsByReviewState(models.JobReviewSkipped)
	if err != nil {
		t.Fatal(err)
	}
	if skippedCount != 54 {
		t.Fatalf("skipped=%d want 54", skippedCount)
	}
}

func TestCollectionRunScopeShowsOnlyRunJobs(t *testing.T) {
	ws := selectionTestServer(t, 3)
	runID, err := ws.st.CreateCollectionRun([]string{"Sales"}, []string{"Indonesia"}, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.st.AddCollectionRunJob(runID, "sel-000", store.DuplicateNew, true); err != nil {
		t.Fatal(err)
	}
	if err := ws.st.AddCollectionRunJob(runID, "sel-002", store.DuplicateNew, true); err != nil {
		t.Fatal(err)
	}
	if err := ws.st.FinishCollectionRun(runID, store.CollectionRunFinish{SearchRuns: 1, SearchedCount: 3, NewCount: 2, PersistedCount: 2, Status: "COMPLETED"}); err != nil {
		t.Fatal(err)
	}

	path := fmt.Sprintf("/app/jobs?review=UNREVIEWED&run=%d", runID)
	pd, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	if pd.RunFilter != runID || pd.TotalRows != 2 {
		t.Fatalf("run scope id=%d rows=%d", pd.RunFilter, pd.TotalRows)
	}
	got := map[string]bool{}
	for _, row := range pd.Jobs {
		got[row.ID] = true
	}
	if !got["sel-000"] || !got["sel-002"] || got["sel-001"] {
		t.Fatalf("unexpected run-scoped jobs: %+v", pd.Jobs)
	}

	// Selection scope includes the run id, so leaving the run clears it rather
	// than carrying hidden choices into the global Inbox.
	postSelection(t, ws, url.Values{
		"selection_action": {"all"},
		"filter_review":    {models.JobReviewUnreviewed},
		"filter_run":       {fmt.Sprintf("%d", runID)},
	})
	global, err := ws.buildAppPage(httptest.NewRequest(http.MethodGet, "/app/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	if global.SelectedJobsCount != 0 || global.SelectionNotice == "" {
		t.Fatalf("leaving run scope should clear selection: selected=%d notice=%q", global.SelectedJobsCount, global.SelectionNotice)
	}
}
