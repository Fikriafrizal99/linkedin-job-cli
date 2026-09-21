package application

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
)

type DraftPayload struct {
	JobID           string   `json:"job_id"`
	To              string   `json:"to"`
	Subject         string   `json:"subject"`
	Body            string   `json:"body"`
	ContentType     string   `json:"content_type"`
	CVProfile       string   `json:"cv_profile"`
	AttachmentFiles []string `json:"attachment_files,omitempty"`
	// AttachmentNames optionally overrides the filename shown by the email
	// provider. It is index-aligned with AttachmentFiles; blank/missing entries
	// fall back to filepath.Base(path).
	AttachmentNames []string `json:"attachment_names,omitempty"`
}

func BuildDraftPayload(app *models.JobApplication, settings config.ApplicationSettings) (DraftPayload, error) {
	if app == nil {
		return DraftPayload{}, fmt.Errorf("nil application")
	}
	if app.State != models.ApplicationStateReadyEmail {
		return DraftPayload{}, fmt.Errorf("application state is %s; expected READY_EMAIL", app.State)
	}
	if strings.TrimSpace(app.GmailDraftID) != "" {
		return DraftPayload{}, fmt.Errorf("application already has Gmail draft %s", app.GmailDraftID)
	}
	if strings.TrimSpace(app.Recipient) == "" {
		return DraftPayload{}, fmt.Errorf("application recipient is empty")
	}
	if strings.TrimSpace(app.Subject) == "" {
		return DraftPayload{}, fmt.Errorf("application subject is empty; run applications prepare first")
	}
	if strings.TrimSpace(app.Body) == "" {
		return DraftPayload{}, fmt.Errorf("application body is empty; run applications prepare first")
	}
	if strings.TrimSpace(app.CVProfile) == "" {
		return DraftPayload{}, fmt.Errorf("application CV profile is empty; configure and prepare a CV profile first")
	}

	var cvPath string
	for _, p := range settings.CVProfiles {
		if strings.EqualFold(strings.TrimSpace(p.ID), strings.TrimSpace(app.CVProfile)) {
			cvPath = strings.TrimSpace(p.Path)
			break
		}
	}
	if cvPath == "" {
		return DraftPayload{}, fmt.Errorf("CV profile %q has no configured path", app.CVProfile)
	}
	info, err := os.Stat(cvPath)
	if err != nil {
		return DraftPayload{}, fmt.Errorf("CV file %q is not accessible: %w", cvPath, err)
	}
	if info.IsDir() {
		return DraftPayload{}, fmt.Errorf("CV path %q is a directory", cvPath)
	}

	return DraftPayload{
		JobID:           app.JobID,
		To:              strings.TrimSpace(app.Recipient),
		Subject:         strings.TrimSpace(app.Subject),
		Body:            strings.TrimSpace(app.Body),
		ContentType:     "text/plain",
		CVProfile:       strings.TrimSpace(app.CVProfile),
		AttachmentFiles: []string{cvPath},
		AttachmentNames: []string{filepath.Base(cvPath)},
	}, nil
}
