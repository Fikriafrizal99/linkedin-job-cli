package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"linkedin-jobs/internal/models"
)

func writeTestGmailCredentials(t *testing.T, path, authURI, tokenURI string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"installed": map[string]any{
			"client_id": "client-id",
			"client_secret": "client-secret",
			"auth_uri": authURI,
			"token_uri": tokenURI,
		},
	})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGmailRedirectURIRequiresLoopback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/app/settings", nil)
	got, err := gmailRedirectURI(req)
	if err != nil {
		t.Fatalf("gmailRedirectURI: %v", err)
	}
	if got != "http://127.0.0.1:8080/app/gmail/oauth/callback" {
		t.Fatalf("got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com:8080/app/settings", nil)
	if _, err := gmailRedirectURI(req); err == nil {
		t.Fatal("expected non-loopback host rejection")
	}
}

func TestGmailConnectStartsOAuthWithCSRF(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	t.Setenv("LJ_GMAIL_CREDENTIALS_FILE", credPath)
	t.Setenv("LJ_GMAIL_TOKEN_FILE", filepath.Join(dir, "token.json"))
	writeTestGmailCredentials(t, credPath, "https://accounts.example/auth", "https://accounts.example/token")

	ws := &webServer{csrf: "csrf-test"}
	form := url.Values{"csrf": {"csrf-test"}}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/app/gmail/connect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	ws.handleGmailConnect(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://accounts.example/auth?") {
		t.Fatalf("unexpected redirect: %s", loc)
	}
	u, _ := url.Parse(loc)
	if u.Query().Get("scope") == "" || u.Query().Get("state") == "" || u.Query().Get("code_challenge") == "" {
		t.Fatalf("OAuth redirect missing required params: %s", loc)
	}
	if len(ws.gmailOAuth) != 1 {
		t.Fatalf("pending OAuth states=%d", len(ws.gmailOAuth))
	}
}

func TestGmailOAuthCallbackStoresToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") != "verifier" {
			t.Fatalf("unexpected token form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()

	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	tokenPath := filepath.Join(dir, "token.json")
	t.Setenv("LJ_GMAIL_CREDENTIALS_FILE", credPath)
	t.Setenv("LJ_GMAIL_TOKEN_FILE", tokenPath)
	writeTestGmailCredentials(t, credPath, "https://accounts.example/auth", tokenServer.URL)

	ws := &webServer{
		gmailOAuth: map[string]gmailOAuthPending{
			"state-1": {
				Verifier: "verifier",
				RedirectURI: "http://127.0.0.1:8080/app/gmail/oauth/callback",
				ExpiresAt: time.Now().Add(time.Minute),
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/app/gmail/oauth/callback?state=state-1&code=code-1", nil)
	rec := httptest.NewRecorder()
	ws.handleGmailOAuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "gmail=connected") {
		t.Fatalf("redirect=%q", rec.Header().Get("Location"))
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if !strings.Contains(string(data), "refresh") {
		t.Fatalf("token not persisted: %s", data)
	}
}

func TestApplicationDetailRendersGmailDraftActionOnlyWhenConnected(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	app := &models.JobApplication{
		JobID: "50010",
		State: models.ApplicationStateReadyEmail,
		Recipient: "jobs@example.com",
		Subject: "Application - Sales Executive",
		Body: "Dear Hiring Team",
		CVProfile: "general",
	}

	render := func(connected bool) string {
		var b strings.Builder
		err := tpl.Execute(&b, appPageData{
			Title: "Application Detail",
			Active: "applications",
			CSRF: "csrf",
			CandidateName: "Candidate",
			CandidateInitials: "C",
			SelectedApplication: app,
			SelectedCVPath: "/tmp/CV.pdf",
			SelectedCVReady: true,
			GmailConnected: connected,
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return b.String()
	}

	connected := render(true)
	if !strings.Contains(connected, `action="/app/applications/50010/draft"`) ||
		!strings.Contains(connected, "Create Gmail Draft") {
		t.Fatal("connected application detail missing draft action")
	}
	disconnected := render(false)
	if strings.Contains(disconnected, `action="/app/applications/50010/draft"`) ||
		!strings.Contains(disconnected, "Connect Gmail in Settings") {
		t.Fatal("disconnected application detail should require Gmail connection")
	}
}


func TestApplicationDetailBlocksGmailDraftWhenCVFileMissing(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	app := &models.JobApplication{
		JobID: "50011",
		State: models.ApplicationStateReadyEmail,
		Recipient: "jobs@example.com",
		Subject: "Application",
		Body: "Body",
		CVProfile: "general",
	}
	var b strings.Builder
	if err := tpl.Execute(&b, appPageData{
		Title: "Application Detail",
		Active: "applications",
		CSRF: "csrf",
		CandidateName: "Candidate",
		CandidateInitials: "C",
		SelectedApplication: app,
		SelectedCVPath: "/missing/CV.pdf",
		SelectedCVReady: false,
		GmailConnected: true,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := b.String()
	if strings.Contains(out, `action="/app/applications/50011/draft"`) ||
		!strings.Contains(out, "selected CV file is not accessible") {
		t.Fatal("missing CV must block Gmail draft creation")
	}
}
