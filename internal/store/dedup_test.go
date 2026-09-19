package store

import (
	"strings"
	"testing"

	"linkedin-jobs/internal/models"
)

func TestContentHash_StableAcrossCosmeticDifferences(t *testing.T) {
	base := ContentHash("Acme Corp", "Staff Engineer", "Build distributed systems in Go.", 1700000000)
	// Identical input must match.
	same := ContentHash("Acme Corp", "Staff Engineer", "Build distributed systems in Go.", 1700000000)
	if same != base {
		t.Errorf("identical input produced different hash: %s vs %s", base, same)
	}
	// Cosmetic differences (case, whitespace, HTML), same listedAt, must match.
	for _, v := range []struct {
		company, title, desc string
	}{
		{"acme corp", "staff engineer", "build distributed systems in go."},
		{"  Acme Corp ", "Staff  Engineer", "Build distributed systems in Go."},
		{"Acme Corp", "Staff Engineer", "Build <b>distributed</b> systems\nin  Go."},
	} {
		h := ContentHash(v.company, v.title, v.desc, 1700000000)
		if h != base {
			t.Errorf("cosmetic variant produced different hash:\n  base=%s\n  got =%s\n  (%q|%q)", base, h, v.company, v.desc)
		}
	}
}

func TestContentHash_DifferentJobsDiffer(t *testing.T) {
	a := ContentHash("Acme", "Staff Engineer", "Go.", 1)
	b := ContentHash("Globex", "Staff Engineer", "Go.", 1)
	if a == b {
		t.Errorf("different companies hashed the same")
	}
}

func TestContentHash_EmptyInputsStable(t *testing.T) {
	h := ContentHash("", "", "", 0)
	if h == "" {
		t.Errorf("empty inputs should still produce a hash")
	}
	if strings.Contains(h, "\x1f") {
		t.Errorf("hash leaked separator bytes")
	}
}

func TestIsDuplicateEnriched(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("d1")
	j.Description = "Some description"
	j.ContentHash = "duphash-1"
	st.Upsert(j)
	// Not enriched yet -> not a duplicate-for-skip.
	if st.IsDuplicateEnriched("duphash-1") {
		t.Errorf("unenriched job should not count as enriched duplicate")
	}
	yp := 5
	st.SetEnrichmentAndScore("d1", models.Enrichment{Industry: "X", YearsExperience: &yp})
	if !st.IsDuplicateEnriched("duphash-1") {
		t.Errorf("enriched job should count as duplicate-for-skip")
	}
	if st.IsDuplicateEnriched("nonexistent") {
		t.Errorf("missing hash should not be a duplicate")
	}
}

func TestStructuralHashIgnoresPostingDate(t *testing.T) {
	a := StructuralHash("Acme", "Sales Executive", "Sell enterprise software")
	b := StructuralHash(" acme ", "SALES EXECUTIVE", "Sell  enterprise software")
	if a != b {
		t.Fatalf("structural hash should ignore cosmetic differences")
	}
}

func TestClassifyStructuralDuplicateExact(t *testing.T) {
	st := tmpDB(t)
	first := sampleJob("dup-a")
	first.Company = "Acme"
	first.Title = "Sales Executive"
	first.Description = "Sell enterprise software"
	first.PostedAt = "2026-09-19T01:00:00Z"
	first.StructuralHash = StructuralHash(first.Company, first.Title, first.Description)
	first.DuplicateClassification = DuplicateNew
	if err := st.Upsert(first); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}

	second := sampleJob("dup-b")
	second.Company = first.Company
	second.Title = first.Title
	second.Description = first.Description
	second.PostedAt = "2026-09-19T09:00:00Z"
	second.StructuralHash = StructuralHash(second.Company, second.Title, second.Description)

	class, duplicateOf, err := st.ClassifyStructuralDuplicate(second)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if class != DuplicateExact || duplicateOf != "dup-a" {
		t.Fatalf("class=%q duplicateOf=%q", class, duplicateOf)
	}
}

func TestClassifyStructuralDuplicateLikelyRepost(t *testing.T) {
	st := tmpDB(t)
	first := sampleJob("repost-a")
	first.Company = "Acme"
	first.Title = "Sales Executive"
	first.Description = "Sell enterprise software"
	first.PostedAt = "2026-09-10"
	first.StructuralHash = StructuralHash(first.Company, first.Title, first.Description)
	first.DuplicateClassification = DuplicateNew
	if err := st.Upsert(first); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}

	second := sampleJob("repost-b")
	second.Company = first.Company
	second.Title = first.Title
	second.Description = first.Description
	second.PostedAt = "2026-09-19"
	second.StructuralHash = StructuralHash(second.Company, second.Title, second.Description)

	class, duplicateOf, err := st.ClassifyStructuralDuplicate(second)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if class != DuplicateLikelyRepost || duplicateOf != "repost-a" {
		t.Fatalf("class=%q duplicateOf=%q", class, duplicateOf)
	}
}

func TestClassifyStructuralDuplicateNew(t *testing.T) {
	st := tmpDB(t)
	j := sampleJob("new-structural")
	j.StructuralHash = StructuralHash(j.Company, j.Title, "unique description")
	class, duplicateOf, err := st.ClassifyStructuralDuplicate(j)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if class != DuplicateNew || duplicateOf != "" {
		t.Fatalf("class=%q duplicateOf=%q", class, duplicateOf)
	}
}

func TestStructuralHashRequiresCompleteIdentity(t *testing.T) {
	if got := StructuralHash("Acme", "Sales Executive", ""); got != "" {
		t.Fatalf("empty description should not produce structural hash: %q", got)
	}
	if got := StructuralHash("", "Sales Executive", "Sell enterprise software"); got != "" {
		t.Fatalf("empty company should not produce structural hash: %q", got)
	}
}
