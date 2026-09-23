package cmd

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	appengine "linkedin-jobs/internal/application"
	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

type appStats struct {
	JobsTotal       int
	EmailTotal      int
	PipelineTotal   int
	SentTotal       int
	CompletedTotal  int
	ReadyTotal      int
	EasyReadyTotal  int
	InProgressTotal int
	AppliedTotal    int
	NeedReviewTotal int
	DraftTotal      int
	ApprovedTotal    int
	InboxTotal       int
	ShortlistedTotal int
	LaterTotal       int
	SkippedTotal     int
}

type appJobRow struct {
	ID, Title, Company, Location, Method, State, ReviewState, Added, URL, Email string
	Selected bool
}

type appApplicationRow struct {
	JobID, Title, Company, Method, State, Recipient, Updated, DraftID string
	Prepared                                                          bool
}

type appSkipReasonRow struct {
	Reason string
	Label  string
	Count  int
	Share  int
}

type appCVProfile struct {
	ID, FileName, Path, Keywords string
	Priority                     int
	Default                      bool
	Exists                       bool
}

type appPageData struct {
	Title, Subtitle, Active, CSRF                  string
	CandidateName, CandidateInitials               string
	Stats                                          appStats
	Jobs                                           []appJobRow
	Applications                                   []appApplicationRow
	SelectedJob                                    *models.JobPosting
	SelectedApplication                            *models.JobApplication
	SelectedApplicationJob                         *models.JobPosting
	SelectedCVPath                                 string
	SelectedCVReady                                bool
	CVProfiles                                     []appCVProfile
	Attachments                                    []appAttachment
	DefaultCVProfile                               string
	SettingsPath                                   string
	DBPath                                         string
	ProfileLocation                                string
	ProfileArrangement                             string
	ProfileSalary                                  string
	Query                                          string
	LocationFilter                                 string
	MethodFilter                                   string
	StateFilter                                    string
	ApplicationScope                               string
	ApplicationStates                              []string
	ReviewFilter                                   string
	RunFilter                                      int64
	Locations                                      []string
	Methods                                        []string
	States                                         []string
	CollectKeywords                                string
	CollectLocation                                string
	CollectLocations                               string
	CollectPostedWithin                            string
	CollectTop                                     int
	CollectMessage                                 string
	CollectError                                   string
	CollectSearchRuns                              int
	CollectSearched                                int
	CollectNew                                     int
	CollectPersisted                               int
	CollectExactDuplicates                         int
	CollectLikelyReposts                           int
	CollectRunID                                   int64
	CollectionRuns                                 []store.CollectionRun
	SkipReasons                                    []appSkipReasonRow
	CollectorInsight                               string
	ActionMessage                                  string
	ActionError                                    string
	SelectionNotice                                string
	SelectedJobsCount                              int
	SelectedEmailCount                             int
	SelectedEasyApplyCount                         int
	SelectedOtherCount                             int
	GmailCredentialsPath                           string
	GmailTokenPath                                 string
	GmailCredentialsFound                          bool
	GmailConnected                                 bool
	ReviewMode                                     bool
	ReviewPosition                                 int
	ReviewTotal                                    int
	ReviewPrevURL                                  string
	ReviewNextURL                                  string
	ReviewStayURL                                  string
	SendConfirmMode                                bool
	SendCandidates                                 []appSendCandidate
	EasyApplyMode                                  bool
	EasyApplyPosition                              int
	EasyApplyTotal                                 int
	EasyApplyPrevURL                               string
	EasyApplyNextURL                               string
	EasyApplyStayURL                               string
	EasyApplyCurrentURL                            string
	EasyApplyRecommendedCV                         string
	EasyApplyRecommendedCVPath                     string
	EasyApplyRecommendedCVReady                    bool
	EasyApplyOpenTargets                           []easyApplyOpenTarget
	EasyApplyBatchURL                              string
	Error                                          string
	ListURL, PrevPageURL, NextPageURL, SinceFilter string
	Page, TotalRows, FirstRow, LastRow             int
	Filtered                                       bool
}

func newAppTemplate() (*template.Template, error) {
	return template.New("app.html").Funcs(template.FuncMap{
		"lower":           strings.ToLower,
		"base":            filepath.Base,
		"methodLabel":     applicationMethodLabel,
		"stateLabel":      applicationStateLabel,
		"reviewLabel":     jobReviewStateLabel,
		"reviewReasonLabel": jobReviewReasonLabel,
		"channelLabel":    applicationChannelLabel,
		"nextStep":        applicationNextStep,
		"date":            displayDate,
		"detailURL":       appDetailURL,
		"easyApplyMethod": isEasyApplyMethod,
	}).Parse(appHTML)
}

func (ws *webServer) handleAppRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/app/dashboard", http.StatusSeeOther)
}

func (ws *webServer) handleLegacyIndex(w http.ResponseWriter, r *http.Request) {
	clone := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/"
	clone.URL = &u
	ws.handleIndex(w, clone)
}

func (ws *webServer) handleAppUI(w http.ResponseWriter, r *http.Request) {
	tpl, err := newAppTemplate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pd, err := ws.buildAppPage(r)
	if err != nil {
		pd.Title, pd.Active = "Unable to load this page", ""
		pd.Subtitle = "Return to Jobs or Applications and try again."
		pd.Error = "The page could not be loaded. Check the local server and database, then retry."
		status := http.StatusInternalServerError
		if errors.Is(err, errAppNotFound) {
			pd.Title = "Page not found"
			pd.Error = "This job, application, or page is no longer available. Use the navigation to continue."
			status = http.StatusNotFound
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = tpl.Execute(w, pd)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.Execute(w, pd); err != nil {
		fmt.Fprintln(w, "render error:", err)
	}
}

func (ws *webServer) handleAppCollectRun(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	plan, err := parseUICollectPlan(r.PostForm)
	if err != nil {
		redirectCollectPlanResult(w, r, plan, nil, err)
		return
	}

	ws.collectMu.Lock()
	result, runErr := runCollectBatch(plan, ws.st, nil)
	ws.collectMu.Unlock()
	redirectCollectPlanResult(w, r, plan, result, runErr)
}

func parseUICollectForm(v url.Values) (collectRequest, error) {
	req := collectRequest{
		Keywords:     strings.TrimSpace(v.Get("keywords")),
		Location:     strings.TrimSpace(v.Get("location")),
		PostedWithin: strings.TrimSpace(v.Get("posted_within")),
	}
	if req.Keywords == "" {
		return req, fmt.Errorf("keywords are required")
	}
	if len(req.Keywords) > 160 {
		return req, fmt.Errorf("keywords are too long")
	}
	if len(req.Location) > 160 {
		return req, fmt.Errorf("location is too long")
	}
	if req.PostedWithin == "" {
		req.PostedWithin = "7d"
	}
	if _, err := resolvePostedWithin(req.PostedWithin); err != nil {
		return req, err
	}

	top := 50
	if raw := strings.TrimSpace(v.Get("top")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return req, fmt.Errorf("maximum results must be a number")
		}
		top = n
	}
	if top < 1 || top > 100 {
		return req, fmt.Errorf("maximum results must be between 1 and 100")
	}
	req.Top = top
	// The browser UI deliberately exposes only the anonymous, non-destructive
	// collector. Session fallback and force-overwrite remain CLI-only controls.
	req.WithSession = false
	req.ForceOverwrite = false
	return req, nil
}

func parseUICollectPlan(v url.Values) (collectPlan, error) {
	base := collectRequest{PostedWithin: strings.TrimSpace(v.Get("posted_within"))}
	if base.PostedWithin == "" {
		base.PostedWithin = "7d"
	}
	if _, err := resolvePostedWithin(base.PostedWithin); err != nil {
		return collectPlan{}, err
	}

	top := 50
	if raw := strings.TrimSpace(v.Get("top")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return collectPlan{}, fmt.Errorf("maximum results must be a number")
		}
		top = n
	}
	if top < 1 || top > 100 {
		return collectPlan{}, fmt.Errorf("maximum results must be between 1 and 100 per search")
	}
	base.Top = top
	base.WithSession = false
	base.ForceOverwrite = false

	locationText := v.Get("locations")
	if strings.TrimSpace(locationText) == "" {
		locationText = v.Get("location")
	}
	return buildCollectPlan(base, []string{v.Get("keywords")}, []string{locationText})
}

