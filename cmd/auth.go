package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/auth"
	"linkedin-jobs/internal/linkedin"
)

// attachSession resolves a LinkedIn session and attaches it to the client.
func attachSession(c *linkedin.Client) (*auth.Session, error) {
	s, err := auth.Resolve(loadCfg())
	if err != nil {
		return nil, err
	}
	c.WithSession(s)
	return s, nil
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Inspect or capture your LinkedIn session",
	Long: `Inspect or capture a usable LinkedIn session for the 'recommended'
and 'url' commands.

  linkedin-jobs auth login    # capture session from Chrome or guided login
  linkedin-jobs auth status   # check whether the session is usable

Sessions can also come from LJ_COOKIE (a raw Cookie header string) or
LJ_COOKIES_FILE (path to a file with one). The csrf-token is derived from
your JSESSIONID cookie.`,
}

// Injectable for testing.
var (
	runtimeGOOS       = runtime.GOOS
	readChromeCookies = auth.ReadChromeCookies
	loginViaBrowser   = auth.LoginViaBrowser
)

var authStatusLive bool

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether a usable session is available",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _ := newClient(true)
		if c == nil {
			return nil
		}
		if !c.HasSession() {
			fmt.Println("No session. Run 'linkedin-jobs auth login' to capture one.")
			return nil
		}
		// HasSession only checks that *some* cookies were captured. A usable
		// Voyager session also needs the li_at cookie and a JSESSIONID-derived
		// csrf-token; without those, `recommended` will 403 with "CSRF check
		// failed". Surface that here so the user knows to refresh their cookie
		// export instead of discovering it at fetch time.
		sess, _ := auth.Resolve(loadCfg())
		if sess == nil || !sess.Valid() {
			var why, src string
			if sess != nil {
				src = sess.Source
				switch {
				case sess.CSRFToken == "":
					why = "no JSESSIONID cookie (cannot derive csrf-token)"
				case !strings.Contains(strings.ToLower(sess.CookieHeader), "li_at="):
					why = "missing li_at auth cookie"
				default:
					why = "incomplete cookies"
				}
			} else {
				why = "session could not be resolved"
			}
			fmt.Printf("Session captured from %s but incomplete (%s).\n", sessionSourceLabel(src), why)
			fmt.Println("This usually means the session is stale — re-run `linkedin-jobs auth login`.")
			return nil
		}
		if !authStatusLive {
			fmt.Printf("Session structurally complete [source: %s]. Run 'linkedin-jobs auth status --live' to verify LinkedIn accepts it.\n", sessionSourceLabel(sess.Source))
			return nil
		}
		if err := c.ProbeSession(); err != nil {
			fmt.Printf("Session structurally complete but live verification failed [source: %s]: %v\n", sessionSourceLabel(sess.Source), err)
			return nil
		}
		fmt.Printf("Session live-verified and accepted by LinkedIn [source: %s].\n", sessionSourceLabel(sess.Source))
		return nil
	},
}

func sessionSourceLabel(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

var authImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a LinkedIn session from stdin (safe for WSL/headless use)",
	Long: "Import a LinkedIn Cookie header from standard input and store it in the local cookies file with owner-only permissions. The input must contain at least li_at and JSESSIONID. Do not pass the Cookie header as a command-line argument because shell history can retain it.",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := os.Stdin.Stat()
		if err != nil {
			return fmt.Errorf("inspect stdin: %w", err)
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return fmt.Errorf("no session data on stdin; pipe a local Cookie header into linkedin-jobs auth import")
		}

		reader := bufio.NewReader(io.LimitReader(os.Stdin, 64*1024))
		data, err := io.ReadAll(reader)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		header := normalizeImportedCookieHeader(string(data))
		if header == "" {
			return fmt.Errorf("empty session input")
		}
		if !cookieHeaderHas(header, "li_at") || !cookieHeaderHas(header, "JSESSIONID") {
			return fmt.Errorf("session input must include both li_at and JSESSIONID cookies")
		}

		writePath := cookiesWritePath()
		if err := auth.WriteCookiesFile(writePath, header); err != nil {
			return fmt.Errorf("write cookies file: %w", err)
		}
		fmt.Printf("Session imported to %s\n", writePath)
		fmt.Println("Run 'linkedin-jobs auth status' to verify.")
		return nil
	},
}

func normalizeImportedCookieHeader(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Fast path: a raw Cookie header value was piped directly.
	if !strings.Contains(raw, "\n") && !strings.HasPrefix(strings.ToLower(raw), "cookie:") {
		return strings.TrimSpace(strings.TrimSuffix(raw, ";"))
	}

	if header := cookieHeaderFromCopiedHeaders(raw); header != "" {
		return strings.TrimSpace(strings.TrimSuffix(header, ";"))
	}

	if strings.HasPrefix(strings.ToLower(raw), "cookie:") {
		raw = strings.TrimSpace(raw[len("cookie:"):])
	}
	return strings.TrimSpace(strings.TrimSuffix(raw, ";"))
}

