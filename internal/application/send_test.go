package application

import (
	"testing"

	"linkedin-jobs/internal/models"
)

func TestBuildSendRequestApproved(t *testing.T) {
	app := &models.JobApplication{
		JobID:        "123",
		State:        models.ApplicationStateApproved,
		GmailDraftID: "draft-123",
	}
	got, err := BuildSendRequest(app)
	if err != nil {
		t.Fatalf("BuildSendRequest: %v", err)
	}
	if got.JobID != "123" || got.GmailDraftID != "draft-123" {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildSendRequestRequiresApproved(t *testing.T) {
	app := &models.JobApplication{
		JobID:        "123",
		State:        models.ApplicationStateDraftCreated,
		GmailDraftID: "draft-123",
	}
	if _, err := BuildSendRequest(app); err == nil {
		t.Fatal("expected non-approved state to be rejected")
	}
}

func TestBuildSendRequestRejectsMissingDraft(t *testing.T) {
	app := &models.JobApplication{
		JobID: "123",
		State: models.ApplicationStateApproved,
	}
	if _, err := BuildSendRequest(app); err == nil {
		t.Fatal("expected missing draft id to be rejected")
	}
}
