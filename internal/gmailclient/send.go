package gmailclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type SendResult struct {
	MessageID string `json:"message_id"`
	ThreadID  string `json:"thread_id,omitempty"`
}

type gmailSendResponse struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}

// SendDraft sends one existing Gmail draft. Callers must enforce their own
// human-review/lifecycle gate before invoking this provider action.
func SendDraft(ctx context.Context, client *http.Client, creds Credentials, tokenPath, draftID string) (SendResult, error) {
	return sendDraftAt(ctx, client, creds, tokenPath, draftID, "https://gmail.googleapis.com/gmail/v1/users/me/drafts/send")
}

func sendDraftAt(ctx context.Context, client *http.Client, creds Credentials, tokenPath, draftID, endpoint string) (SendResult, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return SendResult{}, fmt.Errorf("Gmail draft id is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	accessToken, err := AccessToken(ctx, client, creds, tokenPath)
	if err != nil {
		return SendResult{}, err
	}
	body, err := json.Marshal(map[string]string{"id": draftID})
	if err != nil {
		return SendResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return SendResult{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return SendResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SendResult{}, fmt.Errorf("Gmail drafts.send HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out gmailSendResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return SendResult{}, fmt.Errorf("decode Gmail send response: %w", err)
	}
	if strings.TrimSpace(out.ID) == "" {
		return SendResult{}, fmt.Errorf("Gmail drafts.send returned no message id")
	}
	return SendResult{MessageID: out.ID, ThreadID: out.ThreadID}, nil
}
