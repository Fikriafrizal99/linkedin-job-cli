package parser

import (
	"net/url"
	"regexp"
	"strings"
)

const (
	ApplicationMethodEmail       = "EMAIL"
	ApplicationMethodExternalURL = "EXTERNAL_URL"
	ApplicationMethodLinkedIn    = "LINKEDIN"
	ApplicationMethodUnknown     = "UNKNOWN"
)

var (
	emailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	urlRE   = regexp.MustCompile(`https?://[^\s<>"']+`)
)

var applicationKeywords = []string{
	"apply", "application", "career", "careers", "job", "jobs",
	"recruit", "recruitment", "hiring", "submit", "send", "email",
	"cv", "resume", "lamar", "lamaran", "kirim",
}

// ApplicationData is deterministic application information explicitly present
// in a job description. It never guesses an email address or application URL.
type ApplicationData struct {
	Emails      []string
	PrimaryEmail string
	ApplyURL     string
	Method       string
	Instruction  string
}

// ExtractApplicationData finds explicit application destinations in description.
func ExtractApplicationData(description string) ApplicationData {
	d := strings.TrimSpace(description)
	out := ApplicationData{Method: ApplicationMethodUnknown}
	if d == "" {
		return out
	}

	out.Emails = uniqueEmails(emailRE.FindAllString(d, -1))
	if len(out.Emails) > 0 {
		out.PrimaryEmail = choosePrimaryEmail(d, out.Emails)
		out.Method = ApplicationMethodEmail
		out.Instruction = surroundingInstruction(d, out.PrimaryEmail)
		return out
	}

	for _, raw := range urlRE.FindAllString(d, -1) {
		candidate := cleanURLToken(raw)
		if candidate == "" || isLinkedInURL(candidate) {
			continue
		}
		if !hasApplicationContext(d, raw) {
			continue
		}
		out.ApplyURL = candidate
		out.Method = ApplicationMethodExternalURL
		out.Instruction = surroundingInstruction(d, raw)
		return out
	}

	lower := strings.ToLower(d)
	for _, phrase := range []string{"easy apply", "apply on linkedin", "apply via linkedin"} {
		if idx := strings.Index(lower, phrase); idx >= 0 {
			out.Method = ApplicationMethodLinkedIn
			out.Instruction = surroundingInstruction(d, d[idx:idx+len(phrase)])
			return out
		}
	}

	return out
}

func uniqueEmails(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

func choosePrimaryEmail(text string, emails []string) string {
	if len(emails) == 0 {
		return ""
	}
	best := emails[0]
	bestScore := -1
	lower := strings.ToLower(text)
	for _, email := range emails {
		idx := strings.Index(lower, strings.ToLower(email))
		if idx < 0 {
			continue
		}
		ctx := contextWindow(lower, idx, len(email), 140)
		score := 0
		for _, kw := range applicationKeywords {
			if strings.Contains(ctx, kw) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			best = email
		}
	}
	return best
}

func hasApplicationContext(text, token string) bool {
	idx := strings.Index(text, token)
	if idx < 0 {
		return false
	}
	ctx := strings.ToLower(contextWindow(text, idx, len(token), 180))
	for _, kw := range applicationKeywords {
		if strings.Contains(ctx, kw) {
			return true
		}
	}
	return false
}

func surroundingInstruction(text, token string) string {
	if token == "" {
		return ""
	}
	idx := strings.Index(strings.ToLower(text), strings.ToLower(token))
	if idx < 0 {
		return ""
	}
	ctx := contextWindow(text, idx, len(token), 180)
	ctx = strings.Join(strings.Fields(ctx), " ")
	if len(ctx) > 360 {
		ctx = ctx[:360]
	}
	return strings.TrimSpace(ctx)
}

func contextWindow(text string, idx, tokenLen, radius int) string {
	start := idx - radius
	if start < 0 {
		start = 0
	}
	end := idx + tokenLen + radius
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

func cleanURLToken(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ".,;:!?)]}")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

func isLinkedInURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "linkedin.com" || strings.HasSuffix(h, ".linkedin.com")
}
