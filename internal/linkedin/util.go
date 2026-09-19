package linkedin

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func urlEncode(s string) string { return url.QueryEscape(s) }

func itoa(n int) string { return strconv.Itoa(n) }

func sleep(sec float64) {
	if sec <= 0 {
		return
	}
	time.Sleep(time.Duration(sec * float64(time.Second)))
}

func errf(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}

var tagRE = regexp.MustCompile(`<[^>]*>`)

// cleanHTMLText decodes HTML entities, strips tags, and collapses whitespace.
// The JSON-LD JobPosting description is HTML-escaped HTML, so entities must be
// decoded before tags are stripped.
func cleanHTMLText(s string) string {
	// LinkedIn descriptions can arrive double-encoded (e.g. "&amp;amp;").
	// Decode a few bounded rounds until the text stabilizes.
	for i := 0; i < 3; i++ {
		next := html.UnescapeString(s)
		if next == s {
			break
		}
		s = next
	}
	s = tagRE.ReplaceAllString(s, "\n")
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(t)
		}
	}
	return b.String()
}

// DetectRemote infers remote/hybrid/onsite from free text.
func DetectRemote(text string) string {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "hybrid"):
		return "hybrid"
	case strings.Contains(t, "remote"):
		return "remote"
	case strings.Contains(t, "on-site") || strings.Contains(t, "onsite"):
		return "onsite"
	}
	return "unknown"
}
