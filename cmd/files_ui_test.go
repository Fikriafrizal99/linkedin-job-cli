package cmd

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
)

func multipartUploadRequest(t *testing.T, target string, fields map[string]string, fileName string, fileData []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(fileData); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestCVProfileUploadStoresFileAndSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(home, ".linkedin-jobs", "settings.yaml"))

	req := multipartUploadRequest(t, "/app/cv-profiles/upload", map[string]string{
		"csrf": "csrf-test",
		"profile_id": "General",
		"keywords": "sales, business development",
		"priority": "2",
		"set_default": "1",
	}, "My CV.pdf", []byte("%PDF-test"))
	rec := httptest.NewRecorder()
	ws := &webServer{csrf: "csrf-test"}
	ws.handleCVProfileUpload(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.Application.DefaultCVProfile != "general" || len(settings.Application.CVProfiles) != 1 {
		t.Fatalf("unexpected application settings: %+v", settings.Application)
	}
	p := settings.Application.CVProfiles[0]
	if p.ID != "general" || p.Priority != 2 || len(p.Keywords) != 2 {
		t.Fatalf("profile=%+v", p)
	}
	if _, err := os.Stat(p.Path); err != nil {
		t.Fatalf("uploaded CV missing: %v", err)
	}
}

func TestAttachmentUploadAndResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LJ_SETTINGS_FILE", filepath.Join(home, ".linkedin-jobs", "settings.yaml"))

	req := multipartUploadRequest(t, "/app/cv-profiles/attachments/upload", map[string]string{
		"csrf": "csrf-test",
		"label": "Professional Portfolio",
		"kind": "portfolio",
	}, "Portfolio.pdf", []byte("%PDF-portfolio"))
	rec := httptest.NewRecorder()
	ws := &webServer{csrf: "csrf-test"}
	ws.handleAttachmentUpload(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Application.Attachments) != 1 {
		t.Fatalf("attachments=%+v", settings.Application.Attachments)
	}
	a := settings.Application.Attachments[0]
	paths, err := resolveAttachmentPaths(settings.Application.Attachments, []string{a.ID})
	if err != nil {
		t.Fatalf("resolveAttachmentPaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != a.Path {
		t.Fatalf("paths=%v attachment=%+v", paths, a)
	}
}

func TestCVProfilesPageContainsUploadsAndNoDocumentsNav(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatalf("newAppTemplate: %v", err)
	}
	var out strings.Builder
	err = tpl.Execute(&out, appPageData{
		Title: "CV Profiles",
		Active: "cv-profiles",
		CSRF: "csrf",
		CandidateName: "Candidate",
		CandidateInitials: "C",
		CVProfiles: []appCVProfile{{
			ID: "general", FileName: "general.pdf", Path: "/tmp/general.pdf", Exists: true, Default: true,
		}},
		Attachments: []appAttachment{{
			ID: "portfolio-1", Label: "Portfolio", Kind: "portfolio", FileName: "portfolio.pdf", Path: "/tmp/portfolio.pdf", Exists: true,
		}},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := out.String()
	for _, want := range []string{
		`action="/app/cv-profiles/upload"`,
		`Additional Attachments`,
		`action="/app/cv-profiles/attachments/upload"`,
		`Upload / Replace CV`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(html, `href="/app/documents"`) {
		t.Fatal("CV/file management must not add a Documents navigation page")
	}
}

func TestApplicationDetailRendersOptionalAttachmentPicker(t *testing.T) {
	tpl, err := newAppTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err = tpl.Execute(&out, appPageData{
		Title: "Application Detail",
		Active: "applications",
		CSRF: "csrf",
		CandidateName: "Candidate",
		CandidateInitials: "C",
		GmailConnected: true,
		SelectedCVReady: true,
		SelectedCVPath: "/tmp/general.pdf",
		SelectedApplication: &models.JobApplication{
			JobID: "50020",
			State: models.ApplicationStateReadyEmail,
			Recipient: "jobs@example.com",
			Subject: "Application",
			Body: "Body",
			CVProfile: "general",
		},
		Attachments: []appAttachment{{
			ID: "portfolio-1", Label: "Portfolio", Kind: "portfolio", FileName: "portfolio.pdf", Exists: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `name="attachment" value="portfolio-1"`) ||
		!strings.Contains(html, "Optional Attachments") {
		t.Fatalf("attachment picker missing")
	}
}


func TestFriendlyAttachmentNameAndDraftBody(t *testing.T) {
	got := friendlyAttachmentName(
		"Mochamad Fikri Afrizal",
		"Professional Portfolio 2026",
		"/tmp/portfolio-123456.pdf",
	)
	if got != "Mochamad_Fikri_Afrizal_Professional_Portfolio_2026.pdf" {
		t.Fatalf("friendly name=%q", got)
	}

	dir := t.TempDir()
	stored := filepath.Join(dir, "portofolio-123.pdf")
	if err := os.WriteFile(stored, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveAttachments([]config.AttachmentSettings{{
		ID: "portfolio-1", Label: "Portofolio", Kind: "portfolio", Path: stored,
	}}, []string{"portfolio-1"}, "Mochamad Fikri Afrizal")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Name != "Mochamad_Fikri_Afrizal_Portfolio.pdf" {
		t.Fatalf("resolved=%+v", resolved)
	}

	body := "Dear Hiring Team,\n\nPlease find my CV attached for your review.\n\nKind regards,"
	withPortfolio := draftBodyForAttachments(body, []resolvedAttachment{{Kind: "portfolio"}})
	if !strings.Contains(withPortfolio, "Please find my CV and portfolio attached for your review.") {
		t.Fatalf("portfolio body=%q", withPortfolio)
	}

	withSeveral := draftBodyForAttachments(body, []resolvedAttachment{{Kind: "portfolio"}, {Kind: "certificate"}})
	if !strings.Contains(withSeveral, "Please find my CV and supporting documents attached for your review.") {
		t.Fatalf("multi attachment body=%q", withSeveral)
	}
}
