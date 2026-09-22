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
	"linkedin-jobs/internal/store"
)

const (
	maxBulkJobSelection = 50
	maxProcessToDraft   = 25
)

type jobProcessResult struct {
	Selected          int
	Queued            int
	Prepared          int
	DraftCreated      int
	ExistingDrafts    int
	EasyApplyQueued   int
	EasyApplyExisting int
	NeedReview        int
	NonEmailSkipped   int
	Skipped           int
	Failed            int
	ReviewIDs         []string
	EasyApplyIDs      []string
	FirstErr          error
}

type jobDraftCreator func(context.Context, appengine.DraftPayload) (gmailclient.DraftResult, error)

func (ws *webServer) handleAppBulkQueueJobs(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxBulkJobSelection)
	if err != nil {
		redirectJobBulkResult(w, r, "queue", jobProcessResult{}, err)
		return
	}

	ws.lifecycleMu.Lock()
	defer ws.lifecycleMu.Unlock()

	result := jobProcessResult{Selected: len(ids)}
	includeNeedReview := r.PostFormValue("include_need_review") == "1"
	for _, id := range ids {
		existing, getErr := ws.st.GetApplicationByJobID(id)
		if getErr != nil {
			result.Failed++
			setJobProcessError(&result, fmt.Errorf("%s: %w", id, getErr))
			continue
		}
		if existing != nil {
			result.Skipped++
			continue
		}
		job, getErr := ws.st.Get(id)
		if getErr != nil {
			result.Failed++
			setJobProcessError(&result, fmt.Errorf("%s: %w", id, getErr))
			continue
		}
		if !jobHasExplicitApplicationEmail(job) && !jobIsEasyApply(job) && !includeNeedReview {
			result.NonEmailSkipped++
			continue
		}
		app, queueErr := ws.st.QueueApplication(id)
		if queueErr != nil {
			result.Failed++
			setJobProcessError(&result, fmt.Errorf("%s: %w", id, queueErr))
			continue
		}
		result.Queued++
		switch app.State {
		case models.ApplicationStateReadyEasyApply:
			result.EasyApplyQueued++
		case models.ApplicationStateNeedReview:
			result.NeedReview++
		}
	}
	redirectJobBulkResult(w, r, "queue", result, result.FirstErr)
}

func (ws *webServer) handleAppProcessJobsToDraft(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	ids, err := selectedApplicationIDs(r.PostForm["job_id"], maxProcessToDraft)
	if err != nil {
		redirectJobBulkResult(w, r, "process", jobProcessResult{}, err)
		return
	}
	settings, err := config.LoadSettings()
	if err != nil {
		redirectJobBulkResult(w, r, "process", jobProcessResult{}, fmt.Errorf("load settings: %w", err))
		return
	}
	extraAttachments, err := resolveAttachments(
		settings.Application.Attachments,
		r.PostForm["attachment"],
		settings.Application.CandidateName,
	)
	if err != nil {
		redirectJobBulkResult(w, r, "process", jobProcessResult{}, err)
		return
	}
	creds, gmailErr := gmailclient.LoadCredentials("")
	createDraft := func(ctx context.Context, payload appengine.DraftPayload) (gmailclient.DraftResult, error) {
		if gmailErr != nil {
			return gmailclient.DraftResult{}, fmt.Errorf("Gmail is not configured: %w", gmailErr)
		}
		itemCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		return gmailclient.CreateDraft(itemCtx, http.DefaultClient, creds, "", payload)
	}

	ws.lifecycleMu.Lock()
	result := processJobsToDraft(r.Context(), ws.st, settings.Application, extraAttachments, ids, createDraft)
	ws.lifecycleMu.Unlock()

	if len(result.ReviewIDs) > 0 {
		redirectProcessedJobsToReview(w, r, result)
		return
	}
	if len(result.EasyApplyIDs) > 0 {
		redirectProcessedJobsToEasyApply(w, r, result)
		return
	}
	redirectJobBulkResult(w, r, "process", result, result.FirstErr)
}

