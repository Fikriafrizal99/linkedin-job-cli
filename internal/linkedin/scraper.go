package linkedin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/parser"
	"linkedin-jobs/internal/salary"
	"linkedin-jobs/internal/store"
)

var guestSearchURL = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"

var jobIDRE = regexp.MustCompile(`jobPosting:(\d+)`)

// SearchParams holds the filterable parameters for an anonymous job search.
type SearchParams struct {
	Keywords     string
	Location     string
	WorkType     string // "" | "1"(onsite) | "2"(remote) | "3"(hybrid); comma-separated for OR
	PostedWithin string // "" or a LinkedIn f_TPR value, e.g. "r604800" (past 7 days)

	// MaxJobs caps unique jobs returned. 0 means no explicit cap.
	MaxJobs int

	// Pages is retained for backwards compatibility with existing callers.
	// When > 0, it caps the number of HTTP pages. Pagination itself is adaptive
	// and advances by the actual number of cards LinkedIn returns.
	Pages int
}

// Search runs an anonymous job search and returns parsed job cards (no
// salary/description — call FetchDetail for those).
func (c *Client) Search(p SearchParams) ([]*models.JobPosting, error) {
	var out []*models.JobPosting
	seen := map[string]bool{}

	for start, page := 0, 0; ; page++ {
		if p.Pages > 0 && page >= p.Pages {
			break
		}
		if p.MaxJobs > 0 && len(out) >= p.MaxJobs {
			break
		}

		u := guestSearchURL + "?keywords=" + urlEncode(p.Keywords) + "&location=" + urlEncode(p.Location)
		if p.WorkType != "" {
			u += "&f_WT=" + p.WorkType
		}
		if p.PostedWithin != "" {
			u += "&f_TPR=" + p.PostedWithin
		}
		if start > 0 {
			u += "&start=" + itoa(start)
		}

		html, _, status, err := c.get(u, false, nil)
		if err != nil {
			return out, err
		}
		if status != 200 || strings.TrimSpace(html) == "" {
			break
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			return out, err
		}
		cards := doc.Find("div[data-entity-urn]")
		if cards.Length() == 0 {
			break
		}

		added := 0
		cards.EachWithBreak(func(_ int, sel *goquery.Selection) bool {
			j := parseCard(sel)
			if j == nil || seen[j.ID] {
				return true
			}
			seen[j.ID] = true
			out = append(out, j)
			added++
			return p.MaxJobs <= 0 || len(out) < p.MaxJobs
		})

		// LinkedIn's guest endpoint does not guarantee a fixed page size.
		// Advance by what it actually returned instead of assuming 25 cards.
		start += cards.Length()

		// A page with no new IDs means we are repeating/exhausted.
		if added == 0 {
			break
		}
	}
	return out, nil
}

// SearchURL extracts job postings from an arbitrary LinkedIn search/collection
// URL (e.g. a job-alert email link, a saved-search URL, or a URL pasted from
// the browser). Strategy, in priority order:
//
//  1. URL has a `keywords` query param — replay its filters against the
//     paginated guest seeMoreJobPostings API (the same XHR the browser fires
//     when scrolling /jobs/search/). When a session is available the call is
//     authenticated, exposing the full result set the signed-in browser sees;
//     anonymous it still paginates but over a smaller total.
//     geoId/distance/f_TPR filters from the URL are preserved.
//  2. URL carries explicit job IDs (originToLandingJobPostings from a job-alert
//     email, or currentJobId) and NO keywords — those IDs are used directly.
//  3. Otherwise, fetch the URL HTML and parse cards via the same selectors as
//     Search.
//
// Cards returned here carry only id/title/company/location (cases 1 and 3) or
// just id with title "Unknown Title" (case 2); FetchDetail fills the rest.
//
// Stray backslashes are stripped first: inside single quotes shells preserve
// `\?` `\=` `\&` literally (no escaping happens), so an over-escaped paste
// ('https://…/\?a\=1\&b\=2') would otherwise leave query keys with trailing
// backslashes and silently match nothing. LinkedIn URLs never contain a
// literal backslash, so the strip is always safe.
func (c *Client) SearchURL(rawURL string, top int) ([]*models.JobPosting, error) {
	rawURL = strings.ReplaceAll(rawURL, "\\", "")
	if u, err := url.Parse(rawURL); err == nil && u.Query().Has("keywords") {
		// Replay the URL's filters against the paginated guest seeMoreJobPostings
		// endpoint (the same XHR the browser fires when scrolling /jobs/search/).
		// When a session is available the call is made authenticated, which
		// exposes the full result set the signed-in browser sees; anonymous it
		// still paginates but returns a smaller total. jobsFromSearchURL steps
		// `start` by the page size LinkedIn actually returns (see note there).
		return c.jobsFromSearchURL(rawURL, top)
	}
	if ids := jobIDsFromURL(rawURL); len(ids) > 0 {
		return jobsFromIDs(ids, "url"), nil
	}
	return c.jobsFromHTMLPage(rawURL)
}