func redirectCollectPlanResult(w http.ResponseWriter, r *http.Request, plan collectPlan, result *collectRunResult, runErr error) {
	q := url.Values{}
	if len(plan.Queries) > 0 {
		q.Set("keywords", strings.Join(plan.Queries, "\n"))
	} else {
		q.Set("keywords", strings.TrimSpace(r.PostFormValue("keywords")))
	}
	if len(plan.Locations) > 0 {
		q.Set("locations", collectLocationsText(plan.Locations))
	} else {
		locationText := r.PostFormValue("locations")
		if strings.TrimSpace(locationText) == "" {
			locationText = r.PostFormValue("location")
		}
		q.Set("locations", strings.TrimSpace(locationText))
	}
	if len(plan.Requests) > 0 {
		req := plan.Requests[0]
		q.Set("posted_within", nonEmpty(req.PostedWithin, "7d"))
		if req.Top > 0 {
			q.Set("top", strconv.Itoa(req.Top))
		}
	} else {
		q.Set("posted_within", nonEmpty(strings.TrimSpace(r.PostFormValue("posted_within")), "7d"))
		if raw := strings.TrimSpace(r.PostFormValue("top")); raw != "" {
			q.Set("top", raw)
		}
	}
	if runErr != nil {
		msg := runErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("collect_error", msg)
	} else if result != nil {
		q.Set("collect", "done")
		q.Set("search_runs", strconv.Itoa(result.SearchRuns))
		q.Set("searched", strconv.Itoa(result.Searched))
		q.Set("new", strconv.Itoa(result.NewCandidates))
		q.Set("persisted", strconv.Itoa(result.Persisted))
		q.Set("exact", strconv.Itoa(result.ExactDuplicates))
		q.Set("reposts", strconv.Itoa(result.LikelyReposts))
		if result.RunID > 0 {
			q.Set("run_id", strconv.FormatInt(result.RunID, 10))
		}
	}
	http.Redirect(w, r, "/app/collect?"+q.Encode(), http.StatusSeeOther)
}

func redirectCollectResult(w http.ResponseWriter, r *http.Request, req collectRequest, result *collectRunResult, runErr error) {
	q := url.Values{}
	q.Set("keywords", req.Keywords)
	q.Set("location", req.Location)
	q.Set("posted_within", nonEmpty(req.PostedWithin, "7d"))
	if req.Top > 0 {
		q.Set("top", strconv.Itoa(req.Top))
	}
	if runErr != nil {
		msg := runErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("collect_error", msg)
	} else if result != nil {
		q.Set("collect", "done")
		q.Set("searched", strconv.Itoa(result.Searched))
		q.Set("new", strconv.Itoa(result.NewCandidates))
		q.Set("persisted", strconv.Itoa(result.Persisted))
		q.Set("exact", strconv.Itoa(result.ExactDuplicates))
		q.Set("reposts", strconv.Itoa(result.LikelyReposts))
		if result.RunID > 0 {
			q.Set("run_id", strconv.FormatInt(result.RunID, 10))
		}
	}
	http.Redirect(w, r, "/app/collect?"+q.Encode(), http.StatusSeeOther)
}

func (ws *webServer) handleAppQueueApplication(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	a, err := ws.st.QueueApplication(jobID)
	if err != nil {
		q := url.Values{"action_error": {err.Error()}}
		http.Redirect(w, r, "/app/jobs/"+url.PathEscape(jobID)+"?"+q.Encode(), http.StatusSeeOther)
		return
	}
	q := url.Values{"queued": {"1"}}
	http.Redirect(w, r, "/app/applications/"+url.PathEscape(a.JobID)+"?"+q.Encode(), http.StatusSeeOther)
}

func (ws *webServer) handleAppPrepareApplication(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}

	settings, err := config.LoadSettings()
	if err != nil {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("load settings: %w", err))
		return
	}

	override := strings.TrimSpace(r.PostFormValue("cv_profile"))
	if len(override) > 80 {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("CV profile id is too long"))
		return
	}
	ws.lifecycleMu.Lock()
	a, err := prepareApplicationForUI(ws.st, jobID, settings.Application, override)
	ws.lifecycleMu.Unlock()
	if err != nil {
		redirectApplicationAction(w, r, jobID, err)
		return
	}

	q := url.Values{"prepared": {"1"}}
	if a.CVProfile != "" {
		q.Set("cv_profile", a.CVProfile)
	}
	http.Redirect(w, r, "/app/applications/"+url.PathEscape(jobID)+"?"+q.Encode(), http.StatusSeeOther)
}

