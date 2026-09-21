package cmd

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/fx"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/render"
	"linkedin-jobs/internal/salary"
	"linkedin-jobs/internal/score"
	"linkedin-jobs/internal/store"
)

var (
	serveAddr string
	servePort int
)

//go:embed assets/logo.png
var logoPNG []byte

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the local Job Command Center web UI",
	Long: `Starts the local LinkedIn Job CLI command-center UI.

The default experience is the unified application UI under /app/ with Dashboard,
Jobs, Applications, CV Profiles, Collect Jobs, and Settings. The previous jobs
browser remains available at /legacy while the new UI is wired incrementally.

Binds to localhost only by default.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openStore()
		if err != nil {
			die("failed to open DB: %v", err)
		}
		defer st.Close()

		tpl, err := newPageTemplate()
		if err != nil {
			die("template parse: %v", err)
		}
		token := make([]byte, 24)
		if _, err := rand.Read(token); err != nil {
			die("csrf token: %v", err)
		}
		ws := &webServer{
			st:          st,
			tpl:         tpl,
			csrf:        hex.EncodeToString(token),
			filtersPath: filtersFilePath(loadCfg().DBPath),
		}

		addr := serveAddr
		if addr == "" {
			addr = "127.0.0.1"
		}
		mux := http.NewServeMux()
		mux.HandleFunc("GET /", ws.handleAppRoot)
		mux.HandleFunc("GET /app/", ws.handleAppUI)
		mux.HandleFunc("GET /legacy", ws.handleLegacyIndex)
		mux.HandleFunc("GET /assets/logo.png", handleLogo)
		mux.HandleFunc("POST /jobs/{id}/status", ws.handleStatus)
		mux.HandleFunc("POST /jobs/{id}/view", ws.handleView)
		mux.HandleFunc("POST /jobs/{id}/delete", ws.handleDelete)
		srv := &http.Server{
			Addr:              fmt.Sprintf("%s:%d", addr, servePort),
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		fmt.Printf("Serving LinkedIn Job CLI on http://%s/\n", srv.Addr)
		fmt.Printf("Legacy jobs view: http://%s/legacy\n", srv.Addr)
		fmt.Println("Press Ctrl+C to stop.")
		if err := srv.ListenAndServe(); err != nil {
			die("server error: %v", err)
		}
		return nil
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1", "bind address (defaults to localhost only)")
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "port to serve on")
	rootCmd.AddCommand(serveCmd)
}

// newPageTemplate parses the embedded pageHTML template. Centralized so the
// render path and the render-safety test share one parse entry point.
func newPageTemplate() (*template.Template, error) {
	return template.New("page").Parse(pageHTML)
}

// handleLogo serves the embedded brand logo. Immutable bytes, so headers are
// set once and the body is written straight from the embed.
func handleLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	_, _ = w.Write(logoPNG)
}

// webServer holds the open store, the parsed page template, and a per-session
// CSRF token required for all write endpoints. filtersPath points at a JSON
// file alongside the DB where the last-applied filter state is persisted so it
// survives server restarts; filtersMu guards concurrent read/write.
type webServer struct {
	st          *store.Store
	tpl         *template.Template
	csrf        string
	filtersPath string
	filtersMu   sync.Mutex
}

func (ws *webServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()

	// Three arrival modes:
	//   ?clear=1      → explicit reset: drop saved state, render empty
	//   <any filters> → Apply submit: use the URL's values and persist them
	//   bare "/"      → fresh visit: restore the last-saved filter (if any)
	var f formVals
	switch {
	case q.Get("clear") == "1":
		ws.clearSavedFilters()
		f = defaultFormVals()
	case hasFilterParams(q):
		f = readForm(q)
		ws.saveFilters(f)
	default:
		f = ws.loadSavedFilters()
	}

	// Rebuild query values from the chosen form state so the query/render path
	// runs identically whether the source was a fresh GET or a restored filter.
	qq := f.toQueryValues()
	pd := pageData{F: f, CSRF: ws.csrf}
	jobs, mode, qerr := ws.query(qq)
	pd.Mode = mode
	if qerr != nil {
		pd.Error = qerr.Error()
	} else {
		// Client-side pagination: the store returns all matches (no LIMIT),
		// then we slice the current page and precompute pager links.
		pageSize := f.PageSizeOrDefault()
		page := softPage(q.Get("page"))
		total := len(jobs)
		pd.Pagination = ws.buildPagination(f, page, pageSize, total)
		from := (pd.Pagination.Page - 1) * pageSize
		to := pd.Pagination.Page * pageSize
		if to > total {
			to = total
		}
		pd.N = total
		pd.Jobs = make([]jobView, 0, to-from)
		for _, j := range jobs[from:to] {
			pd.Jobs = append(pd.Jobs, toJobView(j))
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ws.tpl.Execute(w, pd); err != nil {
		fmt.Fprintln(os.Stderr, "render error:", err)
	}
}

// handleStatus sets a job's status to one of the user-facing values. Other
// fields (notes, score, enrichment, …) are never touched.
func (ws *webServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id := r.PathValue("id")
	status := strings.TrimSpace(r.PostFormValue("status"))
	if !validStatus(status) {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	if err := ws.st.SetTag(id, status, ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ws.respond(w, r, map[string]interface{}{"ok": true, "id": id, "status": status})
}

// handleView advances a job from "new" to "viewed" only; any other status is
// left unchanged. Fired automatically when a user opens a job's posting.
func (ws *webServer) handleView(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := ws.st.MarkViewed(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ws.respond(w, r, map[string]interface{}{"ok": true, "id": id})
}

// handleDelete hard-deletes a job and its FTS entry.
func (ws *webServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := ws.st.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ws.respond(w, r, map[string]interface{}{"ok": true, "id": id, "deleted": true})
}

// checkCSRF validates the per-session token from a form field or the
// X-CSRF-Token header. Writes are same-origin only; the token is embedded in the
// page and unreadable by cross-origin scripts.
func (ws *webServer) checkCSRF(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return false
	}
	tok := r.PostFormValue("csrf")
	if tok == "" {
		tok = r.Header.Get("X-CSRF-Token")
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(ws.csrf)) != 1 {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return false
	}
	return true
}

// respond replies as JSON for fetch callers, or redirects (PRG) for plain form
// posts so a no-JS delete still works.
func (ws *webServer) respond(w http.ResponseWriter, r *http.Request, data map[string]interface{}) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
		return
	}
	http.Redirect(w, r, refererOrRoot(r), http.StatusSeeOther)
}

func refererOrRoot(r *http.Request) string {
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Host == r.Host {
			return u.RequestURI()
		}
	}
	return "/"
}

// query runs either an FTS5 search (when "q" is present) or a filtered List.
// Search is ranked by FTS relevance; the column filters and sort only apply in
// list mode — this mirrors the CLI's split between `query` and `list`.
//
// When "q" looks like a LinkedIn job id (a non-empty digit string), a direct
// id lookup is tried first — FTS5 doesn't index the id column, so a bare
// numeric search would otherwise return nothing even when the job is stored.
// On miss it falls through to FTS so a numeric phrase in the description still
// matches.
func (ws *webServer) query(q url.Values) ([]*models.JobPosting, string, error) {
	if term := strings.TrimSpace(q.Get("q")); term != "" {
		if isJobID(term) {
			if j, err := ws.st.Get(term); err == nil && j != nil {
				return []*models.JobPosting{j}, "id", nil
			}
		}
		jobs, err := ws.st.SearchFTS(ftsExpr([]string{term}, nil), 0)
		return jobs, "search", err
	}
	minSal := softSalary(q.Get("min_salary"))
	currency := fx.Normalize(q.Get("salary_currency"))
	if currency != "" && !fx.Supported(currency) {
		// Ignore unknown codes rather than 500-ing the page.
		currency = ""
	}
	f := store.Filters{
		Company:           q.Get("company"),
		Title:             q.Get("title"),
		Location:          q.Get("location"),
		Status:            q.Get("status"),
		Source:            q.Get("source"),
		MinSalary:         minSal,
		MinSalaryCurrency: currency,
		MinScore:          softInt(q.Get("min_score")),
		Remote:            q.Get("remote") == "1",
		Hybrid:            q.Get("hybrid") == "1",
		Onsite:            q.Get("onsite") == "1",
		HasSalary:         q.Get("has_salary") == "1",
		SortBySearched:    q.Get("sort") == "searched",
		SortByScore:       q.Get("sort") != "salary" && q.Get("sort") != "searched", // default: fit score
		SinceSearched:     normalizeSinceSearched(q.Get("since_searched")),
	}
	// FX-aware floor can't be done in SQL: defer it to Go.
	if currency != "" && minSal > 0 {
		f.MinSalary = 0
	}
	jobs, err := ws.st.List(f)
	if err != nil {
		return nil, "list", err
	}
	if currency != "" && minSal > 0 {
		jobs = filterByMinSalary(jobs, minSal, currency)
	}
	return jobs, "list", err
}

type formVals struct {
	Q, Company, Title, Location, Status, Source string
	MinSalary, MinSalaryCurrency, MinScore      string
	Sort                                        string
	Remote, Hybrid, Onsite, HasSalary           bool
	PageSize                                    int
	SinceSearched                               string
}

// defaultPageSize is the per-page job count when none is chosen. validPageSizes
// are the only page sizes the UI offers; any other value falls back to the
// default so the pager can't be coerced into a huge/odd slice via the URL.
const defaultPageSize = 20

var validPageSizes = []int{20, 50, 100}

func readForm(q url.Values) formVals {
	sort := q.Get("sort")
	if sort == "" {
		sort = "score"
	}
	return formVals{
		Q:                 q.Get("q"),
		Company:           q.Get("company"),
		Title:             q.Get("title"),
		Location:          q.Get("location"),
		Status:            q.Get("status"),
		Source:            q.Get("source"),
		MinSalary:         q.Get("min_salary"),
		MinSalaryCurrency: q.Get("salary_currency"),
		MinScore:          q.Get("min_score"),
		Sort:              sort,
		Remote:            q.Get("remote") == "1",
		Hybrid:            q.Get("hybrid") == "1",
		Onsite:            q.Get("onsite") == "1",
		HasSalary:         q.Get("has_salary") == "1",
		PageSize:          validPageSize(q.Get("page_size")),
		SinceSearched:     q.Get("since_searched"),
	}
}

// validPageSize parses the page_size query param, accepting only the offered
// values and defaulting otherwise.
func validPageSize(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return defaultPageSize
	}
	for _, v := range validPageSizes {
		if n == v {
			return n
		}
	}
	return defaultPageSize
}

// softPage parses a 1-indexed page number, clamping bad input to 1.
func softPage(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// isJobID reports whether s looks like a LinkedIn job posting id — a digit
// string of at least 5 characters (real ids are typically 9–11 digits). Used
// to short-circuit the search box to a direct id lookup, since FTS5 doesn't
// index the id column.
func isJobID(s string) bool {
	if len(s) < 5 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// toQueryValues reconstructs url.Values from the form state, mirroring the
// filter form's submit shape. Lets the query/render path run off either a fresh
// GET or a restored saved filter without branching at every call site.
func (f formVals) toQueryValues() url.Values {
	v := url.Values{}
	v.Set("q", f.Q)
	v.Set("company", f.Company)
	v.Set("title", f.Title)
	v.Set("location", f.Location)
	v.Set("status", f.Status)
	v.Set("source", f.Source)
	v.Set("min_salary", f.MinSalary)
	v.Set("salary_currency", f.MinSalaryCurrency)
	v.Set("min_score", f.MinScore)
	v.Set("sort", f.Sort)
	v.Set("page_size", strconv.Itoa(f.PageSizeOrDefault()))
	if f.SinceSearched != "" {
		v.Set("since_searched", f.SinceSearched)
	}
	if f.Remote {
		v.Set("remote", "1")
	}
	if f.Hybrid {
		v.Set("hybrid", "1")
	}
	if f.Onsite {
		v.Set("onsite", "1")
	}
	if f.HasSalary {
		v.Set("has_salary", "1")
	}
	return v
}

// PageSizeOrDefault guards against a zero page size (e.g. from a deserialized
// saved-filter file written before pagination existed) so the pager never
// divides by zero. Exported so the page template can call it directly.
func (f formVals) PageSizeOrDefault() int {
	if f.PageSize <= 0 {
		return defaultPageSize
	}
	return f.PageSize
}

// filterParamKeys are the query params the filter form submits. Used to tell a
// bare "/" visit (restore saved) apart from an explicit Apply submit (persist).
// "page" is intentionally NOT here: pagination clicks are navigation, not a
// filter change, so they shouldn't be persisted as the saved view's page.
var filterParamKeys = []string{
	"q", "company", "title", "location", "status", "source",
	"min_salary", "salary_currency", "min_score", "sort",
	"remote", "hybrid", "onsite", "has_salary", "page_size", "since_searched",
}

// hasFilterParams reports whether the URL carries any filter form params.
func hasFilterParams(q url.Values) bool {
	for _, k := range filterParamKeys {
		if q.Has(k) {
			return true
		}
	}
	return false
}

// defaultFormVals returns the empty filter state (sort defaults to fit score,
// page size to defaultPageSize) used on a fresh visit or after Clear.
func defaultFormVals() formVals {
	return formVals{Sort: "score", PageSize: defaultPageSize}
}

// Section-active checkers report whether a collapsible filter group holds at
// least one non-default value. The sidebar uses these to badge sections that
// contain active filters, so users can see at a glance where their state lives
// without expanding every section.
func (f formVals) SearchActive() bool { return f.Q != "" }

func (f formVals) CompanyLocationActive() bool { return f.Company != "" || f.Location != "" }

func (f formVals) WorkSalaryActive() bool {
	return f.Remote || f.Hybrid || f.Onsite || f.HasSalary ||
		f.MinSalary != "" || f.MinSalaryCurrency != ""
}

func (f formVals) ScorePipelineActive() bool {
	return f.MinScore != "" || f.Status != "" || f.Source != "" || f.SinceSearched != ""
}

func (f formVals) DisplayActive() bool {
	return f.Sort != "score" || f.PageSize != defaultPageSize
}

// filtersFilePath returns the path to the persisted web-filter state, placed
// alongside the SQLite DB so each store carries its own saved view. Returns ""
// when no DB path is known, disabling persistence entirely.
func filtersFilePath(dbPath string) string {
	if dbPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dbPath), "web_filters.json")
}

// loadSavedFilters reads the persisted filter state. A missing or unreadable
// file yields the empty defaults — persistence is best-effort and never fatal.
func (ws *webServer) loadSavedFilters() formVals {
	if ws.filtersPath == "" {
		return defaultFormVals()
	}
	ws.filtersMu.Lock()
	defer ws.filtersMu.Unlock()
	b, err := os.ReadFile(ws.filtersPath)
	if err != nil {
		return defaultFormVals()
	}
	var f formVals
	if err := json.Unmarshal(b, &f); err != nil {
		return defaultFormVals()
	}
	if f.Sort == "" {
		f.Sort = "score"
	}
	if f.PageSize <= 0 {
		f.PageSize = defaultPageSize
	}
	return f
}

// saveFilters persists the filter state alongside the DB. Write failures are
// logged to stderr but never surfaced to the user — the page still renders.
func (ws *webServer) saveFilters(f formVals) {
	if ws.filtersPath == "" {
		return
	}
	ws.filtersMu.Lock()
	defer ws.filtersMu.Unlock()
	b, err := json.Marshal(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "save filters:", err)
		return
	}
	if err := os.WriteFile(ws.filtersPath, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "save filters:", err)
	}
}

// clearSavedFilters removes the persisted state. A no-op if the file is absent
// or persistence is disabled.
func (ws *webServer) clearSavedFilters() {
	if ws.filtersPath == "" {
		return
	}
	ws.filtersMu.Lock()
	defer ws.filtersMu.Unlock()
	_ = os.Remove(ws.filtersPath)
}

type jobView struct {
	ID, Title, Company, Location, URL string
	Salary, SalaryClass               string
	SalaryEstimated                   bool
	Status, Source, Remote            string
	Score, ScoreClass                 string
	ScoreCapped                       bool
	Industry, Seniority, EmpType      string
	CoSize, CoStage, Years            string
	Founding                          string
	ListedDate, FetchedDate           string
	Description                         string
	ShortDescription                   string
	LLMSummary, Summary               string
	CompanyOverview, FitReason, Notes string
	Rubrics                           []rubricView
}

// rubricView is one rubric's evaluated contribution, render-ready for the web
// UI. Stars is the pre-computed 5-char bar (e.g. "★★★★☆") so the template
// needs no arithmetic; Rating/Weight back the "(4/5, w5)" annotation; Reason
// is the optional human note appended after the bar.
type rubricView struct {
	ID     string
	Stars  string
	Rating int
	Weight int
	Reason string
}

func toJobView(j *models.JobPosting) jobView {
	v := jobView{
		ID:              j.ID,
		Title:           j.Title,
		Company:         j.Company,
		Location:        j.Location,
		URL:             j.URL,
		Salary:          j.SalaryDisplay(),
		Status:          j.Status,
		Source:          j.Source,
		Remote:          j.RemoteType,
		Industry:        j.Industry,
		Seniority:       j.Seniority,
		EmpType:         j.EmploymentType,
		CoSize:          j.CompanySizeBand,
		CoStage:          j.CompanyStage,
		Description:      j.Description,
		ShortDescription: j.ShortDescription,
		LLMSummary:      j.LLMSummary,
		Summary:         j.Summary,
		CompanyOverview: j.CompanyOverview,
		FitReason:       j.FitReason,
		Notes:           j.Notes,
	}
	// Salary confidence: description-sourced is authoritative (green); anything
	// else (badge/estimated) is low-confidence (amber + "est." tag).
	if j.HasSalary() {
		if j.IsSalaryEstimated() {
			v.SalaryClass = "low"
			v.SalaryEstimated = true
		} else {
			v.SalaryClass = "high"
		}
	}
	if j.FitScore != nil {
		v.Score = strconv.Itoa(*j.FitScore)
		v.ScoreClass = scoreClass(*j.FitScore)
	}
	v.Rubrics = rubricViews(j)
	v.ScoreCapped = false // caps retired; field kept for template compatibility
	if j.YearsExperience != nil {
		v.Years = strconv.Itoa(*j.YearsExperience) + "+"
	}
	if j.IsFoundingRole {
		v.Founding = "Founding"
	}
	if j.ListedAt > 0 {
		v.ListedDate = time.UnixMilli(j.ListedAt).Format("2006-01-02")
	}
	v.FetchedDate = shortDate(j.FetchedAt)
	return v
}

// scoreClass maps a fit score to its tier slug (high/mid/low). The template
// composes the full class names (e.g. score-badge--high, job--high) and treats
// a missing score as the "none" tier.
func scoreClass(n int) string {
	switch {
	case n >= 70:
		return "high"
	case n >= 40:
		return "mid"
	default:
		return "low"
	}
}

// rubricViews parses the job's persisted RubricScores JSON into render-ready
// per-rubric star bars (id + Stars + rating/weight + reason). Returns nil when
// there's nothing structured to show (legacy jobs scored before the column
// existed, or unparseable payload) so the template falls back to the flat
// FitReason string.
func rubricViews(j *models.JobPosting) []rubricView {
	if j.RubricScores == "" {
		return nil
	}
	var rs []score.RubricScore
	if err := json.Unmarshal([]byte(j.RubricScores), &rs); err != nil || len(rs) == 0 {
		return nil
	}
	out := make([]rubricView, 0, len(rs))
	for _, r := range rs {
		out = append(out, rubricView{
			ID:     r.ID,
			Stars:  render.StarsFor(r.Rating),
			Rating: r.Rating,
			Weight: r.Weight,
			Reason: r.Reason,
		})
	}
	return out
}

// shortDate renders an RFC3339 fetched_at as a short local timestamp.
func shortDate(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Format("2006-01-02 15:04")
}

// softSalary parses a min-salary value ("200k") without exiting on bad input.
func softSalary(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := salary.ParseShorthand(s)
	if err != nil {
		return 0
	}
	return v
}

func softInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// sinceSearchedLayouts are the accepted datetime formats for the "stored since"
// filter, tried in order. The input is interpreted in the user's local time so
// typing "2026-07-03" means local midnight of that day.
var sinceSearchedLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02",
}

// normalizeSinceSearched parses a user-entered datetime floor for the
// searched_at column, accepting a date-only ("2026-07-03"), a space- or
// 'T'-separated datetime ("2026-07-03 00:00:00"), or an RFC3339 value. The
// result is normalized to RFC3339 UTC so a lexicographic string comparison
// against the stored TEXT column matches chronological order. Returns "" on
// empty or unparseable input (the filter is then a no-op).
func normalizeSinceSearched(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	for _, layout := range sinceSearchedLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

type pageData struct {
	Jobs       []jobView
	N          int
	F          formVals
	Mode       string
	Error      string
	CSRF       string
	Pagination pagination
}

// pagination carries everything the template needs to render the pager and the
// "showing X–Y of Z" result count. Page numbers are 1-indexed.
type pagination struct {
	Page     int // current page (1-indexed)
	PageSize int
	Total    int // total matching jobs across all pages
	Pages    int // total page count (>=1)
	From     int // 1-indexed inclusive start of the current page
	To       int // 1-indexed inclusive end of the current page
	HasPrev  bool
	HasNext  bool
	PrevURL  string
	NextURL  string
	Links    []pageLink
}

// pageLink is one entry in the pager. Page==0 means an ellipsis gap. Current
// marks the active page (rendered as non-link).
type pageLink struct {
	Page    int
	URL     string
	Current bool
}

// buildPagination computes the page window, clamps the current page, and
// precomputes the prev/next/page-number links so the template only renders.
func (ws *webServer) buildPagination(f formVals, page, pageSize, total int) pagination {
	pages := 1
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	if page > pages {
		page = pages
	}
	if page < 1 {
		page = 1
	}
	pg := pagination{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
		Pages:    pages,
		HasPrev:  page > 1,
		HasNext:  page < pages,
	}
	if total > 0 {
		pg.From = (page-1)*pageSize + 1
		pg.To = page * pageSize
		if pg.To > total {
			pg.To = total
		}
	}
	if pg.HasPrev {
		pg.PrevURL = ws.pageURL(f, page-1)
	}
	if pg.HasNext {
		pg.NextURL = ws.pageURL(f, page+1)
	}
	pg.Links = ws.pageLinks(f, page, pages)
	return pg
}

// pageLinks returns the page-number entries to render. For modest page counts
// every page is shown; for larger ones a window around the current page is
// shown with ellipses (Page==0) bridging the gaps to the first/last page.
func (ws *webServer) pageLinks(f formVals, page, pages int) []pageLink {
	if pages <= 12 {
		out := make([]pageLink, 0, pages)
		for i := 1; i <= pages; i++ {
			out = append(out, pageLink{Page: i, URL: ws.pageURL(f, i), Current: i == page})
		}
		return out
	}
	want := map[int]bool{}
	add := func(n int) {
		if n >= 1 && n <= pages {
			want[n] = true
		}
	}
	add(1)
	add(pages)
	for i := page - 2; i <= page+2; i++ {
		add(i)
	}
	var nums []int
	for n := range want {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	out := make([]pageLink, 0, len(nums)*2)
	prev := 0
	for _, n := range nums {
		if prev > 0 && n > prev+1 {
			out = append(out, pageLink{Page: 0}) // ellipsis
		}
		out = append(out, pageLink{Page: n, URL: ws.pageURL(f, n), Current: n == page})
		prev = n
	}
	return out
}

// pageURL builds a URL for a given page that preserves the current filter +
// page-size state (page itself is not part of formVals, so it's set here).
func (ws *webServer) pageURL(f formVals, page int) string {
	v := f.toQueryValues()
	v.Set("page", strconv.Itoa(page))
	return "/?" + v.Encode()
}

const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>linkedin-jobs · stored jobs</title>
<meta name="csrf-token" content="{{.CSRF}}">
<style>
  :root {
    --font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", "Inter", Roboto, system-ui, sans-serif;
    --font-mono: ui-monospace, "SF Mono", "JetBrains Mono", "IBM Plex Mono", Menlo, Consolas, monospace;

    --page-bg:      oklch(98.4% 0.003 250);
    --card-bg:      oklch(100% 0 0);
    --card-subtle:  oklch(97.2% 0.004 250);
    --hover-bg:     oklch(96.6% 0.005 250);
    --inset-bg:     oklch(96.8% 0.004 250);

    --ink-1: oklch(22% 0.022 258);
    --ink-2: oklch(43% 0.016 258);
    --ink-3: oklch(52% 0.012 258);
    --ink-4: oklch(63% 0.008 258);

    --line:        oklch(92% 0.005 258);
    --line-strong: oklch(86% 0.009 258);

    --accent:        oklch(52% 0.226 276);
    --accent-hover:  oklch(47% 0.226 276);
    --accent-soft:   oklch(52% 0.226 276 / 0.11);
    --accent-on:     oklch(100% 0 0);

    --score-high:      oklch(56% 0.158 156);
    --score-high-on:   oklch(16% 0.04 156);
    --score-high-soft: oklch(56% 0.158 156 / 0.13);
    --score-high-tint: oklch(56% 0.158 156 / 0.045);

    --score-mid:      oklch(68% 0.150 78);
    --score-mid-on:   oklch(34% 0.085 70);
    --score-mid-soft: oklch(68% 0.150 78 / 0.16);

    --score-low:      oklch(60% 0.162 36);
    --score-low-on:   oklch(36% 0.12 36);
    --score-low-soft: oklch(60% 0.162 36 / 0.13);

    --score-none:      oklch(60% 0.006 258);
    --score-none-on:   oklch(48% 0.008 258);
    --score-none-soft: oklch(60% 0.006 258 / 0.11);

    --status-new:      oklch(54% 0.196 256);
    --status-new-soft: oklch(54% 0.196 256 / 0.14);

    --status-saved:      oklch(58% 0.150 52);
    --status-saved-soft: oklch(58% 0.150 52 / 0.16);

    --status-applied:      oklch(52% 0.110 235);
    --status-applied-soft: oklch(52% 0.110 235 / 0.14);

    --status-rejected:      oklch(56% 0.075 18);
    --status-rejected-soft: oklch(56% 0.075 18 / 0.13);

    --status-filtered:      oklch(52% 0.006 258);
    --status-filtered-soft: oklch(52% 0.006 258 / 0.12);

    --status-viewed:      oklch(50% 0.010 258);
    --status-viewed-soft: oklch(50% 0.010 258 / 0.10);

    --danger:      oklch(55% 0.180 24);
    --danger-soft: oklch(55% 0.180 24 / 0.10);
    --danger-line: oklch(55% 0.180 24 / 0.34);

    --radius-card: 12px;
    --radius-field: 8px;
    --radius-pill: 999px;

    --shadow-card: 0 1px 2px oklch(20% 0.02 258 / 0.04);
    --shadow-high: 0 2px 10px color-mix(in oklch, var(--score-high) 35%, transparent), 0 1px 2px oklch(20% 0.02 258 / 0.05);

    --select-arrow: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 12 12'%3E%3Cpath d='M3 4.6 6 7.6 9 4.6' fill='none' stroke='%23807c92' stroke-width='1.6' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E");
  }

  @media (prefers-color-scheme: dark) {
    :root {
      --page-bg:      oklch(17.5% 0.012 260);
      --card-bg:      oklch(21.5% 0.014 260);
      --card-subtle:  oklch(24.5% 0.016 260);
      --hover-bg:     oklch(26.5% 0.016 260);
      --inset-bg:     oklch(25% 0.014 260);

      --ink-1: oklch(95.5% 0.004 260);
      --ink-2: oklch(76% 0.012 260);
      --ink-3: oklch(66% 0.012 260);
      --ink-4: oklch(52% 0.010 260);

      --line:        oklch(30% 0.014 260);
      --line-strong: oklch(40% 0.017 260);

      --accent:        oklch(70% 0.196 276);
      --accent-hover:  oklch(76% 0.188 276);
      --accent-soft:   oklch(70% 0.196 276 / 0.18);
      --accent-on:     oklch(16% 0.01 260);

      --score-high:      oklch(72% 0.164 156);
      --score-high-on:   oklch(17% 0.04 156);
      --score-high-soft: oklch(72% 0.164 156 / 0.22);
      --score-high-tint: oklch(72% 0.164 156 / 0.085);

      --score-mid:      oklch(80% 0.146 80);
      --score-mid-on:   oklch(24% 0.06 70);
      --score-mid-soft: oklch(80% 0.146 80 / 0.20);

      --score-low:      oklch(72% 0.158 36);
      --score-low-on:   oklch(22% 0.09 36);
      --score-low-soft: oklch(72% 0.158 36 / 0.20);

      --score-none:      oklch(62% 0.006 260);
      --score-none-on:   oklch(80% 0.008 260);
      --score-none-soft: oklch(62% 0.006 260 / 0.18);

      --status-new:      oklch(72% 0.156 256);
      --status-new-soft: oklch(72% 0.156 256 / 0.20);

      --status-saved:      oklch(76% 0.144 58);
      --status-saved-soft: oklch(76% 0.144 58 / 0.20);

      --status-applied:      oklch(70% 0.110 235);
      --status-applied-soft: oklch(70% 0.110 235 / 0.20);

      --status-rejected:      oklch(66% 0.080 20);
      --status-rejected-soft: oklch(66% 0.080 20 / 0.18);

      --status-filtered:      oklch(56% 0.006 260);
      --status-filtered-soft: oklch(56% 0.006 260 / 0.16);

      --status-viewed:      oklch(62% 0.010 260);
      --status-viewed-soft: oklch(62% 0.010 260 / 0.16);

      --danger:      oklch(68% 0.176 22);
      --danger-soft: oklch(68% 0.176 22 / 0.18);
      --danger-line: oklch(68% 0.176 22 / 0.42);

      --shadow-card: 0 1px 2px oklch(0% 0 0 / 0.30);
      --shadow-high: 0 2px 14px color-mix(in oklch, var(--score-high) 40%, transparent), 0 1px 2px oklch(0% 0 0 / 0.35);
    }
  }

  *, *::before, *::after { box-sizing: border-box; }
  html { -webkit-text-size-adjust: 100%; }

  body {
    margin: 0;
    background: var(--page-bg);
    color: var(--ink-1);
    font-family: var(--font-sans);
    font-size: 15px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
    text-rendering: optimizeLegibility;
  }

  a { color: var(--accent); text-decoration: none; }
  a:hover { text-decoration: underline; }

  .wrap {
    max-width: 1180px;
    margin: 0 auto;
    padding: clamp(16px, 3vw, 32px) clamp(14px, 3vw, 28px) 48px;
  }

  /* Header */
  header.app-header {
    display: flex;
    align-items: center;
    gap: 12px;
    padding-bottom: 18px;
    margin-bottom: 18px;
    border-bottom: 1px solid var(--line);
  }
  .brand-mark {
    width: 30px; height: 30px;
    border-radius: 8px;
    flex: 0 0 auto;
    display: block;
    object-fit: contain;
  }
  .app-titles { min-width: 0; }
  .app-title {
    font-family: var(--font-mono);
    font-size: 1.0625rem;
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--ink-1);
    line-height: 1.2;
  }
  .app-subtitle { font-size: 0.8125rem; color: var(--ink-3); line-height: 1.3; }

  /* Two-column layout: sticky filter sidebar + scrolling job list. */
  .layout {
    display: grid;
    grid-template-columns: minmax(240px, 280px) minmax(0, 1fr);
    gap: 24px;
    align-items: start;
  }
  .layout-sidebar {
    position: sticky;
    top: 16px;
    max-height: calc(100vh - 32px);
    overflow-y: auto;
    /* Thin scrollbar so long sidebars don't visually crowd the form. */
    scrollbar-width: thin;
  }
  .layout-main { min-width: 0; }

  /* Filter bar (lives in the sidebar; fields stack vertically). */
  .filters {
    background: var(--card-bg);
    border: 1px solid var(--line);
    border-radius: var(--radius-card);
    padding: 16px;
    box-shadow: var(--shadow-card);
    margin: 6px 0 0;
  }

  /* Collapsible sections (all collapsed by default; <details> keeps it JS-free). */
  details.filter-section { border-top: 1px solid var(--line); }
  details.filter-section:first-of-type { border-top: none; }
  details.filter-section > summary {
    list-style: none; cursor: pointer;
    padding: 10px 2px;
    font-size: 0.75rem; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase;
    color: var(--ink-3);
    display: flex; align-items: center; gap: 7px; user-select: none;
  }
  details.filter-section > summary::-webkit-details-marker { display: none; }
  details.filter-section > summary::before {
    content: ""; width: 0; height: 0;
    border-left: 4px solid var(--ink-4);
    border-top: 4px solid transparent; border-bottom: 4px solid transparent;
    transition: transform .14s;
  }
  details.filter-section[open] > summary::before { transform: rotate(90deg); }
  details.filter-section > summary:hover { color: var(--ink-1); }
  /* Pill badge that flags sections holding active (non-default) values. */
  details.filter-section > summary .filter-section-badge {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 0.625rem; font-weight: 700; letter-spacing: 0;
    text-transform: none;
    background: var(--accent-soft); color: var(--accent);
    padding: 1px 7px; border-radius: 999px;
    line-height: 1.5;
  }
  details.filter-section[open] > summary .filter-section-badge { display: none; }
  .filter-section-body {
    display: flex; flex-direction: column; gap: 12px;
    padding: 2px 2px 14px;
  }
  .filter-section-body .checks { gap: 10px; }

  .field { display: flex; flex-direction: column; gap: 4px; }

  /* In the vertical sidebar, action buttons stretch full width. */
  .filters .actions { margin-left: 0; width: 100%; }
  .filters .actions .btn { flex: 1; text-align: center; }

  .field label {
    font-size: 0.6875rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--ink-3);
    padding-left: 2px;
  }

  input[type="text"], input[type="number"], input[type="search"], select {
    font: inherit;
    color: var(--ink-1);
    background: var(--page-bg);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-field);
    padding: 7px 10px;
    width: 100%;
    transition: border-color .12s, box-shadow .12s, background-color .12s;
  }
  input::placeholder { color: var(--ink-4); }
  input[type="number"] { font-family: var(--font-mono); font-variant-numeric: tabular-nums; }

  select {
    appearance: none;
    -webkit-appearance: none;
    background-image: var(--select-arrow);
    background-repeat: no-repeat;
    background-position: right 8px center;
    background-size: 12px;
    padding-right: 28px;
    cursor: pointer;
  }

  input:focus, select:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-soft);
    background: var(--card-bg);
  }

  .checks { display: flex; gap: 16px; flex-wrap: wrap; align-items: center; }
  .check {
    display: inline-flex; align-items: center; gap: 7px;
    font-size: 0.8125rem; color: var(--ink-2); cursor: pointer; user-select: none;
  }
  .check input { accent-color: var(--accent); width: 15px; height: 15px; cursor: pointer; }

  .actions { display: flex; gap: 8px; margin-left: auto; }

  .btn {
    font: inherit; font-weight: 600; font-size: 0.8125rem;
    border-radius: var(--radius-field);
    padding: 8px 14px;
    cursor: pointer;
    border: 1px solid transparent;
    transition: background-color .12s, border-color .12s, color .12s;
    white-space: nowrap;
    display: inline-block;
  }
  .btn-primary { background: var(--accent); color: var(--accent-on); border-color: var(--accent); }
  .btn-primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); text-decoration: none; }
  .btn-ghost { background: transparent; color: var(--ink-2); border-color: var(--line-strong); }
  .btn-ghost:hover { background: var(--hover-bg); color: var(--ink-1); text-decoration: none; }

  /* Inline notices (search-mode + errors) */
  .notice, .err {
    padding: 8px 12px;
    border-radius: var(--radius-field);
    margin-bottom: 12px;
    font-size: 0.8125rem;
    line-height: 1.45;
  }
  .notice { background: var(--status-saved-soft); border: 1px solid color-mix(in oklch, var(--status-saved) 30%, transparent); color: var(--status-saved); }
  .err { background: var(--danger-soft); border: 1px solid var(--danger-line); color: var(--danger); }
  .err em { color: var(--ink-3); font-style: normal; }

  /* Result count */
  .result-count {
    display: flex; align-items: baseline; gap: 10px;
    padding: 4px 2px 10px;
    font-size: 0.8125rem; color: var(--ink-3);
  }
  .result-count strong { font-family: var(--font-mono); font-size: 0.9375rem; color: var(--ink-1); font-variant-numeric: tabular-nums; }
  .result-count .legend {
    margin-left: auto;
    display: inline-flex; gap: 12px; flex-wrap: wrap;
    font-size: 0.75rem; color: var(--ink-3);
  }
  .result-count .legend span { display: inline-flex; align-items: center; gap: 5px; }
  .result-count .legend i { width: 9px; height: 9px; border-radius: 3px; display: inline-block; }

  /* Job list */
  .job-list { display: flex; flex-direction: column; gap: 12px; }

  .job {
    position: relative;
    background: var(--card-bg);
    border: 1px solid var(--line);
    border-radius: var(--radius-card);
    padding: 16px 18px 14px 20px;
    box-shadow: var(--shadow-card);
    transition: border-color .14s, box-shadow .14s;
  }
  .job:hover { border-color: var(--line-strong); }

  /* HIGH tier: reserved accent treatment */
  .job--high {
    background-color: color-mix(in oklch, var(--card-bg) 93%, var(--score-high));
    border-color: color-mix(in oklch, var(--line-strong) 60%, var(--score-high) 40%);
    box-shadow: var(--shadow-high);
  }
  .job--high::before {
    content: "";
    position: absolute;
    left: 0; top: 0; bottom: 0;
    width: 4px;
    background: var(--score-high);
    border-radius: var(--radius-card) 0 0 var(--radius-card);
  }
  .job--high:hover { box-shadow: 0 4px 16px color-mix(in oklch, var(--score-high) 28%, transparent), var(--shadow-card); }

  /* Filtered: de-emphasize */
  .job--filtered { opacity: 0.62; background: var(--card-subtle); }
  .job--filtered .job-title { color: var(--ink-2); }
  .job--filtered .job-title:hover { color: var(--ink-2); text-decoration: none; }

  /* Card head */
  .job-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 14px; margin-bottom: 6px; }
  .job-head-main { min-width: 0; flex: 1 1 auto; }
  .job-title {
    font-size: 1.0625rem; font-weight: 650; letter-spacing: -0.005em;
    color: var(--ink-1); line-height: 1.25; display: inline-block;
  }
  .job-title:hover { color: var(--accent); }

  /* Fit-score badge */
  .score-badge {
    flex: 0 0 auto;
    display: grid; place-items: center;
    font-family: var(--font-mono); font-weight: 700; line-height: 1;
    border-radius: 10px;
    font-variant-numeric: tabular-nums; letter-spacing: -0.02em;
  }
  .score-badge--high {
    width: 60px; height: 60px; font-size: 1.5rem;
    background: var(--score-high); color: var(--score-high-on);
    box-shadow: 0 1px 0 oklch(100% 0 0 / 0.25) inset, 0 3px 10px color-mix(in oklch, var(--score-high) 45%, transparent);
  }
  .score-badge--mid {
    width: 48px; height: 48px; font-size: 1.1875rem;
    background: var(--score-mid-soft); color: var(--score-mid-on);
    border: 1px solid color-mix(in oklch, var(--score-mid) 45%, transparent);
  }
  .score-badge--low {
    width: 42px; height: 42px; font-size: 1rem;
    background: var(--score-low-soft); color: var(--score-low-on);
    border: 1px solid color-mix(in oklch, var(--score-low) 38%, transparent);
  }
  .score-badge--none {
    width: 42px; height: 42px; font-size: 1.0625rem; font-weight: 600;
    background: var(--score-none-soft); color: var(--score-none-on);
    border: 1px solid var(--line-strong);
  }

  /* Score caption: always-visible fit summary under the badge. Now renders a
     per-rubric star strip ("★★★★★ ★★★★☆ …"); the full labelled breakdown is in
     the expandable <details>. Mono + letter-spacing keep the star glyphs
     evenly spaced; the flat FitReason text fallback still reads fine here. */
  .job-head-aside { flex: 0 0 auto; display: flex; flex-direction: column; align-items: flex-end; gap: 6px; }
   /* Meta line */
  .meta {
    display: flex; flex-wrap: wrap; align-items: center; gap: 6px 8px;
    margin: 2px 0 12px;
    font-size: 0.8125rem; color: var(--ink-2); line-height: 1.4;
  }
  .meta .sep { color: var(--ink-4); user-select: none; }
  .meta .co { font-weight: 650; color: var(--ink-1); }
  .meta .salary { font-family: var(--font-mono); font-variant-numeric: tabular-nums; color: var(--ink-2); font-weight: 600; }
  /* Salary confidence: description-sourced is authoritative (green); badge/estimated is low-confidence (amber). */
  .meta .salary--high { color: var(--score-high); }
  .meta .salary--low { color: var(--score-mid); }
  .meta .salary-tag {
    font-family: var(--font-sans); font-weight: 700; font-size: 0.625rem;
    text-transform: uppercase; letter-spacing: 0.06em; opacity: 0.85;
    padding: 1px 4px; border-radius: var(--radius-pill);
    background: var(--score-mid-soft); color: var(--score-mid-on);
    margin-right: 3px; vertical-align: 1px;
  }
  @media (prefers-color-scheme: dark) {
    .meta .salary--high { color: var(--score-high); }
    .meta .salary--low { color: var(--score-mid); }
  }
  .meta .src {
    font-family: var(--font-mono); font-size: 0.6875rem;
    color: var(--ink-4); text-transform: uppercase; letter-spacing: 0.04em;
  }
  .meta .remote-yes { color: var(--score-high-on); }
  @media (prefers-color-scheme: dark) { .meta .remote-yes { color: var(--score-high); } }

  /* Status pill */
  .status {
    display: inline-flex; align-items: center; gap: 5px;
    font-size: 0.6875rem; font-weight: 600; letter-spacing: 0.02em;
    text-transform: capitalize;
    padding: 2px 8px 2px 7px;
    border-radius: var(--radius-pill);
    line-height: 1.5;
  }
  .status::before {
    content: "";
    width: 6px; height: 6px; border-radius: 50%;
    background: currentColor; flex: 0 0 auto;
  }
  .status--new      { color: var(--status-new);      background: var(--status-new-soft); }
  .status--saved    { color: var(--status-saved);    background: var(--status-saved-soft); }
  .status--applied  { color: var(--status-applied);  background: var(--status-applied-soft); }
  .status--rejected { color: var(--status-rejected); background: var(--status-rejected-soft); }
  .status--filtered { color: var(--status-filtered); background: var(--status-filtered-soft); text-transform: none; }
  .status--viewed   { color: var(--status-viewed);   background: var(--status-viewed-soft); }

  /* Chips */
  .chips { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 12px; }
  .chip {
    display: inline-flex; align-items: center; gap: 4px;
    font-size: 0.6875rem; font-weight: 500; color: var(--ink-2);
    background: var(--inset-bg);
    border: 1px solid var(--line);
    border-radius: var(--radius-pill);
    padding: 3px 9px; line-height: 1.4;
  }
  .chip--founding {
    background: var(--accent); color: var(--accent-on); border-color: var(--accent);
    font-weight: 600; letter-spacing: 0.01em;
  }
  .chip--founding::before {
    content: ""; width: 5px; height: 5px; border-radius: 1px;
    background: var(--accent-on); transform: rotate(45deg);
  }

  /* Actions row */
  .actions-row {
    display: flex; align-items: center; gap: 10px; flex-wrap: wrap;
    padding-top: 10px; border-top: 1px dashed var(--line);
  }
  .actions-row .field { flex-direction: row; align-items: center; gap: 8px; }
  .actions-row .field label { text-transform: none; letter-spacing: 0; font-size: 0.75rem; font-weight: 500; color: var(--ink-3); }
  .actions-row select { width: auto; min-width: 124px; padding: 5px 28px 5px 10px; font-size: 0.8125rem; }
  .actions-row form { margin-left: auto; }
  .btn-delete {
    font: inherit; font-size: 0.8125rem; font-weight: 600;
    color: var(--danger); background: var(--danger-soft);
    border: 1px solid var(--danger-line);
    border-radius: var(--radius-field);
    padding: 5px 12px; cursor: pointer;
    transition: background-color .12s, border-color .12s;
  }
  .btn-delete:hover { background: color-mix(in oklch, var(--danger-soft) 60%, var(--danger) 40%); border-color: var(--danger); }

  .filtered-tag {
    margin-left: auto;
    display: inline-flex; align-items: center; gap: 6px;
    font-size: 0.75rem; color: var(--ink-3); font-family: var(--font-mono);
  }
  .filtered-tag::before { content: ""; width: 6px; height: 6px; border-radius: 50%; background: var(--status-filtered); }

  /* Details */
  .details-grid { margin-top: 10px; display: flex; flex-direction: column; gap: 0; }
  details.job-detail { border-top: 1px solid var(--line); }
  details.job-detail:first-child { border-top: none; }
  details.job-detail > summary {
    list-style: none; cursor: pointer;
    padding: 8px 2px;
    font-size: 0.75rem; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase;
    color: var(--ink-3);
    display: flex; align-items: center; gap: 7px; user-select: none;
  }
  details.job-detail > summary::-webkit-details-marker { display: none; }
  details.job-detail > summary::before {
    content: ""; width: 0; height: 0;
    border-left: 4px solid var(--ink-4);
    border-top: 4px solid transparent; border-bottom: 4px solid transparent;
    transition: transform .14s;
  }
  details.job-detail[open] > summary::before { transform: rotate(90deg); }
  details.job-detail > summary:hover { color: var(--ink-1); }
  details.job-detail > summary em {
    text-transform: none; letter-spacing: 0; font-weight: 400;
    color: var(--ink-4); font-style: normal;
  }
  .detail-body {
    padding: 0 2px 12px;
    font-size: 0.8125rem; color: var(--ink-2); line-height: 1.55;
    white-space: pre-wrap; word-wrap: break-word;
  }
  .fit-reason { color: var(--ink-1); }

  /* Per-rubric star breakdown inside the expandable Fit reason block. Mirrors
     the skill.md format: "<id> <★★★★☆> (4/5) <reason>". The list is one shared
     grid so every row's id column is the same width — stars line up regardless
     of id length. Each .rubric li uses display:contents so its spans flow into
     the shared grid instead of each row sizing its own columns. */
  .rubric-list { list-style: none; margin: 0; padding: 0; white-space: normal; display: grid; grid-template-columns: auto auto 1fr; align-items: baseline; row-gap: 5px; column-gap: 10px; font-size: 0.8125rem; line-height: 1.4; }
  .rubric { display: contents; }
  .rubric-id {
    font-family: var(--font-mono); font-size: 0.75rem; color: var(--ink-2);
    text-transform: lowercase; letter-spacing: 0.01em; white-space: nowrap;
  }
  .rubric-stars {
    font-family: var(--font-mono); letter-spacing: 1px; white-space: nowrap;
    color: var(--score-mid); font-size: 0.85rem;
  }
  @media (prefers-color-scheme: dark) { .rubric-stars { color: var(--score-mid); } }
  .rubric-meta { color: var(--ink-3); font-size: 0.75rem; word-break: break-word; min-width: 0; }
  @media (max-width: 560px) {
    /* Narrow cards: let the reason wrap under the id+stars row. */
    .rubric-list { grid-template-columns: auto 1fr; }
    .rubric-meta { grid-column: 1 / -1; }
  }

  /* Dates line */
  .dates {
    margin-top: 8px; padding-top: 8px; border-top: 1px solid var(--line);
    font-family: var(--font-mono); font-size: 0.6875rem; color: var(--ink-4);
    font-variant-numeric: tabular-nums;
    display: flex; gap: 6px; flex-wrap: wrap;
  }
  .dates .sep { color: var(--line-strong); }

  /* Empty state */
  .empty-state {
    text-align: center; padding: 64px 20px; color: var(--ink-3);
    border: 1px dashed var(--line-strong); border-radius: var(--radius-card);
    background: var(--card-bg);
  }
  .empty-state .empty-title { font-size: 1rem; font-weight: 600; color: var(--ink-2); margin-bottom: 4px; }
  .empty-state .empty-sub { font-size: 0.8125rem; color: var(--ink-4); }
  .empty-state code { font-family: var(--font-mono); font-size: 0.75rem; background: var(--inset-bg); padding: 1px 5px; border-radius: 4px; color: var(--ink-3); }

  /* Pagination */
  .pager {
    display: flex; align-items: center; justify-content: center;
    gap: 4px; flex-wrap: wrap; margin-top: 22px;
  }
  .pager a, .pager span {
    font-family: var(--font-mono); font-size: 0.8125rem; font-variant-numeric: tabular-nums;
    min-width: 34px; height: 34px;
    display: inline-flex; align-items: center; justify-content: center;
    border-radius: var(--radius-field); padding: 0 9px;
  }
  .pager a { color: var(--ink-2); border: 1px solid var(--line); background: var(--card-bg); text-decoration: none; transition: background-color .12s, border-color .12s, color .12s; }
  .pager a:hover { background: var(--hover-bg); border-color: var(--line-strong); color: var(--ink-1); text-decoration: none; }
  .pager .pager-page--current { background: var(--accent); color: var(--accent-on); border: 1px solid var(--accent); font-weight: 700; }
  .pager .pager-gap { color: var(--ink-4); border: 1px solid transparent; background: transparent; }
  .pager .pager-nav { font-weight: 600; }
  .pager .pager-nav--disabled { color: var(--ink-4); opacity: 0.45; border: 1px solid var(--line); background: var(--card-subtle); cursor: default; }

  /* Footer */
  footer.app-footer {
    margin-top: 28px; padding-top: 16px; border-top: 1px solid var(--line);
    font-size: 0.75rem; color: var(--ink-4);
  }
  footer.app-footer code {
    font-family: var(--font-mono); font-size: 0.7rem;
    background: var(--inset-bg); padding: 1px 5px; border-radius: 4px; color: var(--ink-3);
  }

  /* Responsive */
  @media (max-width: 720px) {
    /* Collapse the sidebar layout: filters go back on top as a block. */
    .layout { grid-template-columns: 1fr; gap: 0; }
    .layout-sidebar { position: static; max-height: none; overflow: visible; margin-bottom: 14px; }
    .filters .actions { margin-left: auto; width: auto; }
    .filters .actions .btn { flex: none; }
    .actions { margin-left: 0; width: 100%; }
    .actions .btn { flex: 1; text-align: center; }
    .job { padding: 14px 14px 12px 16px; }
    .job-head { gap: 10px; }
    .score-badge--high { width: 52px; height: 52px; font-size: 1.3125rem; }
    .score-badge--mid { width: 44px; height: 44px; font-size: 1.0625rem; }
    .score-badge--low, .score-badge--none { width: 40px; height: 40px; }
    .actions-row form, .filtered-tag { margin-left: 0; }
    .actions-row .field { flex: 1 1 100%; }
  }
  @media (max-width: 440px) {
    .result-count .legend { display: none; }
    .job-title { font-size: 1rem; }
  }

  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after { transition: none !important; }
  }
</style>
</head>
<body>
  <div class="wrap">

    <header class="app-header">
      <img class="brand-mark" src="/assets/logo.png" alt="linkedin-jobs logo" width="30" height="30" decoding="async">
      <div class="app-titles">
        <div class="app-title">linkedin-jobs</div>
        <div class="app-subtitle">local browser · status &amp; delete editable</div>
      </div>
    </header>

    <div class="layout">
    <aside class="layout-sidebar">
    <form class="filters" method="get" action="/" aria-label="Filters">

      <details class="filter-section">
        <summary>Search{{if .F.SearchActive}}<span class="filter-section-badge">on</span>{{end}}</summary>
        <div class="filter-section-body">
          <div class="field">
            <label for="q">Full-text or job id</label>
            <input type="search" id="q" name="q" value="{{.F.Q}}" placeholder="title, keyword, stack… or paste a job id">
          </div>
        </div>
      </details>

      <details class="filter-section">
        <summary>Company &amp; Location{{if .F.CompanyLocationActive}}<span class="filter-section-badge">on</span>{{end}}</summary>
        <div class="filter-section-body">
          <div class="field">
            <label for="company">Company</label>
            <input type="text" id="company" name="company" value="{{.F.Company}}" placeholder="any">
          </div>
          <div class="field">
            <label for="location">Location</label>
            <input type="text" id="location" name="location" value="{{.F.Location}}" placeholder="any">
          </div>
        </div>
      </details>

      <details class="filter-section">
        <summary>Work &amp; Salary{{if .F.WorkSalaryActive}}<span class="filter-section-badge">on</span>{{end}}</summary>
        <div class="filter-section-body">
          <div class="checks">
            <label class="check"><input type="checkbox" id="remote" name="remote" value="1"{{if .F.Remote}} checked{{end}}> remote</label>
            <label class="check"><input type="checkbox" id="hybrid" name="hybrid" value="1"{{if .F.Hybrid}} checked{{end}}> hybrid</label>
            <label class="check"><input type="checkbox" id="onsite" name="onsite" value="1"{{if .F.Onsite}} checked{{end}}> on-site</label>
            <label class="check"><input type="checkbox" id="has_salary" name="has_salary" value="1"{{if .F.HasSalary}} checked{{end}}> has salary</label>
          </div>
          <div class="field">
            <label for="min_salary">Min salary</label>
            <input type="text" id="min_salary" name="min_salary" value="{{.F.MinSalary}}" placeholder="200k">
          </div>
          <div class="field">
            <label for="salary_currency">Currency</label>
            <select id="salary_currency" name="salary_currency">
              <option value="">raw (any)</option>
              <option value="CAD"{{if eq .F.MinSalaryCurrency "CAD"}} selected{{end}}>CAD</option>
              <option value="USD"{{if eq .F.MinSalaryCurrency "USD"}} selected{{end}}>USD</option>
              <option value="EUR"{{if eq .F.MinSalaryCurrency "EUR"}} selected{{end}}>EUR</option>
              <option value="GBP"{{if eq .F.MinSalaryCurrency "GBP"}} selected{{end}}>GBP</option>
              <option value="AUD"{{if eq .F.MinSalaryCurrency "AUD"}} selected{{end}}>AUD</option>
            </select>
          </div>
        </div>
      </details>

      <details class="filter-section">
        <summary>Score &amp; Pipeline{{if .F.ScorePipelineActive}}<span class="filter-section-badge">on</span>{{end}}</summary>
        <div class="filter-section-body">
          <div class="field">
            <label for="min_score">Min score</label>
            <input type="number" id="min_score" name="min_score" value="{{.F.MinScore}}" placeholder="0–100">
          </div>
          <div class="field">
            <label for="status">Status</label>
            <select id="status" name="status">
              <option value="">any</option>
              <option value="new"{{if eq .F.Status "new"}} selected{{end}}>new</option>
              <option value="viewed"{{if eq .F.Status "viewed"}} selected{{end}}>viewed</option>
              <option value="saved"{{if eq .F.Status "saved"}} selected{{end}}>saved</option>
              <option value="applied"{{if eq .F.Status "applied"}} selected{{end}}>applied</option>
              <option value="rejected"{{if eq .F.Status "rejected"}} selected{{end}}>rejected</option>
              <option value="filtered"{{if eq .F.Status "filtered"}} selected{{end}}>filtered</option>
            </select>
          </div>
          <div class="field">
            <label for="source">Source</label>
            <select id="source" name="source">
              <option value="">any</option>
              <option value="recommended"{{if eq .F.Source "recommended"}} selected{{end}}>recommended</option>
              <option value="search"{{if eq .F.Source "search"}} selected{{end}}>search</option>
            </select>
          </div>
          <div class="field">
            <label for="since_searched">Added since</label>
            <input type="text" id="since_searched" name="since_searched" value="{{.F.SinceSearched}}" placeholder="2026-07-03 00:00:00" title="Only show jobs first stored on or after this date/time">
          </div>
        </div>
      </details>

      <details class="filter-section">
        <summary>Display{{if .F.DisplayActive}}<span class="filter-section-badge">on</span>{{end}}</summary>
        <div class="filter-section-body">
          <div class="field">
            <label for="sort">Sort</label>
            <select id="sort" name="sort">
              <option value="score"{{if eq .F.Sort "score"}} selected{{end}}>fit score</option>
              <option value="searched"{{if eq .F.Sort "searched"}} selected{{end}}>recently searched</option>
              <option value="salary"{{if eq .F.Sort "salary"}} selected{{end}}>salary</option>
            </select>
          </div>
          <div class="field">
            <label for="page_size">Per page</label>
            {{$ps := .F.PageSizeOrDefault}}
            <select id="page_size" name="page_size">
              <option value="20"{{if eq $ps 20}} selected{{end}}>20</option>
              <option value="50"{{if eq $ps 50}} selected{{end}}>50</option>
              <option value="100"{{if eq $ps 100}} selected{{end}}>100</option>
            </select>
          </div>
        </div>
      </details>

      <div class="actions">
        <button class="btn btn-primary" type="submit">Apply</button>
        <a href="/?clear=1" class="btn btn-ghost">Clear</a>
      </div>
    </form>
    </aside>

    <section class="layout-main">
    {{if .Error}}<div class="err">Search error: {{.Error}}<br><em>Tip: wrap multi-word phrases in quotes, e.g. "staff engineer".</em></div>{{end}}
    {{if eq .Mode "search"}}<div class="notice">Showing full-text search results ranked by relevance. Column filters and sort are ignored while searching — clear the search box to filter and sort.</div>{{end}}
    {{if eq .Mode "id"}}<div class="notice">Showing the stored job with id <strong>{{.F.Q}}</strong>. Clear the search box to filter and sort.</div>{{end}}

    <div class="result-count">
      {{if gt .Pagination.Pages 1}}
        Showing <strong>{{.Pagination.From}}–{{.Pagination.To}}</strong> of <strong>{{.N}}</strong> job{{if ne .N 1}}s{{end}}{{if eq .Mode "search"}} matching "{{.F.Q}}"{{else if eq .Mode "id"}} with id "{{.F.Q}}"{{end}}
      {{else}}
        <strong>{{.N}}</strong> job{{if ne .N 1}}s{{end}}{{if eq .Mode "search"}} matching "{{.F.Q}}"{{else if eq .Mode "id"}} with id "{{.F.Q}}"{{end}}
      {{end}}
      <span class="legend" aria-hidden="true">
        <span><i style="background:var(--score-high)"></i>HIGH ≥70</span>
        <span><i style="background:var(--score-mid)"></i>MID 40–69</span>
        <span><i style="background:var(--score-low)"></i>LOW &lt;40</span>
        <span><i style="background:var(--score-none)"></i>unscored</span>
      </span>
    </div>

    {{if not .Jobs}}
      <div class="empty-state">
        <div class="empty-title">No jobs found.</div>
        <div class="empty-sub">{{if not .Error}}Adjust filters or run <code>linkedin-jobs recommended</code> to fetch more.{{end}}</div>
      </div>
    {{end}}

    <main class="job-list">
    {{range .Jobs}}
      <article class="job{{if eq .ScoreClass "high"}} job--high{{end}}{{if eq .Status "filtered"}} job--filtered{{end}}" data-id="{{.ID}}" data-status="{{.Status}}">
        <div class="job-head">
          <div class="job-head-main">
            <a class="job-title" href="{{.URL}}" target="_blank" rel="noopener noreferrer">{{or .Title "(untitled)"}}</a>
          </div>
          <div class="job-head-aside">
            {{if .Score}}<div class="score-badge score-badge--{{.ScoreClass}}" aria-label="Fit score {{.Score}} of 100">{{.Score}}</div>{{else}}<div class="score-badge score-badge--none" aria-label="Unscored">—</div>{{end}}
          </div>
        </div>
        <div class="meta">
          <span class="co">{{or .Company "—"}}</span>
          {{if .Location}}<span class="sep">·</span> <span>{{.Location}}</span>{{end}}
          {{if .Salary}}<span class="sep">·</span> <span class="salary salary--{{.SalaryClass}}"{{if .SalaryEstimated}} title="Estimated — sourced from the page badge, not the job description"{{end}}>{{if .SalaryEstimated}}<span class="salary-tag">est.</span> {{end}}{{.Salary}}</span>{{end}}
          {{if .Remote}}<span class="sep">·</span> <span class="remote-yes">{{.Remote}}</span>{{end}}
          {{if .Status}}<span class="sep">·</span> <span class="status status--{{.Status}} js-status">{{.Status}}</span>{{end}}
          {{if .Source}}<span class="sep">·</span> <span class="src">{{.Source}}</span>{{end}}
        </div>
        <div class="chips">
          {{if .Industry}}<span class="chip">{{.Industry}}</span>{{end}}
          {{if .Seniority}}<span class="chip">{{.Seniority}}</span>{{end}}
          {{if .EmpType}}<span class="chip">{{.EmpType}}</span>{{end}}
          {{if .Years}}<span class="chip">{{.Years}} yrs</span>{{end}}
          {{if .CoSize}}<span class="chip">{{.CoSize}}</span>{{end}}
          {{if .CoStage}}<span class="chip">{{.CoStage}}</span>{{end}}
          {{if .Founding}}<span class="chip chip--founding">Founding</span>{{end}}
        </div>
        <div class="actions-row">
          {{if eq .Status "filtered"}}
            <span class="filtered-tag">filtered (auto)</span>
          {{else}}
            <div class="field">
              <label for="status-{{.ID}}">Status</label>
              <select id="status-{{.ID}}" class="js-status-select">
                {{$s := .Status}}
                <option value="new"{{if eq $s "new"}} selected{{end}}>new</option>
                <option value="viewed"{{if eq $s "viewed"}} selected{{end}}>viewed</option>
                <option value="saved"{{if eq $s "saved"}} selected{{end}}>saved</option>
                <option value="applied"{{if eq $s "applied"}} selected{{end}}>applied</option>
                <option value="rejected"{{if eq $s "rejected"}} selected{{end}}>rejected</option>
              </select>
            </div>
          {{end}}
          <form class="js-delete-form" method="post" action="/jobs/{{.ID}}/delete">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <button type="submit" class="btn-delete js-delete" title="Delete this job permanently">Delete</button>
          </form>
        </div>
        {{if or .LLMSummary .Summary .Description .CompanyOverview .Rubrics .FitReason .Notes}}
        <div class="details-grid">
          {{if .LLMSummary}}
          <details class="job-detail"><summary>Summary</summary><div class="detail-body">{{.LLMSummary}}</div></details>
          {{else if .Summary}}
          <details class="job-detail"><summary>Summary (extractive)</summary><div class="detail-body">{{.Summary}}</div></details>
          {{end}}
          {{if or .ShortDescription .Description}}
          <details class="job-detail"><summary>Description</summary><div class="detail-body">{{if .ShortDescription}}{{.ShortDescription}}{{else}}{{.Description}}{{end}}</div></details>
          {{end}}
          {{if .CompanyOverview}}
          <details class="job-detail"><summary>Company overview</summary><div class="detail-body">{{.CompanyOverview}}</div></details>
          {{end}}
          {{if .Rubrics}}
          <details class="job-detail"><summary>Fit reason</summary>
            <div class="detail-body fit-reason">
              <ul class="rubric-list">
                {{range .Rubrics}}
                <li class="rubric">
                  <span class="rubric-id">{{.ID}}</span>
                  <span class="rubric-stars" title="{{.Rating}} of 5">{{.Stars}}</span>
                  <span class="rubric-meta">{{.Reason}}</span>
                </li>
                {{end}}
              </ul>
            </div>
          </details>
          {{else if .FitReason}}
          <details class="job-detail"><summary>Fit reason</summary><div class="detail-body fit-reason">{{.FitReason}}</div></details>
          {{end}}
          {{if .Notes}}
          <details class="job-detail"><summary>Notes</summary><div class="detail-body">{{.Notes}}</div></details>
          {{end}}
        </div>
        {{end}}
        {{if or .ListedDate .FetchedDate}}
        <div class="dates">{{if .ListedDate}}<span>listed {{.ListedDate}}</span>{{if .FetchedDate}}<span class="sep">·</span>{{end}}{{end}}{{if .FetchedDate}}<span>fetched {{.FetchedDate}}</span>{{end}}</div>
        {{end}}
      </article>
    {{end}}
    </main>

    {{if gt .Pagination.Pages 1}}
    <nav class="pager" aria-label="Pagination">
      {{if .Pagination.HasPrev}}<a class="pager-nav" href="{{.Pagination.PrevURL}}" rel="prev">‹ Prev</a>{{else}}<span class="pager-nav pager-nav--disabled" aria-disabled="true">‹ Prev</span>{{end}}
      {{range .Pagination.Links}}{{if eq .Page 0}}<span class="pager-gap" aria-hidden="true">…</span>{{else if .Current}}<span class="pager-page pager-page--current" aria-current="page">{{.Page}}</span>{{else}}<a class="pager-page" href="{{.URL}}">{{.Page}}</a>{{end}}{{end}}
      {{if .Pagination.HasNext}}<a class="pager-nav" href="{{.Pagination.NextURL}}" rel="next">Next ›</a>{{else}}<span class="pager-nav pager-nav--disabled" aria-disabled="true">Next ›</span>{{end}}
    </nav>
    {{end}}

    </section>
    </div>

    <footer class="app-footer">
      Status &amp; delete editable · everything else read-only · <code>linkedin-jobs serve</code> · localhost only
    </footer>

  </div>
<script>
(function(){
  const meta = document.querySelector('meta[name=csrf-token]');
  const csrf = meta ? meta.content : '';
  async function post(path, params){
    const body = new URLSearchParams(Object.assign({csrf:csrf}, params||{}));
    return fetch(path, {method:'POST', headers:{'X-CSRF-Token':csrf, 'Accept':'application/json'}, body:body});
  }
  function setStatusUI(article, status){
    article.dataset.status = status;
    const sel = article.querySelector('select.js-status-select');
    if (sel) sel.value = status;
    const pill = article.querySelector('.js-status');
    if (pill) {
      pill.textContent = status;
      pill.className = 'status status--' + status + ' js-status';
    }
  }
  document.addEventListener('click', (e)=>{
    const a = e.target.closest('.job-head a');
    if (!a) return;
    const article = a.closest('article.job');
    if (!article || article.dataset.status !== 'new') return;
    post('/jobs/'+encodeURIComponent(article.dataset.id)+'/view');
    setStatusUI(article, 'viewed');
  });
  document.addEventListener('change', async (e)=>{
    const sel = e.target.closest('select.js-status-select');
    if (!sel) return;
    const article = sel.closest('article.job');
    const prev = article.dataset.status;
    const val = sel.value;
    const res = await post('/jobs/'+encodeURIComponent(article.dataset.id)+'/status', {status:val});
    if (!res.ok){ sel.value = prev; alert('Could not save status.'); return; }
    setStatusUI(article, val);
  });
  document.addEventListener('click', async (e)=>{
    const btn = e.target.closest('button.js-delete');
    if (!btn) return;
    e.preventDefault();
    if (!confirm('Delete this job permanently? This cannot be undone.')) return;
    const article = btn.closest('article.job');
    const res = await post('/jobs/'+encodeURIComponent(article.dataset.id)+'/delete');
    if (!res.ok){ alert('Could not delete.'); return; }
    article.remove();
  });
})();
</script>
</body>
</html>`
