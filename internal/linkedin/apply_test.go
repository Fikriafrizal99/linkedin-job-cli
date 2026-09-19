package linkedin

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"linkedin-jobs/internal/parser"
)

func applyDoc(t *testing.T, body string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestExtractApplyControlOnsite(t *testing.T) {
	doc := applyDoc(t, `<button data-tracking-control-name="public_jobs_apply-link-onsite">Apply</button>`)
	got := extractApplyControl(doc)
	if got.Method != parser.ApplicationMethodLinkedIn {
		t.Fatalf("method=%q", got.Method)
	}
	if got.URL != "" {
		t.Fatalf("onsite URL=%q, want empty", got.URL)
	}
}

func TestExtractApplyControlOffsiteUnwrapsDestination(t *testing.T) {
	doc := applyDoc(t, `<a data-tracking-control-name="public_jobs_apply-link-offsite"
		href="https://www.linkedin.com/jobs/view/externalApply/123?url=https%3A%2F%2Fcareers.example.com%2Fjobs%2F42&amp;urlHash=x">Apply on company website</a>`)
	got := extractApplyControl(doc)
	if got.Method != parser.ApplicationMethodExternalURL {
		t.Fatalf("method=%q", got.Method)
	}
	if got.URL != "https://careers.example.com/jobs/42" {
		t.Fatalf("URL=%q", got.URL)
	}
}

func TestExtractApplyControlOffsiteWithoutURLStillClassifies(t *testing.T) {
	doc := applyDoc(t, `<button data-tracking-control-name="public_jobs_apply-link-offsite">Apply</button>`)
	got := extractApplyControl(doc)
	if got.Method != parser.ApplicationMethodExternalURL {
		t.Fatalf("method=%q", got.Method)
	}
	if got.URL != "" {
		t.Fatalf("URL=%q, want empty", got.URL)
	}
}

func TestResolveApplicationDataEmailWins(t *testing.T) {
	app := parser.ApplicationData{
		Emails: []string{"jobs@example.com"}, PrimaryEmail: "jobs@example.com",
		Method: parser.ApplicationMethodEmail, Instruction: "Send CV to jobs@example.com",
	}
	page := applyControl{Method: parser.ApplicationMethodLinkedIn, Instruction: "Apply"}
	got := resolveApplicationData(app, page)
	if got.Method != parser.ApplicationMethodEmail || got.PrimaryEmail != "jobs@example.com" {
		t.Fatalf("email did not win: %+v", got)
	}
}

func TestResolveApplicationDataPageControlWinsOverProseHint(t *testing.T) {
	app := parser.ApplicationData{
		Method: parser.ApplicationMethodLinkedIn,
		Instruction: "Apply via LinkedIn",
	}
	page := applyControl{
		Method: parser.ApplicationMethodExternalURL,
		URL: "https://careers.example.com/jobs/1",
		Instruction: "Apply on company website",
	}
	got := resolveApplicationData(app, page)
	if got.Method != parser.ApplicationMethodExternalURL || got.ApplyURL != page.URL {
		t.Fatalf("page control did not win: %+v", got)
	}
}

func TestResolveApplicationDataRetainsDescriptionURLWhenOffsiteURLHidden(t *testing.T) {
	app := parser.ApplicationData{
		Method: parser.ApplicationMethodExternalURL,
		ApplyURL: "https://careers.example.com/jobs/99",
		Instruction: "Apply at careers.example.com",
	}
	page := applyControl{
		Method: parser.ApplicationMethodExternalURL,
		Instruction: "Apply on company website",
	}
	got := resolveApplicationData(app, page)
	if got.ApplyURL != app.ApplyURL || got.Instruction != app.Instruction {
		t.Fatalf("description URL should be retained: %+v", got)
	}
}