func (ws *webServer) handleAppSaveApplicationContent(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}

	subject := strings.TrimSpace(r.PostFormValue("subject"))
	body := strings.TrimSpace(r.PostFormValue("body"))
	if subject == "" {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("email subject cannot be empty"))
		return
	}
	if body == "" {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("email body cannot be empty"))
		return
	}
	if len(subject) > 240 {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("email subject must be 240 characters or fewer"))
		return
	}
	if len(body) > 12000 {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("email body must be 12000 characters or fewer"))
		return
	}

	ws.lifecycleMu.Lock()
	a, err := ws.st.GetApplicationByJobID(jobID)
	if err == nil && a == nil {
		err = fmt.Errorf("application is not queued")
	}
	if err == nil && a.State != models.ApplicationStateReadyEmail {
		err = fmt.Errorf("application state is %s; email content can only be edited before a Gmail draft is created", a.State)
	}
	if err == nil {
		_, err = ws.st.SaveApplicationPreparation(jobID, subject, body, a.CVProfile)
	}
	ws.lifecycleMu.Unlock()
	if err != nil {
		redirectApplicationAction(w, r, jobID, err)
		return
	}

	q := url.Values{"content_saved": {"1"}}
	http.Redirect(w, r, "/app/applications/"+url.PathEscape(jobID)+"?"+q.Encode(), http.StatusSeeOther)
}

func prepareApplicationForUI(st *store.Store, jobID string, settings config.ApplicationSettings, override string) (*models.JobApplication, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("missing job id")
	}
	a, err := st.GetApplicationByJobID(jobID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, fmt.Errorf("application is not queued")
	}
	if a.State != models.ApplicationStateReadyEmail {
		return nil, fmt.Errorf("application state is %s; expected READY_EMAIL", a.State)
	}
	job, err := st.Get(jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("collector job not found")
	}
	prepared, err := appengine.Prepare(job, settings, strings.TrimSpace(override))
	if err != nil {
		return nil, err
	}
	return st.SaveApplicationPreparation(jobID, prepared.Subject, prepared.Body, prepared.CVProfile)
}

func redirectApplicationAction(w http.ResponseWriter, r *http.Request, jobID string, actionErr error) {
	q := url.Values{}
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("action_error", msg)
	}
	http.Redirect(w, r, "/app/applications/"+url.PathEscape(jobID)+"?"+q.Encode(), http.StatusSeeOther)
}

