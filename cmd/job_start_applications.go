package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"linkedin-jobs/internal/models"
)

type startApplicationsResult struct {
	Selected    int
	Started     int
	Email       int
	EasyApply   int
	NeedReview  int
	Existing    int
	NotEligible int
	Failed      int
	FirstErr    error
}

func (ws *webServer) handleAppStartApplication(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	job, err := ws.st.Get(id)
	if err != nil || job == nil {
		if err == nil {
			err = fmt.Errorf("job %s not found", id)
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if job.ReviewState != models.JobReviewShortlisted {
		http.Error(w, "shortlist this job before starting an application", http.StatusBadRequest)
		return
	}

	ws.lifecycleMu.Lock()
	app, err := ws.st.QueueApplication(id)
	ws.lifecycleMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ws.removeSelectedJobs([]string{id})
	http.Redirect(w, r, "/app/applications/"+url.PathEscape(app.JobID)+"?started=1", http.StatusSeeOther)
}

func (ws *webServer) handleAppStartApplications(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	scope := normalizedJobSelectionScope(jobSelectionValuesFromPost(r))
	seen := ws.selectedJobsForScope(scope)
	if seen == nil {
		seen = map[string]bool{}
	}
	// Current visible checkbox state wins over any pending/stale selection state.
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
	sort.Strings(ids)
	if len(ids) == 0 {
		redirectStartApplications(w, r, startApplicationsResult{}, fmt.Errorf("select at least one shortlisted job"))
		return
	}

	result := startApplicationsResult{Selected: len(ids)}
	ws.lifecycleMu.Lock()
	for _, id := range ids {
		job, err := ws.st.Get(id)
		if err != nil || job == nil {
			result.Failed++
			if result.FirstErr == nil {
				if err != nil {
					result.FirstErr = fmt.Errorf("%s: %w", id, err)
				} else {
					result.FirstErr = fmt.Errorf("%s: job not found", id)
				}
			}
			continue
		}
		if job.ReviewState != models.JobReviewShortlisted {
			result.NotEligible++
			continue
		}
		existing, err := ws.st.GetApplicationByJobID(id)
		if err != nil {
			result.Failed++
			if result.FirstErr == nil {
				result.FirstErr = fmt.Errorf("%s: %w", id, err)
			}
			continue
		}
		if existing != nil {
			result.Existing++
			continue
		}
		app, err := ws.st.QueueApplication(id)
		if err != nil {
			result.Failed++
			if result.FirstErr == nil {
				result.FirstErr = fmt.Errorf("%s: %w", id, err)
			}
			continue
		}
		result.Started++
		switch app.State {
		case models.ApplicationStateReadyEmail:
			result.Email++
		case models.ApplicationStateReadyEasyApply:
			result.EasyApply++
		default:
			result.NeedReview++
		}
	}
	ws.lifecycleMu.Unlock()

	// Remove processed IDs from the current selection. Any failed/non-eligible
	// records remain selected so the user can inspect them.
	var completed []string
	if result.Started+result.Existing > 0 {
		for _, id := range ids {
			job, _ := ws.st.Get(id)
			app, _ := ws.st.GetApplicationByJobID(id)
			if job != nil && job.ReviewState == models.JobReviewShortlisted && app != nil {
				completed = append(completed, id)
			}
		}
		ws.removeSelectedJobs(completed)
	}
	redirectStartApplications(w, r, result, result.FirstErr)
}

func redirectStartApplications(w http.ResponseWriter, r *http.Request, result startApplicationsResult, actionErr error) {
	q := url.Values{}
	q.Set("applications_started", "1")
	q.Set("selected", strconv.Itoa(result.Selected))
	q.Set("started_count", strconv.Itoa(result.Started))
	q.Set("email_count", strconv.Itoa(result.Email))
	q.Set("easy_count", strconv.Itoa(result.EasyApply))
	q.Set("need_review_count", strconv.Itoa(result.NeedReview))
	q.Set("existing_count", strconv.Itoa(result.Existing))
	q.Set("not_eligible_count", strconv.Itoa(result.NotEligible))
	q.Set("failed_count", strconv.Itoa(result.Failed))
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("action_error", msg)
	}
	http.Redirect(w, r, "/app/applications?"+q.Encode(), http.StatusSeeOther)
}
