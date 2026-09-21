package cmd

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (ws *webServer) handleAppRemoveFromQueue(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}

	ws.lifecycleMu.Lock()
	err := ws.st.RemoveApplication(jobID)
	ws.lifecycleMu.Unlock()
	if err != nil {
		q := url.Values{"action_error": {err.Error()}}
		http.Redirect(w, r, "/app/applications/"+url.PathEscape(jobID)+"?"+q.Encode(), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/app/applications?removed=1", http.StatusSeeOther)
}

func (ws *webServer) handleAppBulkRemoveFromQueue(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxBulkApplicationSelection)
	if err != nil {
		redirectBulkResult(w, r, "remove", 0, 0, 0, err)
		return
	}

	ws.lifecycleMu.Lock()
	defer ws.lifecycleMu.Unlock()

	removed, skipped, failed := 0, 0, 0
	var firstErr error
	for _, id := range ids {
		app, getErr := ws.st.GetApplicationByJobID(id)
		if getErr != nil {
			failed++
			if firstErr == nil {
				firstErr = getErr
			}
			continue
		}
		if app == nil {
			skipped++
			continue
		}
		if removeErr := ws.st.RemoveApplication(id); removeErr != nil {
			skipped++
			if firstErr == nil {
				firstErr = removeErr
			}
			continue
		}
		removed++
	}
	redirectBulkResult(w, r, "remove", removed, skipped, failed, firstErr)
}

func removeSummaryQuery(removed, skipped, failed int) string {
	q := url.Values{}
	q.Set("bulk", "remove")
	q.Set("ok", strconv.Itoa(removed))
	q.Set("skipped", strconv.Itoa(skipped))
	q.Set("failed", strconv.Itoa(failed))
	return q.Encode()
}
