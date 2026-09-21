package linkedin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/parser"
	"linkedin-jobs/internal/store"
)

// TestCollectorIntegration_AnonymousSearchDetailAndPersistence exercises the V1
// collector path against a deterministic local HTTP fixture:
// anonymous search -> adaptive pagination -> detail parsing -> application
// classification -> structural metadata -> SQLite round trip.
func TestCollectorIntegration_AnonymousSearchDetailAndPersistence(t *testing.T) {
	var (
		mu     sync.Mutex
		starts []string
		server *httptest.Server
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		starts = append(starts, r.URL.Query().Get("start"))
		mu.Unlock()

		if r.URL.Query().Get("keywords") != "Sales Executive" {
			t.Errorf("keywords=%q", r.URL.Query().Get("keywords"))
		}
		if r.URL.Query().Get("location") != "Indonesia" {
			t.Errorf("location=%q", r.URL.Query().Get("location"))
		}
		if r.URL.Query().Get("f_TPR") != "r604800" {
			t.Errorf("f_TPR=%q", r.URL.Query().Get("f_TPR"))
		}

		switch r.URL.Query().Get("start") {
		case "":
			fmt.Fprint(w, cardHTML("1001", "Account Executive", "Acme A", "Jakarta", "", "2 days ago", server.URL+"/detail/1001"))
			fmt.Fprint(w, cardHTML("1002", "Sales Executive", "Acme B", "Bandung", "2026-09-20", "1 day ago", server.URL+"/detail/1002"))
		case "2":
			fmt.Fprint(w, cardHTML("1003", "Field Sales Executive", "Acme C", "Surabaya", "2026-09-19", "2 days ago", server.URL+"/detail/1003"))
		default:
			// Empty body signals exhaustion.
		}
	})

	mux.HandleFunc("/detail/1001", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head>
		<script type="application/ld+json">{
		  "@type":"JobPosting",
		  "title":"Account Executive",
		  "hiringOrganization":{"@type":"Organization","name":"Acme A"},
		  "description":"Grow enterprise accounts. Please email your CV to jobs@acmea.example."
		}</script>
		</head><body><span class="posted-time-ago__text">2 days ago</span></body></html>`)
	})
	mux.HandleFunc("/detail/1002", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head>
		<script type="application/ld+json">{
		  "@type":"JobPosting",
		  "title":"Sales Executive",
		  "hiringOrganization":{"@type":"Organization","name":"Acme B"},
		  "description":"Own new-logo sales across Indonesia.",
		  "datePosted":"2026-09-20"
		}</script>
		</head><body>
		<a data-tracking-control-name="public_jobs_apply-link-offsite" href="https://careers.acmeb.example/jobs/1002">Apply</a>
		</body></html>`)
	})
	mux.HandleFunc("/detail/1003", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head>
		<script type="application/ld+json">{
		  "@type":"JobPosting",
		  "title":"Field Sales Executive",
		  "hiringOrganization":{"@type":"Organization","name":"Acme C"},
		  "description":"Manage field sales execution.",
		  "datePosted":"2026-09-19"
		}</script>
		</head><body>
		<button data-tracking-control-name="public_jobs_apply-link-onsite">Easy Apply</button>
		</body></html>`)
	})
	server = httptest.NewServer(mux)
	defer server.Close()

	oldSearchURL := guestSearchURL
	guestSearchURL = server.URL + "/search"
	t.Cleanup(func() { guestSearchURL = oldSearchURL })

	cfg := config.Load()
	cfg.RequestTimeoutSeconds = 2
	cfg.HTTPMaxAttempts = 1
	cfg.HTTPRetryBaseSeconds = 0
	client := New(cfg)

	jobs, err := client.Search(SearchParams{
		Keywords:     "Sales Executive",
		Location:     "Indonesia",
		PostedWithin: "r604800",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("Search returned %d jobs, want 3", len(jobs))
	}

	mu.Lock()
	gotStarts := append([]string(nil), starts...)
	mu.Unlock()
	if want := []string{"", "2", "3"}; !reflect.DeepEqual(gotStarts, want) {
		t.Fatalf("adaptive pagination starts=%v want=%v", gotStarts, want)
	}

	for _, j := range jobs {
		if err := client.FetchDetail(j); err != nil {
			t.Fatalf("FetchDetail %s: %v", j.ID, err)
		}
		j.Source = "linkedin"
	}

	if jobs[0].ApplicationMethod != parser.ApplicationMethodEmail || jobs[0].ApplyEmail != "jobs@acmea.example" {
		t.Fatalf("job 1001 application=%q email=%q", jobs[0].ApplicationMethod, jobs[0].ApplyEmail)
	}
	if jobs[0].PostedAt == "" || !jobs[0].PostedAtEstimated {
		t.Fatalf("job 1001 expected estimated posted_at, got %q estimated=%v", jobs[0].PostedAt, jobs[0].PostedAtEstimated)
	}
	if jobs[1].ApplicationMethod != parser.ApplicationMethodExternalURL ||
		jobs[1].ApplyURL != "https://careers.acmeb.example/jobs/1002" {
		t.Fatalf("job 1002 method=%q url=%q", jobs[1].ApplicationMethod, jobs[1].ApplyURL)
	}
	if jobs[1].PostedAt != "2026-09-20" || jobs[1].PostedAtEstimated {
		t.Fatalf("job 1002 posted=%q estimated=%v", jobs[1].PostedAt, jobs[1].PostedAtEstimated)
	}
	if jobs[2].ApplicationMethod != parser.ApplicationMethodLinkedIn {
		t.Fatalf("job 1003 method=%q want LINKEDIN", jobs[2].ApplicationMethod)
	}
	for _, j := range jobs {
		if j.DetailStatus != "DETAIL_COMPLETE" || j.Description == "" {
			t.Fatalf("job %s incomplete detail: status=%q desc=%q", j.ID, j.DetailStatus, j.Description)
		}
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "collector.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	for _, j := range jobs {
		j.ContentHash = store.ContentHash(j.Company, j.Title, j.Description, j.ListedAt)
		j.StructuralHash = store.StructuralHash(j.Company, j.Title, j.Description)
		classification, duplicateOf, err := st.ClassifyStructuralDuplicate(j)
		if err != nil {
			t.Fatalf("ClassifyStructuralDuplicate %s: %v", j.ID, err)
		}
		j.DuplicateClassification = classification
		j.DuplicateOfJobID = duplicateOf
		if err := st.Upsert(j); err != nil {
			t.Fatalf("Upsert %s: %v", j.ID, err)
		}
	}

	got, err := st.Get("1001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("persisted job 1001 missing")
	}
	if got.ApplicationMethod != parser.ApplicationMethodEmail ||
		got.ApplyEmail != "jobs@acmea.example" ||
		!got.PostedAtEstimated ||
		got.StructuralHash == "" ||
		got.DuplicateClassification != store.DuplicateNew {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func cardHTML(id, title, company, location, datetime, relative, href string) string {
	timeAttr := ""
	if datetime != "" {
		timeAttr = ` datetime="` + datetime + `"`
	}
	return fmt.Sprintf(`<div data-entity-urn="urn:li:jobPosting:%s">
		<h3 class="base-search-card__title">%s</h3>
		<h4 class="base-search-card__subtitle"><a>%s</a></h4>
		<span class="job-search-card__location">%s</span>
		<time%s>%s</time>
		<a class="base-card__full-link" href="%s?trk=test">view</a>
	</div>`, id, title, company, location, timeAttr, relative, href)
}