// jobsFromSearchURL paginates through a LinkedIn job search by replaying the
// URL's query params against the guest seeMoreJobPostings endpoint — the same
// XHR the browser fires when you scroll the left panel of /jobs/search/.
// maxJobs <= 0 means pull until the result set is exhausted.
//
// When a session is available the call is made authenticated, which returns
// the full result set the signed-in browser sees; anonymous it still paginates
// but over a smaller total.
//
// IMPORTANT page-size handling: seeMoreJobPostings currently returns ~10 cards
// per page (not 25). We therefore step `start` by the number of cards actually
// returned and stop only when a page yields no NEW job (all duplicates or
// empty), which is the true exhaustion signal. An earlier version assumed
// 25/page and broke on `cards < 25`, which ended pagination after the first
// 10-card page for every query — the root cause of the "only 10 jobs" bug.
//
// Tracking/pinning params (currentJobId, originToLandingJobPostings, trk, …)
// are stripped; everything else (keywords, geoId, distance, f_TPR, f_WT, …) is
// passed through so the user's filters are honored.
func (c *Client) jobsFromSearchURL(rawURL string, maxJobs int) ([]*models.JobPosting, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for _, k := range []string{
		"currentJobId", "origin", "originToLandingJobPostings",
		"sortBy", "trk", "sessionId", "lipi", "refId",
	} {
		q.Del(k)
	}
	var out []*models.JobPosting
	seen := map[string]bool{}
	authed := c.HasSession()
	for start := 0; maxJobs <= 0 || len(out) < maxJobs; {
		q.Set("start", itoa(start))
		apiURL := guestSearchURL + "?" + q.Encode()
		html, _, status, err := c.get(apiURL, authed, nil)
		if err != nil {
			return out, err
		}
		if status != 200 || strings.TrimSpace(html) == "" {
			break
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			return out, err
		}
		cards := doc.Find("div[data-entity-urn]")
		if cards.Length() == 0 {
			break
		}
		added := 0
		cards.Each(func(_ int, s *goquery.Selection) {
			j := parseCard(s)
			if j == nil || seen[j.ID] {
				return
			}
			seen[j.ID] = true
			j.Source = "url"
			out = append(out, j)
			added++
		})
		if added == 0 {
			break // page returned only already-seen jobs → result set exhausted
		}
		start += cards.Length() // adaptive step: matches LinkedIn's real page size
	}
	return out, nil
}

// jobIDsFromURL extracts job IDs from a LinkedIn URL's query params. Prefers
// originToLandingJobPostings (the full list from job-alert emails); falls back
// to currentJobId (a single job). Returns nil when neither is present.
func jobIDsFromURL(rawURL string) []string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	q := u.Query()
	seen := map[string]bool{}
	var ids []string
	if list := q.Get("originToLandingJobPostings"); list != "" {
		for _, id := range strings.Split(list, ",") {
			id = strings.TrimSpace(id)
			if id != "" && isDigits(id) && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return ids
		}
	}
	if id := strings.TrimSpace(q.Get("currentJobId")); id != "" && isDigits(id) {
		ids = append(ids, id)
	}
	return ids
}

// jobsFromIDs builds skeleton JobPosting records from a list of LinkedIn job
// IDs. Title/company/location are filled later by FetchDetail; only the ID +
// canonical view URL are known at this stage.
func jobsFromIDs(ids []string, source string) []*models.JobPosting {
	out := make([]*models.JobPosting, 0, len(ids))
	for _, id := range ids {
		out = append(out, &models.JobPosting{
			ID:         id,
			URL:        "https://www.linkedin.com/jobs/view/" + id + "/",
			Title:      "Unknown Title",
			Source:     source,
			SearchedAt: store.NowISO(),
		})
	}
	return out
}