func (ws *webServer) buildAppPage(r *http.Request) (appPageData, error) {
	pd := appPageData{
		CSRF: ws.csrf, Active: "dashboard", Title: "Dashboard",
		Subtitle:        "Overview of your job search and application progress.",
		CollectKeywords: "Sales Executive", CollectLocation: "Indonesia", CollectLocations: "Indonesia",
		CollectPostedWithin: "7d", CollectTop: 50,
	}
	gmailState := currentGmailUIState()
	pd.GmailCredentialsPath = gmailState.CredentialsPath
	pd.GmailTokenPath = gmailState.TokenPath
	pd.GmailCredentialsFound = gmailState.CredentialsFound
	pd.GmailConnected = gmailState.Connected

	settings, settingsErr := config.LoadSettings()
	if settingsErr == nil {
		pd.CandidateName = strings.TrimSpace(settings.Application.CandidateName)
		if pd.CandidateName == "" {
			pd.CandidateName = "Candidate"
		}
		pd.CandidateInitials = initials(pd.CandidateName)
		pd.DefaultCVProfile = settings.Application.DefaultCVProfile
		pd.SettingsPath = config.SettingsPath()
		pd.ProfileLocation = settings.Profile.Location
		pd.ProfileArrangement = strings.Join(settings.Profile.WorkArrangement, ", ")
		if settings.Profile.MinSalary != nil {
			pd.ProfileSalary = fmt.Sprintf("%s %.0f", settings.Profile.MinSalaryCurrency, *settings.Profile.MinSalary)
		}
		for _, p := range settings.Application.CVProfiles {
			exists := false
			if info, err := os.Stat(strings.TrimSpace(p.Path)); err == nil && !info.IsDir() {
				exists = true
			}
			pd.CVProfiles = append(pd.CVProfiles, appCVProfile{
				ID:       p.ID,
				FileName: filepath.Base(p.Path),
				Path:     p.Path,
				Keywords: strings.Join(p.Keywords, ", "),
				Priority: p.Priority,
				Default:  strings.EqualFold(p.ID, settings.Application.DefaultCVProfile),
				Exists:   exists,
			})
		}
		for _, a := range settings.Application.Attachments {
			pd.Attachments = append(pd.Attachments, attachmentView(a))
		}
	} else {
		pd.Error = "Settings: " + settingsErr.Error()
		pd.CandidateName = "Candidate"
		pd.CandidateInitials = "C"
	}
	pd.DBPath = loadCfg().DBPath

	jobs, err := ws.st.List(store.Filters{SortBySearched: true})
	if err != nil {
		return pd, err
	}
	apps, err := ws.st.ListApplications("", 0)
	if err != nil {
		return pd, err
	}

	pd.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	pd.LocationFilter = strings.TrimSpace(r.URL.Query().Get("location"))
	pd.MethodFilter = strings.TrimSpace(r.URL.Query().Get("method"))
	pd.StateFilter = strings.TrimSpace(r.URL.Query().Get("state"))
	if strings.HasPrefix(r.URL.Path, "/app/applications") {
		pd.ApplicationScope = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
		if pd.ApplicationScope == "" {
			if isCompletedApplicationState(pd.StateFilter) {
				pd.ApplicationScope = "completed"
			} else {
				pd.ApplicationScope = "active"
			}
		}
		if pd.ApplicationScope != "active" && pd.ApplicationScope != "completed" {
			pd.ApplicationScope = "active"
			pd.ActionError = "Unknown Applications view. Showing Active applications instead."
		}
		if pd.ApplicationScope == "completed" {
			pd.ApplicationStates = []string{models.ApplicationStateApplied, models.ApplicationStateSent}
		} else {
			pd.ApplicationStates = []string{
				models.ApplicationStateReadyEmail,
				models.ApplicationStateReadyEasyApply,
				models.ApplicationStateInProgress,
				models.ApplicationStateNeedReview,
				models.ApplicationStateDraftCreated,
				models.ApplicationStateApproved,
			}
		}
	}
	if strings.HasPrefix(r.URL.Path, "/app/jobs") {
		pd.ReviewFilter = strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("review")))
		if pd.ReviewFilter == "" {
			pd.ReviewFilter = models.JobReviewUnreviewed
		}
		if pd.ReviewFilter != "ALL" {
			if normalized, ok := models.NormalizeJobReviewState(pd.ReviewFilter); ok {
				pd.ReviewFilter = normalized
			} else {
				pd.ReviewFilter = models.JobReviewUnreviewed
				pd.ActionError = "Unknown Jobs view. Showing Inbox instead."
			}
		}
	}
	selectionScope := ""
	if strings.TrimSuffix(r.URL.Path, "/") == "/app/jobs" {
		selectionScope = normalizedJobSelectionScope(r.URL.Query())
		if ws.syncJobSelectionScope(selectionScope) {
			pd.SelectionNotice = "Job selection was cleared because the Jobs view or filters changed."
		}
	}
	if strings.HasPrefix(r.URL.Path, "/app/jobs") {
		if rawRun := strings.TrimSpace(r.URL.Query().Get("run")); rawRun != "" {
			runID, err := strconv.ParseInt(rawRun, 10, 64)
			if err != nil || runID <= 0 {
				pd.ActionError = "Invalid collection run. Showing the current Jobs view without run scope."
			} else {
				pd.RunFilter = runID
			}
		}
	}
	pd.SinceFilter = strings.TrimSpace(r.URL.Query().Get("since"))
	if pd.SinceFilter != "" {
		if _, err := time.Parse("2006-01-02", pd.SinceFilter); err != nil {
			pd.SinceFilter = ""
			pd.ActionError = "Choose a valid date for the date filter."
		}
	}
	pd.Filtered = pd.Query != "" || pd.LocationFilter != "" || pd.MethodFilter != "" || pd.StateFilter != "" || pd.SinceFilter != ""

	if r.URL.Query().Has("keywords") {
		pd.CollectKeywords = strings.TrimSpace(r.URL.Query().Get("keywords"))
	}
	if r.URL.Query().Has("locations") {
		pd.CollectLocations = strings.TrimSpace(r.URL.Query().Get("locations"))
		pd.CollectLocation = pd.CollectLocations
	} else if r.URL.Query().Has("location") {
		pd.CollectLocation = strings.TrimSpace(r.URL.Query().Get("location"))
		pd.CollectLocations = pd.CollectLocation
	}
	if r.URL.Query().Has("posted_within") {
		if v := strings.TrimSpace(r.URL.Query().Get("posted_within")); v != "" {
			pd.CollectPostedWithin = v
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("top")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			pd.CollectTop = n
		}
	}
	pd.CollectError = strings.TrimSpace(r.URL.Query().Get("collect_error"))
	if v := strings.TrimSpace(r.URL.Query().Get("action_error")); v != "" {
		pd.ActionError = v
	}
	if triage := strings.TrimSpace(r.URL.Query().Get("triage")); triage != "" {
		if updated, _ := strconv.Atoi(r.URL.Query().Get("updated")); updated > 0 {
			pd.ActionMessage = fmt.Sprintf("Job triage updated: %d job(s) moved to %s.", updated, jobReviewStateLabel(triage))
		}
	}
	if r.URL.Query().Get("started") == "1" {
		pd.ActionMessage = "Application started from the shortlist."
	}
	if r.URL.Query().Get("applications_started") == "1" {
		selected, _ := strconv.Atoi(r.URL.Query().Get("selected"))
		started, _ := strconv.Atoi(r.URL.Query().Get("started_count"))
		emailCount, _ := strconv.Atoi(r.URL.Query().Get("email_count"))
		easyCount, _ := strconv.Atoi(r.URL.Query().Get("easy_count"))
		needReview, _ := strconv.Atoi(r.URL.Query().Get("need_review_count"))
		existing, _ := strconv.Atoi(r.URL.Query().Get("existing_count"))
		notEligible, _ := strconv.Atoi(r.URL.Query().Get("not_eligible_count"))
		failed, _ := strconv.Atoi(r.URL.Query().Get("failed_count"))
		pd.ActionMessage = fmt.Sprintf("Start Applications: %d selected · %d started (%d email, %d Easy Apply, %d needs attention) · %d already in pipeline · %d not eligible · %d failed.", selected, started, emailCount, easyCount, needReview, existing, notEligible, failed)
	}
	if r.URL.Query().Get("queued") == "1" {
		pd.ActionMessage = "Application queued successfully."
	}
	if r.URL.Query().Get("prepared") == "1" {
		profile := strings.TrimSpace(r.URL.Query().Get("cv_profile"))
		pd.ActionMessage = "Default email content generated successfully. Review or edit it before creating the Gmail draft."
		if profile != "" {
			pd.ActionMessage += " CV profile: " + profile + "."
		}
	}
	if r.URL.Query().Get("content_saved") == "1" {
		pd.ActionMessage = "Email subject and body saved. The saved version will be used when you create the Gmail draft."
	}
	if r.URL.Query().Get("manual_sent") == "1" {
		pd.ActionMessage = "Application marked SENT manually and moved to Completed. No email was sent by this app."
	}
	if r.URL.Query().Get("draft_created") == "1" {
		pd.ActionMessage = "Gmail draft created successfully."
		if draftID := strings.TrimSpace(r.URL.Query().Get("draft_id")); draftID != "" {
			pd.ActionMessage += " Draft ID: " + draftID + "."
		}
	}
	if r.URL.Query().Get("draft_recreated") == "1" {
		pd.ActionMessage = "Replacement Gmail draft created successfully. The application is back in DRAFT_CREATED and must be reviewed again."
		if draftID := strings.TrimSpace(r.URL.Query().Get("draft_id")); draftID != "" {
			pd.ActionMessage += " New Draft ID: " + draftID + "."
		}
	}
	if r.URL.Query().Get("easy_applied") == "1" {
		pd.ActionMessage = "Easy Apply marked APPLIED after manual LinkedIn submission."
	}
	if action := strings.TrimSpace(r.URL.Query().Get("job_bulk")); action != "" || r.URL.Query().Get("job_process") == "1" {
		selected, _ := strconv.Atoi(r.URL.Query().Get("selected"))
		queued, _ := strconv.Atoi(r.URL.Query().Get("queued_count"))
		prepared, _ := strconv.Atoi(r.URL.Query().Get("prepared_count"))
		drafts, _ := strconv.Atoi(r.URL.Query().Get("draft_count"))
		existingDrafts, _ := strconv.Atoi(r.URL.Query().Get("existing_draft_count"))
		easyQueued, _ := strconv.Atoi(r.URL.Query().Get("easy_apply_queued_count"))
		easyExisting, _ := strconv.Atoi(r.URL.Query().Get("easy_apply_existing_count"))
		needReview, _ := strconv.Atoi(r.URL.Query().Get("need_review_count"))
		nonEmailSkipped, _ := strconv.Atoi(r.URL.Query().Get("non_email_skipped_count"))
		skipped, _ := strconv.Atoi(r.URL.Query().Get("skipped_count"))
		failed, _ := strconv.Atoi(r.URL.Query().Get("failed_count"))
		switch {
		case r.URL.Query().Get("job_process") == "1":
			pd.ActionMessage = fmt.Sprintf("Process Selected finished: %d selected · %d queued · %d prepared · %d new email drafts · %d existing email drafts · %d new Easy Apply · %d existing Easy Apply · %d unsupported skipped · %d need review · %d skipped · %d failed.", selected, queued, prepared, drafts, existingDrafts, easyQueued, easyExisting, nonEmailSkipped, needReview, skipped, failed)
		case action == "queue":
			pd.ActionMessage = fmt.Sprintf("Bulk queue finished: %d selected · %d queued · %d Easy Apply · %d unsupported skipped · %d need review · %d existing/skipped · %d failed.", selected, queued, easyQueued, nonEmailSkipped, needReview, skipped, failed)
		case action == "process":
			pd.ActionMessage = fmt.Sprintf("Process Selected finished: %d selected · %d queued · %d prepared · %d email drafts · %d Easy Apply · %d unsupported skipped · %d need review · %d skipped · %d failed.", selected, queued, prepared, drafts, easyQueued+easyExisting, nonEmailSkipped, needReview, skipped, failed)
		}
	}
	if processErr := strings.TrimSpace(r.URL.Query().Get("job_process_error")); processErr != "" {
		pd.ActionError = processErr
	}
	switch r.URL.Query().Get("review") {
	case "approved":
		pd.ActionMessage = "Application approved after manual Gmail draft review. No email was sent."
	case "unapproved":
		pd.ActionMessage = "Approval removed. The Gmail draft is back in manual review and no email was sent."
	}
	if r.URL.Query().Get("removed") == "1" {
		pd.ActionMessage = "Application removed from queue. The collected job remains in the Jobs database."
	}
	if action := strings.TrimSpace(r.URL.Query().Get("bulk")); action != "" {
		ok, _ := strconv.Atoi(r.URL.Query().Get("ok"))
		skipped, _ := strconv.Atoi(r.URL.Query().Get("skipped"))
		failed, _ := strconv.Atoi(r.URL.Query().Get("failed"))
		switch action {
		case "prepare":
			pd.ActionMessage = fmt.Sprintf("Bulk prepare finished: %d prepared, %d skipped, %d failed.", ok, skipped, failed)
		case "draft":
			pd.ActionMessage = fmt.Sprintf("Bulk draft creation finished: %d created, %d skipped, %d failed.", ok, skipped, failed)
		case "review":
			pd.ActionMessage = fmt.Sprintf("Review selection: %d eligible, %d skipped, %d failed.", ok, skipped, failed)
		case "send":
			pd.ActionMessage = fmt.Sprintf("Explicit send finished: %d sent, %d skipped, %d failed.", ok, skipped, failed)
		case "remove":
			pd.ActionMessage = fmt.Sprintf("Remove from queue finished: %d removed, %d protected/skipped, %d failed. Collected jobs were not deleted.", ok, skipped, failed)
		}
	}
	if bulkErr := strings.TrimSpace(r.URL.Query().Get("bulk_error")); bulkErr != "" {
		pd.ActionError = bulkErr
	}
	if fileErr := strings.TrimSpace(r.URL.Query().Get("file_error")); fileErr != "" {
		pd.ActionError = fileErr
	}
	if fileMessage := strings.TrimSpace(r.URL.Query().Get("file_message")); fileMessage != "" {
		pd.ActionMessage = fileMessage
	}
	if gmailErr := strings.TrimSpace(r.URL.Query().Get("gmail_error")); gmailErr != "" {
		pd.ActionError = gmailErr
	}
	switch r.URL.Query().Get("gmail") {
	case "connected":
		pd.ActionMessage = "Gmail connected successfully."
	case "disconnected":
		pd.ActionMessage = "Gmail disconnected."
	}
	if r.URL.Query().Get("collect") == "done" {
		pd.CollectSearchRuns, _ = strconv.Atoi(r.URL.Query().Get("search_runs"))
		pd.CollectSearched, _ = strconv.Atoi(r.URL.Query().Get("searched"))
		pd.CollectNew, _ = strconv.Atoi(r.URL.Query().Get("new"))
		pd.CollectPersisted, _ = strconv.Atoi(r.URL.Query().Get("persisted"))
		pd.CollectExactDuplicates, _ = strconv.Atoi(r.URL.Query().Get("exact"))
		pd.CollectLikelyReposts, _ = strconv.Atoi(r.URL.Query().Get("reposts"))
		pd.CollectRunID, _ = strconv.ParseInt(r.URL.Query().Get("run_id"), 10, 64)
		pd.CollectMessage = fmt.Sprintf("Collection finished: %d search combination(s), %d listings scanned, %d new, %d persisted.", pd.CollectSearchRuns, pd.CollectSearched, pd.CollectNew, pd.CollectPersisted)
	}

	locationSet := map[string]bool{}
	methodSet := map[string]bool{}
	for _, j := range jobs {
		if v := strings.TrimSpace(j.Location); v != "" {
			locationSet[v] = true
		}
		if v := applicationMethodLabel(j.ApplicationMethod); v != "" {
			methodSet[v] = true
		}
	}
	for v := range locationSet {
		pd.Locations = append(pd.Locations, v)
	}
	for v := range methodSet {
		pd.Methods = append(pd.Methods, v)
	}
	sort.Strings(pd.Locations)
	sort.Strings(pd.Methods)
	pd.States = []string{"NOT_APPLIED", models.ApplicationStateReadyEmail, models.ApplicationStateReadyEasyApply, models.ApplicationStateInProgress, models.ApplicationStateNeedReview, models.ApplicationStateDraftCreated, models.ApplicationStateApproved, models.ApplicationStateApplied, models.ApplicationStateSent}

	jobByID := map[string]*models.JobPosting{}
	for _, j := range jobs {
		jobByID[j.ID] = j
		switch j.ReviewState {
		case models.JobReviewShortlisted:
			pd.Stats.ShortlistedTotal++
		case models.JobReviewLater:
			pd.Stats.LaterTotal++
		case models.JobReviewSkipped:
			pd.Stats.SkippedTotal++
		default:
			pd.Stats.InboxTotal++
		}
		if strings.EqualFold(j.ApplicationMethod, "EMAIL") && strings.TrimSpace(j.ApplyEmail) != "" {
			pd.Stats.EmailTotal++
		}
	}
	appByJobID := map[string]*models.JobApplication{}
	for i := range apps {
		appByJobID[apps[i].JobID] = &apps[i]
	}
	pd.Stats.JobsTotal = len(jobs)
	for _, a := range apps {
		switch a.State {
		case models.ApplicationStateReadyEmail:
			pd.Stats.ReadyTotal++
		case models.ApplicationStateReadyEasyApply:
			pd.Stats.EasyReadyTotal++
		case models.ApplicationStateInProgress:
			pd.Stats.InProgressTotal++
		case models.ApplicationStateApplied:
			pd.Stats.AppliedTotal++
		case models.ApplicationStateNeedReview:
			pd.Stats.NeedReviewTotal++
		case models.ApplicationStateDraftCreated:
			pd.Stats.DraftTotal++
		case models.ApplicationStateApproved:
			pd.Stats.ApprovedTotal++
		case models.ApplicationStateSent:
			pd.Stats.SentTotal++
		}
	}
	pd.Stats.CompletedTotal = pd.Stats.SentTotal + pd.Stats.AppliedTotal
	pd.Stats.PipelineTotal = len(apps) - pd.Stats.CompletedTotal
	if reasons, reasonErr := ws.st.TopJobReviewReasons(models.JobReviewSkipped, 5); reasonErr == nil {
		for _, row := range reasons {
			share := 0
			if pd.Stats.SkippedTotal > 0 {
				share = row.Count * 100 / pd.Stats.SkippedTotal
			}
			pd.SkipReasons = append(pd.SkipReasons, appSkipReasonRow{
				Reason: row.Reason, Label: jobReviewReasonLabel(row.Reason), Count: row.Count, Share: share,
			})
		}
		if len(pd.SkipReasons) > 0 && pd.Stats.SkippedTotal >= 5 && pd.SkipReasons[0].Share >= 30 {
			top := pd.SkipReasons[0]
			pd.CollectorInsight = fmt.Sprintf("%d%% of skipped jobs are tagged %q. This may be a signal to review your collector queries or filters manually.", top.Share, top.Label)
		}
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/app"), "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}
	if len(parts) > 2 {
		return pd, errAppNotFound
	}
	section := "dashboard"
	if len(parts) > 0 {
		section = parts[0]
	}
	if len(parts) == 2 && section != "jobs" && section != "applications" {
		return pd, errAppNotFound
	}
	switch section {
	case "dashboard":
		pd.Active, pd.Title, pd.Subtitle = "dashboard", "Dashboard", "A focused overview of collected jobs, triage decisions, application progress, and the next actions that need attention."
		for _, j := range jobs {
			if j.ReviewState != models.JobReviewUnreviewed && j.ReviewState != "" {
				continue
			}
			pd.Jobs = append(pd.Jobs, uiJobRow(j, applicationStateFor(j.ID, apps)))
			if len(pd.Jobs) >= 8 {
				break
			}
		}
		pd.SelectedApplication = preferredApplication(apps)
		if pd.SelectedApplication != nil {
			pd.SelectedApplicationJob = jobByID[pd.SelectedApplication.JobID]
			pd.SelectedCVPath, pd.SelectedCVReady = selectedCVStatus(settings.Application.CVProfiles, pd.SelectedApplication.CVProfile)
		}
	case "jobs":
		pd.Active = "jobs"
		switch pd.ReviewFilter {
		case models.JobReviewShortlisted:
			pd.Title, pd.Subtitle = "Shortlisted", "Jobs you want to pursue. Review them before starting application execution."
		case models.JobReviewLater:
			pd.Title, pd.Subtitle = "Later", "Interesting jobs you intentionally parked for another time."
		case models.JobReviewSkipped:
			pd.Title, pd.Subtitle = "Skipped", "Jobs you decided not to pursue. They remain stored for history and deduplication."
		case "ALL":
			pd.Title, pd.Subtitle = "All Jobs", "Search the complete collected job history across every triage decision."
		default:
			pd.Title, pd.Subtitle = "Jobs Inbox", "Review collected jobs and decide: shortlist, save for later, or skip."
		}
		var runJobIDs map[string]bool
		if pd.RunFilter > 0 {
			runJobIDs, err = ws.st.CollectionRunJobIDs(pd.RunFilter, true)
			if err != nil {
				return pd, err
			}
			pd.Subtitle += fmt.Sprintf(" Showing jobs from collection run #%d.", pd.RunFilter)
		}
		for _, j := range jobs {
			if runJobIDs != nil && !runJobIDs[j.ID] {
				continue
			}
			if pd.ReviewFilter != "ALL" && !strings.EqualFold(j.ReviewState, pd.ReviewFilter) {
				continue
			}
			state := applicationStateFor(j.ID, apps)
			if !matchesUIJob(j, state, pd.Query, pd.LocationFilter, pd.MethodFilter, pd.StateFilter) {
				continue
			}
			if pd.SinceFilter != "" && j.SearchedAt < pd.SinceFilter {
				continue
			}
			pd.Jobs = append(pd.Jobs, uiJobRow(j, state))
		}
		if len(parts) > 1 {
			pd.Title, pd.Subtitle = "Job Detail", "Review the collected job data and decide whether it should enter the application pipeline."
			pd.SelectedJob = jobByID[parts[1]]
			if pd.SelectedJob == nil {
				return pd, fmt.Errorf("%w: job %s", errAppNotFound, parts[1])
			}
			pd.SelectedApplication, err = ws.st.GetApplicationByJobID(parts[1])
			if err != nil {
				return pd, err
			}
		}
	case "applications":
		pd.Active, pd.Title = "applications", "Applications"
		if pd.ApplicationScope == "completed" {
			pd.Subtitle = "Completed application history. LinkedIn APPLIED and email SENT records move here automatically."
		} else {
			pd.Subtitle = "Active application queue. Completed applications automatically leave this view."
		}
		for _, a := range apps {
			completed := isCompletedApplicationState(a.State)
			if pd.ApplicationScope == "completed" && !completed {
				continue
			}
			if pd.ApplicationScope != "completed" && completed {
				continue
			}
			j := jobByID[a.JobID]
			if !matchesUIApplication(&a, j, pd.Query, pd.MethodFilter, pd.StateFilter) {
				continue
			}
			row := appApplicationRow{
				JobID: a.JobID, Method: "EMAIL", State: a.State, Recipient: a.Recipient,
				Updated: displayDate(a.UpdatedAt), DraftID: a.GmailDraftID,
				Prepared: strings.TrimSpace(a.Subject) != "" && strings.TrimSpace(a.Body) != "" && strings.TrimSpace(a.CVProfile) != "",
			}
			if j != nil {
				row.Title, row.Company, row.Method = j.Title, j.Company, applicationMethodLabel(j.ApplicationMethod)
			}
			if pd.SinceFilter != "" && a.UpdatedAt < pd.SinceFilter {
				continue
			}
			pd.Applications = append(pd.Applications, row)
		}
		if len(parts) > 1 && parts[1] == "easy-apply" {
			pd.Title, pd.Subtitle = "Easy Apply Queue", "Work through LinkedIn applications one at a time. You submit each application manually."
			pd.EasyApplyMode = true
			queue := easyApplyQueueIDs(apps, r.URL.Query().Get("ids"))
			pd.EasyApplyTotal = len(queue)
			if len(queue) > 0 {
				pos := 0
				if raw := strings.TrimSpace(r.URL.Query().Get("pos")); raw != "" {
					if n, convErr := strconv.Atoi(raw); convErr == nil {
						pos = n
					}
				}
				if pos < 0 {
					pos = 0
				}
				if pos >= len(queue) {
					pos = len(queue) - 1
				}
				pd.EasyApplyPosition = pos + 1
				currentID := queue[pos]
				a, getErr := ws.st.GetApplicationByJobID(currentID)
				if getErr != nil {
					return pd, getErr
				}
				pd.SelectedApplication = a
				if a != nil {
					pd.SelectedApplicationJob = jobByID[a.JobID]
					if safeURL, urlErr := safeLinkedInApplyURL(a.ApplyURL); urlErr == nil {
						pd.EasyApplyCurrentURL = safeURL
					}
					if pd.SelectedApplicationJob != nil {
						profile, profileErr := appengine.SelectCVProfile(pd.SelectedApplicationJob, settings.Application, a.CVProfile)
						if profileErr == nil && profile != nil {
							pd.EasyApplyRecommendedCV = profile.ID
							pd.EasyApplyRecommendedCVPath = profile.Path
							if info, statErr := os.Stat(strings.TrimSpace(profile.Path)); statErr == nil && !info.IsDir() {
								pd.EasyApplyRecommendedCVReady = true
							}
						}
					}
				}
				pd.EasyApplyPrevURL = easyApplyQueueURL(queue, pos-1)
				pd.EasyApplyNextURL = easyApplyQueueURL(queue, pos+1)
				pd.EasyApplyStayURL = easyApplyQueueURL(queue, pos)
				for idx := pos + 1; idx < len(queue) && idx < pos+4; idx++ {
					id := queue[idx]
					app := appByJobID[id]
					job := jobByID[id]
					if app == nil || job == nil {
						continue
					}
					safeURL, urlErr := safeLinkedInApplyURL(app.ApplyURL)
					if urlErr != nil {
						continue
					}
					pd.EasyApplyOpenTargets = append(pd.EasyApplyOpenTargets, easyApplyOpenTarget{
						JobID: id, Title: job.Title, Company: job.Company, URL: safeURL,
						MarkURL: "/app/applications/" + url.PathEscape(id) + "/easy-apply/open",
					})
				}
			}
		} else if len(parts) > 1 && parts[1] == "review" {
			pd.Title, pd.Subtitle = "Review Queue", "Check each Gmail draft, then approve it or skip it for later."
			pd.ReviewMode = true
			if rawEasy := strings.TrimSpace(r.URL.Query().Get("easy_ids")); rawEasy != "" {
				ids := []string{}
				for _, id := range strings.Split(rawEasy, ",") {
					id = strings.TrimSpace(id)
					if id != "" {
						ids = append(ids, id)
					}
				}
				if len(ids) > 0 {
					pd.EasyApplyBatchURL = easyApplyQueueURL(ids, 0)
				}
			}
			queue := reviewQueueIDs(apps, r.URL.Query().Get("ids"))
			pd.ReviewTotal = len(queue)
			if len(queue) > 0 {
				pos := 0
				if raw := strings.TrimSpace(r.URL.Query().Get("pos")); raw != "" {
					if n, convErr := strconv.Atoi(raw); convErr == nil {
						pos = n
					}
				}
				if pos < 0 {
					pos = 0
				}
				if pos >= len(queue) {
					pos = len(queue) - 1
				}
				pd.ReviewPosition = pos + 1
				a, getErr := ws.st.GetApplicationByJobID(queue[pos])
				if getErr != nil {
					return pd, getErr
				}
				pd.SelectedApplication = a
				if a != nil {
					pd.SelectedApplicationJob = jobByID[a.JobID]
					pd.SelectedCVPath, pd.SelectedCVReady = selectedCVStatus(settings.Application.CVProfiles, a.CVProfile)
				}
				pd.ReviewPrevURL = reviewQueueURL(queue, pos-1)
				pd.ReviewNextURL = reviewQueueURL(queue, pos+1)
				pd.ReviewStayURL = reviewQueueURL(queue, pos)
			}
		} else if len(parts) > 1 {
			pd.Title, pd.Subtitle = "Application Detail", "Inspect prepared content, Gmail draft state, review history, and the next lifecycle action."
			a, err := ws.st.GetApplicationByJobID(parts[1])
			if err != nil {
				return pd, err
			}
			if a == nil {
				return pd, fmt.Errorf("%w: application %s", errAppNotFound, parts[1])
			}
			pd.SelectedApplication = a
			pd.SelectedApplicationJob = jobByID[a.JobID]
			pd.SelectedCVPath, pd.SelectedCVReady = selectedCVStatus(settings.Application.CVProfiles, a.CVProfile)
		}
	case "cv-profiles":
		pd.Active, pd.Title, pd.Subtitle = "cv-profiles", "CV Profiles", "Manage the CV and supporting files used during preparation and Gmail draft creation."
	case "collect":
		pd.Active, pd.Title, pd.Subtitle = "collect", "Collect LinkedIn Jobs", "Collect bounded LinkedIn search results into the local jobs database for review and batch processing."
		if runs, runErr := ws.st.ListCollectionRuns(8); runErr == nil {
			pd.CollectionRuns = runs
		}
	case "settings":
		pd.Active, pd.Title, pd.Subtitle = "settings", "Settings", "Connect Gmail and inspect the preferences used by this local command center."
	default:
		return pd, fmt.Errorf("%w: %s", errAppNotFound, section)
	}

	if pd.Active == "jobs" || pd.Active == "applications" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		pd.ListURL = appListURL("/app/"+pd.Active, r.URL.Query(), page)
		if pd.SelectedJob == nil && pd.SelectedApplication == nil && !pd.ReviewMode && !pd.EasyApplyMode {
			total := len(pd.Jobs)
			if pd.Active == "applications" {
				total = len(pd.Applications)
			}
			pages := (total + appPageSize - 1) / appPageSize
			if pages < 1 {
				pages = 1
			}
			if page > pages {
				page = pages
			}
			start := (page - 1) * appPageSize
			end := start + appPageSize
			if end > total {
				end = total
			}
			pd.Page, pd.TotalRows, pd.FirstRow, pd.LastRow = page, total, start+1, end
			if total == 0 {
				pd.FirstRow = 0
			}
			if pd.Active == "jobs" {
				pd.Jobs = pd.Jobs[start:end]
				selectedJobs := ws.selectedJobsForScope(selectionScope)
				pd.SelectedJobsCount = len(selectedJobs)
				summary := ws.selectionSummary(selectionScope)
				pd.SelectedEmailCount = summary.Email
				pd.SelectedEasyApplyCount = summary.EasyApply
				pd.SelectedOtherCount = summary.Other
				for i := range pd.Jobs {
					pd.Jobs[i].Selected = selectedJobs[pd.Jobs[i].ID]
				}
			} else {
				pd.Applications = pd.Applications[start:end]
			}
			if page > 1 {
				pd.PrevPageURL = appListURL("/app/"+pd.Active, r.URL.Query(), page-1)
			}
			if page < pages {
				pd.NextPageURL = appListURL("/app/"+pd.Active, r.URL.Query(), page+1)
			}
		}
	}
	return pd, nil
}

