package gmailclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"linkedin-jobs/internal/config"
)

const GmailComposeScope = "https://www.googleapis.com/auth/gmail.compose"

type Credentials struct {
	ClientID     string
	ClientSecret string
	AuthURI      string
	TokenURI     string
}

type credentialsEnvelope struct {
	Installed *credentialsJSON `json:"installed"`
	Web       *credentialsJSON `json:"web"`
}

type credentialsJSON struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	AuthURI      string   `json:"auth_uri"`
	TokenURI     string   `json:"token_uri"`
	RedirectURIs []string `json:"redirect_uris"`
}

type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	Expiry       time.Time `json:"expiry"`
}

type OAuthStart struct {
	State       string
	Verifier    string
	RedirectURI string
	AuthURL     string
	ExpiresAt   time.Time
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func CredentialsPath() string {
	if p := strings.TrimSpace(os.Getenv("LJ_GMAIL_CREDENTIALS_FILE")); p != "" {
		return p
	}
	return filepath.Join(config.HomeDir(), "gmail-credentials.json")
}

func TokenPath() string {
	if p := strings.TrimSpace(os.Getenv("LJ_GMAIL_TOKEN_FILE")); p != "" {
		return p
	}
	return filepath.Join(config.HomeDir(), "gmail-token.json")
}

func LoadCredentials(path string) (Credentials, error) {
	if strings.TrimSpace(path) == "" {
		path = CredentialsPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	var env credentialsEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Credentials{}, fmt.Errorf("parse Gmail OAuth credentials: %w", err)
	}
	raw := env.Installed
	if raw == nil {
		raw = env.Web
	}
	if raw == nil || strings.TrimSpace(raw.ClientID) == "" {
		return Credentials{}, fmt.Errorf("credentials file has no installed/web OAuth client")
	}
	out := Credentials{
		ClientID:     strings.TrimSpace(raw.ClientID),
		ClientSecret: strings.TrimSpace(raw.ClientSecret),
		AuthURI:      strings.TrimSpace(raw.AuthURI),
		TokenURI:     strings.TrimSpace(raw.TokenURI),
	}
	if out.AuthURI == "" {
		out.AuthURI = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if out.TokenURI == "" {
		out.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return out, nil
}

func NewOAuthStart(creds Credentials, redirectURI string) (OAuthStart, error) {
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return OAuthStart{}, fmt.Errorf("redirect URI is required")
	}
	state, err := randomURLToken(32)
	if err != nil {
		return OAuthStart{}, err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return OAuthStart{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	q := url.Values{}
	q.Set("client_id", creds.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", GmailComposeScope)
	q.Set("access_type", "offline")
	q.Set("include_granted_scopes", "true")
	q.Set("prompt", "consent")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")

	return OAuthStart{
		State:       state,
		Verifier:    verifier,
		RedirectURI: redirectURI,
		AuthURL:     creds.AuthURI + "?" + q.Encode(),
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}, nil
}

func ExchangeCode(ctx context.Context, client *http.Client, creds Credentials, code, verifier, redirectURI string) (Token, error) {
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{}
	form.Set("client_id", creds.ClientID)
	if creds.ClientSecret != "" {
		form.Set("client_secret", creds.ClientSecret)
	}
	form.Set("code", strings.TrimSpace(code))
	form.Set("code_verifier", strings.TrimSpace(verifier))
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", strings.TrimSpace(redirectURI))

	resp, err := postForm(ctx, client, creds.TokenURI, form)
	if err != nil {
		return Token{}, err
	}
	if resp.Error != "" {
		return Token{}, fmt.Errorf("Google OAuth token exchange failed: %s %s", resp.Error, resp.ErrorDesc)
	}
	if resp.AccessToken == "" {
		return Token{}, fmt.Errorf("Google OAuth token exchange returned no access token")
	}
	return Token{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
		Scope:        resp.Scope,
		Expiry:       time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second),
	}, nil
}

func LoadToken(path string) (Token, error) {
	if strings.TrimSpace(path) == "" {
		path = TokenPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Token{}, err
	}
	var tok Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return Token{}, fmt.Errorf("parse Gmail OAuth token: %w", err)
	}
	return tok, nil
}

func SaveToken(path string, tok Token) error {
	if strings.TrimSpace(path) == "" {
		path = TokenPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func AccessToken(ctx context.Context, client *http.Client, creds Credentials, tokenPath string) (string, error) {
	tok, err := LoadToken(tokenPath)
	if err != nil {
		return "", err
	}
	if tok.AccessToken != "" && (tok.Expiry.IsZero() || time.Until(tok.Expiry) > time.Minute) {
		return tok.AccessToken, nil
	}
	if strings.TrimSpace(tok.RefreshToken) == "" {
		return "", fmt.Errorf("Gmail OAuth token expired and has no refresh token")
	}
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{}
	form.Set("client_id", creds.ClientID)
	if creds.ClientSecret != "" {
		form.Set("client_secret", creds.ClientSecret)
	}
	form.Set("refresh_token", tok.RefreshToken)
	form.Set("grant_type", "refresh_token")
	resp, err := postForm(ctx, client, creds.TokenURI, form)
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("Google OAuth refresh failed: %s %s", resp.Error, resp.ErrorDesc)
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("Google OAuth refresh returned no access token")
	}
	tok.AccessToken = resp.AccessToken
	tok.TokenType = resp.TokenType
	tok.Scope = resp.Scope
	tok.Expiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	if err := SaveToken(tokenPath, tok); err != nil {
		return "", fmt.Errorf("save refreshed Gmail token: %w", err)
	}
	return tok.AccessToken, nil
}

func randomURLToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func postForm(ctx context.Context, client *http.Client, endpoint string, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, err
	}
	var out tokenResponse
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if out.Error != "" {
			return out, nil
		}
		return tokenResponse{}, fmt.Errorf("Google OAuth HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bytes.TrimSpace(body))))
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return tokenResponse{}, fmt.Errorf("decode Google OAuth response: %w", err)
	}
	return out, nil
}
