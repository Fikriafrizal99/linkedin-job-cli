package linkedin

import (
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"linkedin-jobs/internal/parser"
)

type applyControl struct {
	Method      string
	URL         string
	Instruction string
}

// extractApplyControl reads LinkedIn's public job-page apply control.
//
// The guest page currently distinguishes LinkedIn-hosted apply from off-site
// apply through data-tracking-control-name values. We treat these as hints from
// the page itself, not as a reason to automate an application.
func extractApplyControl(doc *goquery.Document) applyControl {
	if doc == nil {
		return applyControl{Method: parser.ApplicationMethodUnknown}
	}

	// External/company-site apply. Prefer a real href when LinkedIn exposes it.
	for _, sel := range []string{
		`a[data-tracking-control-name="public_jobs_apply-link-offsite"]`,
		`a[data-is-offsite-apply="true"]`,
	} {
		if a := doc.Find(sel).First(); a.Length() > 0 {
			href, _ := a.Attr("href")
			return applyControl{
				Method:      parser.ApplicationMethodExternalURL,
				URL:         unwrapExternalApplyURL(strings.TrimSpace(href)),
				Instruction: applyControlText(a, "Apply on company website"),
			}
		}
	}

	// Some public variants render off-site apply as a button that opens a
	// sign-in/modal first, so no destination URL is available anonymously.
	if b := doc.Find(`[data-tracking-control-name="public_jobs_apply-link-offsite"]`).First(); b.Length() > 0 {
		return applyControl{
			Method:      parser.ApplicationMethodExternalURL,
			Instruction: applyControlText(b, "Apply on company website"),
		}
	}

	// LinkedIn-hosted application / Easy Apply.
	if b := doc.Find(`[data-tracking-control-name="public_jobs_apply-link-onsite"]`).First(); b.Length() > 0 {
		return applyControl{
			Method:      parser.ApplicationMethodLinkedIn,
			Instruction: applyControlText(b, "LinkedIn Apply"),
		}
	}

	// Authenticated/newer UI fallbacks. Require a strong textual or URL signal
	// so a generic "Apply" button is not mislabeled as Easy Apply.
	var fallback *goquery.Selection
	doc.Find(`.jobs-apply-button, [aria-label]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		aria, _ := s.Attr("aria-label")
		href, _ := s.Attr("href")
		blob := strings.ToLower(strings.TrimSpace(aria + " " + s.Text() + " " + href))
		if strings.Contains(blob, "easy apply") ||
			strings.Contains(blob, "linkedin apply") ||
			strings.Contains(blob, "opensduiapplyflow=true") {
			fallback = s
			return false
		}
		return true
	})
	if fallback != nil && fallback.Length() > 0 {
		return applyControl{
			Method:      parser.ApplicationMethodLinkedIn,
			Instruction: applyControlText(fallback, "LinkedIn Apply"),
		}
	}

	return applyControl{Method: parser.ApplicationMethodUnknown}
}


func resolveApplicationData(app parser.ApplicationData, page applyControl) parser.ApplicationData {
	// Employer-provided email in the description is the strongest signal.
	if app.Method == parser.ApplicationMethodEmail {
		return app
	}
	if page.Method == "" || page.Method == parser.ApplicationMethodUnknown {
		return app
	}

	resolved := app
	resolved.Method = page.Method
	resolved.ApplyURL = page.URL
	resolved.Instruction = page.Instruction

	// Public/off-site controls sometimes prove that apply is external without
	// exposing the final destination until sign-in. Preserve a deterministic
	// URL found in the description in that case.
	if resolved.ApplyURL == "" && page.Method == parser.ApplicationMethodExternalURL &&
		app.Method == parser.ApplicationMethodExternalURL {
		resolved.ApplyURL = app.ApplyURL
		resolved.Instruction = app.Instruction
	}
	return resolved
}

func applyControlText(s *goquery.Selection, fallback string) string {
	if s == nil || s.Length() == 0 {
		return fallback
	}
	if aria, ok := s.Attr("aria-label"); ok && strings.TrimSpace(aria) != "" {
		return strings.Join(strings.Fields(aria), " ")
	}
	if txt := strings.Join(strings.Fields(s.Text()), " "); txt != "" {
		return txt
	}
	return fallback
}

// unwrapExternalApplyURL extracts the real company URL from LinkedIn's
// /jobs/view/externalApply/... wrapper when the public page includes ?url=.
// If no inner URL is available, the original href is retained as a navigable
// fallback rather than inventing a destination.
func unwrapExternalApplyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if inner := strings.TrimSpace(u.Query().Get("url")); inner != "" {
		if parsed, err := url.Parse(inner); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
			return parsed.String()
		}
	}
	return raw
}
