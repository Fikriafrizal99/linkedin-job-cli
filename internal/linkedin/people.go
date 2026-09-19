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

const peopleSearchEndpoint = "https://www.linkedin.com/voyager/api/search/dash/clusters"
const peopleSearchDecoration = "com.linkedin.voyager.dash.deco.search.SearchClusterCollection-185"

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

	query := "(keywords:" + escapeVoyagerSearchValue(keywords) +
		",flagshipSearchIntent:SEARCH_SRP,queryParameters:(currentCompany:List(" +
		companyID + "),resultType:List(PEOPLE)),includeFiltersInResponse:false)"

	params := url.Values{}
	params.Set("decorationId", peopleSearchDecoration)
	params.Set("origin", "GLOBAL_SEARCH_HEADER")
	params.Set("q", "all")
	params.Set("start", "0")
	params.Set("count", itoa(maxResults))

	apiURL := peopleSearchEndpoint + "?" + params.Encode() + "&query=" + query
	hdr := http.Header{
		"Accept":                    {"application/vnd.linkedin.normalized+json+2.1"},
		"Csrf-Token":                {c.session.CSRFToken},
		"X-Restli-Protocol-Version": {"2.0.0"},
		"X-Li-Lang":                 {"en_US"},
		"Referer":                   {"https://www.linkedin.com/search/results/people/"},
	}

	body, _, status, err := c.get(apiURL, true, hdr)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errf("LinkedIn rejected the people-search session (status %d); refresh with linkedin-jobs auth login", status)
	}
	if status != http.StatusOK {
		return nil, errf("LinkedIn people search returned status %d", status)
	}
	return parsePeopleSearch(body), nil
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