// cookieHeaderFromCopiedHeaders accepts both common Chrome DevTools clipboard
// formats:
//
//   Cookie: li_at=...; JSESSIONID=...
//
// and the newer name/value layout:
//
//   cookie
//   li_at=...; JSESSIONID=...
//
// It also tolerates visually wrapped Cookie values. No cookie value is logged.
func cookieHeaderFromCopiedHeaders(raw string) string {
	lines := strings.Split(raw, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		// Traditional "Cookie: value" representation.
		if strings.HasPrefix(lower, "cookie:") {
			parts := []string{}
			if v := strings.TrimSpace(line[len("cookie:"):]); v != "" {
				parts = append(parts, v)
			}
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" {
					continue
				}
				if looksLikeHTTPHeaderLine(next) || looksLikeHeaderNameOnly(next) {
					break
				}
				parts = append(parts, next)
			}
			return strings.Join(parts, " ")
		}

		// Chromium DevTools may copy the grid as alternating header-name and
		// header-value lines, so "cookie" can appear on a line by itself.
		if lower == "cookie" {
			parts := []string{}
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" {
					continue
				}
				if len(parts) > 0 && (looksLikeHTTPHeaderLine(next) || looksLikeHeaderNameOnly(next)) {
					break
				}
				parts = append(parts, next)
			}
			return strings.Join(parts, " ")
		}
	}
	return ""
}

// looksLikeHTTPHeaderLine identifies "name: value" copied-header lines. HTTP/2
// pseudo-headers (:authority, :method) count as headers too.
func looksLikeHTTPHeaderLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, ":") {
		return strings.Count(line, ":") >= 2
	}
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return false
	}
	for _, r := range line[:idx] {
		if !isHTTPHeaderNameRune(r) {
			return false
		}
	}
	return true
}

// looksLikeHeaderNameOnly detects the alternating DevTools copy layout where a
// header name and value are placed on separate lines. Cookie continuation
// chunks contain '=' or ';', so they are not mistaken for names.
func looksLikeHeaderNameOnly(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "=; \t/") {
		return false
	}
	if strings.HasPrefix(line, ":") {
		line = strings.TrimPrefix(line, ":")
	}
	if line == "" {
		return false
	}
	for _, r := range line {
		if !isHTTPHeaderNameRune(r) {
			return false
		}
	}
	return true
}

func isHTTPHeaderNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		strings.ContainsRune("!#$%&'*+-.^_|~", r)
}

func cookieHeaderHas(header, name string) bool {
	target := strings.ToLower(strings.TrimSpace(name))
	for _, part := range strings.Split(header, ";") {
		p := strings.TrimSpace(part)
		idx := strings.IndexByte(p, '=')
		if idx <= 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(p[:idx])) == target && strings.TrimSpace(p[idx+1:]) != "" {
			return true
		}
	}
	return false
}
var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Capture your LinkedIn session from Chrome or a guided browser login",
	Long: `Capture your LinkedIn session without manually exporting cookies.

First tries to read cookies silently from your Chrome browser's cookie store
(no browser window opens). If that fails (you're not logged in, or the cookies
are stale), it launches a Chrome window so you can log in to LinkedIn, then
captures the session automatically.

The captured session is written to a cookies file that 'recommended' and 'url'
use automatically. On macOS or Windows with Chrome, this is the easiest way to authenticate.

The existing LJ_COOKIE / LJ_COOKIES_FILE env path still takes priority for
headless and agent use.`,
	RunE: runAuthLogin,
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	if runtimeGOOS != "darwin" && runtimeGOOS != "windows" {
		fmt.Println("Browser capture is supported on macOS and Windows.")
		fmt.Println("See the README for setting up a session on other platforms.")
		return nil
	}

	writePath := cookiesWritePath()

	fmt.Println("Reading session from Chrome cookie store...")
	cookies, err := readChromeCookies()
	if err == nil && validCookieMap(cookies) {
		header := auth.AssembleCookieHeader(cookies)
		if err := auth.WriteCookiesFile(writePath, header); err != nil {
			return fmt.Errorf("write cookies file: %w", err)
		}
		fmt.Printf("Session captured from Chrome (no browser launched). Written to %s\n", writePath)
		fmt.Println("Run 'linkedin-jobs auth status' to verify.")
		return nil
	}

	if err != nil {
		fmt.Printf("Chrome cookie read failed: %v\n", err)
	} else {
		fmt.Println("Chrome cookies incomplete (missing li_at or JSESSIONID).")
	}
	fmt.Println("Launching guided browser login...")
	cookies, err = loginViaBrowser(auth.ChromeProfileDir(), 5*time.Minute)
	if err != nil {
		return fmt.Errorf("guided login failed: %w", err)
	}
	if !validCookieMap(cookies) {
		return fmt.Errorf("guided login completed but session is incomplete (missing li_at or JSESSIONID)")
	}

	header := auth.AssembleCookieHeader(cookies)
	if err := auth.WriteCookiesFile(writePath, header); err != nil {
		return fmt.Errorf("write cookies file: %w", err)
	}
	fmt.Printf("Session captured via guided login. Written to %s\n", writePath)
	fmt.Println("Run 'linkedin-jobs auth status' to verify.")
	return nil
}

func validCookieMap(cookies map[string]string) bool {
	return cookies["li_at"] != "" && cookies["JSESSIONID"] != ""
}

func cookiesWritePath() string {
	if p := os.Getenv("LJ_COOKIES_FILE"); p != "" {
		return p
	}
	return auth.DefaultCookiesPath()
}

func init() {
	authStatusCmd.Flags().BoolVar(&authStatusLive, "live", false, "verify the session with one authenticated LinkedIn Voyager request")
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authImportCmd)
	rootCmd.AddCommand(authCmd)
}