func processJobsToDraft(
	ctx context.Context,
	st *store.Store,
	settings config.ApplicationSettings,
	extraAttachments []resolvedAttachment,
	ids []string,
	createDraft jobDraftCreator,
) jobProcessResult {
	result := jobProcessResult{Selected: len(ids)}
	if st == nil {
		result.Failed = len(ids)
		result.FirstErr = fmt.Errorf("store is required")
		return result
	}
	if createDraft == nil {
		result.Failed = len(ids)
		result.FirstErr = fmt.Errorf("draft creator is required")
		return result
	}

	seenReview := map[string]bool{}
	addReview := func(id string) {
		if id == "" || seenReview[id] {
			return
		}
		seenReview[id] = true
		result.ReviewIDs = append(result.ReviewIDs, id)
	}
	seenEasy := map[string]bool{}
	addEasy := func(id string) {
		if id == "" || seenEasy[id] {
			return
		}
		seenEasy[id] = true
		result.EasyApplyIDs = append(result.EasyApplyIDs, id)
	}

	for _, id := range ids {
		id = strings.TrimSpace(id)
		app, err := st.GetApplicationByJobID(id)
		if err != nil {
			result.Failed++
			setJobProcessError(&result, fmt.Errorf("%s: %w", id, err))
			continue
		}

		if app == nil {
			job, getErr := st.Get(id)
			if getErr != nil {
				result.Failed++
				setJobProcessError(&result, fmt.Errorf("%s: %w", id, getErr))
				continue
			}
			if !jobHasExplicitApplicationEmail(job) && !jobIsEasyApply(job) {
				result.NonEmailSkipped++
				continue
			}
			app, err = st.QueueApplication(id)
			if err != nil {
				result.Failed++
				setJobProcessError(&result, fmt.Errorf("%s: %w", id, err))
				continue
			}
			result.Queued++
			if app.State == models.ApplicationStateReadyEasyApply {
				result.EasyApplyQueued++
				addEasy(id)
				continue
			}
		}

		switch app.State {
		case models.ApplicationStateReadyEasyApply, models.ApplicationStateInProgress:
			result.EasyApplyExisting++
			addEasy(id)
			continue
		case models.ApplicationStateApplied:
			result.Skipped++
			continue
		case models.ApplicationStateNeedReview:
			result.NeedReview++
			continue
		case models.ApplicationStateDraftCreated:
			result.ExistingDrafts++
			addReview(id)
			continue
		case models.ApplicationStateApproved, models.ApplicationStateSent:
			result.Skipped++
			continue
		case models.ApplicationStateReadyEmail:
			// Continue below.
		default:
			result.Skipped++
			continue
		}

		if !applicationIsPrepared(app) {
			app, err = prepareApplicationForUI(st, id, settings, "")
			if err != nil {
				result.Failed++
				setJobProcessError(&result, fmt.Errorf("%s prepare: %w", id, err))
				continue
			}
			result.Prepared++
		}

		payload, err := appengine.BuildDraftPayload(app, settings)
		if err != nil {
			result.Failed++
			_, _ = st.RecordApplicationDraftError(id, err.Error())
			setJobProcessError(&result, fmt.Errorf("%s draft payload: %w", id, err))
			continue
		}
		for _, attachment := range extraAttachments {
			payload.AttachmentFiles = append(payload.AttachmentFiles, attachment.Path)
			payload.AttachmentNames = append(payload.AttachmentNames, attachment.Name)
		}
		payload.Body = draftBodyForAttachments(payload.Body, extraAttachments)
		if err := validateDraftAttachmentTotal(payload.AttachmentFiles, 18<<20); err != nil {
			result.Failed++
			_, _ = st.RecordApplicationDraftError(id, err.Error())
			setJobProcessError(&result, fmt.Errorf("%s attachments: %w", id, err))
			continue
		}

		draft, err := createDraft(ctx, payload)
		if err != nil {
			result.Failed++
			_, _ = st.RecordApplicationDraftError(id, err.Error())
			setJobProcessError(&result, fmt.Errorf("%s Gmail draft: %w", id, err))
			continue
		}
		if _, err := st.MarkApplicationDraftCreated(id, draft.DraftID); err != nil {
			result.Failed++
			setJobProcessError(&result, fmt.Errorf("%s: Gmail draft %s was created but local state update failed: %w", id, draft.DraftID, err))
			continue
		}
		result.DraftCreated++
		addReview(id)
	}
	return result
}