// jobsFromHTMLPage fetches a LinkedIn search/collection URL and parses job
// cards from the HTML. Uses the same selectors as the guest Search path. Tries
// an authenticated GET when a session is available (LinkedIn returns more
// complete results to signed-in users), then falls back to anonymous.
func (c *Client) jobsFromHTMLPage(rawURL string) ([]*models.JobPosting, error) {
	authed := c.HasSession()
	html, _, status, err := c.get(rawURL, authed, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, errf("page fetch returned status %d for %s", status, rawURL)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var out []*models.JobPosting
	seen := map[string]bool{}
	cards := doc.Find("div[data-entity-urn]")
	cards.Each(func(_ int, s *goquery.Selection) {
		j := parseCard(s)
		if j == nil || seen[j.ID] {
			return
		}
		seen[j.ID] = true
		j.Source = "url"
		out = append(out, j)
	})
	return out, nil
}

// isDigits reports whether s is a non-empty string of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseCard(s *goquery.Selection) *models.JobPosting {
	return parseCardAt(s, time.Now().UTC())
}

func parseCardAt(s *goquery.Selection, base time.Time) *models.JobPosting {
	urn, _ := s.Attr("data-entity-urn")
	m := jobIDRE.FindStringSubmatch(urn)
	if m == nil {
		return nil
	}
	base = base.UTC()
	now := base.Format(time.RFC3339)
	j := &models.JobPosting{
		ID: m[1], SearchedAt: now, Source: "search",
		FirstSeen: now, LastSeen: now, ScrapedAt: now,
	}
	if t := s.Find(".base-search-card__title").First(); t.Length() > 0 {
		j.Title = strings.TrimSpace(t.Text())
	}
	if j.Title == "" {
		j.Title = "Unknown Title"
	}
	if co := s.Find(".base-search-card__subtitle a").First(); co.Length() > 0 {
		j.Company = strings.TrimSpace(co.Text())
	} else if co := s.Find(".base-search-card__subtitle").First(); co.Length() > 0 {
		j.Company = strings.TrimSpace(co.Text())
	}
	if loc := s.Find(".job-search-card__location").First(); loc.Length() > 0 {
		j.Location = strings.TrimSpace(loc.Text())
	}
	if tm := s.Find("time").First(); tm.Length() > 0 {
		if dt, ok := tm.Attr("datetime"); ok && strings.TrimSpace(dt) != "" {
			j.PostedAt = strings.TrimSpace(dt)
			j.PostedAtEstimated = false
		} else if estimated, ok := estimatePostedAtFromText(tm.Text(), base); ok {
			j.PostedAt = estimated
			j.PostedAtEstimated = true
		}
	}
	if link := s.Find("a.base-card__full-link").First(); link.Length() > 0 {
		if href, ok := link.Attr("href"); ok {
			j.URL = cleanURL(strings.TrimSpace(href))
		}
	}
	if j.URL == "" {
		return nil
	}
	return j
}

var relativePostedRE = regexp.MustCompile("(?i)(?:posted|reposted)?\\s*(\\d+)\\s+(minute|hour|day|week|month|year)s?\\s+ago")

// estimatePostedAtFromText converts LinkedIn's relative posting labels into an
// approximate UTC timestamp. Callers must mark the result as estimated; exact
// JSON-LD/date-time values always take precedence when available.
func estimatePostedAtFromText(raw string, base time.Time) (string, bool) {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "" {
		return "", false
	}
	base = base.UTC()
	if strings.Contains(text, "just now") || strings.Contains(text, "moments ago") {
		return base.Format(time.RFC3339), true
	}
	m := relativePostedRE.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 0 {
		return "", false
	}
	var estimated time.Time
	switch strings.ToLower(m[2]) {
	case "minute":
		estimated = base.Add(-time.Duration(n) * time.Minute)
	case "hour":
		estimated = base.Add(-time.Duration(n) * time.Hour)
	case "day":
		estimated = base.AddDate(0, 0, -n)
	case "week":
		estimated = base.AddDate(0, 0, -7*n)
	case "month":
		estimated = base.AddDate(0, -n, 0)
	case "year":
		estimated = base.AddDate(-n, 0, 0)
	default:
		return "", false
	}
	return estimated.UTC().Format(time.RFC3339), true
}

