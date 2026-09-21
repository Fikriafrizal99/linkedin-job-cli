package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	appengine "linkedin-jobs/internal/application"
	"linkedin-jobs/internal/config"
	gmailclient "linkedin-jobs/internal/gmailclient"
	"linkedin-jobs/internal/models"
)

const (
	maxBulkApplicationSelection = 50
	maxBulkGmailActions         = 25
)

type appSendCandidate struct {
	JobID, Title, Company, Recipient, Subject, DraftID string
}

func selectedApplicationIDs(values []string, max int) ([]string, error) {
	if max < 1 {
		max = maxBulkApplicationSelection
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		if len(id) > 100 {
			return nil, fmt.Errorf("invalid job id")
		}
		seen[id] = true
		out = append(out, id)
		if len(out) > max {
			return nil, fmt.Errorf("select at most %d applications at a time", max)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("select at least one application")
	}
	return out, nil
}

func (ws *webServer) handleAppBulkPrepare(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxBulkApplicationSelection)
	if err != nil {
		redirectBulkResult(w, r, "prepare", 0, 0, 0, err)
		return
	}
	settings, err := config.LoadSettings()
	if err != nil {
		redirectBulkResult(w, r, "prepare", 0, 0, 0, fmt.Errorf("load settings: %w", err))
		return
	}

	ws.lifecycleMu.Lock()
	defer ws.lifecycleMu.Unlock()

	ok, skipped, failed := 0, 0, 0
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
		if app == nil || app.State != models.ApplicationStateReadyEmail {
			skipped++
			continue
		}
		if _, prepErr := prepareApplicationForUI(ws.st, id, settings.Application, ""); prepErr != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", id, prepErr)
			}
			continue
		}
		ok++
	}
	redirectBulkResult(w, r, "prepare", ok, skipped, failed, firstErr)
}

func (ws *webServer) handleAppBulkDraft(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxBulkGmailActions)
	if err != nil {
		redirectBulkResult(w, r, "draft", 0, 0, 0, err)
		return
	}
	settings, err := config.LoadSettings()
	if err != nil {
		redirectBulkResult(w, r, "draft", 0, 0, 0, fmt.Errorf("load settings: %w", err))
		return
	}
	extraAttachments, err := resolveAttachments(
		settings.Application.Attachments,
		r.PostForm["attachment"],
		settings.Application.CandidateName,
	)
	if err != nil {
		redirectBulkResult(w, r, "draft", 0, 0, 0, err)
		return
	}
	creds, err := gmailclient.LoadCredentials("")
	if err != nil {
		redirectBulkResult(w, r, "draft", 0, 0, 0, fmt.Errorf("Gmail is not configured: %w", err))
		return
	}

	ws.lifecycleMu.Lock()
	defer ws.lifecycleMu.Unlock()

	ok, skipped, failed := 0, 0, 0
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
		if app == nil || app.State != models.ApplicationStateReadyEmail {
			skipped++
			continue
		}
		payload, buildErr := appengine.BuildDraftPayload(app, settings.Application)
		if buildErr != nil {
			failed++
			_, _ = ws.st.RecordApplicationDraftError(id, buildErr.Error())
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", id, buildErr)
			}
			continue
		}
		for _, attachment := range extraAttachments {
			payload.AttachmentFiles = append(payload.AttachmentFiles, attachment.Path)
			payload.AttachmentNames = append(payload.AttachmentNames, attachment.Name)
		}
		payload.Body = draftBodyForAttachments(payload.Body, extraAttachments)
		if sizeErr := validateDraftAttachmentTotal(payload.AttachmentFiles, 18<<20); sizeErr != nil {
			failed++
			_, _ = ws.st.RecordApplicationDraftError(id, sizeErr.Error())
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", id, sizeErr)
			}
			continue
		}

		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		result, draftErr := gmailclient.CreateDraft(ctx, http.DefaultClient, creds, "", payload)
		cancel()
		if draftErr != nil {
			failed++
			_, _ = ws.st.RecordApplicationDraftError(id, draftErr.Error())
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", id, draftErr)
			}
			continue
		}
		if _, markErr := ws.st.MarkApplicationDraftCreated(id, result.DraftID); markErr != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: Gmail draft %s was created but local state update failed: %w", id, result.DraftID, markErr)
			}
			continue
		}
		ok++
	}
	redirectBulkResult(w, r, "draft", ok, skipped, failed, firstErr)
}

