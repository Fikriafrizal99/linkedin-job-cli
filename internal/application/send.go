package application

import (
	"fmt"
	"strings"

	"linkedin-jobs/internal/models"
)

type SendRequest struct {
	JobID        string `json:"job_id"`
	GmailDraftID string `json:"gmail_draft_id"`
}

func BuildSendRequest(app *models.JobApplication) (SendRequest, error) {
	if app == nil {
		return SendRequest{}, fmt.Errorf("nil application")
	}
	if app.State != models.ApplicationStateApproved {
		return SendRequest{}, fmt.Errorf("application state is %s; expected APPROVED", app.State)
	}
	draftID := strings.TrimSpace(app.GmailDraftID)
	if draftID == "" {
		return SendRequest{}, fmt.Errorf("application has no Gmail draft id")
	}
	if strings.TrimSpace(app.GmailMessageID) != "" {
		return SendRequest{}, fmt.Errorf("application already has sent Gmail message %s", app.GmailMessageID)
	}
	return SendRequest{
		JobID:        app.JobID,
		GmailDraftID: draftID,
	}, nil
}
