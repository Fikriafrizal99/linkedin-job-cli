package gmailclient

import (
	"context"
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
)

func TestNewOAuthStartUsesComposeScopeAndPKCE(t *testing.T) {
	start, err := NewOAuthStart(Credentials{
		ClientID: "client-id",
		AuthURI: "https://accounts.example/auth",
		TokenURI: "https://accounts.example/token",
	}, "http://127.0.0.1:8080/app/gmail/oauth/callback")
	if err != nil {
		t.Fatalf("NewOAuthStart: %v", err)
	}
	u, err := url.Parse(start.AuthURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := u.Query()
	if q.Get("scope") != GmailComposeScope {
		t.Fatalf("scope=%q", q.Get("scope"))
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("PKCE missing: %s", start.AuthURL)
	}
	if q.Get("access_type") != "offline" || q.Get("state") != start.State || start.Verifier == "" {
		t.Fatalf("unexpected OAuth start: %+v", start)
	}
}

func TestExchangeCodeAndRefresh(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code_verifier") != "verifier" {
				t.Errorf("verifier=%q", r.Form.Get("code_verifier"))
			}
			io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600,"token_type":"Bearer","scope":"https://www.googleapis.com/auth/gmail.compose"}`)
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh-1" {
				t.Errorf("refresh=%q", r.Form.Get("refresh_token"))
			}
			io.WriteString(w, `{"access_token":"access-2","expires_in":3600,"token_type":"Bearer"}`)
		default:
			http.Error(w, "bad grant", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	creds := Credentials{ClientID: "id", ClientSecret: "secret", TokenURI: srv.URL}
	tok, err := ExchangeCode(context.Background(), srv.Client(), creds, "code", "verifier", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "access-1" || tok.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected token: %+v", tok)
	}

	tokenPath := filepath.Join(t.TempDir(), "token.json")
	tok.Expiry = time.Now().Add(-time.Minute)
	if err := SaveToken(tokenPath, tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	access, err := AccessToken(context.Background(), srv.Client(), creds, tokenPath)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if access != "access-2" {
		t.Fatalf("access=%q", access)
	}
	saved, err := LoadToken(tokenPath)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if saved.RefreshToken != "refresh-1" || saved.AccessToken != "access-2" {
		t.Fatalf("refresh token not preserved: %+v", saved)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestLoadCredentialsInstalled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	data := map[string]any{
		"installed": map[string]any{
			"client_id": "client",
			"client_secret": "secret",
			"auth_uri": "https://accounts.example/auth",
			"token_uri": "https://accounts.example/token",
		},
	}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCredentials(path)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if got.ClientID != "client" || got.ClientSecret != "secret" {
		t.Fatalf("got %+v", got)
	}
}

func TestSaveTokenUsesPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "token.json")
	if err := SaveToken(path, Token{AccessToken: "secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token permissions too open: %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "access_token") {
		t.Fatal("token file malformed")
	}
}
