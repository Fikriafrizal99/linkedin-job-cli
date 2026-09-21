package linkedin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// PeopleSearchCandidate is a public professional profile returned by LinkedIn's
// authenticated people-search surface.
type PeopleSearchCandidate struct {
	Name        string
	Headline    string
	Location    string
	ProfileURL  string
	TrackingURN string
}

var peopleSearchEndpoint = "https://www.linkedin.com/voyager/api/graphql"

// Persisted GraphQL query ids change as LinkedIn ships web bundles. The first
// entry is the freshest observed public web-client id; the second is a recent
// verified fallback. We only fall back on HTTP 400 (stale/invalid persisted
// query), never on auth/rate-limit/challenge responses.
var peopleSearchQueryIDs = []string{
	"voyagerSearchDashClusters.a7a0567fa66c52d645b5ff2f960b92aa",
	"voyagerSearchDashClusters.e438ab99259203e9c1cd3f358e217282",
}

// SearchPeopleAtCompany searches a small, bounded set of LinkedIn people at one
// current company. It requires the user's authenticated LinkedIn session.
// maxResults is capped at 10 to keep contact resolution narrow and polite.
func (c *Client) SearchPeopleAtCompany(companyID, keywords string, maxResults int) ([]PeopleSearchCandidate, error) {
	companyID = strings.TrimSpace(companyID)
	keywords = strings.TrimSpace(keywords)
	if companyID == "" {
		return nil, errf("people search requires a LinkedIn company id")
	}
	if keywords == "" {
		return nil, errf("people search requires keywords")
	}
	if !c.HasSession() || c.session == nil || !c.session.Valid() {
		return nil, ErrAuthRequired
	}
	if maxResults < 1 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	hdr := http.Header{
		"Accept":                    {"application/vnd.linkedin.normalized+json+2.1"},
		"Csrf-Token":                {c.session.CSRFToken},
		"X-Restli-Protocol-Version": {"2.0.0"},
		"X-Li-Lang":                 {"en_US"},
		"Referer":                   {"https://www.linkedin.com/search/results/people/"},
	}

	var lastStatus int
	for i, queryID := range peopleSearchQueryIDs {
		apiURL := peopleSearchGraphQLURL(companyID, keywords, maxResults, queryID)
		body, _, status, err := c.get(apiURL, true, hdr)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "stopped after 10 redirects") {
				return nil, errf("LinkedIn redirected the people-search API repeatedly; the session may be incomplete, expired, or challenged")
			}
			return nil, err
		}
		lastStatus = status

		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, errf("LinkedIn rejected the people-search session (status %d); refresh the local LinkedIn session", status)
		}
		if status == http.StatusTooManyRequests {
			return nil, errf("LinkedIn rate-limited people search (status 429); try again later")
		}
		if status == http.StatusOK {
			return parsePeopleSearch(body), nil
		}
		// A persisted GraphQL query id commonly becomes stale after a LinkedIn
		// web release and answers 400. Try one recent verified fallback only.
		if status == http.StatusBadRequest && i+1 < len(peopleSearchQueryIDs) {
			continue
		}
		return nil, errf("LinkedIn people search returned status %d", status)
	}

	return nil, errf("LinkedIn people search returned status %d", lastStatus)
}

func peopleSearchGraphQLURL(companyID, keywords string, maxResults int, queryID string) string {
	variables := "(start:0,count:" + itoa(maxResults) +
		",origin:GLOBAL_SEARCH_HEADER,query:(keywords:" + escapeVoyagerSearchValue(keywords) +
		",flagshipSearchIntent:SEARCH_SRP,queryParameters:List(" +
		"(key:currentCompany,value:List(" + escapeVoyagerSearchValue(companyID) + "))," +
		"(key:resultType,value:List(PEOPLE))),includeFiltersInResponse:false))"
	return peopleSearchEndpoint + "?variables=" + variables + "&queryId=" + url.QueryEscape(queryID)
}

func escapeVoyagerSearchValue(s string) string {
	return strings.ReplaceAll(url.QueryEscape(strings.TrimSpace(s)), "+", "%20")
}

func parsePeopleSearch(body string) []PeopleSearchCandidate {
	var resp struct {
		Included []struct {
			Type              string  `json:"$type"`
			TrackingURN       string  `json:"trackingUrn"`
			EntityURN         string  `json:"entityUrn"`
			NavigationURL     string  `json:"navigationUrl"`
			Title             *textVM `json:"title"`
			PrimarySubtitle   *textVM `json:"primarySubtitle"`
			SecondarySubtitle *textVM `json:"secondarySubtitle"`
		} `json:"included"`
	}
	if json.Unmarshal([]byte(body), &resp) != nil {
		return nil
	}

	seen := map[string]bool{}
	var out []PeopleSearchCandidate
	for _, inc := range resp.Included {
		if !strings.HasSuffix(inc.Type, "EntityResultViewModel") {
			continue
		}
		profileURL := cleanProfileURL(inc.NavigationURL)
		tracking := firstNonEmpty(inc.TrackingURN, inc.EntityURN)
		if profileURL == "" && !isPeopleTrackingURN(tracking) {
			continue
		}
		name := textVMText(inc.Title)
		if name == "" {
			continue
		}
		key := profileURL
		if key == "" {
			key = tracking
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, PeopleSearchCandidate{
			Name:        name,
			Headline:    textVMText(inc.PrimarySubtitle),
			Location:    textVMText(inc.SecondarySubtitle),
			ProfileURL:  profileURL,
			TrackingURN: tracking,
		})
	}
	return out
}

func cleanProfileURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if !strings.Contains(strings.ToLower(u.Host), "linkedin.com") || !strings.HasPrefix(u.Path, "/in/") {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String()
}

func isPeopleTrackingURN(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, ":member:") || strings.Contains(s, ":fsd_profile:")
}
