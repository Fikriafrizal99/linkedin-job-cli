package linkedin

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// JobContext is rich metadata about a single LinkedIn job posting, augmented
// beyond the stored JobPosting with the two pieces needed to build people-search
// facets: the company's LinkedIn URL slug and its numeric company ID (both
// pulled from the public job detail page's "See who you know" / org links).
// Filled by FetchJobContext.
type JobContext struct {
	JobID          string
	Title          string
	Company        string
	CompanySlug    string // e.g. "getclera" from /company/getclera
	CompanyID      string // numeric, e.g. "105863333", for facetCurrentCompany
	Location       string
	Description    string
	EmploymentType string
	Seniority      string
	ApplicantCount string
	URL            string
}

// CompanyProfile is best-effort public metadata about a company, pulled from
// its LinkedIn page's static <head> (og:* meta tags) plus any rendered
// top-card fields. LinkedIn company pages are heavily JS-rendered, so every
// field is optional; the job description is usually the richer signal anyway.
type CompanyProfile struct {
	Slug     string
	Name     string
	Tagline  string
	About    string
	Industry string
	Size     string
	HQ       string
	Website  string
}

var (
	// companySlugRE captures the slug from a LinkedIn company URL of any form
	// (.../company/getclera, .../company/getclera/about, with query params).
	companySlugRE = regexp.MustCompile(`linkedin\.com/company/([A-Za-z0-9_-]+)`)
	// facetCompanyRE captures the numeric company id behind the
	// facetCurrentCompany search facet, whether "=" is raw or %-encoded.
	facetCompanyRE = regexp.MustCompile(`facetCurrentCompany(?:%3D|=)(\d+)`)
	// viewJobIDRE captures a job id from both LinkedIn public URL forms:
	// /jobs/view/<id>/ and /jobs/view/<slug>-<id>.
	viewJobIDRE = regexp.MustCompile(`/jobs/view/(?:[^/?#]*-)?(\d+)(?:[/?#]|$)`)
	// applicantsCountRE captures the "N applicants" badge figure.
	applicantsCountRE = regexp.MustCompile(`(?i)(\d+)\s+applicants?`)
)

// ResolveJobID extracts the primary LinkedIn job id from any job URL shape:
// a /jobs/view/<id>/ path, a ?currentJobId=<id> param, or the first id in a
// ?originToLandingJobPostings=<id>,<id> list. Returns "" when no id is found.
// Stray shell backslashes are removed first (same fix as SearchURL).
func ResolveJobID(rawURL string) string {
	rawURL = strings.ReplaceAll(rawURL, "\\", "")
	if m := viewJobIDRE.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	if id := strings.TrimSpace(q.Get("currentJobId")); isDigits(id) {
		return id
	}
	if list := q.Get("originToLandingJobPostings"); list != "" {
		for _, id := range strings.Split(list, ",") {
			if id = strings.TrimSpace(id); isDigits(id) {
				return id
			}
		}
	}
	return ""
}

// jobViewURL returns the canonical public view URL for a job id.
func jobViewURL(id string) string {
	return "https://www.linkedin.com/jobs/view/" + id + "/"
}

// FetchJobContext fetches a LinkedIn job's public detail page and extracts the
// metadata needed to research who to reach out to: title, company, the
// company's LinkedIn slug + numeric id (for people-search facets), location,
// full description, seniority/employment-type, and applicant count. Works
// anonymously; a session is used opportunistically when present for a richer
// description via the Voyager fallback inside FetchDetail-style recovery is
// intentionally NOT duplicated here (the guest page description is sufficient
// for outreach research).
func (c *Client) FetchJobContext(rawURL string) (*JobContext, error) {
	id := ResolveJobID(rawURL)
	if id == "" {
		return nil, errf("no job id found in URL: %s", rawURL)
	}
	viewURL := jobViewURL(id)
	html, _, status, err := c.get(viewURL, false, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, errf("job page fetch returned status %d for %s", status, viewURL)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	ctx := &JobContext{JobID: id, URL: viewURL}

	// JSON-LD first (cleanest source); rendered topcard fills any gaps.
	meta := extractJobMeta(doc)
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
	ctx.Title = firstNonEmpty(meta.Title, tc.Title)
	ctx.Company = firstNonEmpty(meta.Company, tc.Company)
	ctx.Location = firstNonEmpty(meta.Location, tc.Location)
	ctx.Description = firstNonEmpty(meta.Description, extractDescriptionHTML(doc))

	// Company slug + numeric id come from any /company/<slug> link and any
	// facetCurrentCompany=<id> link on the page (the "See who you know" /
	// "See who X has hired for this role" CTAs). Both are emitted even on the
	// anonymous guest page, so this works without a session.
	doc.Find("a[href]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		href, _ := s.Attr("href")
		if ctx.CompanySlug == "" {
			if m := companySlugRE.FindStringSubmatch(href); m != nil {
				ctx.CompanySlug = m[1]
			}
		}
		if ctx.CompanyID == "" {
			if m := facetCompanyRE.FindStringSubmatch(href); m != nil {
				ctx.CompanyID = m[1]
			}
		}
		return ctx.CompanySlug == "" || ctx.CompanyID == ""
	})

	// Seniority / employment-type / applicant count from rendered bullets.
	ctx.Seniority, ctx.EmploymentType = extractCriteria(doc)
	if m := applicantsCountRE.FindStringSubmatch(html); m != nil {
		ctx.ApplicantCount = m[1]
	}
	return ctx, nil
}

// extractCriteria pulls the Seniority level and Employment type from the job
// detail page's criteria list (LinkedIn renders them as labeled paragraphs).
// Returns ("", "") when the page serves no criteria block.
func extractCriteria(doc *goquery.Document) (seniority, employment string) {
	seniority = findCriteriaText(doc, "Seniority level")
	employment = findCriteriaText(doc, "Employment type")
	return
}

