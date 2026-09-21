package cmd

import (
	"crypto/subtle"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"linkedin-jobs/internal/config"
)

const (
	maxManagedUploadBytes = int64(12 << 20) // 12 MiB/file
	maxManagedRequestBytes = int64(14 << 20)
)

var allowedManagedExtensions = map[string]bool{
	".pdf": true,
	".doc": true,
	".docx": true,
	".ppt": true,
	".pptx": true,
	".png": true,
	".jpg": true,
	".jpeg": true,
}

type appAttachment struct {
	ID, Label, Kind, FileName, Path string
	Exists bool
	Size string
}

func managedFilesRoot() string {
	return filepath.Join(config.HomeDir(), "files")
}

func (ws *webServer) handleCVProfileUpload(w http.ResponseWriter, r *http.Request) {
	if !ws.parseManagedMultipart(w, r) {
		return
	}
	profileID, err := normalizeDocumentID(r.PostFormValue("profile_id"))
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	keywords := splitKeywords(r.PostFormValue("keywords"))
	priority := 1
	if raw := strings.TrimSpace(r.PostFormValue("priority")); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 0 || n > 100 {
			redirectCVProfiles(w, r, "", fmt.Errorf("priority must be between 0 and 100"))
			return
		}
		priority = n
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		redirectCVProfiles(w, r, "", fmt.Errorf("choose a CV file to upload"))
		return
	}
	defer file.Close()

	ext, err := validateManagedUpload(header)
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	destDir := filepath.Join(managedFilesRoot(), "cv")
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	destPath := filepath.Join(destDir, profileID+ext)
	if err := writeManagedUpload(file, destPath); err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}

	ws.documentMu.Lock()
	defer ws.documentMu.Unlock()

	settings, err := config.LoadSettings()
	if err != nil {
		_ = os.Remove(destPath)
		redirectCVProfiles(w, r, "", fmt.Errorf("load settings: %w", err))
		return
	}
	app := settings.Application
	oldPath := ""
	found := false
	for i := range app.CVProfiles {
		if strings.EqualFold(strings.TrimSpace(app.CVProfiles[i].ID), profileID) {
			oldPath = strings.TrimSpace(app.CVProfiles[i].Path)
			app.CVProfiles[i].ID = profileID
			app.CVProfiles[i].Path = destPath
			app.CVProfiles[i].Keywords = keywords
			app.CVProfiles[i].Priority = priority
			found = true
			break
		}
	}
	if !found {
		app.CVProfiles = append(app.CVProfiles, config.CVProfileSettings{
			ID: profileID, Path: destPath, Keywords: keywords, Priority: priority,
		})
	}
	if strings.TrimSpace(app.DefaultCVProfile) == "" || r.PostFormValue("set_default") == "1" {
		app.DefaultCVProfile = profileID
	}
	if err := config.SaveApplicationSettings(app); err != nil {
		_ = os.Remove(destPath)
		redirectCVProfiles(w, r, "", fmt.Errorf("save CV profile: %w", err))
		return
	}
	if oldPath != "" && oldPath != destPath {
		removeManagedFile(oldPath)
	}
	redirectCVProfiles(w, r, "CV profile "+profileID+" saved.", nil)
}

func (ws *webServer) handleCVProfileDefault(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id, err := normalizeDocumentID(r.PathValue("id"))
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	ws.documentMu.Lock()
	defer ws.documentMu.Unlock()
	settings, err := config.LoadSettings()
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	found := false
	for _, p := range settings.Application.CVProfiles {
		if strings.EqualFold(strings.TrimSpace(p.ID), id) {
			found = true
			break
		}
	}
	if !found {
		redirectCVProfiles(w, r, "", fmt.Errorf("CV profile %s not found", id))
		return
	}
	settings.Application.DefaultCVProfile = id
	if err := config.SaveApplicationSettings(settings.Application); err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	redirectCVProfiles(w, r, "Default CV changed to "+id+".", nil)
}

func (ws *webServer) handleCVProfileDelete(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id, err := normalizeDocumentID(r.PathValue("id"))
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	ws.documentMu.Lock()
	defer ws.documentMu.Unlock()
	settings, err := config.LoadSettings()
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	app := settings.Application
	var next []config.CVProfileSettings
	removedPath := ""
	for _, p := range app.CVProfiles {
		if strings.EqualFold(strings.TrimSpace(p.ID), id) {
			removedPath = strings.TrimSpace(p.Path)
			continue
		}
		next = append(next, p)
	}
	if removedPath == "" {
		redirectCVProfiles(w, r, "", fmt.Errorf("CV profile %s not found", id))
		return
	}
	app.CVProfiles = next
	if strings.EqualFold(strings.TrimSpace(app.DefaultCVProfile), id) {
		app.DefaultCVProfile = ""
		if len(next) > 0 {
			app.DefaultCVProfile = next[0].ID
		}
	}
	if err := config.SaveApplicationSettings(app); err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	removeManagedFile(removedPath)
	redirectCVProfiles(w, r, "CV profile "+id+" deleted.", nil)
}

