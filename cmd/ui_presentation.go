package cmd

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"linkedin-jobs/internal/models"
)

var errAppNotFound = errors.New("page not found")

// Labels are presentation only. Persisted lifecycle and method values stay intact.
func applicationStateLabel(state string) string {
	labels := map[string]string{
		"NOT_APPLIED": "Not queued", "READY_EMAIL": "Ready for Email",
		"READY_EASY_APPLY": "Ready for Easy Apply", "IN_PROGRESS": "In progress",
		"NEED_REVIEW": "Needs attention", "DRAFT_CREATED": "Draft Ready",
		"APPROVED": "Approved", "APPLIED": "Applied", "SENT": "Sent", "FAILED": "Failed",
	}
	if label, ok := labels[state]; ok {
		return label
	}
	return state
}

func applicationChannelLabel(method string) string {
	switch applicationMethodLabel(method) {
	case "EMAIL":
		return "Email"
	case "EASY_APPLY":
		return "Easy Apply"
	case "EXTERNAL_URL":
		return "External website"
	case "UNKNOWN", "":
		return "Unknown method"
	default:
		return method
	}
}

func applicationNextStep(a *models.JobApplication) string {
	if a == nil {
		return "Select jobs to start an application."
	}
	switch a.State {
	case models.ApplicationStateReadyEmail:
		if applicationIsPrepared(a) {
			return "Email prepared. Check the CV and supporting files, then create a Gmail draft."
		}
		return "Choose a CV and prepare the email. You can review the content before creating a Gmail draft."
	case models.ApplicationStateDraftCreated:
		return "Review the actual Gmail draft, including attachments, then approve it here. Approval does not send email."
	case models.ApplicationStateApproved:
		return "Review complete. Continue to the separate send confirmation when you are ready."
	case models.ApplicationStateReadyEasyApply:
		return "Open LinkedIn, submit the application yourself, then return here to confirm."
	case models.ApplicationStateInProgress:
		return "LinkedIn has been opened. Submit manually, return here, and confirm only after submission."
	case models.ApplicationStateNeedReview:
		return "This record needs a verified application destination. Check the original job. If its method is now supported, remove it from the queue and queue it again from Jobs."
	case models.ApplicationStateApplied:
		return "You confirmed manual submission on LinkedIn. This application is complete."
	case models.ApplicationStateSent:
		return "Email sent. This application is complete."
	default:
		return "Check the application details before continuing."
	}
}

const appPageSize = 50

func appListURL(path string, q url.Values, page int) string {
	out := url.Values{}
	keys := []string{"q", "location", "method", "state", "since"}
	if strings.HasPrefix(path, "/app/jobs") {
		keys = append(keys, "review", "run")
	}
	if strings.HasPrefix(path, "/app/applications") {
		keys = append(keys, "scope")
	}
	for _, key := range keys {
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			out.Set(key, v)
		}
	}
	if page > 1 {
		out.Set("page", strconv.Itoa(page))
	}
	if len(out) == 0 {
		return path
	}
	return path + "?" + out.Encode()
}

func appDetailURL(prefix, id, listURL string) string {
	target := prefix + url.PathEscape(id)
	u, err := url.Parse(listURL)
	if err == nil && u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target
}
