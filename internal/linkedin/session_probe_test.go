package linkedin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkedin-jobs/internal/auth"
	"linkedin-jobs/internal/config"
)

func TestProbeSessionAccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "li_at=test") {
			t.Fatalf("cookie=%q", got)
		}
		if got := r.Header.Get("Csrf-Token"); got != "ajax:123" {
			t.Fatalf("csrf=%q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	old := sessionProbeEndpoint
	sessionProbeEndpoint = server.URL
	t.Cleanup(func() { sessionProbeEndpoint = old })

	cfg := config.Load()
	c := New(cfg).WithSession(&auth.Session{
		CookieHeader: `li_at=test; JSESSIONID="ajax:123"; bcookie=x`,
		CSRFToken:    "ajax:123",
		Source:       "test",
	})
	if err := c.ProbeSession(); err != nil {
		t.Fatalf("ProbeSession: %v", err)
	}
}

func TestProbeSessionStopsAtRedirect(t *testing.T) {
	target := "https://www.linkedin.com/login"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer server.Close()

	old := sessionProbeEndpoint
	sessionProbeEndpoint = server.URL
	t.Cleanup(func() { sessionProbeEndpoint = old })

	cfg := config.Load()
	c := New(cfg).WithSession(&auth.Session{
		CookieHeader: `li_at=test; JSESSIONID="ajax:123"`,
		CSRFToken:    "ajax:123",
		Source:       "test",
	})
	err := c.ProbeSession()
	if err == nil {
		t.Fatal("expected redirect error")
	}
	if !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("unexpected error: %v", err)
	}
}