func reviewQueueIDs(apps []models.JobApplication, raw string) []string {
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
	for _, a := range apps {
		if a.State != models.ApplicationStateDraftCreated {
			continue
		}
		if len(selected) > 0 && !selected[a.JobID] {
			continue
		}
		out = append(out, a.JobID)
		if len(out) >= maxBulkApplicationSelection {
			break
		}
	}
	return out
}

func reviewQueueURL(ids []string, pos int) string {
	if len(ids) == 0 {
		return "/app/applications/review"
	}
	for pos < 0 {
		pos += len(ids)
	}
	pos = pos % len(ids)
	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	q.Set("pos", strconv.Itoa(pos))
	return "/app/applications/review?" + q.Encode()
}

func selectedCVStatus(profiles []config.CVProfileSettings, profileID string) (string, bool) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "", false
	}
	for _, p := range profiles {
		if !strings.EqualFold(strings.TrimSpace(p.ID), profileID) {
			continue
		}
		path := strings.TrimSpace(p.Path)
		if path == "" {
			return "", false
		}
		info, err := os.Stat(path)
		return path, err == nil && !info.IsDir()
	}
	return "", false
}

func matchesUIJob(j *models.JobPosting, state, q, location, method, appState string) bool {
	if j == nil {
		return false
	}
	if q != "" {
		blob := strings.ToLower(strings.Join([]string{j.Title, j.Company, j.Location, j.ApplyEmail}, " "))
		if !strings.Contains(blob, strings.ToLower(q)) {
			return false
		}
	}
	if location != "" && !strings.EqualFold(strings.TrimSpace(j.Location), location) {
		return false
	}
	if method != "" && !strings.EqualFold(applicationMethodLabel(j.ApplicationMethod), method) {
		return false
	}
	if appState != "" && !strings.EqualFold(state, appState) {
		return false
	}
	return true
}

