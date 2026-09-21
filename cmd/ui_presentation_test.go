package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

func TestAppListsPageAndPreserveFilterContext(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 55; i++ {
		if err := st.Upsert(&models.JobPosting{ID: fmt.Sprintf("page-%02d", i), Title: "Sales", Company: "Example", SearchedAt: "2026-09-21T08:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	ws := &webServer{st: st}
	pd, err := ws.buildAppPage(httptest.NewRequest("GET", "/app/jobs?q=Sales&since=2026-09-20&page=2", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(pd.Jobs) != 5 || pd.TotalRows != 55 || pd.FirstRow != 51 || pd.LastRow != 55 || pd.NextPageURL != "" {
		t.Fatalf("wrong second page: rows=%d total=%d range=%d-%d", len(pd.Jobs), pd.TotalRows, pd.FirstRow, pd.LastRow)
	}
	u, _ := url.Parse(pd.PrevPageURL)
	if u.Query().Get("q") != "Sales" || u.Query().Get("since") != "2026-09-20" {
		t.Fatalf("filters lost: %s", pd.PrevPageURL)
	}
	pd, err = ws.buildAppPage(httptest.NewRequest("GET", "/app/jobs?since=2026-09-22", nil))
	if err != nil {
		t.Fatal(err)
	}
	if pd.TotalRows != 0 {
		t.Fatal("date filter ignored")
	}
	pd, err = ws.buildAppPage(httptest.NewRequest("GET", "/app/jobs?page=999999", nil))
	if err != nil {
		t.Fatal(err)
	}
	if pd.Page != 2 {
		t.Fatal("page should clamp to last page")
	}
}

func TestMissingAppPagesKeepNavigationAndReturn404(t *testing.T) {
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.yaml"))
	st, err := store.Open(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ws := &webServer{st: st}
	for _, path := range []string{"/app/jobs/missing", "/app/applications/missing", "/app/missing", "/app/settings/missing", "/app/jobs/a/b"} {
		rec := httptest.NewRecorder()
		ws.handleAppUI(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Page not found") || !strings.Contains(rec.Body.String(), `href="/app/jobs"`) {
			t.Fatalf("unusable missing-page response for %s: %d", path, rec.Code)
		}
	}
}

func TestRepeatedCVControlsHaveUniqueAccessibleLabels(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tpl.Execute(&out, appPageData{Active: "cv-profiles", CVProfiles: []appCVProfile{{ID: "general", Default: true}, {ID: "sales"}, {ID: "finance"}}}); err != nil {
		t.Fatal(err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	doc.Find("[id]").Each(func(_ int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		if ids[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		ids[id] = true
	})
	doc.Find("input:not([type=hidden]),select,textarea").Each(func(_ int, s *goquery.Selection) {
		if _, ok := s.Attr("aria-label"); ok {
			return
		}
		if s.ParentsFiltered("label").Length() > 0 {
			return
		}
		id, _ := s.Attr("id")
		if id == "" || doc.Find(`label[for="`+id+`"]`).Length() != 1 {
			t.Errorf("missing unique label: %s", goquery.NodeName(s))
		}
	})
}

func TestPresentationDoesNotExposeInvalidActions(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"NEED_REVIEW", "READY_EMAIL", "DRAFT_CREATED", "APPROVED", "SENT", "READY_EASY_APPLY", "IN_PROGRESS", "APPLIED"} {
		var out strings.Builder
		app := &models.JobApplication{JobID: "j1", State: state}
		pd := appPageData{Active: "applications", SelectedApplication: app}
		if err := tpl.Execute(&out, pd); err != nil {
			t.Fatal(err)
		}
		doc, _ := goquery.NewDocumentFromReader(strings.NewReader(out.String()))
		if state == "NEED_REVIEW" && (doc.Find(`form[action$="/prepare"]`).Length() != 0 || doc.Find(".email-body").Length() != 0) {
			t.Fatal("unsupported record should explain next steps instead of showing empty email actions")
		}
		if state == "SENT" || state == "APPLIED" {
			if doc.Find(`form[action$="/remove"],form[action$="/approve"],form[action$="/prepare"]`).Length() != 0 {
				t.Fatalf("terminal %s exposes lifecycle mutation", state)
			}
		}
		if state == "APPROVED" && (doc.Find(`form[action$="send-confirm"]`).Length() != 1 || doc.Find(`form[action$="bulk/send"]`).Length() != 0) {
			t.Fatal("approved detail must route to separate confirmation")
		}
	}
}

func TestApplicationBulkRedirectPreservesFilters(t *testing.T) {
	form := url.Values{"filter_q": {"Sales & support"}, "filter_method": {"EMAIL"}, "filter_state": {"READY_EMAIL"}, "filter_since": {"2026-09-20"}, "filter_page": {"2"}}
	req := httptest.NewRequest("POST", "/app/applications/bulk/prepare", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	redirectBulkResult(rec, req, "prepare", 1, 0, 0, nil)
	u, _ := url.Parse(rec.Header().Get("Location"))
	for _, key := range []string{"q", "method", "state", "since", "page"} {
		if u.Query().Get(key) != form.Get("filter_"+key) {
			t.Errorf("lost filter %s", key)
		}
	}
}

func TestDetailLinksPreserveIndividualQueryParameters(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	pd := appPageData{Active: "jobs", ListURL: "/app/jobs?page=2&q=Sales%26Support", Jobs: []appJobRow{{ID: "safe-id", Title: "Sales"}}}
	if err := tpl.Execute(&out, pd); err != nil {
		t.Fatal(err)
	}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(out.String()))
	href, _ := doc.Find(".jobs-table a.job-link").Attr("href")
	u, err := url.Parse(href)
	if err != nil || u.Query().Get("page") != "2" || u.Query().Get("q") != "Sales&Support" {
		t.Fatalf("detail link lost individual query params: %q", href)
	}
}
