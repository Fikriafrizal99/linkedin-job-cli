package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

var jobSelectionFilterKeys = []string{"q", "location", "method", "state", "since", "review", "run"}

type jobSelectionSummary struct {
	Count      int
	Email      int
	EasyApply  int
	Other      int
}

func normalizedJobSelectionScope(v url.Values) string {
	out := url.Values{}
	for _, key := range jobSelectionFilterKeys {
		if value := strings.TrimSpace(v.Get(key)); value != "" {
			out.Set(key, value)
		}
	}
	review := strings.ToUpper(strings.TrimSpace(out.Get("review")))
	if review == "" {
		review = models.JobReviewUnreviewed
	}
	if review != "ALL" {
		if normalized, ok := models.NormalizeJobReviewState(review); ok {
			review = normalized
		} else {
			review = models.JobReviewUnreviewed
		}
	}
	out.Set("review", review)
	return out.Encode()
}

func jobSelectionValuesFromPost(r *http.Request) url.Values {
	v := url.Values{}
	for _, key := range jobSelectionFilterKeys {
		if value := strings.TrimSpace(r.PostFormValue("filter_" + key)); value != "" {
			v.Set(key, value)
		}
	}
	return v
}

// syncJobSelectionScope keeps one explicit selection set for the current Jobs
// filter context. Page is deliberately excluded, so moving between pages keeps
// the selection. A material view/filter change clears the old set.
func (ws *webServer) syncJobSelectionScope(scope string) bool {
	ws.selectionMu.Lock()
	defer ws.selectionMu.Unlock()
	if ws.selectedJobIDs == nil {
		ws.selectedJobIDs = map[string]bool{}
	}
	if ws.jobSelectionScope == "" {
		ws.jobSelectionScope = scope
		return false
	}
	if ws.jobSelectionScope == scope {
		return false
	}
	cleared := len(ws.selectedJobIDs) > 0
	ws.selectedJobIDs = map[string]bool{}
	ws.jobSelectionScope = scope
	return cleared
}

func (ws *webServer) selectedJobsForScope(scope string) map[string]bool {
	ws.selectionMu.Lock()
	defer ws.selectionMu.Unlock()
	out := map[string]bool{}
	if ws.jobSelectionScope != scope {
		return out
	}
	for id := range ws.selectedJobIDs {
		out[id] = true
	}
	return out
}

func (ws *webServer) setSelectedJobs(scope string, ids []string, selected bool) int {
	ws.selectionMu.Lock()
	defer ws.selectionMu.Unlock()
	if ws.selectedJobIDs == nil || ws.jobSelectionScope != scope {
		ws.selectedJobIDs = map[string]bool{}
		ws.jobSelectionScope = scope
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if selected {
			ws.selectedJobIDs[id] = true
		} else {
			delete(ws.selectedJobIDs, id)
		}
	}
	return len(ws.selectedJobIDs)
}

func (ws *webServer) replaceSelectedJobs(scope string, ids []string) int {
	ws.selectionMu.Lock()
	defer ws.selectionMu.Unlock()
	ws.jobSelectionScope = scope
	ws.selectedJobIDs = map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			ws.selectedJobIDs[id] = true
		}
	}
	return len(ws.selectedJobIDs)
}

func (ws *webServer) clearSelectedJobs() {
	ws.selectionMu.Lock()
	defer ws.selectionMu.Unlock()
	ws.selectedJobIDs = map[string]bool{}
}

func (ws *webServer) removeSelectedJobs(ids []string) {
	ws.selectionMu.Lock()
	defer ws.selectionMu.Unlock()
	for _, id := range ids {
		delete(ws.selectedJobIDs, strings.TrimSpace(id))
	}
}