func matchesUIApplication(a *models.JobApplication, j *models.JobPosting, q, method, state string) bool {
	if a == nil {
		return false
	}
	if state != "" && !strings.EqualFold(a.State, state) {
		return false
	}
	if method != "" {
		jobMethod := ""
		if j != nil {
			jobMethod = applicationMethodLabel(j.ApplicationMethod)
		}
		if !strings.EqualFold(strings.TrimSpace(jobMethod), method) {
			return false
		}
	}
	if q != "" {
		parts := []string{a.JobID, a.Recipient, a.Subject}
		if j != nil {
			parts = append(parts, j.Title, j.Company, j.Location)
		}
		if !strings.Contains(strings.ToLower(strings.Join(parts, " ")), strings.ToLower(q)) {
			return false
		}
	}
	return true
}

func isCompletedApplicationState(state string) bool {
	state = strings.ToUpper(strings.TrimSpace(state))
	return state == models.ApplicationStateApplied || state == models.ApplicationStateSent
}

func preferredApplication(apps []models.JobApplication) *models.JobApplication {
	for _, state := range []string{
		models.ApplicationStateApproved,
		models.ApplicationStateDraftCreated,
		models.ApplicationStateReadyEmail,
		models.ApplicationStateInProgress,
		models.ApplicationStateReadyEasyApply,
		models.ApplicationStateSent,
		models.ApplicationStateApplied,
	} {
		for i := range apps {
			if apps[i].State == state {
				a := apps[i]
				return &a
			}
		}
	}
	if len(apps) > 0 {
		a := apps[0]
		return &a
	}
	return nil
}

