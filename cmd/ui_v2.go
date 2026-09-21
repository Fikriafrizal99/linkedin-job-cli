package cmd

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

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
	DraftTotal     int
	ApprovedTotal  int
}

type appJobRow struct {
	ID, Title, Company, Location, Method, State, Added, URL, Email string
}

type appApplicationRow struct {
	JobID, Title, Company, Method, State, Recipient, Updated, DraftID string
}

type appCVProfile struct {
	ID, FileName, Path, Keywords string
	Priority int
	Default bool
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
	CVProfiles []appCVProfile
	DefaultCVProfile string
	SettingsPath string
	DBPath string
	ProfileLocation string
	ProfileArrangement string
	ProfileSalary string
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

func (ws *webServer) buildAppPage(r *http.Request) (appPageData, error) {
	pd := appPageData{CSRF: ws.csrf, Active: "dashboard", Title: "Dashboard", Subtitle: "Overview of your job search and application progress."}

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
			pd.CVProfiles = append(pd.CVProfiles, appCVProfile{
				ID: p.ID,
				FileName: filepath.Base(p.Path),
				Path: p.Path,
				Keywords: strings.Join(p.Keywords, ", "),
				Priority: p.Priority,
				Default: strings.EqualFold(p.ID, settings.Application.DefaultCVProfile),
			})
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
		pd.Active, pd.Title, pd.Subtitle = "dashboard", "Dashboard", "Overview of your job search and application progress."
		for i, j := range jobs {
			if i >= 8 { break }
			pd.Jobs = append(pd.Jobs, uiJobRow(j, applicationStateFor(j.ID, apps)))
		}
		pd.SelectedApplication = preferredApplication(apps)
		if pd.SelectedApplication != nil {
			pd.SelectedApplicationJob = jobByID[pd.SelectedApplication.JobID]
		}
	case "jobs":
		pd.Active, pd.Title, pd.Subtitle = "jobs", "Jobs", "View, search, and manage all collected job listings."
		for _, j := range jobs {
			pd.Jobs = append(pd.Jobs, uiJobRow(j, applicationStateFor(j.ID, apps)))
		}
		if len(parts) > 1 {
			pd.Title, pd.Subtitle = "Job Detail", "Detailed view of a collected LinkedIn job."
			pd.SelectedJob = jobByID[parts[1]]
			if pd.SelectedJob == nil {
				return pd, fmt.Errorf("job %s not found", parts[1])
			}
		}
	case "applications":
		pd.Active, pd.Title, pd.Subtitle = "applications", "Applications", "Track and manage your application pipeline."
		for _, a := range apps {
			j := jobByID[a.JobID]
			row := appApplicationRow{
				JobID: a.JobID, Method: "EMAIL", State: a.State, Recipient: a.Recipient,
				Updated: displayDate(a.UpdatedAt), DraftID: a.GmailDraftID,
			}
			if j != nil {
				row.Title, row.Company, row.Method = j.Title, j.Company, j.ApplicationMethod
			}
			pd.Applications = append(pd.Applications, row)
		}
		if len(parts) > 1 {
			pd.Title, pd.Subtitle = "Application Detail", "Review and manage a prepared application."
			a, err := ws.st.GetApplicationByJobID(parts[1])
			if err != nil { return pd, err }
			if a == nil { return pd, fmt.Errorf("application %s not found", parts[1]) }
			pd.SelectedApplication = a
			pd.SelectedApplicationJob = jobByID[a.JobID]
		}
	case "cv-profiles":
		pd.Active, pd.Title, pd.Subtitle = "cv-profiles", "CV Profiles", "Manage CV profiles used by deterministic application preparation."
	case "collect":
		pd.Active, pd.Title, pd.Subtitle = "collect", "Collect LinkedIn Jobs", "Run the LinkedIn collector with your preferred search criteria."
	case "settings":
		pd.Active, pd.Title, pd.Subtitle = "settings", "Settings", "Review application preferences and local system configuration."
	default:
		return pd, fmt.Errorf("unknown UI page %q", section)
	}
	return pd, nil
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
.main{margin-left:var(--sidebar);padding:calc(var(--top) + 24px) 22px 28px;min-height:100vh}.page-head{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:20px}.page-head h1{margin:0;font-size:26px;line-height:1.1}.page-head p{margin:7px 0 0;color:#a3b2c5}.btn{border:1px solid #315273;background:#18324d;color:var(--text);padding:9px 14px;border-radius:8px;cursor:pointer}.btn.primary{background:linear-gradient(180deg,#348fff,#1976df);border-color:#3d9aff;color:white}.btn.ghost{background:#12243a}.btn:disabled{opacity:.45;cursor:not-allowed}
.grid-kpi{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-bottom:18px}.kpi{background:linear-gradient(145deg,#10243a,#0e1f33);border:1px solid #223b58;border-radius:12px;padding:17px;min-height:104px;display:flex;gap:14px;align-items:flex-start}.kpi-icon{width:48px;height:48px;border-radius:50%;display:grid;place-items:center;font-weight:800}.kpi-icon.amber{background:#4c4126;color:#ffc04d}.kpi-icon.cyan{background:#123f56;color:#58d6ff}.kpi-icon.purple{background:#292b62;color:#b692ff}.kpi-icon.green{background:#174737;color:#52dda7}.kpi .value{font-size:26px;font-weight:750;line-height:1}.kpi .label{color:#c8d5e4;margin-top:7px}.kpi .hint{font-size:11px;color:var(--muted);margin-top:8px}.hint.good{color:#4bd99f}
.panel{background:linear-gradient(145deg,#0f2135,#0d1c2e);border:1px solid #223a55;border-radius:12px;overflow:hidden}.panel-head{height:56px;padding:0 16px;display:flex;align-items:center;border-bottom:1px solid #223a55}.panel-head h2{margin:0;font-size:17px}.panel-head .sub{margin-left:auto;color:#6caeff;font-size:12px}.dashboard-grid{display:grid;grid-template-columns:minmax(0,1.8fr) minmax(320px,1fr);gap:14px}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse}th{height:38px;padding:0 12px;text-align:left;font-size:11px;font-weight:500;color:#9bacc0;background:#0e1e30;border-bottom:1px solid #25405d;white-space:nowrap}td{padding:11px 12px;border-bottom:1px solid #21384f;color:#cbd6e4;white-space:nowrap}tr:hover td{background:#112944}.job-link{color:#62aaff;font-weight:500}.muted{color:var(--muted)}.badge{display:inline-flex;align-items:center;border:1px solid transparent;border-radius:7px;padding:4px 8px;font-size:10px;font-weight:600;letter-spacing:.02em}.state-ready_email{background:#123d72;color:#7db7ff;border-color:#1d4d88}.state-draft_created{background:#342362;color:#b89aff;border-color:#4b327e}.state-approved,.state-sent{background:#124b3a;color:#6be2af;border-color:#1c614a}.state-not_applied{background:#26354a;color:#b9c6d5;border-color:#35475c}.method-email{background:#193653;color:#b8d7f7;border-color:#2e4e6e}.method-linkedin{background:#172f4a;color:#83b7f1;border-color:#274a70}.method-unknown{background:#303847;color:#bdc7d3;border-color:#465162}
.detail{padding:18px}.detail-title{display:flex;align-items:start;justify-content:space-between;gap:12px}.detail h2{margin:0 0 3px;font-size:19px}.company{color:#aebed0}.meta{display:grid;gap:8px;margin:16px 0;color:#9fb1c6;font-size:12px}.tabs{display:flex;border-bottom:1px solid #28415d;margin:0 -18px 16px;padding:0 18px}.tab{padding:10px 14px;color:#91a5bc;border-bottom:2px solid transparent}.tab.active{color:#73b2ff;border-bottom-color:#3d99ff}.field-label{font-size:11px;color:#9fb0c4;margin:12px 0 6px}.field{background:#152a42;border:1px solid #294760;border-radius:8px;padding:10px;color:#e0e8f2;white-space:pre-wrap}.email-body{min-height:170px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}.detail-actions{display:flex;gap:8px;margin-top:16px}.detail-actions .btn{flex:1}
.quick-actions{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin-top:14px}.quick{border:1px solid #223d58;background:#10243a;border-radius:10px;padding:14px}.quick strong{display:block}.quick small{color:var(--muted)}
.toolbar{display:flex;gap:8px;align-items:center;margin-bottom:12px}.toolbar .search{flex:1}.control{height:38px;border:1px solid #29455f;border-radius:8px;background:#13273e;color:#dbe5f1;padding:0 11px}.content-card{background:#0f2033;border:1px solid #223b56;border-radius:12px;overflow:hidden}.content-pad{padding:18px}.two-col{display:grid;grid-template-columns:minmax(0,2fr) minmax(280px,.8fr);gap:14px}.detail-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.info-card{border:1px solid #27425d;background:#102338;border-radius:10px;padding:14px}.info-card h3{margin:0 0 10px;font-size:14px}.info-row{display:flex;justify-content:space-between;gap:14px;padding:7px 0;border-bottom:1px solid #20384f}.info-row:last-child{border-bottom:0}.info-row span:first-child{color:#8194ab}
.cv-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px}.cv-card{background:#10243a;border:1px solid #24415e;border-radius:12px;padding:18px;min-height:220px}.cv-card h3{margin:0;font-size:16px}.cv-default{float:right;background:#124b3a;color:#6be2af;border-radius:8px;padding:3px 7px;font-size:10px}.cv-path{color:#63aaff;margin:18px 0 8px;word-break:break-all}.cv-card p{color:#9cafc4;font-size:12px}.form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.form-group label{display:block;color:#96a9bf;font-size:11px;margin-bottom:6px}.form-group input,.form-group select,.form-group textarea{width:100%;border:1px solid #29475f;background:#142a42;color:#e5edf6;border-radius:8px;padding:10px}.collect-grid{display:grid;grid-template-columns:minmax(0,1.6fr) minmax(300px,.7fr);gap:14px}.progress-list{display:grid;gap:13px;margin-top:14px}.progress-item{display:flex;gap:9px;align-items:center;color:#a9b9cb}.dot{width:9px;height:9px;border-radius:50%;background:#25c58b;box-shadow:0 0 10px rgba(37,197,139,.45)}
.settings-grid{display:grid;grid-template-columns:210px minmax(0,1fr);gap:14px}.settings-menu a{display:block;padding:10px 11px;border-radius:8px;color:#a9b9cb}.settings-menu a.active{background:#173b64;color:#76b4ff}.empty{padding:44px;text-align:center;color:#8194aa}
.alert{padding:10px 13px;border:1px solid #6e4b25;background:#382919;color:#f1c178;border-radius:8px;margin-bottom:14px}
.footer-note{color:#637991;font-size:11px;margin-top:14px}
@media(max-width:1050px){.grid-kpi{grid-template-columns:repeat(2,1fr)}.dashboard-grid,.two-col,.collect-grid{grid-template-columns:1fr}.cv-grid{grid-template-columns:1fr 1fr}.quick-actions{grid-template-columns:1fr 1fr}}
@media(max-width:760px){:root{--sidebar:0px}.sidebar{display:none}.topbar{left:0}.main{margin-left:0;padding-left:14px;padding-right:14px}.grid-kpi,.cv-grid,.form-grid,.detail-grid{grid-template-columns:1fr}.top-user .name{display:none}.global-search{width:70vw}}
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
  <input class="global-search" placeholder="Search jobs, companies, or keywords…" aria-label="Global search">
  <div class="top-user"><div class="avatar">{{.CandidateInitials}}</div><div class="name">{{.CandidateName}}<small>Local Job Command Center</small></div></div>
</header>
<main class="main">
  {{if .Error}}<div class="alert">{{.Error}}</div>{{end}}
  <div class="page-head"><div><h1>{{.Title}}</h1><p>{{.Subtitle}}</p></div>{{if eq .Active "dashboard"}}<a class="btn primary" href="/app/collect">＋ Collect New Jobs</a>{{end}}</div>

  {{if eq .Active "dashboard"}}
    <section class="grid-kpi">
      <div class="kpi"><div class="kpi-icon amber">J</div><div><div class="value">{{.Stats.JobsTotal}}</div><div class="label">Jobs Collected</div><div class="hint">Stored in SQLite</div></div></div>
      <div class="kpi"><div class="kpi-icon cyan">@</div><div><div class="value">{{.Stats.EmailTotal}}</div><div class="label">With Email Contact</div><div class="hint">Explicit application emails</div></div></div>
      <div class="kpi"><div class="kpi-icon purple">A</div><div><div class="value">{{.Stats.PipelineTotal}}</div><div class="label">In Application Pipeline</div><div class="hint">{{.Stats.ReadyTotal}} ready · {{.Stats.DraftTotal}} draft · {{.Stats.ApprovedTotal}} approved</div></div></div>
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
      <a class="quick" href="/app/cv-profiles"><strong>▤ Manage CVs</strong><small>Review configured CV profiles</small></a>
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
        <aside class="content-card"><div class="content-pad"><h3>Application</h3><p class="muted">Explicit data extracted from the posting.</p><div class="info-row"><span>Method</span><b>{{.SelectedJob.ApplicationMethod}}</b></div><div class="info-row"><span>Email</span><b>{{.SelectedJob.ApplyEmail}}</b></div>{{if .SelectedJob.ApplyURL}}<a class="btn ghost" style="display:block;text-align:center;margin-top:12px" target="_blank" href="{{.SelectedJob.ApplyURL}}">Open Apply URL ↗</a>{{end}}</div></aside>
      </section>
    {{else}}
      <div class="toolbar"><input class="control search" placeholder="Search jobs, companies, or keywords…"><select class="control"><option>Location</option></select><select class="control"><option>Method</option></select><select class="control"><option>Status</option></select><button class="btn ghost">Reset</button></div>
      <div class="content-card"><div class="table-wrap"><table><thead><tr><th>Job Title</th><th>Company</th><th>Location</th><th>Method</th><th>Status</th><th>Added</th></tr></thead><tbody>
      {{range .Jobs}}<tr><td><a class="job-link" href="/app/jobs/{{.ID}}">{{.Title}}</a></td><td>{{.Company}}</td><td class="muted">{{.Location}}</td><td><span class="badge method-{{lower .Method}}">{{.Method}}</span></td><td><span class="badge state-{{lower .State}}">{{.State}}</span></td><td class="muted">{{.Added}}</td></tr>{{end}}
      </tbody></table></div>{{if not .Jobs}}<div class="empty">No jobs found.</div>{{end}}</div>
    {{end}}
  {{end}}

  {{if eq .Active "applications"}}
    {{if .SelectedApplication}}
      <div class="content-card"><div class="content-pad"><div class="detail-title"><div><h2>{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Title}}{{else}}Application{{end}}</h2><div class="company">{{if .SelectedApplicationJob}}{{.SelectedApplicationJob.Company}}{{end}}</div></div><span class="badge state-{{lower .SelectedApplication.State}}">{{.SelectedApplication.State}}</span></div>
        <div class="tabs"><span class="tab active">Email</span><span class="tab">CV &amp; Files</span><span class="tab">Timeline</span><span class="tab">Notes</span></div>
        <div class="form-grid"><div><div class="field-label">To</div><div class="field">{{.SelectedApplication.Recipient}}</div></div><div><div class="field-label">CV Profile</div><div class="field">{{.SelectedApplication.CVProfile}}</div></div></div>
        <div class="field-label">Subject</div><div class="field">{{.SelectedApplication.Subject}}</div>
        <div class="field-label">Email Body</div><div class="field email-body">{{.SelectedApplication.Body}}</div>
        <div class="detail-grid" style="margin-top:14px"><div class="info-card"><h3>Provider</h3><div class="info-row"><span>Draft ID</span><b>{{.SelectedApplication.GmailDraftID}}</b></div><div class="info-row"><span>Message ID</span><b>{{.SelectedApplication.GmailMessageID}}</b></div><div class="info-row"><span>Thread ID</span><b>{{.SelectedApplication.GmailThreadID}}</b></div></div><div class="info-card"><h3>Review</h3><div class="info-row"><span>Reviewed</span><b>{{.SelectedApplication.ReviewedAt}}</b></div><div class="info-row"><span>Note</span><b>{{.SelectedApplication.ReviewNote}}</b></div><div class="info-row"><span>Sent</span><b>{{.SelectedApplication.SentAt}}</b></div></div></div>
        <div class="detail-actions"><button class="btn" disabled>Edit (Locked)</button><button class="btn ghost" disabled>Unapprove</button><button class="btn primary" disabled>Send (Optional)</button></div><div class="footer-note">Action wiring will reuse the existing guarded backend lifecycle. No send action is enabled in this UI phase.</div>
      </div></div>
    {{else}}
      <div class="toolbar"><input class="control search" placeholder="Search applications…"><select class="control"><option>Status</option></select><select class="control"><option>Method</option></select><button class="btn ghost">Reset</button></div>
      <div class="content-card"><div class="table-wrap"><table><thead><tr><th>Job Title</th><th>Company</th><th>Method</th><th>Status</th><th>Recipient</th><th>Updated</th></tr></thead><tbody>
      {{range .Applications}}<tr><td><a class="job-link" href="/app/applications/{{.JobID}}">{{.Title}}</a></td><td>{{.Company}}</td><td><span class="badge method-{{lower .Method}}">{{.Method}}</span></td><td><span class="badge state-{{lower .State}}">{{.State}}</span></td><td class="muted">{{.Recipient}}</td><td class="muted">{{.Updated}}</td></tr>{{end}}
      </tbody></table></div>{{if not .Applications}}<div class="empty">No applications queued yet.</div>{{end}}</div>
    {{end}}
  {{end}}

  {{if eq .Active "cv-profiles"}}
    {{if .CVProfiles}}<div class="cv-grid">{{range .CVProfiles}}<article class="cv-card">{{if .Default}}<span class="cv-default">Default</span>{{end}}<h3>{{.ID}}</h3><div class="cv-path">{{.FileName}}</div><p>{{.Path}}</p><div class="field-label">Keywords</div><p>{{if .Keywords}}{{.Keywords}}{{else}}No keyword rules configured.{{end}}</p><div class="field-label">Priority</div><p>{{.Priority}}</p><button class="btn ghost" disabled>Edit</button></article>{{end}}</div>{{else}}<div class="content-card"><div class="empty">No CV profiles configured in settings.yaml.</div></div>{{end}}
  {{end}}

  {{if eq .Active "collect"}}
    <div class="collect-grid">
      <section class="content-card"><div class="content-pad"><h2>Search Criteria</h2><div class="form-grid"><div class="form-group"><label>Keywords / Job Title</label><input value="sales executive" placeholder="e.g. Sales Executive"></div><div class="form-group"><label>Location</label><input value="Indonesia"></div><div class="form-group"><label>Posted Within</label><select><option>Past week</option><option>Past 24 hours</option><option>Past month</option></select></div><div class="form-group"><label>Maximum Results</label><input value="50"></div></div><div class="form-group" style="margin-top:12px"><label>Command Preview</label><div class="field">linkedin-jobs collect "Sales Executive" --location "Indonesia" --posted-within 7d</div></div><button class="btn primary" disabled style="margin-top:14px">Start Collecting</button><div class="footer-note">UI action is intentionally disabled until the collector POST endpoint is wired with CSRF and bounded execution.</div></div></section>
      <aside class="content-card"><div class="content-pad"><h2>Collection Progress</h2><div class="progress-list"><div class="progress-item"><span class="dot"></span>Ready to run collector</div><div class="progress-item"><span class="dot"></span>SQLite store available</div><div class="progress-item"><span class="dot"></span>Application extraction enabled</div><div class="progress-item"><span class="dot"></span>Rate-limit safeguards active</div></div></div></aside>
    </div>
  {{end}}

  {{if eq .Active "settings"}}
    <div class="settings-grid"><aside class="content-card settings-menu"><div class="content-pad"><a class="active">General</a><a>Collector</a><a>Email &amp; Gmail</a><a>CV Profiles</a><a>Database</a><a>About</a></div></aside>
    <section class="content-card"><div class="content-pad"><h2>General Settings</h2><p class="muted">Read-only view of current local configuration for this UI phase.</p><div class="form-grid"><div class="form-group"><label>Candidate Name</label><input value="{{.CandidateName}}" readonly></div><div class="form-group"><label>Default CV Profile</label><input value="{{.DefaultCVProfile}}" readonly></div><div class="form-group"><label>Preferred Location</label><input value="{{.ProfileLocation}}" readonly></div><div class="form-group"><label>Work Arrangement</label><input value="{{.ProfileArrangement}}" readonly></div><div class="form-group"><label>Salary Floor</label><input value="{{.ProfileSalary}}" readonly></div><div class="form-group"><label>Database</label><input value="{{.DBPath}}" readonly></div></div><div class="field-label">Settings file</div><div class="field">{{.SettingsPath}}</div></div></section></div>
  {{end}}

</main>
</div>
</body>
</html>`;