func jobHasExplicitApplicationEmail(job *models.JobPosting) bool {
	return job != nil &&
		strings.EqualFold(strings.TrimSpace(job.ApplicationMethod), "EMAIL") &&
		strings.TrimSpace(job.ApplyEmail) != ""
}

func jobIsEasyApply(job *models.JobPosting) bool {
	if job == nil {
		return false
	}
	method := strings.ToUpper(strings.TrimSpace(job.ApplicationMethod))
	return method == "LINKEDIN" || method == "EASY_APPLY"
}

func applicationIsPrepared(app *models.JobApplication) bool {
	return app != nil &&
		strings.TrimSpace(app.Subject) != "" &&
		strings.TrimSpace(app.Body) != "" &&
		strings.TrimSpace(app.CVProfile) != ""
}

func setJobProcessError(result *jobProcessResult, err error) {
	if result != nil && result.FirstErr == nil && err != nil {
		result.FirstErr = err
	}
}

func redirectProcessedJobsToReview(w http.ResponseWriter, r *http.Request, result jobProcessResult) {
	q := url.Values{}
	q.Set("ids", strings.Join(result.ReviewIDs, ","))
	q.Set("pos", "0")
	q.Set("job_process", "1")
	if len(result.EasyApplyIDs) > 0 {
		q.Set("easy_ids", strings.Join(result.EasyApplyIDs, ","))
	}
	addJobProcessResultQuery(q, result)
	if result.FirstErr != nil {
		msg := result.FirstErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("job_process_error", msg)
	}
	http.Redirect(w, r, "/app/applications/review?"+q.Encode(), http.StatusSeeOther)
}

func redirectProcessedJobsToEasyApply(w http.ResponseWriter, r *http.Request, result jobProcessResult) {
	q := url.Values{}
	q.Set("ids", strings.Join(result.EasyApplyIDs, ","))
	q.Set("pos", "0")
	q.Set("job_process", "1")
	addJobProcessResultQuery(q, result)
	if result.FirstErr != nil {
		msg := result.FirstErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("job_process_error", msg)
	}
	http.Redirect(w, r, "/app/applications/easy-apply?"+q.Encode(), http.StatusSeeOther)
}

func redirectJobBulkResult(w http.ResponseWriter, r *http.Request, action string, result jobProcessResult, actionErr error) {
	q := url.Values{}
	q.Set("job_bulk", action)
	addJobProcessResultQuery(q, result)
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("job_process_error", msg)
	}
	for formKey, queryKey := range map[string]string{
		"filter_q":        "q",
		"filter_location": "location",
		"filter_method":   "method",
		"filter_state":    "state",
		"filter_since":    "since",
		"filter_review":   "review",
		"filter_run":      "run",
		"filter_page":     "page",
	} {
		if v := strings.TrimSpace(r.PostFormValue(formKey)); v != "" {
			q.Set(queryKey, v)
		}
	}
	http.Redirect(w, r, "/app/jobs?"+q.Encode(), http.StatusSeeOther)
}

func addJobProcessResultQuery(q url.Values, result jobProcessResult) {
	q.Set("selected", strconv.Itoa(result.Selected))
	q.Set("queued_count", strconv.Itoa(result.Queued))
	q.Set("prepared_count", strconv.Itoa(result.Prepared))
	q.Set("draft_count", strconv.Itoa(result.DraftCreated))
	q.Set("existing_draft_count", strconv.Itoa(result.ExistingDrafts))
	q.Set("easy_apply_queued_count", strconv.Itoa(result.EasyApplyQueued))
	q.Set("easy_apply_existing_count", strconv.Itoa(result.EasyApplyExisting))
	q.Set("need_review_count", strconv.Itoa(result.NeedReview))
	q.Set("non_email_skipped_count", strconv.Itoa(result.NonEmailSkipped))
	q.Set("skipped_count", strconv.Itoa(result.Skipped))
	q.Set("failed_count", strconv.Itoa(result.Failed))
}