// relativePostedText returns the first detail-page text that looks like a
// relative posted-age label. It is used only when no exact posting date exists.
func relativePostedText(doc *goquery.Document) string {
	selectors := []string{"time", ".posted-time-ago__text", ".topcard__flavor--metadata"}
	for _, selector := range selectors {
		found := ""
		doc.Find(selector).EachWithBreak(func(_ int, sel *goquery.Selection) bool {
			text := strings.TrimSpace(sel.Text())
			lower := strings.ToLower(text)
			if relativePostedRE.MatchString(text) ||
				strings.Contains(lower, "just now") ||
				strings.Contains(lower, "moments ago") {
				found = text
				return false
			}
			return true
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// FetchDetail fetches a job's detail page and fills salary + description.
func (c *Client) FetchDetail(j *models.JobPosting) error {
	html, _, status, err := c.get(j.URL, false, nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return errf("detail fetch returned status %d for %s", status, j.URL)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return err
	}

	// 1. Salary badge (HTML). LinkedIn's badge is a low-confidence, frequently
	// rounded/generic band that the description body may not corroborate. Its
	// currency is always captured so the description-body parser (step 3) can
	// inherit the locale when a labeled bare-$ range appears. The badge range
	// itself is retained as a fallback: when the description has no salary, we
	// surface the badge figure tagged "badge" so the UI shows an "est. salary"
	// rather than nothing.
	//
	// The lookup is scoped to the main job's own region: a detail page also
	// renders "Similar jobs" / "People also viewed" sidebars whose cards reuse
	// the same `.main-job-card__salary-info` class, and naively grabbing the
	// first match on the page misattributes a *different* posting's salary to
	// the job being viewed. Matches inside a `.similar-jobs` section or any
	// `.aside-section-container` sidebar are therefore skipped.
	badgeCurrency, badgeSal := extractSalaryBadge(doc)

	// 2. JSON-LD JobPosting — source for description AND any missing card
	// metadata (title/company/location). Cards from the listing page already
	// carry these, but jobs built from a bare ID (e.g. via the `url` command's
	// originToLandingJobPostings path, or the `job` command) only have an
	// ID + view URL. LinkedIn now ships the detail page as a React SPA that
	// frequently omits the JobPosting JSON-LD block, so when JSON-LD misses we
	// fall back to the rendered topcard__ elements for title/company/location.
	meta := extractJobMeta(doc)
	if meta.Description != "" {
		j.Description = meta.Description
	} else {
		// HTML fallbacks for pages where LinkedIn ships no JobPosting JSON-LD.
		j.Description = extractDescriptionHTML(doc)
	}
	// topcard__ fallback for title/company/location when JSON-LD gave nothing.
	tc := extractTopCardMeta(doc)
	if meta.Title == "" {
		meta.Title = tc.Title
	}
	if meta.Company == "" {
		meta.Company = tc.Company
	}
	if meta.Location == "" {
		meta.Location = tc.Location
	}
	if (j.Title == "" || j.Title == "Unknown Title") && meta.Title != "" {
		j.Title = meta.Title
	}
	if j.Company == "" && meta.Company != "" {
		j.Company = meta.Company
	}
	if j.Location == "" && meta.Location != "" {
		j.Location = meta.Location
	}
	if meta.DatePosted != "" {
		j.PostedAt = meta.DatePosted
		j.PostedAtEstimated = false
	} else {
		exactFound := false
		if tm := doc.Find("time").First(); tm.Length() > 0 {
			if dt, ok := tm.Attr("datetime"); ok && strings.TrimSpace(dt) != "" {
				j.PostedAt = strings.TrimSpace(dt)
				j.PostedAtEstimated = false
				exactFound = true
			}
		}
		if !exactFound && j.PostedAt == "" {
			if estimated, ok := estimatePostedAtFromText(relativePostedText(doc), time.Now().UTC()); ok {
				j.PostedAt = estimated
				j.PostedAtEstimated = true
			}
		}
	}

	// 2b. LinkedIn's detail page is now a React Server Components SPA: the
	// initial HTML often omits the description body, and the guest page omits
	// the workplace type (Remote/Hybrid/On-site) entirely. DetectRemote gives a
	// best-effort guess from prose, but it false-positives on listings whose
	// description merely mentions "hybrid" in passing. The authenticated
	// Voyager jobPostings API returns the authoritative workplaceTypes badge,
	// so when a CSRF-bearing session is available we call it and let its
	// remoteType override the heuristic; it also recovers the description body
	// when the SPA shell shipped none.
	j.RemoteType = DetectRemote(j.Location + " " + j.Description)
	if c.HasSession() {
		desc, rt, err := c.fetchJobPostingViaAPI(j.ID)
		if err == nil {
			if strings.TrimSpace(j.Description) == "" && desc != "" {
				j.Description = desc
				// Only re-run the heuristic if the API exposed no workplace
				// type — otherwise the authoritative badge wins below.
				if rt == "" {
					j.RemoteType = DetectRemote(j.Location + " " + j.Description)
				}
			}
			// Authoritative workplace badge overrides DetectRemote's guess.
			if rt != "" {
				j.RemoteType = rt
			}
		}
	}

	// Normalize one final time after all description sources (HTML, JSON-LD,
	// authenticated API) have had a chance to fill the body.
	j.Description = cleanHTMLText(j.Description)

	// 3. Salary resolution. Description-body salary is authoritative: it carries
	// the localized band + currency actually posted by the employer, so it is
	// marked high-confidence ("description"). A bare "$lo - $hi" range
	// introduced by a "Salary:" label is also accepted, inheriting the badge's
	// currency so we don't lose the locale signal. When the description has no
	// salary, fall back to the page-chrome badge range tagged "badge" —
	// low-confidence but better than nothing (the UI renders it as "est.").
	if descSal := salary.InDescriptionWithDefault(j.Description, badgeCurrency); descSal != nil {
		j.SalaryRaw = descSal.Raw
		j.SalaryLow = descSal.Low
		j.SalaryHigh = descSal.High
		j.SalaryCurrency = descSal.Currency
		j.SalarySource = models.SalarySourceDescription
	} else if badgeSal != nil {
		j.SalaryRaw = badgeSal.Raw
		j.SalaryLow = badgeSal.Low
		j.SalaryHigh = badgeSal.High
		j.SalaryCurrency = badgeSal.Currency
		j.SalarySource = models.SalarySourceBadge
	}

	app := parser.ExtractApplicationData(j.Description)
	resolvedApply := resolveApplicationData(app, extractApplyControl(doc))
	j.ApplyEmails = resolvedApply.Emails
	j.ApplyEmail = resolvedApply.PrimaryEmail
	j.ApplyURL = resolvedApply.ApplyURL
	j.ApplicationMethod = resolvedApply.Method
	j.ApplicationInstruction = resolvedApply.Instruction
	if strings.TrimSpace(j.Description) == "" {
		j.DetailStatus = "DETAIL_INCOMPLETE"
	} else {
		j.DetailStatus = "DETAIL_COMPLETE"
	}
	now := store.NowISO()
	j.FetchedAt = now
	j.ScrapedAt = now
	j.LastSeen = now
	if j.FirstSeen == "" {
		j.FirstSeen = j.SearchedAt
		if j.FirstSeen == "" {
			j.FirstSeen = now
		}
	}
	return nil
}

// similarJobsAncestor matches any container that renders *other* postings on a
// detail page — the "Similar jobs" rail and the "People also viewed" /
// "Similar searches" aside sidebars. Salary badges inside these belong to a
// different job and must not be attributed to the one being viewed.
const similarJobsAncestor = ".similar-jobs, .aside-section-container, .people-also-viewed"

// salaryBadgeSelectors lists the LinkedIn salary-badge selectors in priority
// order. The "Base pay range" compensation section (.compensation__salary) is
// the prominent, structured figure LinkedIn renders in the job header; many
// modern postings carry it and no main-region .main-job-card__salary-info (the
// only such spans on the page belong to the similar-jobs rail). The legacy
// .main-job-card__salary-info badge is kept as a fallback for older postings.
var salaryBadgeSelectors = []string{
	".compensation__salary",
	".main-job-card__salary-info",
}

// extractSalaryBadge reads the salary badge scoped to the main job's own
// region. It returns the parsed badge (and its currency) or nil when no badge
// of its own is found. See FetchDetail's step-1 comment for why the scope
// excludes the similar-jobs/aside sidebars. Selectors are tried in priority
// order; the first one whose (non-sidebar) text parses to a salary wins.
func extractSalaryBadge(doc *goquery.Document) (currency string, sal *salary.Salary) {
	for _, sel := range salaryBadgeSelectors {
		t := firstScopedSalaryText(doc, sel)
		if t == "" {
			continue
		}
		parsed := salary.Parse(t)
		if parsed == nil {
			continue
		}
		return parsed.Currency, parsed
	}
	return "", nil
}

// firstScopedSalaryText returns the trimmed text of the first element matching
// sel that is NOT inside a similar-jobs/aside sidebar. Among matches it prefers
// one whose text carries a currency signal ($, CAD, USD); otherwise it accepts
// the first non-empty one.
func firstScopedSalaryText(doc *goquery.Document, sel string) string {
	var salaryText string
	doc.Find(sel).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if s.Closest(similarJobsAncestor).Length() > 0 {
			return true
		}
		t := strings.TrimSpace(s.Text())
		if t == "" {
			return true
		}
		upper := strings.ToUpper(t)
		if strings.Contains(t, "$") || strings.Contains(upper, "CAD") || strings.Contains(upper, "USD") {
			salaryText = t
			return false
		}
		if salaryText == "" {
			salaryText = t
		}
		return true
	})
	return salaryText
}

// extractDescription pulls the job description body, trying JSON-LD first
// (preferred — structured and clean) and falling back to rendered HTML
// containers when the page serves no JobPosting JSON-LD. FetchDetail calls
// extractJobMeta + extractDescriptionHTML directly so it can also pick up
// title/company/location; this wrapper is kept for tests and other callers
// that only need the description.
func extractDescription(doc *goquery.Document) string {
	if m := extractJobMeta(doc); m.Description != "" {
		return m.Description
	}
	return extractDescriptionHTML(doc)
}

// extractDescriptionHTML pulls the job description body from rendered HTML
// containers. Used as a fallback when the page ships no JobPosting JSON-LD
// (the JSON-LD path lives in extractJobMeta, which the caller already tried).
func extractDescriptionHTML(doc *goquery.Document) string {
	// HTML fallbacks: order matters; the most specific selector first.
	for _, sel := range []string{
		".description__text .show-more-less-html__markup",
		".description__text",
		".jobs-description__content",
		".jobs-description-content",
		".jobs-box__html-content",
	} {
		if t := strings.TrimSpace(doc.Find(sel).First().Text()); t != "" {
			return cleanHTMLText(t)
		}
	}
	return ""
}

// applicantBulletRE matches the "N applicants" topcard bullet so the location
// fallback can skip it and pick the geography bullet instead.
var applicantBulletRE = regexp.MustCompile(`(?i)\d+\s+applicant`)

// extractTopCardMeta pulls title/company/location from the rendered topcard__
// elements on a LinkedIn job detail page. Used as a fallback when the page
// ships no JobPosting JSON-LD (LinkedIn's detail page is now a React SPA, so
// the initial HTML frequently lacks the JSON-LD block). Any field may be empty.
func extractTopCardMeta(doc *goquery.Document) jobMeta {
	var m jobMeta
	if t := strings.TrimSpace(doc.Find(".topcard__title").First().Text()); t != "" {
		m.Title = t
	}
	if a := doc.Find("a.topcard__org-name-link").First(); a.Length() > 0 {
		if c := strings.TrimSpace(a.Text()); c != "" {
			m.Company = c
		}
	}
	if m.Company == "" {
		if f := doc.Find(".topcard__flavor").First(); f.Length() > 0 {
			if c := strings.TrimSpace(f.Text()); c != "" {
				m.Company = c
			}
		}
	}
	// The first non-applicant bullet is the location (e.g. "Toronto, Ontario,
	// Canada"); later bullets are things like "40 applicants".
	doc.Find(".topcard__flavor--bullet").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		t := strings.TrimSpace(s.Text())
		if t == "" || applicantBulletRE.MatchString(t) {
			return true
		}
		m.Location = t
		return false
	})
	return m
}

