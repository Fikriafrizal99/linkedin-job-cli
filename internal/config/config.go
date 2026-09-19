package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config holds runtime configuration resolved from the environment.
type Config struct {
	DBPath string

	// LLM (OpenAI-compatible)
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string

	// Scraping
	UserAgent             string
	RequestTimeoutSeconds int
	DetailDelaySeconds    float64
	HTTPMaxAttempts       int
	HTTPRetryBaseSeconds  float64

	// LLM pacing: seconds to wait between successive scoring calls in a run,
	// to avoid provider rate limits (HTTP 429). 0 = no delay.
	LLMDelaySeconds float64

	// LLM concurrency: max jobs enriched+scored in parallel per batch. The LLM
	// HTTP call dominates; SQLite writes serialize through MaxOpenConns(1).
	LLMConcurrency int

	// LinkedIn session (for recommended). Press-auth is preferred; these are fallbacks.
	CookiesFile  string // path to a file holding a raw "Cookie:" header value or Netscape cookies.txt
	CookieHeader string // raw cookie header override (env LJ_COOKIE)
}

// Load resolves configuration from the environment with sensible defaults.
func Load() Config {
	c := Config{
		DBPath:                envOr("LJ_DB_PATH", defaultDBPath()),
		LLMBaseURL:            envOr("LJ_LLM_BASE_URL", envOr("OPENAI_BASE_URL", "https://api.openai.com/v1")),
		LLMAPIKey:             envOr("LJ_LLM_API_KEY", envOr("OPENAI_API_KEY", "")),
		LLMModel:              envOr("LJ_LLM_MODEL", "gpt-4o-mini"),
		UserAgent:             defaultUA(),
		RequestTimeoutSeconds: 20,
		DetailDelaySeconds:    0.8,
		HTTPMaxAttempts:       envInt("LJ_HTTP_MAX_ATTEMPTS", 3),
		HTTPRetryBaseSeconds:  envFloat("LJ_HTTP_RETRY_BASE_SECONDS", 1.5),
		LLMDelaySeconds:       envFloat("LJ_LLM_DELAY_SECONDS", 2.0),
		LLMConcurrency:       envInt("LJ_LLM_CONCURRENCY", 5),
		CookiesFile:           os.Getenv("LJ_COOKIES_FILE"),
		CookieHeader:          os.Getenv("LJ_COOKIE"),
	}
	return c
}

// WithDBPath returns a copy with the DB path overridden (used by --db flag).
func (c Config) WithDBPath(p string) Config {
	c.DBPath = p
	return c
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envFloat parses key as a float, falling back to def on missing/invalid.
func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return def
	}
	return f
}

// envInt parses key as a positive int, falling back to def on missing/invalid.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// defaultDBPath returns the global DB location at ~/.linkedin-jobs/linkedin_jobs.db,
// creating the directory if needed so the CLI behaves the same regardless of CWD.
func defaultDBPath() string {
	dir := HomeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return filepath.Join(dir, "linkedin_jobs.db")
	}
	return filepath.Join(dir, "linkedin_jobs.db")
}

func defaultUA() string {
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
}
