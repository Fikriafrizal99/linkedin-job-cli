package cmd

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

var uiFixtureAddr = flag.String("ui-fixture-addr", "", "loopback address for opt-in visual fixture")

// TestUIVisualFixture is an opt-in local browser fixture. It never reads the
// user's database, credentials or settings, and exposes no send/collector route.
// LJ_UI_AUDIT_ADDR=127.0.0.1:8081 go test ./cmd -run TestUIVisualFixture -v -timeout 2h
func TestUIVisualFixture(t *testing.T) {
	addr := *uiFixtureAddr
	if addr == "" {
		addr = os.Getenv("LJ_UI_AUDIT_ADDR")
	}
	if addr == "" {
		t.Skip("opt-in browser fixture")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("fixture must bind to 127.0.0.1")
	}
	dir := t.TempDir()
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(dir, "settings.yaml"))
	t.Setenv("LJ_GMAIL_CREDENTIALS_FILE", filepath.Join(dir, "no-credentials.json"))
	t.Setenv("LJ_GMAIL_TOKEN_FILE", filepath.Join(dir, "no-token.json"))
	cv := filepath.Join(dir, "General CV.pdf")
	if err := os.WriteFile(cv, []byte("%PDF-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := config.ApplicationSettings{CandidateName: "Alex Candidate", DefaultCVProfile: "general", CVProfiles: []config.CVProfileSettings{
		{ID: "general", Path: cv, Priority: 1}, {ID: "sales", Path: cv, Keywords: []string{"sales", "account"}, Priority: 2}, {ID: "finance", Path: filepath.Join(dir, "missing.pdf"), Priority: 1},
	}, Attachments: []config.AttachmentSettings{{ID: "portfolio", Label: "Professional Portfolio", Kind: "portfolio", Path: cv}}}
	if err := config.SaveApplicationSettings(settings); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("990%03d", i)
		method := "EMAIL"
		if i%3 == 1 {
			method = "LINKEDIN"
		}
		if i%3 == 2 {
			method = "UNKNOWN"
		}
		job := &models.JobPosting{ID: id, Title: []string{"Senior Account Executive — Enterprise Partnerships", "Product Specialist", "Business Development Manager"}[i%3], Company: fmt.Sprintf("Example Company %02d", i), Location: "Jakarta, Indonesia", URL: "https://www.linkedin.com/jobs/view/" + id, ApplicationMethod: method, Description: "About this role\n\nJoin our team to build lasting customer relationships and develop new business opportunities.\n\nResponsibilities\n• Manage enterprise accounts and consult with clients.\n• Work with product teams and report on customer outcomes.\n\nRequirements\n• Clear written communication and strong analytical skills.\n• Experience managing cross-functional projects.\n\nApply using the method listed in the original job posting.", PostedAt: "2026-09-20T08:00:00Z", SearchedAt: "2026-09-21T08:00:00Z", Source: "linkedin"}
		if i == 16 {
			job.URL = "https://example.com/not-linkedin"
		}
		if method == "EMAIL" {
			job.ApplyEmail = fmt.Sprintf("careers%02d@example.com", i)
		}
		if err := st.Upsert(job); err != nil {
			t.Fatal(err)
		}
		if i >= 18 {
			continue
		}
		if _, err := st.QueueApplication(id); err != nil {
			t.Fatal(err)
		}
		if method == "EMAIL" && i >= 3 {
			if _, err := st.SaveApplicationPreparation(id, "Application — Account Executive", "Dear Hiring Team,\n\nI am applying for the Account Executive role. My background in consultative sales and customer partnerships aligns with your team's work.\n\nPlease find my CV attached for your review. I would welcome the opportunity to discuss how I can contribute.\n\nKind regards,\nAlex Candidate", "general"); err != nil {
				t.Fatal(err)
			}
		}
		if method == "EMAIL" && i >= 6 {
			if _, err := st.MarkApplicationDraftCreated(id, "fixture-draft-"+id); err != nil {
				t.Fatal(err)
			}
		}
		if i == 12 {
			if _, err := st.ApproveApplication(id, "Reviewed fixture"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.MarkApplicationSent(id, "fixture-message", "fixture-thread"); err != nil {
				t.Fatal(err)
			}
		}
		if i == 9 {
			if _, err := st.ApproveApplication(id, "Reviewed fixture"); err != nil {
				t.Fatal(err)
			}
		}
		if method == "LINKEDIN" && i >= 4 && i != 16 {
			if _, err := st.MarkEasyApplyOpened(id); err != nil {
				t.Fatal(err)
			}
		}
		if i == 10 {
			if _, err := st.MarkEasyApplyApplied(id, "sales"); err != nil {
				t.Fatal(err)
			}
		}
	}
	ws := &webServer{st: st, csrf: "fixture-csrf"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fixture_gmail") == "1" {
			pd, err := ws.buildAppPage(r)
			if err != nil {
				t.Error(err)
				http.Error(w, "fixture page error", 500)
				return
			}
			pd.GmailConnected = true
			tpl, err := newAppTemplate()
			if err != nil {
				t.Error(err)
				return
			}
			if err := tpl.Execute(w, pd); err != nil {
				t.Error(err)
			}
			return
		}
		ws.handleAppUI(w, r)
	})
	mux.HandleFunc("POST /app/jobs/bulk/queue", ws.handleAppBulkQueueJobs)
	mux.HandleFunc("POST /app/applications/bulk/prepare", ws.handleAppBulkPrepare)
	mux.HandleFunc("POST /app/applications/bulk/review", ws.handleAppBulkReviewRedirect)
	mux.HandleFunc("POST /app/applications/bulk/remove", ws.handleAppBulkRemoveFromQueue)
	mux.HandleFunc("POST /app/applications/send-confirm", ws.handleAppSendConfirm)
	mux.HandleFunc("POST /app/applications/{id}/approve", ws.handleAppApproveApplication)
	mux.HandleFunc("POST /app/applications/{id}/easy-apply/open", ws.handleAppOpenEasyApply)
	mux.HandleFunc("POST /app/applications/{id}/easy-apply/applied", ws.handleAppMarkEasyApplyApplied)
	t.Log("Isolated UI fixture at http://" + addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		t.Fatal(err)
	}
}
