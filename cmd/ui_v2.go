package cmd

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sort"
	"strings"
	"time"

	appengine "linkedin-jobs/internal/application"
	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

type appStats struct {
	JobsTotal      int
	EmailTotal     int
	PipelineTotal  int
	SentTotal      int
	ReadyTotal     int
	NeedReviewTotal int
	DraftTotal     int
	ApprovedTotal  int
}

type appJobRow struct {
	ID, Title, Company, Location, Method, State, Added, URL, Email string
}

type appApplicationRow struct {
	JobID, Title, Company, Method, State, Recipient, Updated, DraftID string
	Prepared bool
}

type appCVProfile struct {
	ID, FileName, Path, Keywords string
	Priority int
	Default bool
	Exists bool
}

type appPageData struct {
	Title, Subtitle, Active, CSRF string
	CandidateName, CandidateInitials string
	Stats appStats
	Jobs []appJobRow
	Applications []appApplicationRow
	SelectedJob *models.JobPosting
	SelectedApplication *models.JobApplication
	SelectedApplicationJob *models.JobPosting
	SelectedCVPath string
	SelectedCVReady bool
	CVProfiles []appCVProfile
	Attachments []appAttachment
	DefaultCVProfile string
	SettingsPath string
	DBPath string
	ProfileLocation string
	ProfileArrangement string
	ProfileSalary string
	Query string
	LocationFilter string
	MethodFilter string
	StateFilter string
	Locations []string
	Methods []string
	States []string
	CollectKeywords string
	CollectLocation string
	CollectPostedWithin string
	CollectTop int
	CollectMessage string
	CollectError string
	CollectSearched int
	CollectNew int
	CollectPersisted int
	CollectExactDuplicates int
	CollectLikelyReposts int
	ActionMessage string
	ActionError string
	GmailCredentialsPath string
	GmailTokenPath string
	GmailCredentialsFound bool
	GmailConnected bool
	ReviewMode bool
	ReviewPosition int
	ReviewTotal int
	ReviewPrevURL string
	ReviewNextURL string
	ReviewStayURL string
	SendConfirmMode bool
	SendCandidates []appSendCandidate
	Error string
}