func (ws *webServer) selectionSummary(scope string) jobSelectionSummary {
	selected := ws.selectedJobsForScope(scope)
	summary := jobSelectionSummary{Count: len(selected)}
	if len(selected) == 0 {
		return summary
	}
	jobs, err := ws.st.List(store.Filters{SortBySearched: true})
	if err != nil {
		summary.Other = summary.Count
		return summary
	}
	for _, job := range jobs {
		if !selected[job.ID] {
			continue
		}
		switch applicationMethodLabel(job.ApplicationMethod) {
		case "EMAIL":
			summary.Email++
		case "EASY_APPLY":
			summary.EasyApply++
		default:
			summary.Other++
		}
	}
	return summary
}

func (ws *webServer) handleAppJobSelection(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	values := jobSelectionValuesFromPost(r)
	scope := normalizedJobSelectionScope(values)
	action := strings.TrimSpace(r.PostFormValue("selection_action"))
	count := 0
	var err error

	switch action {
	case "toggle", "visible":
		selected := r.PostFormValue("selected") == "1"
		ids := r.PostForm["job_id"]
		if action == "toggle" && len(ids) > 1 {
			ids = ids[:1]
		}
		count = ws.setSelectedJobs(scope, ids, selected)
	case "all":
		var ids []string
		ids, err = ws.matchingJobIDs(values)
		if err == nil {
			count = ws.replaceSelectedJobs(scope, ids)
		}
	case "clear":
		ws.clearSelectedJobs()
		count = 0
	default:
		err = fmt.Errorf("unknown selection action %q", action)
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		summary := ws.selectionSummary(scope)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true, "count": count,
			"email": summary.Email, "easy_apply": summary.EasyApply, "other": summary.Other,
		})
		return
	}
	redirectJobSelection(w, r, err)
}

func redirectJobSelection(w http.ResponseWriter, r *http.Request, actionErr error) {
	q := jobSelectionValuesFromPost(r)
	if page := strings.TrimSpace(r.PostFormValue("filter_page")); page != "" && page != "1" {
		q.Set("page", page)
	}
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("action_error", msg)
	}
	http.Redirect(w, r, "/app/jobs?"+q.Encode(), http.StatusSeeOther)
}

func (ws *webServer) matchingJobIDs(v url.Values) ([]string, error) {
	jobs, err := ws.st.List(store.Filters{SortBySearched: true})
	if err != nil {
		return nil, err
	}
	apps, err := ws.st.ListApplications("", 0)
	if err != nil {
		return nil, err
	}

	review := strings.ToUpper(strings.TrimSpace(v.Get("review")))
	if review == "" {
		review = models.JobReviewUnreviewed
	}
	if review != "ALL" {
		normalized, ok := models.NormalizeJobReviewState(review)
		if !ok {
			return nil, fmt.Errorf("invalid review view %q", review)
		}
		review = normalized
	}
	var runIDs map[string]bool
	if rawRun := strings.TrimSpace(v.Get("run")); rawRun != "" {
		runID, parseErr := strconv.ParseInt(rawRun, 10, 64)
		if parseErr != nil || runID <= 0 {
			return nil, fmt.Errorf("invalid collection run")
		}
		runIDs, err = ws.st.CollectionRunJobIDs(runID, true)
		if err != nil {
			return nil, err
		}
	}

	since := strings.TrimSpace(v.Get("since"))
	if since != "" {
		if _, err := time.Parse("2006-01-02", since); err != nil {
			return nil, fmt.Errorf("invalid since date")
		}
	}

	ids := make([]string, 0)
	for _, j := range jobs {
		if runIDs != nil && !runIDs[j.ID] {
			continue
		}
		if review != "ALL" && !strings.EqualFold(j.ReviewState, review) {
			continue
		}
		state := applicationStateFor(j.ID, apps)
		if !matchesUIJob(j, state,
			strings.TrimSpace(v.Get("q")),
			strings.TrimSpace(v.Get("location")),
			strings.TrimSpace(v.Get("method")),
			strings.TrimSpace(v.Get("state"))) {
			continue
		}
		if since != "" && j.SearchedAt < since {
			continue
		}
		ids = append(ids, j.ID)
	}
	sort.Strings(ids)
	return ids, nil
}
