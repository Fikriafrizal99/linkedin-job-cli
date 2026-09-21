package gmailclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSendDraftPostsDraftID(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(tokenPath, Token{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		Expiry:       time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization=%q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["id"] != "draft-123" {
			t.Errorf("draft id=%q", body["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg-123","threadId":"thread-123"}`)
	}))
	defer srv.Close()

	got, err := sendDraftAt(context.Background(), srv.Client(), Credentials{}, tokenPath, "draft-123", srv.URL)
	if err != nil {
		t.Fatalf("sendDraftAt: %v", err)
	}
	if got.MessageID != "msg-123" || got.ThreadID != "thread-123" {
		t.Fatalf("got %+v", got)
	}
}

func TestSendDraftRequiresDraftID(t *testing.T) {
	if _, err := sendDraftAt(context.Background(), http.DefaultClient, Credentials{}, "", "", "http://example.invalid"); err == nil {
		t.Fatal("expected missing draft id error")
	}
}
