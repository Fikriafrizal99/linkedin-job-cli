package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	appengine "linkedin-jobs/internal/application"
	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
)

const maxEasyApplyQueue = 50

type easyApplyOpenTarget struct {
	JobID   string
	Title   string
	Company string
	URL     string
	MarkURL string
}

func (ws *webServer) handleAppOpenEasyApply(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}

	app, err := ws.st.GetApplicationByJobID(jobID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if app == nil {
		http.Error(w, "application is not queued", http.StatusNotFound)
		return
	}
	target, err := safeLinkedInApplyURL(app.ApplyURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ws.lifecycleMu.Lock()
	_, err = ws.st.MarkEasyApplyOpened(jobID)
	ws.lifecycleMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	if r.PostFormValue("no_redirect") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (ws *webServer) handleAppMarkEasyApplyApplied(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("apply_confirm") != "1" {
		redirectEasyApplyAction(w, r, jobID, fmt.Errorf("confirm that you submitted the application on LinkedIn"))
		return
	}

	job, err := ws.st.Get(jobID)
	if err != nil {
		redirectEasyApplyAction(w, r, jobID, err)
		return
	}
	if job == nil {
		redirectEasyApplyAction(w, r, jobID, fmt.Errorf("collector job not found"))
		return
	}
	settings, err := config.LoadSettings()
	if err != nil {
		redirectEasyApplyAction(w, r, jobID, fmt.Errorf("load settings: %w", err))
		return
	}

	profileID := strings.TrimSpace(r.PostFormValue("cv_profile"))
	if len(profileID) > 80 {
		redirectEasyApplyAction(w, r, jobID, fmt.Errorf("CV profile id is too long"))
		return
	}
	profile, err := appengine.SelectCVProfile(job, settings.Application, profileID)
	if err != nil {
		redirectEasyApplyAction(w, r, jobID, err)
		return
	}
	if profile != nil {
		profileID = profile.ID
	}

	ws.lifecycleMu.Lock()
	_, err = ws.st.MarkEasyApplyApplied(jobID, profileID)
	ws.lifecycleMu.Unlock()
	if err != nil {
		redirectEasyApplyAction(w, r, jobID, err)
		return
	}

	target := safeEasyApplyReturnTo(r.PostFormValue("return_to"))
	if target == "" {
		target = "/app/applications/easy-apply?easy_applied=1"
	} else {
		target = addQueryValue(target, "easy_applied", "1")
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func safeLinkedInApplyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("Easy Apply URL is missing")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid LinkedIn apply URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("invalid LinkedIn apply URL scheme")
	}
	host := strings.ToLower(u.Hostname())
	if host != "linkedin.com" && !strings.HasSuffix(host, ".linkedin.com") {
		return "", fmt.Errorf("Easy Apply URL must point to LinkedIn")
	}
	if !strings.HasPrefix(strings.ToLower(u.Path), "/jobs/") {
		return "", fmt.Errorf("Easy Apply URL must point to a LinkedIn job")
	}
	u.Fragment = ""
	return u.String(), nil
}

func easyApplyQueueIDs(apps []models.JobApplication, raw string) []string {
	selected := map[string]bool{}
	raw = strings.TrimSpace(raw)
	if raw != "" {
		for _, id := range strings.Split(raw, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				selected[id] = true
			}
		}
	}
	out := make([]string, 0)
	for _, app := range apps {
		if app.State != models.ApplicationStateReadyEasyApply &&
			app.State != models.ApplicationStateInProgress {
			continue
		}
		if len(selected) > 0 && !selected[app.JobID] {
			continue
		}
		out = append(out, app.JobID)
		if len(out) >= maxEasyApplyQueue {
			break
		}
	}
	return out
}

func easyApplyQueueURL(ids []string, pos int) string {
	if len(ids) == 0 {
		return "/app/applications/easy-apply"
	}
	for pos < 0 {
		pos += len(ids)
	}
	pos = pos % len(ids)
	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	q.Set("pos", strconv.Itoa(pos))
	return "/app/applications/easy-apply?" + q.Encode()
}

func safeEasyApplyReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	if u.Path != "/app/applications/easy-apply" {
		return ""
	}
	return u.RequestURI()
}

func addQueryValue(target, key, value string) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.RequestURI()
}

func redirectEasyApplyAction(w http.ResponseWriter, r *http.Request, jobID string, actionErr error) {
	target := safeEasyApplyReturnTo(r.PostFormValue("return_to"))
	if target == "" {
		target = "/app/applications/" + url.PathEscape(strings.TrimSpace(jobID))
	}
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		target = addQueryValue(target, "action_error", msg)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
