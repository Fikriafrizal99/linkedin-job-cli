package gmailclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"

	appengine "linkedin-jobs/internal/application"
)

type DraftResult struct {
	DraftID   string `json:"draft_id"`
	MessageID string `json:"message_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
}

type gmailDraftResponse struct {
	ID      string `json:"id"`
	Message struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	} `json:"message"`
}

func CreateDraft(ctx context.Context, client *http.Client, creds Credentials, tokenPath string, payload appengine.DraftPayload) (DraftResult, error) {
	return createDraftAt(ctx, client, creds, tokenPath, payload, "https://gmail.googleapis.com/gmail/v1/users/me/drafts")
}

func createDraftAt(ctx context.Context, client *http.Client, creds Credentials, tokenPath string, payload appengine.DraftPayload, endpoint string) (DraftResult, error) {
	if client == nil {
		client = http.DefaultClient
	}
	raw, err := BuildMIME(payload)
	if err != nil {
		return DraftResult{}, err
	}
	accessToken, err := AccessToken(ctx, client, creds, tokenPath)
	if err != nil {
		return DraftResult{}, err
	}

	body, err := json.Marshal(map[string]any{
		"message": map[string]string{
			"raw": base64.RawURLEncoding.EncodeToString(raw),
		},
	})
	if err != nil {
		return DraftResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return DraftResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return DraftResult{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return DraftResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DraftResult{}, fmt.Errorf("Gmail drafts.create HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out gmailDraftResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return DraftResult{}, fmt.Errorf("decode Gmail draft response: %w", err)
	}
	if strings.TrimSpace(out.ID) == "" {
		return DraftResult{}, fmt.Errorf("Gmail drafts.create returned no draft id")
	}
	return DraftResult{
		DraftID:   out.ID,
		MessageID: out.Message.ID,
		ThreadID:  out.Message.ThreadID,
	}, nil
}

func BuildMIME(payload appengine.DraftPayload) ([]byte, error) {
	if strings.TrimSpace(payload.To) == "" {
		return nil, fmt.Errorf("draft recipient is empty")
	}
	if strings.TrimSpace(payload.Subject) == "" {
		return nil, fmt.Errorf("draft subject is empty")
	}
	if strings.TrimSpace(payload.Body) == "" {
		return nil, fmt.Errorf("draft body is empty")
	}
	if len(payload.AttachmentFiles) == 0 {
		return nil, fmt.Errorf("draft has no attachment")
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	textHeader.Set("Content-Transfer-Encoding", "8bit")
	textPart, err := mw.CreatePart(textHeader)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(textPart, payload.Body); err != nil {
		return nil, err
	}

	for _, path := range payload.AttachmentFiles {
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, fmt.Errorf("attachment path is empty")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read attachment %q: %w", path, err)
		}
		filename := filepath.Base(path)
		contentType := mime.TypeByExtension(filepath.Ext(filename))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", contentType+"; name="+fmt.Sprintf("%q", filename))
		h.Set("Content-Disposition", "attachment; filename="+fmt.Sprintf("%q", filename))
		h.Set("Content-Transfer-Encoding", "base64")
		part, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		enc := base64.NewEncoder(base64.StdEncoding, part)
		if _, err := enc.Write(data); err != nil {
			enc.Close()
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "To: %s\r\n", payload.To)
	fmt.Fprintf(&msg, "Subject: %s\r\n", payload.Subject)
	fmt.Fprint(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%q\r\n", mw.Boundary())
	fmt.Fprint(&msg, "\r\n")
	msg.Write(body.Bytes())
	return msg.Bytes(), nil
}