func (ws *webServer) handleAppBulkReviewRedirect(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxBulkApplicationSelection)
	if err != nil {
		redirectBulkResult(w, r, "review", 0, 0, 0, err)
		return
	}
	eligible := make([]string, 0, len(ids))
	for _, id := range ids {
		app, getErr := ws.st.GetApplicationByJobID(id)
		if getErr == nil && app != nil && app.State == models.ApplicationStateDraftCreated {
			eligible = append(eligible, id)
		}
	}
	if len(eligible) == 0 {
		redirectBulkResult(w, r, "review", 0, len(ids), 0, fmt.Errorf("none of the selected applications are DRAFT_CREATED"))
		return
	}
	q := url.Values{}
	q.Set("ids", strings.Join(eligible, ","))
	q.Set("pos", "0")
	http.Redirect(w, r, "/app/applications/review?"+q.Encode(), http.StatusSeeOther)
}

func (ws *webServer) handleAppSendConfirm(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxBulkGmailActions)
	if err != nil {
		redirectBulkResult(w, r, "send", 0, 0, 0, err)
		return
	}

	clone := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/app/applications"
	u.RawQuery = ""
	clone.URL = &u
	clone.Method = http.MethodGet
	pd, err := ws.buildAppPage(clone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pd.SendConfirmMode = true
	pd.SelectedApplication = nil
	pd.Applications = nil
	pd.Title = "Confirm Application Send"
	pd.Subtitle = "Final human confirmation before Gmail sends approved drafts."

	skipped := 0
	for _, id := range ids {
		app, getErr := ws.st.GetApplicationByJobID(id)
		if getErr != nil || app == nil {
			skipped++
			continue
		}
		if _, sendErr := appengine.BuildSendRequest(app); sendErr != nil {
			skipped++
			continue
		}
		job, _ := ws.st.Get(id)
		item := appSendCandidate{
			JobID: id, Recipient: app.Recipient, Subject: app.Subject, DraftID: app.GmailDraftID,
		}
		if job != nil {
			item.Title, item.Company = job.Title, job.Company
		}
		pd.SendCandidates = append(pd.SendCandidates, item)
	}
	if len(pd.SendCandidates) == 0 {
		redirectBulkResult(w, r, "send", 0, skipped, 0, fmt.Errorf("none of the selected applications are APPROVED and send-ready"))
		return
	}
	if skipped > 0 {
		pd.ActionMessage = fmt.Sprintf("%d selected record(s) were skipped because they are not APPROVED/send-ready.", skipped)
	}

	tpl, err := newAppTemplate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.Execute(w, pd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (ws *webServer) handleAppBulkSend(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	if r.PostFormValue("send_confirm") != "1" {
		redirectBulkResult(w, r, "send", 0, 0, 0, fmt.Errorf("confirm the final send checkbox before sending email"))
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxBulkGmailActions)
	if err != nil {
		redirectBulkResult(w, r, "send", 0, 0, 0, err)
		return
	}
	creds, err := gmailclient.LoadCredentials("")
	if err != nil {
		redirectBulkResult(w, r, "send", 0, 0, 0, fmt.Errorf("Gmail is not configured: %w", err))
		return
	}

	ws.lifecycleMu.Lock()
	defer ws.lifecycleMu.Unlock()

	ok, skipped, failed := 0, 0, 0
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
		sendReq, buildErr := appengine.BuildSendRequest(app)
		if buildErr != nil {
			skipped++
			continue
		}

		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		result, sendErr := gmailclient.SendDraft(ctx, http.DefaultClient, creds, "", sendReq.GmailDraftID)
		cancel()
		if sendErr != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", id, sendErr)
			}
			continue
		}
		if _, markErr := ws.st.MarkApplicationSent(id, result.MessageID, result.ThreadID); markErr != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: Gmail sent message %s but local SENT state update failed: %w", id, result.MessageID, markErr)
			}
			continue
		}
		ok++
	}
	redirectBulkResult(w, r, "send", ok, skipped, failed, firstErr)
}

func redirectBulkResult(w http.ResponseWriter, r *http.Request, action string, ok, skipped, failed int, actionErr error) {
	q := url.Values{}
	q.Set("bulk", action)
	q.Set("ok", strconv.Itoa(ok))
	q.Set("skipped", strconv.Itoa(skipped))
	q.Set("failed", strconv.Itoa(failed))
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("bulk_error", msg)
	}
	http.Redirect(w, r, "/app/applications?"+q.Encode(), http.StatusSeeOther)
}
