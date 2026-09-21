package linkedin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkedin-jobs/internal/auth"
	"linkedin-jobs/internal/config"
)

func TestParsePeopleSearch(t *testing.T) {
	body := `{
	  "included": [
	    {
	      "$type":"com.linkedin.voyager.dash.search.EntityResultViewModel",
	      "trackingUrn":"urn:li:member:123",
	      "navigationUrl":"https://www.linkedin.com/in/jane-doe/?miniProfileUrn=x",
	      "title":{"text":"Jane Doe"},
	      "primarySubtitle":{"text":"Talent Acquisition Partner at Acme"},
	      "secondarySubtitle":{"text":"Jakarta, Indonesia"}
	    },
	    {
	      "$type":"com.linkedin.voyager.dash.search.EntityResultViewModel",
	      "trackingUrn":"urn:li:company:456",
	      "navigationUrl":"https://www.linkedin.com/company/acme/",
	      "title":{"text":"Acme"}
	    }
	  ]
	}`
	got := parsePeopleSearch(body)
	if len(got) != 1 {
		t.Fatalf("got %d people, want 1", len(got))
	}
	if got[0].Name != "Jane Doe" {
		t.Fatalf("name=%q", got[0].Name)
	}
	if got[0].Headline != "Talent Acquisition Partner at Acme" {
		t.Fatalf("headline=%q", got[0].Headline)
	}
	if got[0].ProfileURL != "https://www.linkedin.com/in/jane-doe/" {
		t.Fatalf("profile=%q", got[0].ProfileURL)
	}
}

func TestParsePeopleSearchDeduplicatesProfile(t *testing.T) {
	body := `{
	  "included": [
	    {
	      "$type":"com.linkedin.voyager.dash.search.EntityResultViewModel",
	      "trackingUrn":"urn:li:member:123",
	      "navigationUrl":"https://www.linkedin.com/in/jane-doe/",
	      "title":{"text":"Jane Doe"}
	    },
	    {
	      "$type":"com.linkedin.voyager.dash.search.EntityResultViewModel",
	      "trackingUrn":"urn:li:member:123",
	      "navigationUrl":"https://www.linkedin.com/in/jane-doe/?trk=x",
	      "title":{"text":"Jane Doe"}
	    }
	  ]
	}`
	got := parsePeopleSearch(body)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
}

func TestCleanProfileURLRejectsNonProfile(t *testing.T) {
	if got := cleanProfileURL("https://www.linkedin.com/company/acme/"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}


func TestPeopleSearchGraphQLURL(t *testing.T) {
	oldEndpoint := peopleSearchEndpoint
	peopleSearchEndpoint = "https://www.linkedin.com/voyager/api/graphql"
	t.Cleanup(func() { peopleSearchEndpoint = oldEndpoint })

	got := peopleSearchGraphQLURL(
		"3476",
		"Talent Acquisition",
		5,
		"voyagerSearchDashClusters.testhash",
	)
	if !strings.Contains(got, "/voyager/api/graphql?variables=") {
		t.Fatalf("missing GraphQL endpoint: %s", got)
	}
	if !strings.Contains(got, "keywords:Talent%20Acquisition") {
		t.Fatalf("keywords not Rest.li encoded: %s", got)
	}
	if !strings.Contains(got, "(key:currentCompany,value:List(3476))") {
		t.Fatalf("missing currentCompany facet: %s", got)
	}
	if !strings.Contains(got, "(key:resultType,value:List(PEOPLE))") {
		t.Fatalf("missing PEOPLE facet: %s", got)
	}
	if !strings.Contains(got, "queryId=voyagerSearchDashClusters.testhash") {
		t.Fatalf("missing query id: %s", got)
	}
}

func TestSearchPeopleAtCompanyFallsBackOnStaleGraphQLQueryID(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Csrf-Token"); got != "ajax:123" {
			t.Errorf("csrf-token=%q", got)
		}
		if got := r.Header.Get("X-Restli-Protocol-Version"); got != "2.0.0" {
			t.Errorf("restli=%q", got)
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "li_at=test") {
			t.Errorf("cookie=%q", got)
		}
		qid := r.URL.Query().Get("queryId")
		calls = append(calls, qid)
		if qid == "voyagerSearchDashClusters.stale" {
			http.Error(w, "stale query id", http.StatusBadRequest)
			return
		}
		if qid != "voyagerSearchDashClusters.fresh" {
			http.Error(w, "unexpected query id", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{
		  "included":[{
		    "$type":"com.linkedin.voyager.dash.search.EntityResultViewModel",
		    "trackingUrn":"urn:li:member:123",
		    "navigationUrl":"https://www.linkedin.com/in/jane-doe/",
		    "title":{"text":"Jane Doe"},
		    "primarySubtitle":{"text":"Talent Acquisition Partner at Acme"},
		    "secondarySubtitle":{"text":"Jakarta, Indonesia"}
		  }]
		}`)
	}))
	defer server.Close()

	oldEndpoint := peopleSearchEndpoint
	oldIDs := peopleSearchQueryIDs
	peopleSearchEndpoint = server.URL
	peopleSearchQueryIDs = []string{
		"voyagerSearchDashClusters.stale",
		"voyagerSearchDashClusters.fresh",
	}
	t.Cleanup(func() {
		peopleSearchEndpoint = oldEndpoint
		peopleSearchQueryIDs = oldIDs
	})

	cfg := config.Load()
	cfg.HTTPMaxAttempts = 1
	c := New(cfg)
	c.http = server.Client()
	c.WithSession(&auth.Session{
		CookieHeader: `li_at=test; JSESSIONID="ajax:123"`,
		CSRFToken:    "ajax:123",
		Source:       "test",
	})

	got, err := c.SearchPeopleAtCompany("3476", "Talent Acquisition", 5)
	if err != nil {
		t.Fatalf("SearchPeopleAtCompany: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Jane Doe" {
		t.Fatalf("got %+v", got)
	}
	if len(calls) != 2 || calls[0] != "voyagerSearchDashClusters.stale" || calls[1] != "voyagerSearchDashClusters.fresh" {
		t.Fatalf("query-id calls=%v", calls)
	}
}
