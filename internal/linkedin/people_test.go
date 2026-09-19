package linkedin

import "testing"

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