// jobMeta captures the structured fields available in a JSON-LD JobPosting
// block on a LinkedIn detail page. Any field may be empty if absent.
type jobMeta struct {
	Title       string
	Company     string
	Location    string
	Description string
	DatePosted  string
}

// extractJobMeta scans JSON-LD <script> blocks for a JobPosting and returns its
// structured fields. Returns a zero-value jobMeta when no JobPosting is found.
func extractJobMeta(doc *goquery.Document) jobMeta {
	var meta jobMeta
	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			return true
		}
		if m := jobMetaFromJSONLD(raw); m != nil {
			meta = *m
			return false
		}
		return true
	})
	return meta
}

// jobMetaFromJSONLD parses a raw JSON-LD blob (single object or array) and
// returns the first JobPosting's fields, or nil if none is present.
func jobMetaFromJSONLD(raw string) *jobMeta {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if m := jobMetaFromBytes([]byte(raw)); m != nil {
		return m
	}
	if raw[0] == '[' {
		var arr []map[string]interface{}
		if json.Unmarshal([]byte(raw), &arr) == nil {
			for _, o := range arr {
				if m := jobMetaFromMap(o); m != nil {
					return m
				}
			}
		}
	}
	return nil
}

func jobMetaFromBytes(b []byte) *jobMeta {
	var o map[string]interface{}
	if json.Unmarshal(b, &o) != nil {
		return nil
	}
	return jobMetaFromMap(o)
}