func (ws *webServer) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if !ws.parseManagedMultipart(w, r) {
		return
	}
	label := strings.TrimSpace(r.PostFormValue("label"))
	if label == "" || len(label) > 100 {
		redirectCVProfiles(w, r, "", fmt.Errorf("attachment label is required and must be at most 100 characters"))
		return
	}
	kind := strings.ToLower(strings.TrimSpace(r.PostFormValue("kind")))
	switch kind {
	case "portfolio", "cover_letter", "certificate", "other":
	default:
		kind = "other"
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		redirectCVProfiles(w, r, "", fmt.Errorf("choose an attachment file to upload"))
		return
	}
	defer file.Close()
	ext, err := validateManagedUpload(header)
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	baseID, _ := normalizeDocumentID(label)
	if baseID == "" {
		baseID = "attachment"
	}
	id := fmt.Sprintf("%s-%d", baseID, time.Now().UnixNano())
	destDir := filepath.Join(managedFilesRoot(), "attachments")
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	destPath := filepath.Join(destDir, id+ext)
	if err := writeManagedUpload(file, destPath); err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}

	ws.documentMu.Lock()
	defer ws.documentMu.Unlock()
	settings, err := config.LoadSettings()
	if err != nil {
		_ = os.Remove(destPath)
		redirectCVProfiles(w, r, "", err)
		return
	}
	settings.Application.Attachments = append(settings.Application.Attachments, config.AttachmentSettings{
		ID: id, Label: label, Kind: kind, Path: destPath,
	})
	if err := config.SaveApplicationSettings(settings.Application); err != nil {
		_ = os.Remove(destPath)
		redirectCVProfiles(w, r, "", err)
		return
	}
	redirectCVProfiles(w, r, "Attachment "+label+" uploaded.", nil)
}

func (ws *webServer) handleAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	if !ws.checkCSRF(w, r) {
		return
	}
	id, err := normalizeDocumentID(r.PathValue("id"))
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	ws.documentMu.Lock()
	defer ws.documentMu.Unlock()
	settings, err := config.LoadSettings()
	if err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	var next []config.AttachmentSettings
	removedPath, label := "", ""
	for _, a := range settings.Application.Attachments {
		if strings.EqualFold(strings.TrimSpace(a.ID), id) {
			removedPath, label = strings.TrimSpace(a.Path), strings.TrimSpace(a.Label)
			continue
		}
		next = append(next, a)
	}
	if removedPath == "" {
		redirectCVProfiles(w, r, "", fmt.Errorf("attachment %s not found", id))
		return
	}
	settings.Application.Attachments = next
	if err := config.SaveApplicationSettings(settings.Application); err != nil {
		redirectCVProfiles(w, r, "", err)
		return
	}
	removeManagedFile(removedPath)
	redirectCVProfiles(w, r, "Attachment "+label+" deleted.", nil)
}

func (ws *webServer) parseManagedMultipart(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxManagedRequestBytes)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "upload is too large or malformed", http.StatusBadRequest)
		return false
	}
	tok := r.PostFormValue("csrf")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(ws.csrf)) != 1 {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return false
	}
	return true
}

func validateManagedUpload(h *multipart.FileHeader) (string, error) {
	if h == nil {
		return "", fmt.Errorf("missing upload")
	}
	if h.Size <= 0 {
		return "", fmt.Errorf("uploaded file is empty")
	}
	if h.Size > maxManagedUploadBytes {
		return "", fmt.Errorf("file is larger than 12 MiB")
	}
	name := filepath.Base(strings.TrimSpace(h.Filename))
	ext := strings.ToLower(filepath.Ext(name))
	if !allowedManagedExtensions[ext] {
		return "", fmt.Errorf("unsupported file type %q; use PDF, DOC/DOCX, PPT/PPTX, PNG, or JPG", ext)
	}
	return ext, nil
}

func writeManagedUpload(src multipart.File, dest string) error {
	tmp := dest + ".uploading"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(src, maxManagedUploadBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if n > maxManagedUploadBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("file is larger than 12 MiB")
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(dest, 0o600)
}

func normalizeDocumentID(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", fmt.Errorf("id is required")
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || len(out) > 80 {
		return "", fmt.Errorf("id must contain letters or numbers and be at most 80 characters")
	}
	return out, nil
}

func splitKeywords(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func removeManagedFile(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	root, err1 := filepath.Abs(managedFilesRoot())
	abs, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return
	}
	_ = os.Remove(abs)
}

func resolveAttachmentPaths(configured []config.AttachmentSettings, ids []string) ([]string, error) {
	byID := map[string]config.AttachmentSettings{}
	for _, a := range configured {
		byID[strings.ToLower(strings.TrimSpace(a.ID))] = a
	}
	seen := map[string]bool{}
	var out []string
	var total int64
	for _, raw := range ids {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		a, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("attachment %q is not configured", raw)
		}
		path := strings.TrimSpace(a.Path)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return nil, fmt.Errorf("attachment %q is not accessible", a.Label)
		}
		total += info.Size()
		if total > 18<<20 {
			return nil, fmt.Errorf("selected additional attachments exceed 18 MiB")
		}
		out = append(out, path)
	}
	return out, nil
}

func attachmentView(a config.AttachmentSettings) appAttachment {
	path := strings.TrimSpace(a.Path)
	view := appAttachment{
		ID: strings.TrimSpace(a.ID),
		Label: strings.TrimSpace(a.Label),
		Kind: strings.TrimSpace(a.Kind),
		Path: path,
		FileName: filepath.Base(path),
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		view.Exists = true
		view.Size = humanFileSize(info.Size())
	}
	return view
}

func humanFileSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func redirectCVProfiles(w http.ResponseWriter, r *http.Request, message string, actionErr error) {
	q := url.Values{}
	if message != "" {
		q.Set("file_message", message)
	}
	if actionErr != nil {
		msg := actionErr.Error()
		if len(msg) > 240 {
			msg = msg[:240]
		}
		q.Set("file_error", msg)
	}
	target := "/app/cv-profiles"
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
