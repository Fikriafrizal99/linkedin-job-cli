package linkedin

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"linkedin-jobs/internal/auth"
	"linkedin-jobs/internal/config"
)

// Client is the LinkedIn HTTP client. Anonymous calls (search/detail) need no
// session; authenticated calls (recommended) require one.
type Client struct {
	cfg     config.Config
	http    *http.Client
	session *auth.Session
}

// New constructs a Client with the given config. WithSession should be called
// separately if authenticated calls are needed.
func New(cfg config.Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second},
	}
}

// WithSession attaches a resolved LinkedIn session for authenticated calls.
func (c *Client) WithSession(s *auth.Session) *Client {
	c.session = s
	return c
}

// HasSession reports whether an authenticated session is available.
func (c *Client) HasSession() bool { return c.session != nil && c.session.CookieHeader != "" }

// ErrAuthRequired is returned when an authenticated call is made without a session.
var ErrAuthRequired = errors.New("authenticated call requires a LinkedIn session: run `linkedin-jobs auth login` to capture one")

var sessionProbeEndpoint = "https://www.linkedin.com/voyager/api/me"

// ProbeSession performs one lightweight authenticated Voyager request without
// following redirects. It distinguishes a structurally complete cookie set
// from a session LinkedIn actually accepts. It does not attempt to bypass
// login/challenge redirects.
func (c *Client) ProbeSession() error {
	if !c.HasSession() || c.session == nil || !c.session.Valid() {
		return ErrAuthRequired
	}

	req, err := http.NewRequest(http.MethodGet, sessionProbeEndpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/vnd.linkedin.normalized+json+2.1")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cookie", c.session.CookieHeader)
	req.Header.Set("Csrf-Token", c.session.CSRFToken)
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	req.Header.Set("Referer", "https://www.linkedin.com/feed/")

	probeClient := *c.http
	probeClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc := resp.Header.Get("Location")
		if loc == "" {
			loc = "(redirect target hidden)"
		}
		return errf("LinkedIn redirected the authenticated session to %s; import the full LinkedIn Cookie header from an active browser session", loc)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errf("LinkedIn rejected the authenticated session (status %d)", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return errf("LinkedIn rate-limited the session probe (status 429); try again later")
	default:
		return errf("LinkedIn session probe returned status %d", resp.StatusCode)
	}
}

// get fetches a URL with browser-like headers, optionally authenticated.
func (c *Client) get(rawURL string, authenticated bool, extra http.Header) (string, http.Header, int, error) {
	maxAttempts := c.cfg.HTTPMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return "", nil, 0, err
		}
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,application/json,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		if extra != nil {
			for k, vs := range extra {
				for _, v := range vs {
					req.Header.Set(k, v)
				}
			}
		}
		if authenticated {
			if !c.HasSession() {
				return "", nil, 0, ErrAuthRequired
			}
			req.Header.Set("Cookie", c.session.CookieHeader)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts {
				sleep(c.retryBackoff(attempt))
				continue
			}
			return "", nil, 0, err
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < maxAttempts {
				sleep(c.retryBackoff(attempt))
				continue
			}
			return "", resp.Header, resp.StatusCode, readErr
		}

		status := resp.StatusCode
		if status == http.StatusForbidden {
			// Treat 403 as a hard block for this request; retrying immediately only
			// increases pressure and is unlikely to change the outcome.
			return string(body), resp.Header, status, nil
		}

		if status == http.StatusTooManyRequests && attempt < maxAttempts {
			if d, ok := retryAfterSeconds(resp.Header.Get("Retry-After")); ok {
				sleep(d)
			} else {
				sleep(c.retryBackoff(attempt))
			}
			continue
		}

		if isTransientHTTPStatus(status) && attempt < maxAttempts {
			sleep(c.retryBackoff(attempt))
			continue
		}

		return string(body), resp.Header, status, nil
	}

	return "", nil, 0, lastErr
}

func (c *Client) retryBackoff(attempt int) float64 {
	base := c.cfg.HTTPRetryBaseSeconds
	if base < 0 {
		base = 0
	}
	if attempt < 1 {
		attempt = 1
	}
	// Exponential backoff: base, 2*base, 4*base...
	multiplier := 1 << uint(attempt-1)
	return base * float64(multiplier)
}

func isTransientHTTPStatus(status int) bool {
	switch status {
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryAfterSeconds(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		if seconds > 60 {
			seconds = 60
		}
		return float64(seconds), true
	}
	if when, err := http.ParseTime(raw); err == nil {
		d := time.Until(when)
		if d < 0 {
			d = 0
		}
		if d > 60*time.Second {
			d = 60 * time.Second
		}
		return d.Seconds(), true
	}
	return 0, false
}

// getJSON is like get but sets an application/json accept header.
func (c *Client) getJSON(url string, authenticated bool, extra http.Header) (string, int, error) {
	if extra == nil {
		extra = http.Header{}
	}
	extra.Set("Accept", "application/json")
	body, _, status, err := c.get(url, authenticated, extra)
	return body, status, err
}

// cleanURL strips tracking query params from a LinkedIn job URL.
func cleanURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}