// jobMetaFromMap extracts JobPosting fields from a decoded JSON-LD object.
// Returns nil if the object isn't a JobPosting or has none of the fields we
// care about, so the caller can keep scanning.
func jobMetaFromMap(o map[string]interface{}) *jobMeta {
	if !hasJobPostingType(o["@type"]) {
		return nil
	}
	m := &jobMeta{}
	if t, ok := o["title"].(string); ok {
		m.Title = strings.TrimSpace(t)
	}
	if org, ok := o["hiringOrganization"].(map[string]interface{}); ok {
		if name, ok := org["name"].(string); ok {
			m.Company = strings.TrimSpace(name)
		}
	}
	m.Location = locationStringFromJSONLD(o["jobLocation"])
	if d, ok := o["description"].(string); ok {
		m.Description = cleanHTMLText(d)
	}
	if d, ok := o["datePosted"].(string); ok {
		m.DatePosted = strings.TrimSpace(d)
	}
	if m.Title == "" && m.Company == "" && m.Location == "" && m.Description == "" && m.DatePosted == "" {
		return nil
	}
	return m
}

// locationStringFromJSONLD renders "City, Region, Country" from a JSON-LD
// jobLocation value (single object or array). Returns "" when no address is
// present.
func locationStringFromJSONLD(v interface{}) string {
	var locs []interface{}
	switch x := v.(type) {
	case map[string]interface{}:
		locs = []interface{}{x}
	case []interface{}:
		locs = x
	default:
		return ""
	}
	for _, l := range locs {
		lm, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		addr, _ := lm["address"].(map[string]interface{})
		if addr == nil {
			continue
		}
		var parts []string
		if s, ok := addr["addressLocality"].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		if s, ok := addr["addressRegion"].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		if s, ok := addr["addressCountry"].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ", ")
		}
	}
	return ""
}

