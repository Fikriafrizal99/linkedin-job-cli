package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidCookieMap(t *testing.T) {
	cases := []struct {
		name    string
		cookies map[string]string
		want    bool
	}{
		{"complete", map[string]string{"li_at": "abc", "JSESSIONID": "ajax:1"}, true},
		{"missing li_at", map[string]string{"JSESSIONID": "ajax:1"}, false},
		{"missing jsessionid", map[string]string{"li_at": "abc"}, false},
		{"empty values", map[string]string{"li_at": "", "JSESSIONID": ""}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validCookieMap(tc.cookies); got != tc.want {
				t.Errorf("validCookieMap() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCookiesWritePath(t *testing.T) {
	os.Unsetenv("LJ_COOKIES_FILE")
	if got := cookiesWritePath(); filepath.Base(got) != "cookies.txt" {
		t.Errorf("default path base = %q, want cookies.txt", filepath.Base(got))
	}

	os.Setenv("LJ_COOKIES_FILE", "/tmp/test-cookies.txt")
	defer os.Unsetenv("LJ_COOKIES_FILE")
	if got := cookiesWritePath(); got != "/tmp/test-cookies.txt" {
		t.Errorf("env path = %q, want /tmp/test-cookies.txt", got)
	}
}

func TestRunAuthLoginNonMacOS(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "linux"
	defer func() { runtimeGOOS = origGOOS }()

	called := false
	origRead := readChromeCookies
	readChromeCookies = func() (map[string]string, error) {
		called = true
		return nil, nil
	}
	defer func() { readChromeCookies = origRead }()

	if err := runAuthLogin(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("readChromeCookies should not be called on non-macOS")
	}
}

func TestRunAuthLoginWindowsSilentSuccess(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	defer func() { runtimeGOOS = origGOOS }()

	tmpFile := filepath.Join(t.TempDir(), "cookies.txt")
	os.Setenv("LJ_COOKIES_FILE", tmpFile)
	defer os.Unsetenv("LJ_COOKIES_FILE")

	origRead := readChromeCookies
	readChromeCookies = func() (map[string]string, error) {
		return map[string]string{"li_at": "win", "JSESSIONID": "ajax:w"}, nil
	}
	defer func() { readChromeCookies = origRead }()

	browserCalled := false
	origLogin := loginViaBrowser
	loginViaBrowser = func(string, time.Duration) (map[string]string, error) {
		browserCalled = true
		return nil, nil
	}
	defer func() { loginViaBrowser = origLogin }()

	if err := runAuthLogin(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if browserCalled {
		t.Error("loginViaBrowser should not be called when silent read succeeds")
	}
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("cookies file not written: %v", err)
	}
	if !strings.Contains(string(data), "li_at=win") {
		t.Errorf("cookies file does not contain li_at: %s", string(data))
	}
}

func TestRunAuthLoginSilentSuccess(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "darwin"
	defer func() { runtimeGOOS = origGOOS }()

	tmpFile := filepath.Join(t.TempDir(), "cookies.txt")
	os.Setenv("LJ_COOKIES_FILE", tmpFile)
	defer os.Unsetenv("LJ_COOKIES_FILE")

	origRead := readChromeCookies
	readChromeCookies = func() (map[string]string, error) {
		return map[string]string{"li_at": "abc", "JSESSIONID": "ajax:1"}, nil
	}
	defer func() { readChromeCookies = origRead }()

	browserCalled := false
	origLogin := loginViaBrowser
	loginViaBrowser = func(string, time.Duration) (map[string]string, error) {
		browserCalled = true
		return nil, nil
	}
	defer func() { loginViaBrowser = origLogin }()

	if err := runAuthLogin(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if browserCalled {
		t.Error("loginViaBrowser should not be called when silent read succeeds")
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("cookies file not written: %v", err)
	}
	if !strings.Contains(string(data), "li_at=abc") {
		t.Errorf("cookies file does not contain li_at: %s", string(data))
	}
}

func TestRunAuthLoginGuidedFallback(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "darwin"
	defer func() { runtimeGOOS = origGOOS }()

	tmpFile := filepath.Join(t.TempDir(), "cookies.txt")
	os.Setenv("LJ_COOKIES_FILE", tmpFile)
	defer os.Unsetenv("LJ_COOKIES_FILE")

	origRead := readChromeCookies
	readChromeCookies = func() (map[string]string, error) {
		return nil, fmt.Errorf("no chrome")
	}
	defer func() { readChromeCookies = origRead }()

	origLogin := loginViaBrowser
	loginViaBrowser = func(string, time.Duration) (map[string]string, error) {
		return map[string]string{"li_at": "frombrowser", "JSESSIONID": "ajax:b"}, nil
	}
	defer func() { loginViaBrowser = origLogin }()

	if err := runAuthLogin(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("cookies file not written: %v", err)
	}
	if !strings.Contains(string(data), "li_at=frombrowser") {
		t.Errorf("cookies file should contain browser-captured li_at: %s", string(data))
	}
}

func TestRunAuthLoginBothFail(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "darwin"
	defer func() { runtimeGOOS = origGOOS }()

	os.Setenv("LJ_COOKIES_FILE", filepath.Join(t.TempDir(), "cookies.txt"))
	defer os.Unsetenv("LJ_COOKIES_FILE")

	origRead := readChromeCookies
	readChromeCookies = func() (map[string]string, error) {
		return nil, fmt.Errorf("no chrome")
	}
	defer func() { readChromeCookies = origRead }()

	origLogin := loginViaBrowser
	loginViaBrowser = func(string, time.Duration) (map[string]string, error) {
		return nil, fmt.Errorf("browser launch failed")
	}
	defer func() { loginViaBrowser = origLogin }()

	if err := runAuthLogin(nil, nil); err == nil {
		t.Error("expected error when both capture methods fail")
	}
}

func TestNormalizeImportedCookieHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "raw",
			in:   "li_at=abc; JSESSIONID=\"ajax:123\";",
			want: "li_at=abc; JSESSIONID=\"ajax:123\"",
		},
		{
			name: "header line",
			in:   "Cookie: li_at=abc; JSESSIONID=\"ajax:123\"",
			want: "li_at=abc; JSESSIONID=\"ajax:123\"",
		},
		{
			name: "copied headers",
			in:   "accept: */*\nCookie: li_at=abc; JSESSIONID=\"ajax:123\"\nreferer: https://www.linkedin.com/",
			want: "li_at=abc; JSESSIONID=\"ajax:123\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeImportedCookieHeader(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestCookieHeaderHas(t *testing.T) {
	header := "bcookie=x; li_at=secret; JSESSIONID=\"ajax:123\""
	if !cookieHeaderHas(header, "li_at") {
		t.Fatal("expected li_at")
	}
	if !cookieHeaderHas(header, "JSESSIONID") {
		t.Fatal("expected JSESSIONID")
	}
	if cookieHeaderHas(header, "missing") {
		t.Fatal("unexpected missing cookie")
	}
	if cookieHeaderHas("li_at=; JSESSIONID=x", "li_at") {
		t.Fatal("empty cookie value should not count")
	}
}