func newAppTemplate() (*template.Template, error) {
	return template.New("app").Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"base": filepath.Base,
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	req, err := parseUICollectForm(r.PostForm)
	if err != nil {
		redirectCollectResult(w, r, req, nil, err)
		return
	}

	ws.collectMu.Lock()
	result, runErr := runCollect(req, ws.st, nil)
	ws.collectMu.Unlock()
	redirectCollectResult(w, r, req, result, runErr)
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
		Subtitle: "Overview of your job search and application progress.",
		CollectKeywords: "Sales Executive", CollectLocation: "Indonesia",
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
				ID: p.ID,
				FileName: filepath.Base(p.Path),
				Path: p.Path,
				Keywords: strings.Join(p.Keywords, ", "),
				Priority: p.Priority,
				Default: strings.EqualFold(p.ID, settings.Application.DefaultCVProfile),
				Exists: exists,
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

	if r.URL.Query().Has("keywords") { pd.CollectKeywords = strings.TrimSpace(r.URL.Query().Get("keywords")) }
	if r.URL.Query().Has("location") { pd.CollectLocation = strings.TrimSpace(r.URL.Query().Get("location")) }
	if r.URL.Query().Has("posted_within") {
		if v := strings.TrimSpace(r.URL.Query().Get("posted_within")); v != "" { pd.CollectPostedWithin = v }
	}
	if v := strings.TrimSpace(r.URL.Query().Get("top")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 { pd.CollectTop = n }
	}
	pd.CollectError = strings.TrimSpace(r.URL.Query().Get("collect_error"))
	pd.ActionError = strings.TrimSpace(r.URL.Query().Get("action_error"))
	if r.URL.Query().Get("queued") == "1" {
		pd.ActionMessage = "Application queued successfully."
	}
	if r.URL.Query().Get("prepared") == "1" {
		profile := strings.TrimSpace(r.URL.Query().Get("cv_profile"))
		pd.ActionMessage = "Application prepared successfully."
		if profile != "" {
			pd.ActionMessage += " CV profile: " + profile + "."
		}
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
	if action := strings.TrimSpace(r.URL.Query().Get("job_bulk")); action != "" || r.URL.Query().Get("job_process") == "1" {
		selected, _ := strconv.Atoi(r.URL.Query().Get("selected"))
		queued, _ := strconv.Atoi(r.URL.Query().Get("queued_count"))
		prepared, _ := strconv.Atoi(r.URL.Query().Get("prepared_count"))
		drafts, _ := strconv.Atoi(r.URL.Query().Get("draft_count"))
		existingDrafts, _ := strconv.Atoi(r.URL.Query().Get("existing_draft_count"))
		needReview, _ := strconv.Atoi(r.URL.Query().Get("need_review_count"))
		nonEmailSkipped, _ := strconv.Atoi(r.URL.Query().Get("non_email_skipped_count"))
		skipped, _ := strconv.Atoi(r.URL.Query().Get("skipped_count"))
		failed, _ := strconv.Atoi(r.URL.Query().Get("failed_count"))
		switch {
		case r.URL.Query().Get("job_process") == "1":
			pd.ActionMessage = fmt.Sprintf("Process to Draft finished: %d selected · %d queued · %d prepared · %d new drafts · %d existing drafts in review · %d non-email skipped · %d existing need review · %d skipped · %d failed.", selected, queued, prepared, drafts, existingDrafts, nonEmailSkipped, needReview, skipped, failed)
		case action == "queue":
			pd.ActionMessage = fmt.Sprintf("Bulk queue finished: %d selected · %d queued · %d non-email skipped · %d need review · %d existing/skipped · %d failed.", selected, queued, nonEmailSkipped, needReview, skipped, failed)
		case action == "process":
			pd.ActionMessage = fmt.Sprintf("Process to Draft finished: %d selected · %d queued · %d prepared · %d drafts · %d non-email skipped · %d existing need review · %d skipped · %d failed.", selected, queued, prepared, drafts, nonEmailSkipped, needReview, skipped, failed)
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
		pd.CollectSearched, _ = strconv.Atoi(r.URL.Query().Get("searched"))
		pd.CollectNew, _ = strconv.Atoi(r.URL.Query().Get("new"))
		pd.CollectPersisted, _ = strconv.Atoi(r.URL.Query().Get("persisted"))
		pd.CollectExactDuplicates, _ = strconv.Atoi(r.URL.Query().Get("exact"))
		pd.CollectLikelyReposts, _ = strconv.Atoi(r.URL.Query().Get("reposts"))
		pd.CollectMessage = fmt.Sprintf("Collection finished: %d searched, %d new, %d persisted.", pd.CollectSearched, pd.CollectNew, pd.CollectPersisted)
	}

	locationSet := map[string]bool{}
	methodSet := map[string]bool{}
	for _, j := range jobs {
		if v := strings.TrimSpace(j.Location); v != "" { locationSet[v] = true }
		if v := strings.TrimSpace(j.ApplicationMethod); v != "" { methodSet[v] = true }
	}
	for v := range locationSet { pd.Locations = append(pd.Locations, v) }
	for v := range methodSet { pd.Methods = append(pd.Methods, v) }
	sort.Strings(pd.Locations)
	sort.Strings(pd.Methods)
	pd.States = []string{"NOT_APPLIED", models.ApplicationStateReadyEmail, models.ApplicationStateNeedReview, models.ApplicationStateDraftCreated, models.ApplicationStateApproved, models.ApplicationStateSent}

	jobByID := map[string]*models.JobPosting{}
	for _, j := range jobs {
		jobByID[j.ID] = j
		if strings.EqualFold(j.ApplicationMethod, "EMAIL") && strings.TrimSpace(j.ApplyEmail) != "" {
			pd.Stats.EmailTotal++
		}
	}
	pd.Stats.JobsTotal = len(jobs)
	pd.Stats.PipelineTotal = len(apps)
	for _, a := range apps {
		switch a.State {
		case models.ApplicationStateReadyEmail:
			pd.Stats.ReadyTotal++
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

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/app"), "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}
	section := "dashboard"
	if len(parts) > 0 {
		section = parts[0]
	}
	switch section {
	case "dashboard":
		pd.Active, pd.Title, pd.Subtitle = "dashboard", "Dashboard", "A focused overview of collected jobs, application progress, and the next actions that need attention."
		for i, j := range jobs {
			if i >= 8 { break }
			pd.Jobs = append(pd.Jobs, uiJobRow(j, applicationStateFor(j.ID, apps)))
		}
		pd.SelectedApplication = preferredApplication(apps)
		if pd.SelectedApplication != nil {
			pd.SelectedApplicationJob = jobByID[pd.SelectedApplication.JobID]
			pd.SelectedCVPath, pd.SelectedCVReady = selectedCVStatus(settings.Application.CVProfiles, pd.SelectedApplication.CVProfile)
		}
	case "jobs":
		pd.Active, pd.Title, pd.Subtitle = "jobs", "Jobs", "Filter the collected database, select multiple jobs, and move them into the application workflow."
		for _, j := range jobs {
			state := applicationStateFor(j.ID, apps)
			if !matchesUIJob(j, state, pd.Query, pd.LocationFilter, pd.MethodFilter, pd.StateFilter) {
				continue
			}
			pd.Jobs = append(pd.Jobs, uiJobRow(j, state))
		}
		if len(parts) > 1 {
			pd.Title, pd.Subtitle = "Job Detail", "Review the collected job data and decide whether it should enter the application pipeline."
			pd.SelectedJob = jobByID[parts[1]]
			if pd.SelectedJob == nil {
				return pd, fmt.Errorf("job %s not found", parts[1])
			}
			pd.SelectedApplication, err = ws.st.GetApplicationByJobID(parts[1])
			if err != nil {
				return pd, err
			}
		}
	case "applications":
		pd.Active, pd.Title, pd.Subtitle = "applications", "Applications", "Prepare, review, approve, and send applications from one controlled workbench."
		for _, a := range apps {
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
				row.Title, row.Company, row.Method = j.Title, j.Company, j.ApplicationMethod
			}
			pd.Applications = append(pd.Applications, row)
		}
		if len(parts) > 1 && parts[1] == "review" {
			pd.Title, pd.Subtitle = "Review Queue", "Review prepared Gmail drafts one by one, approve the good ones, and keep moving without reopening each record."
			pd.ReviewMode = true
			queue := reviewQueueIDs(apps, r.URL.Query().Get("ids"))
			pd.ReviewTotal = len(queue)
			if len(queue) > 0 {
				pos := 0
				if raw := strings.TrimSpace(r.URL.Query().Get("pos")); raw != "" {
					if n, convErr := strconv.Atoi(raw); convErr == nil {
						pos = n
					}
				}
				if pos < 0 { pos = 0 }
				if pos >= len(queue) { pos = len(queue)-1 }
				pd.ReviewPosition = pos + 1
				a, getErr := ws.st.GetApplicationByJobID(queue[pos])
				if getErr != nil { return pd, getErr }
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
			if err != nil { return pd, err }
			if a == nil { return pd, fmt.Errorf("application %s not found", parts[1]) }
			pd.SelectedApplication = a
			pd.SelectedApplicationJob = jobByID[a.JobID]
			pd.SelectedCVPath, pd.SelectedCVReady = selectedCVStatus(settings.Application.CVProfiles, a.CVProfile)
		}
	case "cv-profiles":
		pd.Active, pd.Title, pd.Subtitle = "cv-profiles", "CV Profiles", "Manage the CV and supporting files used during preparation and Gmail draft creation."
	case "collect":
		pd.Active, pd.Title, pd.Subtitle = "collect", "Collect LinkedIn Jobs", "Collect bounded LinkedIn search results into the local jobs database for review and batch processing."
	case "settings":
		pd.Active, pd.Title, pd.Subtitle = "settings", "Settings", "Manage candidate preferences, Gmail connectivity, CV defaults, and local runtime paths."
	default:
		return pd, fmt.Errorf("unknown UI page %q", section)
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
	if j == nil { return false }
	if q != "" {
		blob := strings.ToLower(strings.Join([]string{j.Title, j.Company, j.Location, j.ApplyEmail}, " "))
		if !strings.Contains(blob, strings.ToLower(q)) { return false }
	}
	if location != "" && !strings.EqualFold(strings.TrimSpace(j.Location), location) { return false }
	if method != "" && !strings.EqualFold(strings.TrimSpace(j.ApplicationMethod), method) { return false }
	if appState != "" && !strings.EqualFold(state, appState) { return false }
	return true
}

func matchesUIApplication(a *models.JobApplication, j *models.JobPosting, q, method, state string) bool {
	if a == nil { return false }
	if state != "" && !strings.EqualFold(a.State, state) { return false }
	if method != "" {
		jobMethod := ""
		if j != nil { jobMethod = j.ApplicationMethod }
		if !strings.EqualFold(strings.TrimSpace(jobMethod), method) { return false }
	}
	if q != "" {
		parts := []string{a.JobID, a.Recipient, a.Subject}
		if j != nil { parts = append(parts, j.Title, j.Company, j.Location) }
		if !strings.Contains(strings.ToLower(strings.Join(parts, " ")), strings.ToLower(q)) { return false }
	}
	return true
}

func preferredApplication(apps []models.JobApplication) *models.JobApplication {
	for _, state := range []string{models.ApplicationStateApproved, models.ApplicationStateDraftCreated, models.ApplicationStateReadyEmail, models.ApplicationStateSent} {
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
	if added == "" { added = j.FirstSeen }
	if added == "" { added = j.PostedAt }
	return appJobRow{
		ID: j.ID, Title: j.Title, Company: j.Company, Location: j.Location,
		Method: nonEmpty(j.ApplicationMethod, "UNKNOWN"), State: state,
		Added: displayDate(added), URL: j.URL, Email: j.ApplyEmail,
	}
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" { return fallback }
	return v
}

func displayDate(v string) string {
	v = strings.TrimSpace(v)
	if v == "" { return "—" }
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("Jan 02, 2006")
		}
	}
	if len(v) >= 10 { return v[:10] }
	return v
}

func initials(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 { return "C" }
	out := strings.ToUpper(string([]rune(fields[0])[0]))
	if len(fields) > 1 {
		out += strings.ToUpper(string([]rune(fields[len(fields)-1])[0]))
	}
	return out
}

const appHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="csrf-token" content="{{.CSRF}}">
<title>{{.Title}} · LinkedIn Job CLI</title>
<style>
:root{
  --bg:#071321;--bg2:#0a1728;--panel:#102238;--card:#12263d;--input:#152a42;--border:#223a55;
  --line:#29435e;--primary:#2e8bff;--success:#25c58b;--purple:#8d66ff;--warning:#f3a23a;--danger:#ef5d67;
  --text:#e8f0fa;--muted:#8799b2;--sidebar:208px;--top:72px;--radius:12px
}
*{box-sizing:border-box}html,body{margin:0;background:var(--bg);color:var(--text);font:14px/1.45 Inter,ui-sans-serif,system-ui,-apple-system,Segoe UI,sans-serif}
a{color:inherit;text-decoration:none}button,input,select,textarea{font:inherit}
.shell{min-height:100vh;background:radial-gradient(circle at 70% -20%,#132942 0,transparent 36%),linear-gradient(145deg,var(--bg),var(--bg2))}
.sidebar{position:fixed;inset:0 auto 0 0;width:var(--sidebar);background:#091727;border-right:1px solid #172d45;padding:18px 12px;z-index:5}
.brand{display:flex;gap:12px;align-items:center;padding:2px 8px 24px}.brand-mark{width:32px;height:32px;border-radius:8px;background:linear-gradient(145deg,#62a9ff,#2e8bff);display:grid;place-items:center;font-weight:800}.brand strong{display:block;font-size:15px}.brand small{color:var(--muted);font-size:10px}
.nav-label{margin:18px 10px 8px;color:#647992;font-size:10px;letter-spacing:.12em}.nav a{display:flex;align-items:center;gap:10px;height:42px;padding:0 12px;border-radius:8px;color:#aebcd0;margin:3px 0}.nav a:hover{background:#10263e;color:#fff}.nav a.active{background:#17395f;color:#68adff;box-shadow:inset 2px 0 var(--primary)}.nav-icon{width:22px;height:22px;border:1px solid #5f7897;border-radius:6px;display:grid;place-items:center;font-size:11px}.nav-count{margin-left:auto;background:#1d3047;border-radius:10px;padding:2px 7px;font-size:10px;color:#d2deec}
.quote{position:absolute;left:16px;right:16px;bottom:20px;border:1px solid #1f3550;border-radius:10px;padding:14px;color:#9eb0c6;font-size:11px}.quote b{display:block;color:#dbe7f5;margin-top:10px;font-weight:500}
.topbar{position:fixed;left:var(--sidebar);right:0;top:0;height:var(--top);background:rgba(8,22,37,.94);backdrop-filter:blur(14px);border-bottom:1px solid #1b334c;display:flex;align-items:center;padding:0 26px;z-index:4}
.global-search{width:min(520px,52vw);height:38px;border:1px solid #28425f;border-radius:8px;background:#13263b;color:var(--text);padding:0 14px}.top-user{margin-left:auto;display:flex;align-items:center;gap:10px}.avatar{width:34px;height:34px;border-radius:50%;background:linear-gradient(145deg,#3f9cff,#1767c5);display:grid;place-items:center;font-size:12px}.top-user small{display:block;color:var(--muted);font-size:10px}
.main{margin-left:var(--sidebar);padding:calc(var(--top) + 26px) 24px 32px;min-height:100vh}.page-head{display:flex;align-items:flex-start;justify-content:space-between;gap:24px;margin-bottom:22px}.page-head h1{margin:0;font-size:28px;font-weight:760;letter-spacing:-.025em;line-height:1.12}.page-head p{max-width:760px;margin:6px 0 0;color:#8fa2b9;font-size:13px;line-height:1.5}.btn{min-height:38px;border:1px solid #315273;background:#18324d;color:var(--text);padding:9px 14px;border-radius:8px;cursor:pointer;display:inline-flex;align-items:center;justify-content:center;gap:7px;font-size:12px;font-weight:650;line-height:1.1}.btn.primary{background:linear-gradient(180deg,#348fff,#1976df);border-color:#3d9aff;color:white}.btn.ghost{background:#12243a}.btn:disabled{opacity:.45;cursor:not-allowed}
.grid-kpi{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-bottom:18px}.kpi{background:linear-gradient(145deg,#10243a,#0e1f33);border:1px solid #223b58;border-radius:12px;padding:17px;min-height:104px;display:flex;gap:14px;align-items:flex-start}.kpi-icon{width:48px;height:48px;border-radius:50%;display:grid;place-items:center;font-weight:800}.kpi-icon.amber{background:#4c4126;color:#ffc04d}.kpi-icon.cyan{background:#123f56;color:#58d6ff}.kpi-icon.purple{background:#292b62;color:#b692ff}.kpi-icon.green{background:#174737;color:#52dda7}.kpi .value{font-size:26px;font-weight:750;line-height:1}.kpi .label{color:#c8d5e4;margin-top:7px}.kpi .hint{font-size:11px;color:var(--muted);margin-top:8px}.hint.good{color:#4bd99f}
.panel{background:linear-gradient(145deg,#0f2135,#0d1c2e);border:1px solid #223a55;border-radius:12px;overflow:hidden;box-shadow:0 10px 30px rgba(0,0,0,.08)}.panel-head{min-height:56px;padding:0 18px;display:flex;align-items:center;border-bottom:1px solid #223a55}.panel-head h2{margin:0;font-size:16px;font-weight:700;letter-spacing:-.01em}.panel-head .sub{margin-left:auto;color:#6caeff;font-size:12px}.dashboard-grid{display:grid;grid-template-columns:minmax(0,1.8fr) minmax(320px,1fr);gap:14px;align-items:start}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse}th{height:40px;padding:0 12px;text-align:left;font-size:10.5px;font-weight:650;letter-spacing:.035em;text-transform:uppercase;color:#8fa2b9;background:#0e1e30;border-bottom:1px solid #25405d;white-space:nowrap}td{padding:11px 12px;border-bottom:1px solid #21384f;color:#cbd6e4;white-space:nowrap;vertical-align:middle}tr:last-child td{border-bottom:0}tr:hover td{background:#112944}tr.row-selected td{background:#112d4a}.table-title{min-width:220px;max-width:380px;white-space:normal;line-height:1.35}.table-company{min-width:150px;max-width:240px;white-space:normal}.apply-cell{min-width:180px}.apply-cell small{display:block;margin-top:5px;color:#71869f;font-size:10.5px;max-width:240px;overflow:hidden;text-overflow:ellipsis}.job-link{color:#6baeff;font-weight:650}.muted{color:var(--muted)}.badge{display:inline-flex;align-items:center;border:1px solid transparent;border-radius:7px;padding:4px 8px;font-size:10px;font-weight:650;letter-spacing:.02em}.state-ready_email{background:#123d72;color:#7db7ff;border-color:#1d4d88}.state-need_review{background:#4b3820;color:#f4bd66;border-color:#654c2c}.state-draft_created{background:#342362;color:#b89aff;border-color:#4b327e}.state-approved,.state-sent{background:#124b3a;color:#6be2af;border-color:#1c614a}.state-not_applied{background:#26354a;color:#b9c6d5;border-color:#35475c}.method-email{background:#193653;color:#b8d7f7;border-color:#2e4e6e}.method-linkedin{background:#172f4a;color:#83b7f1;border-color:#274a70}.method-unknown{background:#303847;color:#bdc7d3;border-color:#465162}
.detail{padding:20px}.detail-title{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.detail-title h2,.detail h2{margin:0 0 4px;font-size:20px;font-weight:720;letter-spacing:-.015em;line-height:1.25}.detail-title p{margin:5px 0 0}.company{color:#9fb1c5;font-size:13px}.meta{display:grid;gap:8px;margin:16px 0;color:#9fb1c6;font-size:12px}.tabs{display:flex;border-bottom:1px solid #28415d;margin:2px -20px 18px;padding:0 20px}.tab{padding:10px 14px;color:#91a5bc;border-bottom:2px solid transparent;font-size:12px}.tab.active{color:#73b2ff;border-bottom-color:#3d99ff}.field-label{font-size:10.5px;font-weight:650;letter-spacing:.035em;text-transform:uppercase;color:#8fa2b9;margin:14px 0 6px}.field{background:#152a42;border:1px solid #294760;border-radius:8px;padding:11px 12px;color:#e0e8f2;white-space:pre-wrap;line-height:1.5}.email-body{min-height:160px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}.detail-actions{display:flex;gap:8px;margin-top:16px}.detail-actions .btn{flex:1}
.quick-actions{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin-top:14px}.quick{border:1px solid #223d58;background:#10243a;border-radius:10px;padding:14px}.quick strong{display:block}.quick small{color:var(--muted)}
.toolbar{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:14px}.toolbar .search{flex:1;min-width:260px}.toolbar form{display:contents}.control{height:38px;border:1px solid #29455f;border-radius:8px;background:#13273e;color:#dbe5f1;padding:0 11px}.content-card{background:#0f2033;border:1px solid #223b56;border-radius:12px;overflow:hidden;box-shadow:0 10px 30px rgba(0,0,0,.07)}.content-pad{padding:20px}.content-pad>h2{margin:0 0 6px;font-size:18px;font-weight:720;letter-spacing:-.01em}.content-pad>p.muted{margin-top:0}.two-col{display:grid;grid-template-columns:minmax(0,1.9fr) minmax(300px,.72fr);gap:14px;align-items:start}.detail-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.info-card{border:1px solid #27425d;background:#102338;border-radius:10px;padding:14px;min-width:0}.info-card h3{margin:0 0 10px;font-size:13px;font-weight:700}.info-row{display:flex;justify-content:space-between;align-items:flex-start;gap:16px;padding:8px 0;border-bottom:1px solid #20384f}.info-row:last-child{border-bottom:0}.info-row span:first-child{color:#8194ab}.info-row b{text-align:right;font-weight:600;white-space:normal;overflow-wrap:anywhere}
.cv-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;align-items:start}.cv-card{background:#10243a;border:1px solid #24415e;border-radius:12px;padding:17px;min-height:0;box-shadow:0 8px 24px rgba(0,0,0,.06)}.cv-card h3{margin:0;font-size:16px}.cv-default{float:right;background:#124b3a;color:#6be2af;border-radius:8px;padding:3px 7px;font-size:10px}.cv-path{color:#63aaff;margin:16px 0 7px;word-break:break-all}.cv-card p{color:#9cafc4;font-size:12px;line-height:1.45}.form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.form-group label{display:block;color:#96a9bf;font-size:11px;font-weight:600;margin-bottom:6px}.form-group input,.form-group select,.form-group textarea{width:100%;border:1px solid #29475f;background:#142a42;color:#e5edf6;border-radius:8px;padding:10px;line-height:1.4}.collect-grid{display:grid;grid-template-columns:minmax(0,1.6fr) minmax(300px,.7fr);gap:14px;align-items:start}.progress-list{display:grid;gap:13px;margin-top:14px}.progress-item{display:flex;gap:9px;align-items:center;color:#a9b9cb}.dot{width:9px;height:9px;border-radius:50%;background:#25c58b;box-shadow:0 0 10px rgba(37,197,139,.45)}
.settings-grid{display:grid;grid-template-columns:210px minmax(0,1fr);gap:14px;align-items:start}.settings-menu button{display:block;width:100%;padding:10px 11px;border:0;border-radius:8px;color:#a9b9cb;background:transparent;text-align:left;cursor:pointer}.settings-menu button:hover{background:#112a43}.settings-menu button.active{background:#173b64;color:#76b4ff}.settings-pane{display:none}.settings-pane.active{display:block}.empty{padding:44px;text-align:center;color:#8194aa}
.alert{padding:10px 13px;border:1px solid #6e4b25;background:#382919;color:#f1c178;border-radius:8px;margin-bottom:14px}.alert.success{border-color:#1f664d;background:#123a2e;color:#79ddb5}.collect-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px;margin-top:14px}.collect-summary .mini{background:#13283f;border:1px solid #29465f;border-radius:8px;padding:10px}.collect-summary b{display:block;font-size:18px}.collect-summary span{color:#8fa4bc;font-size:10px}
.pipeline-strip{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:10px;margin-bottom:12px}.pipeline-mini{background:#10243a;border:1px solid #223d58;border-radius:10px;padding:12px 14px}.pipeline-mini b{font-size:20px;display:block}.pipeline-mini span{font-size:11px;color:#8fa4bc}.footer-note{color:#637991;font-size:11px;margin-top:14px}
.upload-form{margin-top:14px}.checkline{display:flex;align-items:center;gap:9px;color:#b7c6d8;font-size:12px;margin-top:12px}.checkline input{width:auto}.inline-actions{display:flex;gap:8px;align-items:center;margin-top:14px}.inline-actions form{margin:0}.file-list{display:grid;gap:9px;margin-top:16px}.file-card{display:grid;grid-template-columns:minmax(0,1fr) auto auto;gap:12px;align-items:center;padding:12px 14px;border:1px solid #29465f;border-radius:9px;background:#10243a}.file-card strong{display:block}.file-card small{display:block;color:#7f94ad;margin-top:3px;word-break:break-all}.attachment-picker{display:grid;gap:8px;margin-top:7px}.attachment-option{margin:0;padding:10px 12px;border:1px solid #29465f;border-radius:8px;background:#10243a}.attachment-option span{display:block}.attachment-option b,.attachment-option small{display:block}.attachment-option small{color:#7f94ad;margin-top:2px}
.bulk-bar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;padding:14px 16px;background:linear-gradient(180deg,#10263d,#0f2237);border:1px solid #29465f;border-radius:11px;margin-bottom:14px}.bulk-bar .spacer{flex:1}.bulk-primary-row{display:flex;width:100%;align-items:center;gap:12px}.bulk-select{display:flex;align-items:center;gap:10px;min-width:180px}.bulk-select .checkline{margin:0}.bulk-actions{margin-left:auto;display:flex;gap:8px;align-items:center;flex-wrap:wrap}.bulk-hint{width:100%;color:#7f94ad;font-size:11px;line-height:1.45}.bulk-attachments{display:flex;gap:10px;align-items:center;flex-wrap:wrap;width:100%;padding-top:10px;border-top:1px solid #203b55}.bulk-attachments .checkline{margin:0}.row-check{width:16px;height:16px;accent-color:#2e8bff}.review-progress{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:16px;padding:12px 14px;border:1px solid #25425e;background:#0e2136;border-radius:10px}.review-counter{font-weight:750;font-size:13px}.review-nav{display:flex;gap:8px}.send-confirm-table td:first-child{white-space:normal}.danger-zone{border:1px solid #6f3540;background:#321d24;border-radius:10px;padding:14px;margin-top:16px}.danger-zone .checkline{color:#ffd8dc}.batch-note{font-size:11px;color:#91a5bc}
@media(max-width:1400px) and (min-width:1051px){.main{padding-top:calc(var(--top) + 22px)}.page-head{margin-bottom:18px}.grid-kpi{gap:12px;margin-bottom:14px}.kpi{min-height:96px;padding:15px}.panel-head{min-height:52px}.detail,.content-pad{padding:17px}.tabs{margin-left:-17px;margin-right:-17px;padding-left:17px;padding-right:17px}.email-body{min-height:145px}}
@media(max-width:1050px){.grid-kpi{grid-template-columns:repeat(2,1fr)}.dashboard-grid,.two-col,.collect-grid{grid-template-columns:1fr}.cv-grid{grid-template-columns:1fr 1fr}.quick-actions{grid-template-columns:1fr 1fr}.bulk-primary-row{align-items:flex-start;flex-direction:column}.bulk-actions{margin-left:0}.toolbar .search{flex-basis:100%}}
@media(max-width:760px){:root{--sidebar:0px}.sidebar{display:none}.topbar{left:0;padding:0 14px}.main{margin-left:0;padding:calc(var(--top) + 20px) 14px 24px}.page-head{margin-bottom:18px}.page-head h1{font-size:24px}.grid-kpi,.cv-grid,.form-grid,.detail-grid{grid-template-columns:1fr}.top-user .name{display:none}.global-search{width:68vw}.bulk-actions{width:100%}.bulk-actions .btn{flex:1;min-width:145px}.review-progress{align-items:flex-start;flex-direction:column}.review-nav{width:100%}.review-nav .btn{flex:1}.toolbar .control,.toolbar .btn{flex:1;min-width:140px}.toolbar .search{flex-basis:100%}.table-title{min-width:190px}}
</style>
</head>
<body>
<div class="shell">
<aside class="sidebar">
  <div class="brand"><div class="brand-mark">▣</div><div><strong>LinkedIn Job CLI</strong><small>Find Jobs. Prepare. Apply.</small></div></div>
  <nav class="nav">
    <a href="/app/dashboard" class="{{if eq .Active "dashboard"}}active{{end}}"><span class="nav-icon">⌂</span>Dashboard</a>
    <a href="/app/jobs" class="{{if eq .Active "jobs"}}active{{end}}"><span class="nav-icon">J</span>Jobs <span class="nav-count">{{.Stats.JobsTotal}}</span></a>
    <a href="/app/applications" class="{{if eq .Active "applications"}}active{{end}}"><span class="nav-icon">✓</span>Applications <span class="nav-count">{{.Stats.PipelineTotal}}</span></a>
    <a href="/app/cv-profiles" class="{{if eq .Active "cv-profiles"}}active{{end}}"><span class="nav-icon">CV</span>CV Profiles <span class="nav-count">{{len .CVProfiles}}</span></a>
    <div class="nav-label">TOOLS</div>
    <a href="/app/collect" class="{{if eq .Active "collect"}}active{{end}}"><span class="nav-icon">⌕</span>Collect Jobs</a>
    <a href="/app/settings" class="{{if eq .Active "settings"}}active{{end}}"><span class="nav-icon">⚙</span>Settings</a>
  </nav>
  <div class="quote">“A better career is a series of small, consistent steps.”<b>Keep going. 🚀</b></div>
</aside>
<header class="topbar">
  <form action="/app/jobs" method="get" style="display:contents"><input class="global-search" name="q" value="{{.Query}}" placeholder="Search jobs, companies, or keywords…" aria-label="Global search"></form>
  <div class="top-user"><div class="avatar">{{.CandidateInitials}}</div><div class="name">{{.CandidateName}}<small>Local Job Command Center</small></div></div>
</header>
<main class="main">
  <div class="page-head"><div><h1>{{.Title}}</h1><p>{{.Subtitle}}</p></div>{{if eq .Active "dashboard"}}<a class="btn primary" href="/app/collect">＋ Collect New Jobs</a>{{end}}</div>
  {{if .Error}}<div class="alert">{{.Error}}</div>{{end}}
  {{if .ActionError}}<div class="alert">{{.ActionError}}</div>{{end}}
  {{if .ActionMessage}}<div class="alert success">{{.ActionMessage}}</div>{{end}}

  {{if eq .Active "dashboard"}}
    <section class="grid-kpi">
      <div class="kpi"><div class="kpi-icon amber">J</div><div><div class="value">{{.Stats.JobsTotal}}</div><div class="label">Jobs Collected</div><div class="hint">Stored in SQLite</div></div></div>
      <div class="kpi"><div class="kpi-icon cyan">@</div><div><div class="value">{{.Stats.EmailTotal}}</div><div class="label">With Email Contact</div><div class="hint">Explicit application emails</div></div></div>
      <div class="kpi"><div class="kpi-icon purple">A</div><div><div class="value">{{.Stats.PipelineTotal}}</div><div class="label">In Application Pipeline</div><div class="hint">{{.Stats.ReadyTotal}} ready · {{.Stats.NeedReviewTotal}} review · {{.Stats.DraftTotal}} draft · {{.Stats.ApprovedTotal}} approved</div></div></div>
      <div class="kpi"><div class="kpi-icon green">✓</div><div><div class="value">{{.Stats.SentTotal}}</div><div class="label">Applications Sent</div><div class="hint good">Explicit sends only</div></div></div>
    </section>
    <section class="dashboard-grid">
      <div class="panel"><div class="panel-head"><h2>Recent Jobs</h2><a class="sub" href="/app/jobs">View All →</a></div>
      {{if .Jobs}}<div class="table-wrap"><table><thead><tr><th>Job Title</th><th>Company</th><th>Location</th><th>Method</th><th>Status</th><th>Added</th></tr></thead><tbody>
      {{range .Jobs}}<tr><td><a class="job-link" href="/app/jobs/{{.ID}}">{{.Title}}</a></td><td>{{.Company}}</td><td class="muted">{{.Location}}</td><td><span class="badge method-{{lower .Method}}">{{.Method}}</span></td><td><span class="badge state-{{lower .State}}">{{.State}}</span></td><td class="muted">{{.Added}}</td></tr>{{end}}
      </tbody></table></div>{{else}}<div class="empty">No jobs collected yet.</div>{{end}}</div>
      <div class="panel">
      {{if .SelectedApplication}}<div class="panel-head"><h2>Application Detail</h2>{{if .SelectedApplication.GmailDraftID}}<span class="sub">Gmail draft ready</span>{{end}}</div>
        <div class="detail"><div class="detail-title"><div><h2>{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Title}}{{else}}Application{{end}}</h2><div class="company">{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Company}}{{end}}</div></div><span class="badge state-{{lower .SelectedApplication.State}}">{{.SelectedApplication.State}}</span></div>
        <div class="meta"><span>Job ID: {{.SelectedApplication.JobID}}</span><span>✉ {{.SelectedApplication.Recipient}}</span></div>
        <div class="tabs"><span class="tab active">Email</span><span class="tab">Details</span><span class="tab">Timeline</span></div>
        <div class="field-label">Subject</div><div class="field">{{.SelectedApplication.Subject}}</div>
        <div class="field-label">Email Body</div><div class="field email-body">{{.SelectedApplication.Body}}</div>
        <div class="detail-actions"><a class="btn ghost" href="/app/applications/{{.SelectedApplication.JobID}}">Open Detail</a></div></div>
      {{else}}<div class="empty">No application records yet.</div>{{end}}
      </div>
    </section>
    <section class="quick-actions">
      <a class="quick" href="/app/collect"><strong>⌕ Collect Jobs</strong><small>Fetch latest jobs from LinkedIn</small></a>
      <a class="quick" href="/app/cv-profiles"><strong>▤ Manage CVs</strong><small>CV, portfolio & supporting files</small></a>
      <a class="quick" href="/app/applications"><strong>✓ View Applications</strong><small>Manage lifecycle states</small></a>
      <a class="quick" href="/app/settings"><strong>⚙ Settings</strong><small>Inspect preferences and paths</small></a>
    </section>
  {{end}}

  {{if eq .Active "jobs"}}
    {{if .SelectedJob}}
      <section class="two-col">
        <div class="content-card"><div class="content-pad"><div class="detail-title"><div><h2>{{.SelectedJob.Title}}</h2><div class="company">{{.SelectedJob.Company}}</div></div><a class="btn primary" target="_blank" rel="noreferrer" href="{{.SelectedJob.URL}}">Open in LinkedIn ↗</a></div>
          <div class="tabs"><span class="tab active">Overview</span><span class="tab">Description</span><span class="tab">Company</span><span class="tab">Application</span></div>
          <div class="detail-grid">
            <div class="info-card"><h3>Job Information</h3><div class="info-row"><span>Location</span><b>{{.SelectedJob.Location}}</b></div><div class="info-row"><span>Method</span><b>{{.SelectedJob.ApplicationMethod}}</b></div><div class="info-row"><span>Contact</span><b>{{.SelectedJob.ApplyEmail}}</b></div><div class="info-row"><span>Posted</span><b>{{.SelectedJob.PostedAt}}</b></div></div>
            <div class="info-card"><h3>Collection Metadata</h3><div class="info-row"><span>LinkedIn ID</span><b>{{.SelectedJob.ID}}</b></div><div class="info-row"><span>Source</span><b>{{.SelectedJob.Source}}</b></div><div class="info-row"><span>Status</span><b>{{.SelectedJob.Status}}</b></div><div class="info-row"><span>Detail status</span><b>{{.SelectedJob.DetailStatus}}</b></div></div>
          </div>
          <div class="field-label">Description</div><div class="field email-body">{{if .SelectedJob.ShortDescription}}{{.SelectedJob.ShortDescription}}{{else}}{{.SelectedJob.Description}}{{end}}</div>
        </div></div>
        <aside class="content-card"><div class="content-pad"><h3>Application</h3><p class="muted">Explicit data extracted from the posting.</p><div class="info-row"><span>Method</span><b>{{.SelectedJob.ApplicationMethod}}</b></div><div class="info-row"><span>Email</span><b>{{if .SelectedJob.ApplyEmail}}{{.SelectedJob.ApplyEmail}}{{else}}—{{end}}</b></div>
        {{if .SelectedApplication}}
          <div class="info-row"><span>Pipeline State</span><span class="badge state-{{lower .SelectedApplication.State}}">{{.SelectedApplication.State}}</span></div>
          <a class="btn primary" style="display:block;text-align:center;margin-top:12px" href="/app/applications/{{.SelectedJob.ID}}">View Application</a>
        {{else}}
          <form method="post" action="/app/jobs/{{.SelectedJob.ID}}/queue" style="margin-top:12px" {{if not (and (eq .SelectedJob.ApplicationMethod "EMAIL") .SelectedJob.ApplyEmail)}}onsubmit="return confirm('This job has no explicit application email. Queue it as NEED_REVIEW anyway?')"{{end}}>
            <input type="hidden" name="csrf" value="{{.CSRF}}">
            <button class="btn primary" type="submit" style="width:100%">{{if and (eq .SelectedJob.ApplicationMethod "EMAIL") .SelectedJob.ApplyEmail}}Queue Application{{else}}Queue as NEED_REVIEW{{end}}</button>
          </form>
          <div class="footer-note">{{if and (eq .SelectedJob.ApplicationMethod "EMAIL") .SelectedJob.ApplyEmail}}This job will enter READY_EMAIL.{{else}}No explicit email detected. This is an optional manual-review path and requires confirmation.{{end}}</div>
        {{end}}
        {{if .SelectedJob.ApplyURL}}<a class="btn ghost" style="display:block;text-align:center;margin-top:8px" target="_blank" rel="noreferrer" href="{{.SelectedJob.ApplyURL}}">Open Apply URL ↗</a>{{end}}</div></aside>
      </section>
    {{else}}
      <form class="toolbar" method="get" action="/app/jobs">
        <input class="control search" name="q" value="{{.Query}}" placeholder="Search jobs, companies, or keywords…">
        <select class="control" name="location"><option value="">All locations</option>{{range .Locations}}<option value="{{.}}" {{if eq $.LocationFilter .}}selected{{end}}>{{.}}</option>{{end}}</select>
        <select class="control" name="method"><option value="">All methods</option>{{range .Methods}}<option value="{{.}}" {{if eq $.MethodFilter .}}selected{{end}}>{{.}}</option>{{end}}</select>
        <select class="control" name="state"><option value="">All states</option>{{range .States}}<option value="{{.}}" {{if eq $.StateFilter .}}selected{{end}}>{{.}}</option>{{end}}</select>
        <button class="btn primary" type="submit">Apply Filters</button><a class="btn ghost" href="/app/jobs">Reset</a>
      </form>

      <form method="post" id="bulk-jobs-form">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <input type="hidden" name="filter_q" value="{{.Query}}">
        <input type="hidden" name="filter_location" value="{{.LocationFilter}}">
        <input type="hidden" name="filter_method" value="{{.MethodFilter}}">
        <input type="hidden" name="filter_state" value="{{.StateFilter}}">
        <div class="bulk-bar">
          <div class="bulk-primary-row">
            <div class="bulk-select">
              <label class="checkline"><input id="select-all-jobs" type="checkbox"> Select all visible</label>
              <span id="selected-jobs-count" class="batch-note">0 selected</span>
            </div>
            <div class="bulk-actions">
              <button class="btn ghost" id="queue-selected-jobs" type="submit" formaction="/app/jobs/bulk/queue">Queue Selected</button>
              <button class="btn primary" id="process-selected-jobs" data-gmail="{{if .GmailConnected}}1{{else}}0{{end}}" type="submit" formaction="/app/jobs/bulk/process-to-draft" {{if not .GmailConnected}}disabled title="Connect Gmail first"{{end}}>Process Selected to Draft</button>
            </div>
          </div>
          <div class="bulk-hint">By default, jobs without an explicit application email are skipped and stay only in the Jobs database. Process Selected to Draft always skips them.</div>
          <label class="checkline" style="margin:0"><input type="checkbox" name="include_need_review" value="1"> Include jobs without email as NEED_REVIEW when using Queue Selected</label>
          <div class="bulk-hint">Queue Selected supports up to 50 jobs. Process Selected to Draft supports up to 25 and runs Queue → deterministic Prepare → Gmail Draft → Review Queue. Neither action approves or sends email.</div>
          {{if not .GmailConnected}}<div class="bulk-hint">Gmail is not connected, so Process Selected to Draft is disabled. Queue Selected remains available.</div>{{end}}
          {{if .Attachments}}<div class="bulk-attachments"><span class="batch-note">Attachments for created drafts:</span>{{range .Attachments}}{{if .Exists}}<label class="checkline"><input type="checkbox" name="attachment" value="{{.ID}}" {{if eq .Kind "portfolio"}}checked{{end}}> {{.Label}}</label>{{end}}{{end}}</div>{{end}}
        </div>

        <div class="content-card"><div class="table-wrap"><table class="jobs-table"><thead><tr><th style="width:42px"></th><th>Job</th><th>Company</th><th>Location</th><th>Apply</th><th>Status</th><th>Added</th></tr></thead><tbody>
        {{range .Jobs}}<tr class="js-selectable-row"><td><input class="row-check js-job-check" type="checkbox" name="job_id" value="{{.ID}}"></td><td class="table-title"><a class="job-link" href="/app/jobs/{{.ID}}">{{.Title}}</a></td><td class="table-company">{{.Company}}</td><td class="muted">{{.Location}}</td><td class="apply-cell"><span class="badge method-{{lower .Method}}">{{.Method}}</span>{{if .Email}}<small>{{.Email}}</small>{{else}}<small>No explicit email · skipped by default</small>{{end}}</td><td><span class="badge state-{{lower .State}}">{{.State}}</span></td><td class="muted">{{.Added}}</td></tr>{{end}}
        </tbody></table></div>{{if not .Jobs}}<div class="empty">No jobs match the current filters.</div>{{end}}</div>
      </form>
    {{end}}
  {{end}}

  {{if eq .Active "applications"}}
    {{if .SendConfirmMode}}
      <section class="content-card"><div class="content-pad">
        <div class="detail-title"><div><h2>Final Send Confirmation</h2><p class="muted">Only APPROVED Gmail drafts are listed below. This is the final step that will actually send email.</p></div><span class="badge state-approved">{{len .SendCandidates}} READY TO SEND</span></div>
        <form method="post" action="/app/applications/bulk/send" style="margin-top:16px">
          <input type="hidden" name="csrf" value="{{.CSRF}}">
          <div class="table-wrap"><table class="send-confirm-table"><thead><tr><th>Application</th><th>Recipient</th><th>Subject</th><th>Draft</th></tr></thead><tbody>
          {{range .SendCandidates}}
            <tr>
              <td><strong>{{.Title}}</strong><div class="muted">{{.Company}} · {{.JobID}}</div><input type="hidden" name="job_id" value="{{.JobID}}"></td>
              <td>{{.Recipient}}</td><td>{{.Subject}}</td><td class="muted">{{.DraftID}}</td>
            </tr>
          {{end}}
          </tbody></table></div>
          <div class="danger-zone">
            <strong>This action sends the emails above through Gmail.</strong>
            <label class="checkline"><input type="checkbox" name="send_confirm" value="1" required> I confirm these approved applications are ready to be sent.</label>
            <div class="detail-actions"><a class="btn ghost" href="/app/applications">Cancel</a><button class="btn primary" type="submit">Send {{len .SendCandidates}} Application(s)</button></div>
          </div>
        </form>
      </div></section>
    {{else if .ReviewMode}}
      {{if .SelectedApplication}}
        <section class="content-card"><div class="content-pad">
          <div class="review-progress"><div><span class="review-counter">Review {{.ReviewPosition}} of {{.ReviewTotal}}</span><div class="muted">Approve &amp; Next keeps you in this queue.</div></div><div class="review-nav"><a class="btn ghost" id="review-prev" href="{{.ReviewPrevURL}}">← Previous</a><a class="btn ghost" id="review-next" href="{{.ReviewNextURL}}">Skip / Next →</a></div></div>
          <div class="detail-title"><div><h2>{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Title}}{{else}}Application{{end}}</h2><div class="company">{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Company}}{{end}}</div></div><span class="badge state-draft_created">DRAFT_CREATED</span></div>
          <div class="form-grid"><div><div class="field-label">To</div><div class="field">{{.SelectedApplication.Recipient}}</div></div><div><div class="field-label">CV Profile</div><div class="field">{{.SelectedApplication.CVProfile}}</div></div></div>
          <div class="field-label">Subject</div><div class="field">{{.SelectedApplication.Subject}}</div>
          <div class="field-label">Email Body</div><div class="field email-body">{{.SelectedApplication.Body}}</div>
          <div class="detail-grid" style="margin-top:14px"><div class="info-card"><h3>Gmail Draft</h3><div class="info-row"><span>Draft ID</span><b>{{.SelectedApplication.GmailDraftID}}</b></div><div class="info-row"><span>Created</span><b>{{.SelectedApplication.DraftCreatedAt}}</b></div></div><div class="info-card"><h3>Review Gate</h3><div class="info-row"><span>Current state</span><b>DRAFT_CREATED</b></div><div class="info-row"><span>Email sent</span><b>No</b></div></div></div>
          <div class="detail-actions"><a class="btn ghost" target="_blank" rel="noreferrer" href="https://mail.google.com/mail/u/0/#drafts">Open Gmail Drafts ↗</a></div>
          {{if and .GmailConnected .SelectedCVReady}}
          <details style="margin-top:12px"><summary class="job-link" style="cursor:pointer">Draft missing or deleted? Recreate it</summary>
            <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/recreate-draft" style="margin-top:12px" onsubmit="return confirm('Create a replacement Gmail draft? The old local draft reference will be replaced.')">
              <input type="hidden" name="csrf" value="{{.CSRF}}">
              {{if .Attachments}}<div class="attachment-picker">{{range .Attachments}}{{if .Exists}}<label class="checkline attachment-option"><input type="checkbox" name="attachment" value="{{.ID}}" {{if eq .Kind "portfolio"}}checked{{end}}><span><b>{{.Label}}</b><small>{{.Kind}} · {{.FileName}}</small></span></label>{{end}}{{end}}</div>{{end}}
              <label class="checkline"><input type="checkbox" name="recreate_confirm" value="1" required> I confirm the Gmail draft is missing/unusable and want a replacement draft.</label>
              <div class="detail-actions"><button class="btn ghost" type="submit">Recreate Gmail Draft</button></div>
            </form>
            <div class="footer-note">The saved recipient, subject, body and CV profile are reused. The replacement remains DRAFT_CREATED and still requires review.</div>
          </details>
          {{end}}
          <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/approve" style="margin-top:14px">
            <input type="hidden" name="csrf" value="{{.CSRF}}">
            <input type="hidden" name="return_to" value="{{.ReviewStayURL}}">
            <div class="form-group"><label>Review Note <span class="muted">(optional)</span></label><textarea name="review_note" maxlength="500" rows="3" placeholder="Recipient, subject, body and attachments checked in Gmail."></textarea></div>
            <label class="checkline"><input type="checkbox" name="review_confirm" value="1" required> I reviewed the Gmail draft, recipient, email content, and attachments.</label>
            <div class="detail-actions"><a class="btn ghost" href="{{.ReviewNextURL}}">Skip</a><button class="btn primary" type="submit">Approve &amp; Next</button></div>
          </form>
          <div class="footer-note">Keyboard: J / → next, K / ← previous. Approval never sends email.</div>
        </div></section>
      {{else}}
        <section class="content-card"><div class="empty">No DRAFT_CREATED applications remain in this review queue. <a class="job-link" href="/app/applications">Back to Applications</a></div></section>
      {{end}}
    {{else if .SelectedApplication}}
      <div class="content-card"><div class="content-pad"><div class="detail-title"><div><h2>{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Title}}{{else}}Application{{end}}</h2><div class="company">{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Company}}{{end}}</div></div><span class="badge state-{{lower .SelectedApplication.State}}">{{.SelectedApplication.State}}</span></div>
        <div class="tabs"><span class="tab active">Email</span><span class="tab">CV &amp; Files</span><span class="tab">Timeline</span><span class="tab">Notes</span></div>
        <div class="form-grid"><div><div class="field-label">To</div><div class="field">{{.SelectedApplication.Recipient}}</div></div><div><div class="field-label">CV Profile</div><div class="field">{{.SelectedApplication.CVProfile}}</div></div></div>
        <div class="field-label">Subject</div><div class="field">{{.SelectedApplication.Subject}}</div>
        <div class="field-label">Email Body</div><div class="field email-body">{{.SelectedApplication.Body}}</div>
        <div class="detail-grid" style="margin-top:14px"><div class="info-card"><h3>Provider</h3><div class="info-row"><span>Draft ID</span><b>{{.SelectedApplication.GmailDraftID}}</b></div><div class="info-row"><span>Message ID</span><b>{{.SelectedApplication.GmailMessageID}}</b></div><div class="info-row"><span>Thread ID</span><b>{{.SelectedApplication.GmailThreadID}}</b></div></div><div class="info-card"><h3>Review</h3><div class="info-row"><span>Reviewed</span><b>{{.SelectedApplication.ReviewedAt}}</b></div><div class="info-row"><span>Note</span><b>{{.SelectedApplication.ReviewNote}}</b></div><div class="info-row"><span>Sent</span><b>{{.SelectedApplication.SentAt}}</b></div></div></div>
        {{if eq .SelectedApplication.State "READY_EMAIL"}}
          <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/remove" style="margin-top:12px" onsubmit="return confirm('Remove this application from the queue? The collected job will remain in Jobs.')">
            <input type="hidden" name="csrf" value="{{.CSRF}}">
            <button class="btn ghost" type="submit">Remove from Queue</button>
          </form>
          <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/prepare" style="margin-top:16px">
            <input type="hidden" name="csrf" value="{{.CSRF}}">
            <div class="form-grid">
              <div class="form-group"><label>CV Profile</label><select name="cv_profile"><option value="">Auto (deterministic)</option>{{range .CVProfiles}}<option value="{{.ID}}" {{if eq $.SelectedApplication.CVProfile .ID}}selected{{end}}>{{.ID}}{{if .Default}} · default{{end}}</option>{{end}}</select></div>
              <div class="form-group"><label>Preparation Mode</label><input value="Deterministic template · no LLM" readonly></div>
            </div>
            <div class="detail-actions"><button class="btn ghost" type="submit">{{if .SelectedApplication.Subject}}Re-prepare Application{{else}}Prepare Application{{end}}</button></div>
          </form>
          {{if and .SelectedApplication.Subject .SelectedApplication.Body .SelectedApplication.CVProfile}}
            <div class="field-label">CV Attachment</div><div class="field">{{if .SelectedCVPath}}{{.SelectedCVPath}}{{else}}No configured file path{{end}}</div>
            {{if not .SelectedCVReady}}
              <div class="alert" style="margin-top:12px">The selected CV file is not accessible. Update the CV profile path before creating a Gmail draft.</div>
            {{else if .GmailConnected}}
              <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/draft" style="margin-top:10px">
                <input type="hidden" name="csrf" value="{{.CSRF}}">
                {{if .Attachments}}
                  <div class="field-label">Optional Attachments</div>
                  <div class="attachment-picker">
                  {{range .Attachments}}
                    {{if .Exists}}<label class="checkline attachment-option"><input type="checkbox" name="attachment" value="{{.ID}}"><span><b>{{.Label}}</b><small>{{.Kind}} · {{.FileName}}{{if .Size}} · {{.Size}}{{end}}</small></span></label>{{end}}
                  {{end}}
                  </div>
                {{end}}
                <button class="btn primary" type="submit" style="width:100%;margin-top:12px">Create Gmail Draft</button>
              </form>
              <div class="footer-note">The selected CV is always attached. Portfolio or other supporting files are attached only when you tick them above. No email is sent.</div>
            {{else}}
              <div class="alert" style="margin-top:12px">Gmail is not connected. <a class="job-link" href="/app/settings?tab=email">Connect Gmail in Settings</a> before creating a draft.</div>
            {{end}}
          {{else}}
            <div class="footer-note">Prepare the application first. This only stores subject/body + CV profile; it does not create a Gmail draft or send email.</div>
          {{end}}
        {{else if eq .SelectedApplication.State "NEED_REVIEW"}}
          <div class="alert" style="margin-top:16px">Recipient/email is not confirmed. Resolve the application contact before preparing this record.</div>
          <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/remove" style="margin-top:12px" onsubmit="return confirm('Remove this NEED_REVIEW application from the queue? The collected job will remain in Jobs.')">
            <input type="hidden" name="csrf" value="{{.CSRF}}">
            <button class="btn ghost" type="submit">Remove from Queue</button>
          </form>
        {{else if eq .SelectedApplication.State "DRAFT_CREATED"}}
          <div class="alert success" style="margin-top:16px">Gmail draft is created and ready for manual review.</div>
          <div class="detail-actions"><a class="btn ghost" target="_blank" rel="noreferrer" href="https://mail.google.com/mail/u/0/#drafts">Open Gmail Drafts ↗</a></div>
          {{if and .GmailConnected .SelectedCVReady}}
          <details style="margin-top:12px"><summary class="job-link" style="cursor:pointer">Draft missing or deleted? Recreate it</summary>
            <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/recreate-draft" style="margin-top:12px" onsubmit="return confirm('Create a replacement Gmail draft? The old local draft reference will be replaced.')">
              <input type="hidden" name="csrf" value="{{.CSRF}}">
              {{if .Attachments}}<div class="attachment-picker">{{range .Attachments}}{{if .Exists}}<label class="checkline attachment-option"><input type="checkbox" name="attachment" value="{{.ID}}" {{if eq .Kind "portfolio"}}checked{{end}}><span><b>{{.Label}}</b><small>{{.Kind}} · {{.FileName}}</small></span></label>{{end}}{{end}}</div>{{end}}
              <label class="checkline"><input type="checkbox" name="recreate_confirm" value="1" required> I confirm the Gmail draft is missing/unusable and want a replacement draft.</label>
              <div class="detail-actions"><button class="btn ghost" type="submit">Recreate Gmail Draft</button></div>
            </form>
            <div class="footer-note">The saved recipient, subject, body and CV profile are reused. The replacement remains DRAFT_CREATED and still requires review.</div>
          </details>
          {{end}}
          <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/approve" style="margin-top:14px">
            <input type="hidden" name="csrf" value="{{.CSRF}}">
            <div class="form-group"><label>Review Note <span class="muted">(optional)</span></label><textarea name="review_note" maxlength="500" rows="3" placeholder="e.g. Recipient, subject, body, CV and portfolio checked in Gmail."></textarea></div>
            <label class="checkline"><input type="checkbox" name="review_confirm" value="1" required> I reviewed the Gmail draft, recipient, email content, and attachments.</label>
            <div class="detail-actions"><button class="btn primary" type="submit">Approve Application</button></div>
          </form>
          <div class="footer-note">Approval only changes the local lifecycle to APPROVED. It does not send the email.</div>
        {{else if eq .SelectedApplication.State "APPROVED"}}
          <div class="alert success" style="margin-top:16px">Application is approved for a separate explicit send action. No email has been sent by approval.</div>
          <div class="detail-grid" style="margin-top:14px">
            <div class="info-card"><h3>Manual Review</h3><div class="info-row"><span>Reviewed at</span><b>{{.SelectedApplication.ReviewedAt}}</b></div><div class="info-row"><span>Review note</span><b>{{if .SelectedApplication.ReviewNote}}{{.SelectedApplication.ReviewNote}}{{else}}—{{end}}</b></div></div>
            <div class="info-card"><h3>Send Gate</h3><div class="info-row"><span>Status</span><b>APPROVED</b></div><div class="info-row"><span>Email sent</span><b>No</b></div></div>
          </div>
          <div class="detail-actions"><a class="btn ghost" target="_blank" rel="noreferrer" href="https://mail.google.com/mail/u/0/#drafts">Open Gmail Drafts ↗</a></div>
          {{if and .GmailConnected .SelectedCVReady}}
          <details style="margin-top:12px"><summary class="job-link" style="cursor:pointer">Approved draft missing or deleted? Recreate it</summary>
            <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/recreate-draft" style="margin-top:12px" onsubmit="return confirm('Create a replacement Gmail draft? Approval will be reset and the new draft must be reviewed again.')">
              <input type="hidden" name="csrf" value="{{.CSRF}}">
              {{if .Attachments}}<div class="attachment-picker">{{range .Attachments}}{{if .Exists}}<label class="checkline attachment-option"><input type="checkbox" name="attachment" value="{{.ID}}" {{if eq .Kind "portfolio"}}checked{{end}}><span><b>{{.Label}}</b><small>{{.Kind}} · {{.FileName}}</small></span></label>{{end}}{{end}}</div>{{end}}
              <label class="checkline"><input type="checkbox" name="recreate_confirm" value="1" required> I confirm the approved Gmail draft is missing/unusable and want a replacement.</label>
              <div class="detail-actions"><button class="btn ghost" type="submit">Recreate Gmail Draft &amp; Reset Approval</button></div>
            </form>
            <div class="footer-note">A successful replacement changes APPROVED → DRAFT_CREATED and clears the old review approval. The replacement must be reviewed again before it can be sent.</div>
          </details>
          {{end}}
          <form method="post" action="/app/applications/send-confirm" style="margin-top:10px"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="job_id" value="{{.SelectedApplication.JobID}}"><button class="btn primary" type="submit" style="width:100%">Review &amp; Send Application</button></form>
          <form method="post" action="/app/applications/{{.SelectedApplication.JobID}}/unapprove" style="margin-top:14px" onsubmit="return confirm('Return this application to DRAFT_CREATED for more review?')">
            <input type="hidden" name="csrf" value="{{.CSRF}}">
            <div class="form-group"><label>Reason for reopening <span class="muted">(optional)</span></label><textarea name="review_note" maxlength="500" rows="2" placeholder="e.g. Need to revise the Gmail draft before sending."></textarea></div>
            <div class="detail-actions"><button class="btn ghost" type="submit">Unapprove &amp; Reopen Review</button></div>
          </form>
          <div class="footer-note">Unapprove keeps the existing Gmail draft and returns the local state to DRAFT_CREATED.</div>
        {{else if eq .SelectedApplication.State "SENT"}}
          <div class="alert success" style="margin-top:16px">Application is recorded as SENT.</div>
          <div class="detail-actions"><button class="btn" disabled>Edit (Locked)</button></div>
          <div class="footer-note">Sent applications are locked from preparation and review state changes.</div>
        {{else}}
          <div class="detail-actions"><button class="btn" disabled>Edit (Locked)</button></div>
          <div class="footer-note">This lifecycle state is protected from re-preparation.</div>
        {{end}}
      </div></div>
    {{else}}
      <div class="pipeline-strip">
        <div class="pipeline-mini"><b>{{.Stats.ReadyTotal}}</b><span>Ready Email</span></div>
        <div class="pipeline-mini"><b>{{.Stats.NeedReviewTotal}}</b><span>Need Review</span></div>
        <div class="pipeline-mini"><b>{{.Stats.DraftTotal}}</b><span>Draft Created</span></div>
        <div class="pipeline-mini"><b>{{.Stats.ApprovedTotal}}</b><span>Approved</span></div>
        <div class="pipeline-mini"><b>{{.Stats.SentTotal}}</b><span>Sent</span></div>
      </div>
      <form class="toolbar" method="get" action="/app/applications">
        <input class="control search" name="q" value="{{.Query}}" placeholder="Search applications…">
        <select class="control" name="state"><option value="">All states</option>{{range .States}}{{if ne . "NOT_APPLIED"}}<option value="{{.}}" {{if eq $.StateFilter .}}selected{{end}}>{{.}}</option>{{end}}{{end}}</select>
        <select class="control" name="method"><option value="">All methods</option>{{range .Methods}}<option value="{{.}}" {{if eq $.MethodFilter .}}selected{{end}}>{{.}}</option>{{end}}</select>
        <button class="btn primary" type="submit">Apply Filters</button><a class="btn ghost" href="/app/applications">Reset</a>
      </form>
      <form method="post" id="bulk-app-form">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <div class="bulk-bar">
          <div class="bulk-primary-row">
            <div class="bulk-select"><label class="checkline"><input id="select-all-apps" type="checkbox"> Select all visible</label><span id="selected-count" class="batch-note">0 selected</span></div>
            <div class="bulk-actions">
              <button class="btn ghost" type="submit" formaction="/app/applications/bulk/prepare">Prepare Selected</button>
              <button class="btn ghost" type="submit" formaction="/app/applications/bulk/draft">Create Drafts</button>
              <button class="btn ghost" type="submit" formaction="/app/applications/bulk/review">Review Selected</button>
              <button class="btn ghost" type="submit" formaction="/app/applications/bulk/remove" onclick="return confirm('Remove eligible selected applications from the queue? READY_EMAIL/NEED_REVIEW only; collected Jobs stay in the database.')">Remove Selected</button>
              <a class="btn ghost" href="/app/applications/review">Review Draft Queue ({{.Stats.DraftTotal}})</a>
              <button class="btn primary" type="submit" formaction="/app/applications/send-confirm">Confirm Send</button>
            </div>
          </div>
          <div class="bulk-hint">Use preparation and draft actions for repeatable work. Human review and final send stay separate so batch processing never bypasses approval.</div>
          {{if .Attachments}}<div class="bulk-attachments"><span class="batch-note">Attachments for batch draft creation:</span>{{range .Attachments}}{{if .Exists}}<label class="checkline"><input type="checkbox" name="attachment" value="{{.ID}}" {{if eq .Kind "portfolio"}}checked{{end}}> {{.Label}}</label>{{end}}{{end}}</div>{{end}}
        </div>
        <div class="content-card"><div class="table-wrap"><table><thead><tr><th style="width:42px"></th><th>Job</th><th>Company</th><th>Method</th><th>Status</th><th>Preparation</th><th>Recipient</th><th>Updated</th></tr></thead><tbody>
        {{range .Applications}}<tr class="js-selectable-row"><td><input class="row-check js-app-check" type="checkbox" name="job_id" value="{{.JobID}}"></td><td class="table-title"><a class="job-link" href="/app/applications/{{.JobID}}">{{.Title}}</a></td><td class="table-company">{{.Company}}</td><td><span class="badge method-{{lower .Method}}">{{.Method}}</span></td><td><span class="badge state-{{lower .State}}">{{.State}}</span></td><td>{{if .Prepared}}<span class="badge state-approved">PREPARED</span>{{else}}<span class="muted">Not prepared</span>{{end}}</td><td class="muted">{{.Recipient}}</td><td class="muted">{{.Updated}}</td></tr>{{end}}
        </tbody></table></div>{{if not .Applications}}<div class="empty">No applications match the current filters.</div>{{end}}</div>
      </form>
    {{end}}
  {{end}}

  {{if eq .Active "cv-profiles"}}
    <section class="content-card"><div class="content-pad">
      <div class="detail-title"><div><h2>Primary CV Library</h2><p class="muted">Upload a new CV or use the same profile ID to replace/update an existing one.</p></div><span class="badge state-approved">{{len .CVProfiles}} PROFILES</span></div>
      <form method="post" action="/app/cv-profiles/upload" enctype="multipart/form-data" class="upload-form">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <div class="form-grid">
          <div class="form-group"><label>Profile ID</label><input name="profile_id" placeholder="general, sales, business-development" required maxlength="80"></div>
          <div class="form-group"><label>CV File</label><input type="file" name="file" accept=".pdf,.doc,.docx" required></div>
          <div class="form-group"><label>Keywords</label><input name="keywords" placeholder="sales, business development, account executive"></div>
          <div class="form-group"><label>Priority</label><input name="priority" type="number" min="0" max="100" value="1"></div>
        </div>
        <label class="checkline"><input type="checkbox" name="set_default" value="1"> Set this profile as default</label>
        <div class="detail-actions"><button class="btn primary" type="submit">Upload / Replace CV</button></div>
        <div class="footer-note">Files are stored locally under ~/.linkedin-jobs/files. Uploading the same profile ID replaces its active CV path.</div>
      </form>
    </div></section>

    {{if .CVProfiles}}
      <div class="cv-grid" style="margin-top:14px">
      {{range .CVProfiles}}
        <article class="cv-card">
          {{if .Default}}<span class="cv-default">Default</span>{{end}}
          <h3>{{.ID}}</h3>
          <div class="cv-path">{{.FileName}}</div>
          <p>{{.Path}}</p>
          {{if .Exists}}<span class="badge state-approved">FILE READY</span>{{else}}<span class="badge state-need_review">FILE MISSING</span>{{end}}
          <form method="post" action="/app/cv-profiles/{{.ID}}/update" style="margin-top:14px">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <div class="form-group"><label>Keywords</label><input name="keywords" value="{{.Keywords}}" placeholder="sales, business development"></div>
            <div class="form-group" style="margin-top:8px"><label>Priority</label><input name="priority" type="number" min="0" max="100" value="{{.Priority}}"></div>
            {{if not .Default}}<label class="checkline"><input type="checkbox" name="set_default" value="1"> Set as default CV</label>{{end}}
            <div class="inline-actions"><button class="btn ghost" type="submit">Save Changes</button></div>
          </form>
          <div class="inline-actions">
            {{if not .Default}}<form method="post" action="/app/cv-profiles/{{.ID}}/default"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="btn ghost" type="submit">Set Default</button></form>{{end}}
            <form method="post" action="/app/cv-profiles/{{.ID}}/delete" onsubmit="return confirm('Delete CV profile {{.ID}}?')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="btn ghost" type="submit">Delete</button></form>
          </div>
        </article>
      {{end}}
      </div>
    {{else}}
      <div class="content-card" style="margin-top:14px"><div class="empty">No CV profiles yet. Upload your first CV above.</div></div>
    {{end}}

    <section class="content-card" style="margin-top:18px"><div class="content-pad">
      <div class="detail-title"><div><h2>Additional Attachments</h2><p class="muted">Portfolio, cover letter, certificates, or other files you may want to attach to selected applications.</p></div><span class="badge method-email">{{len .Attachments}} FILES</span></div>
      <form method="post" action="/app/cv-profiles/attachments/upload" enctype="multipart/form-data" class="upload-form">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <div class="form-grid">
          <div class="form-group"><label>Label</label><input name="label" placeholder="Professional Portfolio 2026" required maxlength="100"></div>
          <div class="form-group"><label>Type</label><select name="kind"><option value="portfolio">Portfolio</option><option value="cover_letter">Cover Letter</option><option value="certificate">Certificate</option><option value="other">Other</option></select></div>
          <div class="form-group"><label>File</label><input type="file" name="file" accept=".pdf,.doc,.docx,.ppt,.pptx,.png,.jpg,.jpeg" required></div>
        </div>
        <div class="detail-actions"><button class="btn primary" type="submit">Upload Attachment</button></div>
      </form>

      {{if .Attachments}}
        <div class="file-list">
        {{range .Attachments}}
          <div class="file-card">
            <div><strong>{{.Label}}</strong><small>{{.Kind}} · {{.FileName}}{{if .Size}} · {{.Size}}{{end}}</small><small>Stored locally · friendly filename used in Gmail</small></div>
            <div>{{if .Exists}}<span class="badge state-approved">READY</span>{{else}}<span class="badge state-need_review">MISSING</span>{{end}}</div>
            <form method="post" action="/app/cv-profiles/attachments/{{.ID}}/delete" onsubmit="return confirm('Delete attachment {{.Label}}?')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="btn ghost" type="submit">Delete</button></form>
          </div>
        {{end}}
        </div>
      {{else}}
        <div class="empty">No additional attachments uploaded yet.</div>
      {{end}}
    </div></section>

    <section class="content-card" style="margin-top:18px"><div class="content-pad"><h2>Selection Rules</h2><p class="muted">CV selection remains deterministic; additional attachments are selected manually for each Gmail draft.</p><div class="detail-grid"><div class="info-card"><div class="info-row"><span>Configured profiles</span><b>{{len .CVProfiles}}</b></div><div class="info-row"><span>Default profile</span><b>{{.DefaultCVProfile}}</b></div></div><div class="info-card"><div class="info-row"><span>Additional files</span><b>{{len .Attachments}}</b></div><div class="info-row"><span>Attachment behavior</span><b>Manual per application</b></div></div></div></div></section>
  {{end}}

  {{if eq .Active "collect"}}
    {{if .CollectError}}<div class="alert">{{.CollectError}}</div>{{end}}
    {{if .CollectMessage}}<div class="alert success">{{.CollectMessage}}</div>{{end}}
    <div class="collect-grid">
      <section class="content-card"><div class="content-pad"><h2>Search Criteria</h2>
        <form method="post" action="/app/collect/run" id="collect-form">
          <input type="hidden" name="csrf" value="{{.CSRF}}">
          <div class="form-grid">
            <div class="form-group"><label>Keywords / Job Title</label><input id="collect-keywords" name="keywords" value="{{.CollectKeywords}}" placeholder="e.g. Sales Executive" required maxlength="160"></div>
            <div class="form-group"><label>Location</label><input id="collect-location" name="location" value="{{.CollectLocation}}" maxlength="160"></div>
            <div class="form-group"><label>Posted Within</label><select id="collect-posted" name="posted_within"><option value="7d" {{if eq .CollectPostedWithin "7d"}}selected{{end}}>Past week</option><option value="1d" {{if eq .CollectPostedWithin "1d"}}selected{{end}}>Past 24 hours</option><option value="30d" {{if eq .CollectPostedWithin "30d"}}selected{{end}}>Past month</option></select></div>
            <div class="form-group"><label>Maximum Results</label><input id="collect-top" name="top" type="number" min="1" max="100" value="{{.CollectTop}}" required></div>
          </div>
          <div class="form-group" style="margin-top:12px"><label>Command Preview</label><div class="field" id="collect-preview"></div></div>
          <button class="btn primary" id="collect-submit" type="submit" style="margin-top:14px">Start Collecting</button>
          <div class="footer-note">Anonymous public collection only. UI limit: 100 jobs per run. Existing LinkedIn IDs are skipped; force overwrite and session fallback remain CLI-only.</div>
        </form>
        {{if .CollectMessage}}<div class="collect-summary"><div class="mini"><b>{{.CollectSearched}}</b><span>Searched</span></div><div class="mini"><b>{{.CollectNew}}</b><span>New candidates</span></div><div class="mini"><b>{{.CollectPersisted}}</b><span>Persisted</span></div><div class="mini"><b>{{.CollectExactDuplicates}} / {{.CollectLikelyReposts}}</b><span>Exact / repost</span></div></div>{{end}}
      </div></section>
      <aside class="content-card"><div class="content-pad"><h2>Collection Progress</h2><div class="progress-list"><div class="progress-item"><span class="dot"></span>Anonymous LinkedIn search</div><div class="progress-item"><span class="dot"></span>Full detail + application extraction</div><div class="progress-item"><span class="dot"></span>Job-ID and structural dedup</div><div class="progress-item"><span class="dot"></span>Persist results to SQLite</div></div><div class="footer-note">The request remains open while collection runs. Do not submit the form twice.</div></div></aside>
    </div>
  {{end}}

  {{if eq .Active "settings"}}
    <div class="settings-grid"><aside class="content-card settings-menu"><div class="content-pad">
      <button class="active js-settings-tab" data-target="settings-general">General</button>
      <button class="js-settings-tab" data-target="settings-collector">Collector</button>
      <button class="js-settings-tab" data-target="settings-email">Email &amp; Gmail</button>
      <button class="js-settings-tab" data-target="settings-cv">CV Profiles</button>
      <button class="js-settings-tab" data-target="settings-db">Database</button>
      <button class="js-settings-tab" data-target="settings-about">About</button>
    </div></aside>
    <section class="content-card"><div class="content-pad">
      <div id="settings-general" class="settings-pane active"><h2>General Settings</h2><p class="muted">Current local candidate and application preferences.</p><div class="form-grid"><div class="form-group"><label>Candidate Name</label><input value="{{.CandidateName}}" readonly></div><div class="form-group"><label>Default CV Profile</label><input value="{{.DefaultCVProfile}}" readonly></div><div class="form-group"><label>Preferred Location</label><input value="{{.ProfileLocation}}" readonly></div><div class="form-group"><label>Work Arrangement</label><input value="{{.ProfileArrangement}}" readonly></div><div class="form-group"><label>Salary Floor</label><input value="{{.ProfileSalary}}" readonly></div><div class="form-group"><label>CV Profiles</label><input value="{{len .CVProfiles}} configured" readonly></div></div></div>
      <div id="settings-collector" class="settings-pane"><h2>Collector</h2><p class="muted">Runtime boundaries inherited from the collector workflow.</p><div class="info-row"><span>Source</span><b>LinkedIn</b></div><div class="info-row"><span>Default mode</span><b>Anonymous public collection</b></div><div class="info-row"><span>Detail fetching</span><b>Bounded retry/backoff</b></div><div class="info-row"><span>Dedup</span><b>Job ID + structural fingerprint</b></div><div class="info-row"><span>LLM required</span><b>No</b></div></div>
      <div id="settings-email" class="settings-pane"><h2>Email &amp; Gmail</h2><p class="muted">Native Gmail OAuth is used only for explicit application actions.</p>
        <div class="info-row"><span>Status</span>{{if .GmailConnected}}<span class="badge state-approved">CONNECTED</span>{{else}}<span class="badge state-need_review">NOT CONNECTED</span>{{end}}</div>
        <div class="field-label">OAuth credentials file</div><div class="field">{{.GmailCredentialsPath}}</div>
        <div class="field-label">OAuth token file</div><div class="field">{{.GmailTokenPath}}</div>
        <div class="info-row"><span>OAuth scope</span><b>gmail.compose</b></div><div class="info-row"><span>Draft creation</span><b>Explicit</b></div><div class="info-row"><span>Explicit send</span><b>After APPROVED + final confirmation</b></div><div class="info-row"><span>Automatic send</span><b>Disabled</b></div>
        {{if .GmailConnected}}
          <form method="post" action="/app/gmail/disconnect" style="margin-top:14px"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="btn ghost" type="submit">Disconnect Gmail</button></form>
        {{else if .GmailCredentialsFound}}
          <form method="post" action="/app/gmail/connect" style="margin-top:14px"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="btn primary" type="submit">Connect Gmail</button></form>
          <div class="footer-note">Google opens in your browser for consent. The app stores the resulting token locally with private file permissions.</div>
        {{else}}
          <div class="alert" style="margin-top:14px">Gmail OAuth credentials are not configured yet. Create a Google OAuth Desktop client with the Gmail API enabled, download its JSON, and save it to the credentials path shown above.</div>
        {{end}}
      </div>
      <div id="settings-cv" class="settings-pane"><h2>CV Profiles</h2><p class="muted">Profiles currently loaded from settings.yaml.</p><div class="info-row"><span>Profiles</span><b>{{len .CVProfiles}}</b></div><div class="info-row"><span>Default</span><b>{{.DefaultCVProfile}}</b></div>{{range .CVProfiles}}<div class="info-row"><span>{{.ID}}</span><b>{{.FileName}}</b></div>{{end}}</div>
      <div id="settings-db" class="settings-pane"><h2>Database &amp; Files</h2><div class="field-label">SQLite database</div><div class="field">{{.DBPath}}</div><div class="field-label">Settings file</div><div class="field">{{.SettingsPath}}</div><div class="footer-note">All data remains local to the CLI environment unless an explicit external action is requested.</div></div>
      <div id="settings-about" class="settings-pane"><h2>About</h2><p class="muted">LinkedIn Job CLI local command center.</p><div class="info-row"><span>UI reference</span><b>Full Application Suite</b></div><div class="info-row"><span>Design</span><b>Dark command center</b></div><div class="info-row"><span>Application workflow</span><b>Queue → Prepare → Draft → Review → Optional Send</b></div><div class="info-row"><span>Legacy UI</span><b>/legacy</b></div></div>
    </div></section></div>
  {{end}}

</main>
</div>
<script>
(function(){
  var tabs=document.querySelectorAll('.js-settings-tab');
  tabs.forEach(function(btn){btn.addEventListener('click',function(){
    tabs.forEach(function(x){x.classList.remove('active')});
    document.querySelectorAll('.settings-pane').forEach(function(x){x.classList.remove('active')});
    btn.classList.add('active');
    var pane=document.getElementById(btn.getAttribute('data-target')); if(pane)pane.classList.add('active');
  })});
  if(new URLSearchParams(window.location.search).get('tab')==='email'){
    var emailTab=document.querySelector('.js-settings-tab[data-target="settings-email"]'); if(emailTab) emailTab.click();
  }
  var k=document.getElementById('collect-keywords'), l=document.getElementById('collect-location'), p=document.getElementById('collect-posted'), t=document.getElementById('collect-top'), out=document.getElementById('collect-preview');
  function q(v){return '"' + String(v||'').replace(/"/g,'\\\"') + '"'}
  function update(){
    if(!out||!k)return;
    var cmd='linkedin-jobs collect '+q(k.value.trim()||'Sales Executive');
    if(l&&l.value.trim())cmd+=' --location '+q(l.value.trim());
    if(p&&p.value)cmd+=' --posted-within '+p.value;
    if(t&&t.value)cmd+=' --top '+t.value;
    out.textContent=cmd;
  }
  [k,l,p,t].forEach(function(el){if(el){el.addEventListener('input',update);el.addEventListener('change',update)}}); update();
  var form=document.getElementById('collect-form'), submit=document.getElementById('collect-submit');
  if(form&&submit){form.addEventListener('submit',function(){submit.disabled=true;submit.textContent='Collecting…';});}
  var selectAll=document.getElementById('select-all-apps'), appChecks=Array.prototype.slice.call(document.querySelectorAll('.js-app-check')), selectedCount=document.getElementById('selected-count');
  function updateSelected(){var n=appChecks.filter(function(x){return x.checked}).length;if(selectedCount)selectedCount.textContent=n+' selected';if(selectAll){selectAll.checked=n>0&&n===appChecks.length;selectAll.indeterminate=n>0&&n<appChecks.length;}appChecks.forEach(function(x){var row=x.closest('tr');if(row)row.classList.toggle('row-selected',x.checked);});}
  if(selectAll){selectAll.addEventListener('change',function(){appChecks.forEach(function(x){x.checked=selectAll.checked});updateSelected();});}
  appChecks.forEach(function(x){x.addEventListener('change',updateSelected)});updateSelected();
  var selectAllJobs=document.getElementById('select-all-jobs'), jobChecks=Array.prototype.slice.call(document.querySelectorAll('.js-job-check')), selectedJobsCount=document.getElementById('selected-jobs-count'), queueJobsBtn=document.getElementById('queue-selected-jobs'), processJobsBtn=document.getElementById('process-selected-jobs');
  function updateSelectedJobs(){
    var n=jobChecks.filter(function(x){return x.checked}).length;
    var processLimit=n>25, queueLimit=n>50, gmailOK=!processJobsBtn||processJobsBtn.getAttribute('data-gmail')==='1';
    if(selectedJobsCount)selectedJobsCount.textContent=n+' selected'+(processLimit?' · Process max 25':'');
    if(selectAllJobs){selectAllJobs.checked=n>0&&n===jobChecks.length;selectAllJobs.indeterminate=n>0&&n<jobChecks.length;}
    if(queueJobsBtn){queueJobsBtn.disabled=n===0||queueLimit;queueJobsBtn.title=queueLimit?'Select at most 50 jobs':'';}
    if(processJobsBtn){processJobsBtn.disabled=n===0||processLimit||!gmailOK;processJobsBtn.title=!gmailOK?'Connect Gmail first':(processLimit?'Select at most 25 jobs':'');}
    jobChecks.forEach(function(x){var row=x.closest('tr');if(row)row.classList.toggle('row-selected',x.checked);});
  }
  if(selectAllJobs){selectAllJobs.addEventListener('change',function(){jobChecks.forEach(function(x){x.checked=selectAllJobs.checked});updateSelectedJobs();});}
  jobChecks.forEach(function(x){x.addEventListener('change',updateSelectedJobs)});updateSelectedJobs();
  var reviewNext=document.getElementById('review-next'), reviewPrev=document.getElementById('review-prev');
  document.addEventListener('keydown',function(e){
    if(e.target&&/INPUT|TEXTAREA|SELECT/.test(e.target.tagName))return;
    if((e.key==='j'||e.key==='J'||e.key==='ArrowRight')&&reviewNext){window.location.href=reviewNext.href;}
    if((e.key==='k'||e.key==='K'||e.key==='ArrowLeft')&&reviewPrev){window.location.href=reviewPrev.href;}
  });
})();
</script>
</body>
</html>`;