func applicationStateFor(jobID string, apps []models.JobApplication) string {
	for i := range apps {
		if apps[i].JobID == jobID {
			return apps[i].State
		}
	}
	return "NOT_APPLIED"
}

func uiJobRow(j *models.JobPosting, state string) appJobRow {
	added := j.SearchedAt
	if added == "" {
		added = j.FirstSeen
	}
	if added == "" {
		added = j.PostedAt
	}
	return appJobRow{
		ID: j.ID, Title: j.Title, Company: j.Company, Location: j.Location,
		Method: applicationMethodLabel(j.ApplicationMethod), State: state, ReviewState: j.ReviewState,
		Added: displayDate(added), URL: j.URL, Email: j.ApplyEmail,
	}
}

func isEasyApplyMethod(method string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	return method == "LINKEDIN" || method == "EASY_APPLY"
}

func jobReviewReasonLabel(reason string) string {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case models.JobReviewReasonRoleMismatch:
		return "Role mismatch"
	case models.JobReviewReasonLocation:
		return "Location"
	case models.JobReviewReasonExperienceTooHigh:
		return "Experience too high"
	case models.JobReviewReasonIndustry:
		return "Industry"
	case models.JobReviewReasonCompany:
		return "Company"
	case models.JobReviewReasonCompensation:
		return "Compensation"
	case models.JobReviewReasonUnclearPosting:
		return "Unclear posting"
	case models.JobReviewReasonAlreadySeen:
		return "Already seen"
	case models.JobReviewReasonOther:
		return "Other"
	default:
		return nonEmpty(strings.TrimSpace(reason), "Not specified")
	}
}

