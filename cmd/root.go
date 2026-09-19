package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/linkedin"
	"linkedin-jobs/internal/store"
)

var (
	cfgFlag dbFlag
	jsonOut bool
)

// dbFlag stores the optional --db path override.
type dbFlag struct{ path string }

var rootCmd = &cobra.Command{
	Use:   "linkedin-jobs",
	Short: "LinkedIn jobs CLI — recommended jobs from your session, search, filter, score, store.",
	Long: `linkedin-jobs pulls your personalized "Recommended for you" job feed from your
LinkedIn session, searches the public job board, parses salaries, enriches and
fit-scores postings with an LLM, and persists everything to a local SQLite
store with offline full-text search.

Recommended jobs (the headline command) and url use a logged-in session.
Capture it once with:
    linkedin-jobs auth login         # captures your LinkedIn session
    linkedin-jobs recommended        # pull your personalized feed
    linkedin-jobs url "<url>"        # score every job on a pasted search URL

Collector mode works without a session or LLM provider:
    linkedin-jobs collect "Sales Executive" --location Indonesia --posted-within 7d

The upstream scored search command remains available:
    linkedin-jobs search "Staff Engineer" --location Toronto --remote`,
	SilenceUsage: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFlag.path, "db", "", "path to the SQLite DB file (default: ~/.linkedin-jobs/linkedin_jobs.db or $LJ_DB_PATH)")
	// --json is added per-command where supported, but we also expose a global.
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON (agent-native output)")
}

func loadCfg() config.Config {
	cfg := config.Load()
	if cfgFlag.path != "" {
		cfg = cfg.WithDBPath(cfgFlag.path)
	}
	return cfg
}

// openStore opens (creating) the database from the resolved config.
func openStore() (*store.Store, error) {
	return store.Open(loadCfg().DBPath)
}

// newClient builds a LinkedIn client; withSession controls whether an
// authenticated session is attached.
func newClient(withSession bool) (*linkedin.Client, error) {
	cfg := loadCfg()
	c := linkedin.New(cfg)
	if withSession {
		if _, err := attachSession(c); err != nil {
			return c, err
		}
	}
	return c, nil
}

// die prints an error to stderr and exits 1.
func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
