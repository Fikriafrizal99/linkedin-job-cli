package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveApplicationSettingsPreservesOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	t.Setenv("LJ_SETTINGS_FILE", path)
	initial := `scoring:
  rubrics:
    - id: salary
      kind: system
      weight: 7
profile:
  min_salary: 100
  min_salary_currency: USD
application:
  candidate_name: Old Name
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	app := ApplicationSettings{
		CandidateName: "Candidate",
		DefaultCVProfile: "general",
		CVProfiles: []CVProfileSettings{{ID: "general", Path: "/tmp/general.pdf", Priority: 1}},
		Attachments: []AttachmentSettings{{ID: "portfolio-1", Label: "Portfolio", Kind: "portfolio", Path: "/tmp/portfolio.pdf"}},
	}
	if err := SaveApplicationSettings(app); err != nil {
		t.Fatalf("SaveApplicationSettings: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"weight: 7", "min_salary: 100", "candidate_name: Candidate", "attachments:", "Portfolio"} {
		if !strings.Contains(text, want) {
			t.Errorf("settings missing %q:\n%s", want, text)
		}
	}
}