func jobReviewStateLabel(state string) string {
	state = strings.ToUpper(strings.TrimSpace(state))
	switch state {
	case models.JobReviewUnreviewed:
		return "Inbox"
	case models.JobReviewShortlisted:
		return "Shortlisted"
	case models.JobReviewLater:
		return "Later"
	case models.JobReviewSkipped:
		return "Skipped"
	case "ALL":
		return "All Jobs"
	default:
		return nonEmpty(state, "Inbox")
	}
}

func applicationMethodLabel(method string) string {
	if isEasyApplyMethod(method) {
		return "EASY_APPLY"
	}
	return nonEmpty(strings.ToUpper(strings.TrimSpace(method)), "UNKNOWN")
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func displayDate(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "—"
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("Jan 02, 2006")
		}
	}
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}

func initials(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return "C"
	}
	out := strings.ToUpper(string([]rune(fields[0])[0]))
	if len(fields) > 1 {
		out += strings.ToUpper(string([]rune(fields[len(fields)-1])[0]))
	}
	return out
}

//go:embed templates/app.html templates/app.css templates/app.js
var appTemplateFiles embed.FS

var appHTML = func() string {
	html, _ := appTemplateFiles.ReadFile("templates/app.html")
	css, _ := appTemplateFiles.ReadFile("templates/app.css")
	js, _ := appTemplateFiles.ReadFile("templates/app.js")
	return string(html) + `{{define "styles"}}` + string(css) + `{{end}}{{define "scripts"}}` + string(js) + `{{end}}`
}()
