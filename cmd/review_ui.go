package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const maxReviewNoteLength = 500

func (ws *webServer) handleAppApproveApplication(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("review_confirm") != "1" {
		redirectApplicationReviewAction(w, r, jobID, "", fmt.Errorf("confirm that you reviewed the Gmail draft before approving"))
		return
	}
	note := strings.TrimSpace(r.PostFormValue("review_note"))
	if len(note) > maxReviewNoteLength {
		redirectApplicationReviewAction(w, r, jobID, "", fmt.Errorf("review note must be at most %d characters", maxReviewNoteLength))
		return
	}
	ws.lifecycleMu.Lock()
	_, err := ws.st.ApproveApplication(jobID, note)
	ws.lifecycleMu.Unlock()
	if err != nil {
		redirectApplicationReviewAction(w, r, jobID, "", err)
		return
	}
	redirectApplicationReviewAction(w, r, jobID, "approved", nil)
}

func (ws *webServer) handleAppUnapproveApplication(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("review_note"))
	if len(note) > maxReviewNoteLength {
		redirectApplicationReviewAction(w, r, jobID, "", fmt.Errorf("review note must be at most %d characters", maxReviewNoteLength))
		return
	}
	ws.lifecycleMu.Lock()
	_, err := ws.st.UnapproveApplication(jobID, note)
	ws.lifecycleMu.Unlock()
	if err != nil {
		redirectApplicationReviewAction(w, r, jobID, "", err)
		return
	}
	redirectApplicationReviewAction(w, r, jobID, "unapproved", nil)
}

func redirectApplicationReviewAction(w http.ResponseWriter, r *http.Request, jobID, status string, actionErr error) {
	q := url.Values{}
	if status != "" {
		q.Set("review", status)
	}
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("action_error", msg)
	}
	target := "/app/applications/" + url.PathEscape(strings.TrimSpace(jobID))
	if returnTo := safeApplicationReturnTo(r.PostFormValue("return_to")); returnTo != "" {
		target = returnTo
		if enc := q.Encode(); enc != "" {
			if strings.Contains(target, "?") {
				target += "&" + enc
			} else {
				target += "?" + enc
			}
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}


func safeApplicationReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	if u.Path != "/app/applications/review" {
		return ""
	}
	return u.RequestURI()
}