// findCriteriaText scans the rendered criteria list for a label and returns its
// adjacent value text. LinkedIn's markup varies, so we look for any element
// whose text matches the label then take the following sibling / next text.
func findCriteriaText(doc *goquery.Document, label string) string {
	found := ""
	doc.Find("h3, .description__job-criteria-subheader, .job-criteria-subheader, dt").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if strings.Contains(strings.TrimSpace(s.Text()), label) {
			// Prefer an explicit sibling value node; fall back to next text.
			if v := strings.TrimSpace(s.Next().Text()); v != "" {
				found = v
				return false
			}
		}
		return true
	})
	return found
}

// FetchCompanyProfile fetches a company's public LinkedIn page and extracts the
// metadata available in the static HTML head (og:title / og:description) plus
// any rendered top-card fields. Every field is best-effort: LinkedIn company
// pages are JS-rendered and partially gated, so misses are returned empty rather
// than erroring. The slug is echoed back even when nothing else parses.
func (c *Client) FetchCompanyProfile(slug string) (*CompanyProfile, error) {
	slug = strings.Trim(slug, "/ ")
	if slug == "" {
		return nil, errf("empty company slug")
	}
	companyURL := "https://www.linkedin.com/company/" + slug + "/"
	html, _, status, err := c.get(companyURL, false, nil)
	if err != nil {
		return &CompanyProfile{Slug: slug}, err
	}
	if status != 200 {
		return &CompanyProfile{Slug: slug}, errf("company page fetch returned status %d for %s", status, companyURL)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return &CompanyProfile{Slug: slug}, err
	}
	p := &CompanyProfile{Slug: slug}
	p.Name = cleanCompanyText(metaContent(doc, "og:title", "name", "title"))
	if p.Name == "" {
		if t := cleanCompanyText(doc.Find("h1.org-top-card-summary__title, h1.org-top-card__headline, .topcard__name").First().Text()); t != "" {
			p.Name = t
		}
	}
	p.Tagline = cleanCompanyText(metaContent(doc, "og:description", "property", "description"))
	// Rendered tagline / about containers (present on the legacy layout).
	if p.Tagline == "" {
		if t := cleanCompanyText(doc.Find(".org-top-card-summary__tagline, .topcard__tagline, p.org-about-us-organization-description__text").First().Text()); t != "" {
			p.Tagline = t
		}
	}
	if p.About == "" {
		if t := cleanCompanyText(doc.Find(".org-about-us-organization-description__text, .break-words").First().Text()); t != "" {
			p.About = t
		}
	}
	// og:description often reads "<Name> | 11,270 followers on LinkedIn. <tagline>";
	// strip the leading "<Name> | <N> followers on LinkedIn." noise so only the
	// real tagline remains.
	p.Tagline = trimFollowersPrefix(p.Tagline, p.Name)
	p.About = trimFollowersPrefix(p.About, p.Name)
	// Industry / size / HQ from the definition lists on the about layout.
	p.Industry = defListValue(doc, "Industry")
	p.Size = defListValue(doc, "Company size")
	p.HQ = defListValue(doc, "Headquarters")
	return p, nil
}

// metaContent reads a <meta> node by attribute+key (e.g. property="og:title")
// or name=key. Returns "" when absent.
func metaContent(doc *goquery.Document, key, attr, fallbackKey string) string {
	if v := strings.TrimSpace(doc.Find(fmt.Sprintf("meta[%s=%q]", attr, key)).First().AttrOr("content", "")); v != "" {
		return v
	}
	if fallbackKey == "" {
		return ""
	}
	return strings.TrimSpace(doc.Find(fmt.Sprintf("meta[%s=%q]", attr, fallbackKey)).First().AttrOr("content", ""))
}

// defListValue scans <dt>/<dd> and label/value pairs for a label and returns
// the matching value. Tolerates both the dt/dd and the newer div-based layouts.
func defListValue(doc *goquery.Document, label string) string {
	found := ""
	doc.Find("dt, .org-page-details__definition-term").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if strings.Contains(strings.TrimSpace(s.Text()), label) {
			if v := strings.TrimSpace(s.Next().Text()); v != "" {
				found = v
				return false
			}
		}
		return true
	})
	return found
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// followersPrefixRE strips LinkedIn's og:description noise: a leading
// "<Name> | 11,270 followers on LinkedIn." clause, possibly with HTML entities.
var followersPrefixRE = regexp.MustCompile(`(?i)^.*?\|\s*[\d,.]+\s+followers?\s+on\s+linkedin\.\s*`)

// cleanCompanyText decodes HTML entities and collapses whitespace in a company
// page field. LinkedIn's og:* meta values are HTML-escaped, so "&amp;" must be
// decoded before display.
func cleanCompanyText(s string) string {
	s = html.UnescapeString(s)
	s = strings.TrimSpace(s)
	return s
}

// trimFollowersPrefix removes the "<Name> | <N> followers on LinkedIn." prefix
// that LinkedIn prepends to og:description, leaving the real tagline. It only
// trims when the prefix is present so clean taglines are untouched.
func trimFollowersPrefix(s, name string) string {
	if s == "" {
		return s
	}
	if trimmed := followersPrefixRE.ReplaceAllString(s, ""); trimmed != "" {
		s = trimmed
	}
	return dedupPipedDuplicate(s)
}

// dedupPipedDuplicate collapses LinkedIn's og:description quirk where the
// tagline is repeated verbatim separated by " | " (e.g. "X. | X."). When the
// final |-segment equals an earlier one, only the first copy is kept.
func dedupPipedDuplicate(s string) string {
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, " | ")
}
