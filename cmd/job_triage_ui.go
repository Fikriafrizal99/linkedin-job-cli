package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"linkedin-jobs/internal/models"
)

// handleAppSetJobReviewState records one explicit job-triage decision.
func (ws *webServer) handleAppSetJobReviewState(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	state := strings.TrimSpace(r.PostFormValue("review_state"))
	reason := strings.TrimSpace(r.PostFormValue("review_reason"))
	if _, ok := models.NormalizeJobReviewState(state); !ok {
		http.Error(w, "invalid review state", http.StatusBadRequest)
		return
	}
	if err := ws.st.SetJobReviewState(id, state, reason); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ws.removeSelectedJobs([]string{id})

	target := strings.TrimSpace(r.PostFormValue("return_url"))
	if target == "" || !strings.HasPrefix(target, "/app/") {
		target = "/app/jobs"
	}
	u, err := url.Parse(target)
	if err != nil || u.IsAbs() || u.Host != "" {
		target = "/app/jobs"
	}
	q := url.Values{}
	q.Set("triage", strings.ToUpper(state))
	q.Set("updated", "1")
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	http.Redirect(w, r, target+sep+q.Encode(), http.StatusSeeOther)
}

// handleAppBulkReviewState applies one local triage decision to the selected
// jobs. This is a SQLite-only mutation; provider/Gmail batch limits do not
// apply here.
func (ws *webServer) handleAppBulkReviewState(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	state := strings.TrimSpace(r.PostFormValue("review_state"))
	reason := strings.TrimSpace(r.PostFormValue("review_reason"))
	if _, ok := models.NormalizeJobReviewState(state); !ok {
		redirectJobTriageResult(w, r, state, 0, fmt.Errorf("invalid review state %q", state))
		return
	}

	scope := normalizedJobSelectionScope(jobSelectionValuesFromPost(r))
	seen := ws.selectedJobsForScope(scope)
	if seen == nil {
		seen = map[string]bool{}
	}
	// The submitted page is authoritative for currently visible rows. This
	// closes the race where an async checkbox persistence request is still in
	// flight when the user immediately triggers a triage action.
	for _, id := range r.PostForm["visible_job_id"] {
		delete(seen, strings.TrimSpace(id))
	}
	for _, id := range r.PostForm["job_id"] {
		id = strings.TrimSpace(id)
		if id != "" {
			seen[id] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		redirectJobTriageResult(w, r, state, 0, fmt.Errorf("select at least one job"))
		return
	}

	updated, err := ws.st.BulkSetJobReviewState(ids, state, reason)
	if err == nil {
		ws.removeSelectedJobs(ids)
	}
	redirectJobTriageResult(w, r, state, updated, err)
}

func redirectJobTriageResult(w http.ResponseWriter, r *http.Request, state string, updated int, actionErr error) {
	q := url.Values{}
	q.Set("triage", strings.ToUpper(strings.TrimSpace(state)))
	q.Set("updated", strconv.Itoa(updated))
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("action_error", msg)
	}
	for formKey, queryKey := range map[string]string{
		"filter_q":        "q",
		"filter_location": "location",
		"filter_method":   "method",
		"filter_state":    "state",
		"filter_since":    "since",
		"filter_review":   "review",
		"filter_page":     "page",
	} {
		if v := strings.TrimSpace(r.PostFormValue(formKey)); v != "" {
			q.Set(queryKey, v)
		}
	}
	http.Redirect(w, r, "/app/jobs?"+q.Encode(), http.StatusSeeOther)
}
