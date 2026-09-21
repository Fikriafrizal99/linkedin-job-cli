package gmailclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appengine "linkedin-jobs/internal/application"
)

func TestBuildMIMEIncludesBodyAndAttachment(t *testing.T) {
	cv := filepath.Join(t.TempDir(), "CV.pdf")
	if err := os.WriteFile(cv, []byte("%PDF-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := BuildMIME(appengine.DraftPayload{
		To: "jobs@example.com",
		Subject: "Application - Sales Executive",
		Body: "Dear Hiring Team",
		AttachmentFiles: []string{cv},
	})
	if err != nil {
		t.Fatalf("BuildMIME: %v", err)
	}
	text := string(raw)
	for _, want := range []string{"To: jobs@example.com", "Subject: Application - Sales Executive", "multipart/mixed", "Dear Hiring Team", "CV.pdf"} {
		if !strings.Contains(text, want) {
			t.Errorf("MIME missing %q", want)
		}
	}
}

func TestCreateDraftPostsBase64URLMessage(t *testing.T) {
	cv := filepath.Join(t.TempDir(), "CV.pdf")
	if err := os.WriteFile(cv, []byte("%PDF-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(tokenPath, Token{
		AccessToken: "access-token",
		RefreshToken: "refresh-token",
		Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization=%q", got)
		}
		var body struct {
			Message struct {
				Raw string `json:"raw"`
			} `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(body.Message.Raw)
		if err != nil {
			t.Fatalf("decode raw: %v", err)
		}
		if !strings.Contains(string(decoded), "jobs@example.com") || !strings.Contains(string(decoded), "CV.pdf") {
			t.Errorf("unexpected MIME: %s", decoded)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"draft-123","message":{"id":"msg-123","threadId":"thread-123"}}`)
	}))
	defer srv.Close()

	got, err := createDraftAt(context.Background(), srv.Client(), Credentials{}, tokenPath, appengine.DraftPayload{
		To: "jobs@example.com",
		Subject: "Application",
		Body: "Body",
		AttachmentFiles: []string{cv},
	}, srv.URL)
	if err != nil {
		t.Fatalf("createDraftAt: %v", err)
	}
	if got.DraftID != "draft-123" || got.MessageID != "msg-123" || got.ThreadID != "thread-123" {
		t.Fatalf("got %+v", got)
	}
}
