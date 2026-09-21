package cmd

import (
	"bytes"
	"strings"
	"testing"

	"linkedin-jobs/internal/models"
)

func TestSelectedApplicationIDsDeduplicatesAndRequiresSelection(t *testing.T) {
	got, err := selectedApplicationIDs([]string{" 100 ", "100", "200", ""}, 10)
	if err != nil {
		t.Fatalf("selectedApplicationIDs: %v", err)
	}
	if len(got) != 2 || got[0] != "100" || got[1] != "200" {
		t.Fatalf("got %#v", got)
	}
	if _, err := selectedApplicationIDs(nil, 10); err == nil {
		t.Fatal("expected empty selection error")
	}
	if _, err := selectedApplicationIDs([]string{"1", "2", "3"}, 2); err == nil {
		t.Fatal("expected selection limit error")
	}
}

func TestReviewQueueIDsOnlyKeepsDraftCreated(t *testing.T) {
	apps := []models.JobApplication{
		{JobID: "1", State: models.ApplicationStateDraftCreated},
		{JobID: "2", State: models.ApplicationStateApproved},
		{JobID: "3", State: models.ApplicationStateDraftCreated},
	}
	got := reviewQueueIDs(apps, "3,2,1")
	if len(got) != 2 || got[0] != "1" || got[1] != "3" {
		t.Fatalf("got %#v", got)
	}
	if u := reviewQueueURL(got, 2); !strings.Contains(u, "pos=0") {
		t.Fatalf("review queue should wrap, url=%q", u)
	}
}

func TestApplicationWorkbenchRendersBulkActions(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, appPageData{
		Title:             "Applications",
		Active:            "applications",
		CSRF:              "csrf-test",
		CandidateName:     "Candidate",
		CandidateInitials: "C",
		Stats:             appStats{DraftTotal: 2},
		Applications: []appApplicationRow{{
			JobID: "101", Title: "Sales Executive", Company: "Example", Method: "EMAIL",
			State: models.ApplicationStateReadyEmail, Recipient: "jobs@example.com", Prepared: true,
		}},
		Attachments: []appAttachment{{ID: "portfolio-1", Label: "Portfolio", Kind: "portfolio", Exists: true}},
	}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		"id=\"select-all-apps\"",
		"name=\"job_id\" value=\"101\"",
		"formaction=\"/app/applications/bulk/prepare\"",
		"formaction=\"/app/applications/bulk/draft\"",
		"formaction=\"/app/applications/bulk/review\"",
		"formaction=\"/app/applications/send-confirm\"",
		"href=\"/app/applications/review\"",
		"name=\"attachment\" value=\"portfolio-1\" checked",
		"PREPARED",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("workbench missing %q", want)
		}
	}
}

func TestReviewQueueRendersApproveAndNext(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, appPageData{
		Title:             "Review Queue",
		Active:            "applications",
		CSRF:              "csrf-test",
		CandidateName:     "Candidate",
		CandidateInitials: "C",
		ReviewMode:        true,
		ReviewPosition:    1,
		ReviewTotal:       3,
		ReviewPrevURL:     "/app/applications/review?pos=2",
		ReviewNextURL:     "/app/applications/review?pos=1",
		ReviewStayURL:     "/app/applications/review?pos=0",
		SelectedApplication: &models.JobApplication{
			JobID: "201", State: models.ApplicationStateDraftCreated,
			Recipient: "jobs@example.com", Subject: "Application", Body: "Body",
			CVProfile: "general", GmailDraftID: "draft-201",
		},
	}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		"Review 1 of 3",
		"Approve &amp; Next",
		"name=\"return_to\" value=\"/app/applications/review?pos=0\"",
		"id=\"review-next\"",
		"id=\"review-prev\"",
		"Approval never sends email.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("review queue missing %q", want)
		}
	}
}

func TestSendConfirmationRendersFinalHumanGate(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, appPageData{
		Title:             "Confirm Application Send",
		Active:            "applications",
		CSRF:              "csrf-test",
		CandidateName:     "Candidate",
		CandidateInitials: "C",
		SendConfirmMode:   true,
		SendCandidates: []appSendCandidate{{
			JobID: "301", Title: "Sales Executive", Company: "Example",
			Recipient: "jobs@example.com", Subject: "Application - Sales Executive", DraftID: "draft-301",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		"Final Send Confirmation",
		"action=\"/app/applications/bulk/send\"",
		"name=\"job_id\" value=\"301\"",
		"name=\"send_confirm\" value=\"1\" required",
		"Send 1 Application(s)",
		"This action sends the emails above through Gmail.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("send confirmation missing %q", want)
		}
	}
}
