package application

import (
	"os"
	"path/filepath"
	"testing"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
)

func TestBuildDraftPayload(t *testing.T) {
	cv := filepath.Join(t.TempDir(), "cv.pdf")
	if err := os.WriteFile(cv, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	app := &models.JobApplication{
		JobID:     "123",
		State:     models.ApplicationStateReadyEmail,
		Recipient: "jobs@example.com",
		Subject:   "Application - Sales Executive",
		Body:      "Dear Hiring Team...",
		CVProfile: "sales",
	}
	settings := config.ApplicationSettings{
		CVProfiles: []config.CVProfileSettings{
			{ID: "sales", Path: cv},
		},
	}
	got, err := BuildDraftPayload(app, settings)
	if err != nil {
		t.Fatalf("BuildDraftPayload: %v", err)
	}
	if got.To != "jobs@example.com" || got.Subject == "" || len(got.AttachmentFiles) != 1 || got.AttachmentFiles[0] != cv {
		t.Fatalf("payload=%+v", got)
	}
}

func TestBuildDraftPayloadRejectsUnprepared(t *testing.T) {
	app := &models.JobApplication{
		JobID:     "123",
		State:     models.ApplicationStateReadyEmail,
		Recipient: "jobs@example.com",
	}
	if _, err := BuildDraftPayload(app, config.ApplicationSettings{}); err == nil {
		t.Fatal("expected unprepared application error")
	}
}

func TestBuildDraftPayloadRejectsExistingDraft(t *testing.T) {
	app := &models.JobApplication{
		JobID:        "123",
		State:        models.ApplicationStateReadyEmail,
		Recipient:    "jobs@example.com",
		Subject:      "Subject",
		Body:         "Body",
		CVProfile:    "sales",
		GmailDraftID: "draft-1",
	}
	if _, err := BuildDraftPayload(app, config.ApplicationSettings{}); err == nil {
		t.Fatal("expected existing draft error")
	}
}