// hasJobPostingType reports whether the JSON-LD @type value(s) include JobPosting.
func hasJobPostingType(t interface{}) bool {
	switch v := t.(type) {
	case string:
		return v == "JobPosting"
	case []interface{}:
		for _, e := range v {
			if s, ok := e.(string); ok && s == "JobPosting" {
				return true
			}
		}
	}
	return false
}

// fetchJobPostingViaAPI pulls the description body and workplace type for a job
// from the Voyager jobPostings REST API. Used as a fallback when the detail
// HTML (a React Server Components SPA) ships no description in the initial
// HTML, and to recover the workplace type (Remote/Hybrid/On-site), which the
// guest page omits entirely. workplaceTypes is a list of URNs
// (urn:li:fs_workplaceType:1=on-site, :2=remote, :3=hybrid); workRemoteAllowed
// is a boolean fallback. See jobPostingAPIFields for the two supported shapes.
//
// Requires a CSRF-bearing session (authenticated calls only). Returns empty
// strings on any soft miss so the caller can treat it as a non-fatal miss.
func (c *Client) fetchJobPostingViaAPI(id string) (description, remoteType string, err error) {
	if id == "" || !c.HasSession() || c.session.CSRFToken == "" {
		return "", "", nil
	}
	url := "https://www.linkedin.com/voyager/api/jobs/jobPostings/" + id
	hdr := http.Header{
		"Referer":                   {"https://www.linkedin.com/jobs/view/" + id + "/"},
		"X-Restli-Protocol-Version": {"2.0.0"},
		"Csrf-Token":                {c.session.CSRFToken},
	}
	body, status, err := c.getJSON(url, true, hdr)
	if err != nil {
		return "", "", err
	}
	if status != 200 {
		return "", "", errf("jobPostings API returned status %d for %s", status, id)
	}
	description, remoteType = jobPostingAPIFields(body)
	return description, remoteType, nil
}

