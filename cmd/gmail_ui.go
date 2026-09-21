package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	appengine "linkedin-jobs/internal/application"
	"linkedin-jobs/internal/config"
	gmailclient "linkedin-jobs/internal/gmailclient"
)

type gmailOAuthPending struct {
	Verifier    string
	RedirectURI string
	ExpiresAt   time.Time
}

type gmailUIState struct {
	CredentialsPath  string
	TokenPath        string
	CredentialsFound bool
	Connected        bool
}

func currentGmailUIState() gmailUIState {
	out := gmailUIState{
		CredentialsPath: gmailclient.CredentialsPath(),
		TokenPath:       gmailclient.TokenPath(),
	}
	if _, err := os.Stat(out.CredentialsPath); err == nil {
		out.CredentialsFound = true
	}
	if out.CredentialsFound {
		if tok, err := gmailclient.LoadToken(out.TokenPath); err == nil {
			hasRefresh := strings.TrimSpace(tok.RefreshToken) != ""
			hasLiveAccess := strings.TrimSpace(tok.AccessToken) != "" &&
				(tok.Expiry.IsZero() || time.Until(tok.Expiry) > 0)
			out.Connected = hasRefresh || hasLiveAccess
		}
	}
	return out
}

func (ws *webServer) handleGmailConnect(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	creds, err := gmailclient.LoadCredentials("")
	if err != nil {
		redirectGmailSettings(w, r, fmt.Errorf("load Gmail OAuth credentials: %w", err), "")
		return
	}
	redirectURI, err := gmailRedirectURI(r)
	if err != nil {
		redirectGmailSettings(w, r, err, "")
		return
	}
	start, err := gmailclient.NewOAuthStart(creds, redirectURI)
	if err != nil {
		redirectGmailSettings(w, r, err, "")
		return
	}

	ws.gmailOAuthMu.Lock()
	if ws.gmailOAuth == nil {
		ws.gmailOAuth = map[string]gmailOAuthPending{}
	}
	for state, pending := range ws.gmailOAuth {
		if time.Now().After(pending.ExpiresAt) {
			delete(ws.gmailOAuth, state)
		}
	}
	ws.gmailOAuth[start.State] = gmailOAuthPending{
		Verifier:    start.Verifier,
		RedirectURI: start.RedirectURI,
		ExpiresAt:   start.ExpiresAt,
	}
	ws.gmailOAuthMu.Unlock()

	http.Redirect(w, r, start.AuthURL, http.StatusSeeOther)
}

func (ws *webServer) handleGmailOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if oauthErr := strings.TrimSpace(r.URL.Query().Get("error")); oauthErr != "" {
		redirectGmailSettings(w, r, fmt.Errorf("Google authorization failed: %s", oauthErr), "")
		return
	}
	if state == "" || code == "" {
		redirectGmailSettings(w, r, fmt.Errorf("Google OAuth callback is missing state or code"), "")
		return
	}

	ws.gmailOAuthMu.Lock()
	pending, ok := ws.gmailOAuth[state]
	if ok {
		delete(ws.gmailOAuth, state)
	}
	ws.gmailOAuthMu.Unlock()
	if !ok || time.Now().After(pending.ExpiresAt) {
		redirectGmailSettings(w, r, fmt.Errorf("Google OAuth state is invalid or expired; connect again"), "")
		return
	}

	creds, err := gmailclient.LoadCredentials("")
	if err != nil {
		redirectGmailSettings(w, r, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, err := gmailclient.ExchangeCode(ctx, http.DefaultClient, creds, code, pending.Verifier, pending.RedirectURI)
	if err != nil {
		redirectGmailSettings(w, r, err, "")
		return
	}
	if tok.RefreshToken == "" {
		if previous, loadErr := gmailclient.LoadToken(""); loadErr == nil {
			tok.RefreshToken = previous.RefreshToken
		}
	}
	if err := gmailclient.SaveToken("", tok); err != nil {
		redirectGmailSettings(w, r, fmt.Errorf("save Gmail OAuth token: %w", err), "")
		return
	}
	redirectGmailSettings(w, r, nil, "connected")
}

func (ws *webServer) handleGmailDisconnect(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	if err := os.Remove(gmailclient.TokenPath()); err != nil && !os.IsNotExist(err) {
		redirectGmailSettings(w, r, err, "")
		return
	}
	redirectGmailSettings(w, r, nil, "disconnected")
}

func (ws *webServer) handleAppCreateGmailDraft(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	a, err := ws.st.GetApplicationByJobID(jobID)
	if err != nil {
		redirectApplicationAction(w, r, jobID, err)
		return
	}
	if a == nil {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("application is not queued"))
		return
	}
	settings, err := config.LoadSettings()
	if err != nil {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("load settings: %w", err))
		return
	}
	payload, err := appengine.BuildDraftPayload(a, settings.Application)
	if err != nil {
		redirectApplicationAction(w, r, jobID, err)
		return
	}
	extraPaths, err := resolveAttachmentPaths(settings.Application.Attachments, r.PostForm["attachment"])
	if err != nil {
		redirectApplicationAction(w, r, jobID, err)
		return
	}
	payload.AttachmentFiles = append(payload.AttachmentFiles, extraPaths...)
	if err := validateDraftAttachmentTotal(payload.AttachmentFiles, 18<<20); err != nil {
		redirectApplicationAction(w, r, jobID, err)
		return
	}
	creds, err := gmailclient.LoadCredentials("")
	if err != nil {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("Gmail is not configured: %w", err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result, err := gmailclient.CreateDraft(ctx, http.DefaultClient, creds, "", payload)
	if err != nil {
		_, _ = ws.st.RecordApplicationDraftError(jobID, err.Error())
		redirectApplicationAction(w, r, jobID, err)
		return
	}
	if _, err := ws.st.MarkApplicationDraftCreated(jobID, result.DraftID); err != nil {
		redirectApplicationAction(w, r, jobID, fmt.Errorf("Gmail draft %s was created, but local state update failed: %w", result.DraftID, err))
		return
	}

	q := url.Values{"draft_created": {"1"}, "draft_id": {result.DraftID}}
	http.Redirect(w, r, "/app/applications/"+url.PathEscape(jobID)+"?"+q.Encode(), http.StatusSeeOther)
}

func gmailRedirectURI(r *http.Request) (string, error) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		return "", fmt.Errorf("local web host must include a port")
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	switch host {
	case "127.0.0.1", "localhost", "::1":
	default:
		return "", fmt.Errorf("Gmail OAuth is only allowed from the local loopback UI")
	}
	if port == "" {
		return "", fmt.Errorf("local web port is missing")
	}
	return "http://127.0.0.1:" + port + "/app/gmail/oauth/callback", nil
}

func redirectGmailSettings(w http.ResponseWriter, r *http.Request, actionErr error, status string) {
	q := url.Values{"tab": {"email"}}
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("gmail_error", msg)
	}
	if status != "" {
		q.Set("gmail", status)
	}
	target := "/app/settings"
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