// jobPostingAPIFields extracts the description text and workplace type from a
// Voyager jobPostings response. The endpoint has shipped two shapes — a flat
// object with fields at the root (current), and a legacy Pemberly envelope that
// nests them under "data" — so both are tried. Returns ("", "") on any shape
// mismatch so the caller can treat it as a soft miss.
func jobPostingAPIFields(body string) (description, remoteType string) {
	var resp struct {
		Description struct {
			Text string `json:"text"`
		} `json:"description"`
		WorkplaceTypes    []string `json:"workplaceTypes"`
		WorkRemoteAllowed bool     `json:"workRemoteAllowed"`
		Data struct {
			Description struct {
				Text string `json:"text"`
			} `json:"description"`
			WorkplaceTypes    []string `json:"workplaceTypes"`
			WorkRemoteAllowed bool     `json:"workRemoteAllowed"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(body), &resp) != nil {
		return "", ""
	}
	description = strings.TrimSpace(resp.Description.Text)
	remoteType = workplaceTypeFromURNs(resp.WorkplaceTypes)
	if remoteType == "" && resp.WorkRemoteAllowed {
		remoteType = "remote"
	}
	// Fall back to the legacy "data" envelope when the root fields were absent.
	if description == "" {
		description = strings.TrimSpace(resp.Data.Description.Text)
	}
	if remoteType == "" {
		remoteType = workplaceTypeFromURNs(resp.Data.WorkplaceTypes)
		if remoteType == "" && resp.Data.WorkRemoteAllowed {
			remoteType = "remote"
		}
	}
	return description, remoteType
}

// fetchDescriptionViaAPI fetches only the description body. Thin wrapper over
// fetchJobPostingViaAPI, kept for callers/tests that only need the description.
func (c *Client) fetchDescriptionViaAPI(id string) (string, error) {
	desc, _, err := c.fetchJobPostingViaAPI(id)
	return desc, err
}

// workplaceTypeFromURNs maps a Voyager workplaceType URN to the remote/hybrid/
// onsite vocabulary used by DetectRemote. Returns "" when no known URN is
// present. URN ids: 1=on-site, 2=remote, 3=hybrid.
func workplaceTypeFromURNs(urns []string) string {
	for _, u := range urns {
		switch {
		case strings.HasSuffix(u, ":2"):
			return "remote"
		case strings.HasSuffix(u, ":3"):
			return "hybrid"
		case strings.HasSuffix(u, ":1"):
			return "onsite"
		}
	}
	return ""
}

// descriptionFromJobPostingAPI extracts the plain text from a Voyager
// jobPostings response's data.description.text field. Returns "" on any shape
// mismatch so the caller can fall through without erroring.
func descriptionFromJobPostingAPI(body string) string {
	var resp struct {
		Data struct {
			Description struct {
				Text string `json:"text"`
			} `json:"description"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(body), &resp) != nil {
		return ""
	}
	return strings.TrimSpace(resp.Data.Description.Text)
}

// FetchDetailsBatch fetches detail pages for multiple jobs with a politeness
// delay. Per-job errors are recorded but do not abort the batch.
func (c *Client) FetchDetailsBatch(jobs []*models.JobPosting, delay float64, progress func(done, total int)) {
	for i, j := range jobs {
		if err := c.FetchDetail(j); err != nil {
			j.Summary = "[fetch error: " + err.Error() + "]"
		}
		if progress != nil {
			progress(i+1, len(jobs))
		}
		if i < len(jobs)-1 {
			sleep(delay)
		}
	}
}
