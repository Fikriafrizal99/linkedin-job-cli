package store

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"

	"linkedin-jobs/internal/models"
)

var htmlTagRE = regexp.MustCompile(`<[^>]*>`)

const (
	DuplicateNew         = "NEW_JOB"
	DuplicateSameJobID   = "SAME_JOB_ID"
	DuplicateExact       = "EXACT_DUPLICATE"
	DuplicateLikelyRepost = "LIKELY_REPOST"
)

// ContentHash fingerprints a job for LLM-free dedup. It is stable across
// cosmetic differences (case, whitespace, HTML tags) so a re-fetched or
// cross-source duplicate of the same posting matches. The full description is
// included per R3; listedAt anchors the posting in time.
func ContentHash(company, title, description string, listedAt int64) string {
	s := normalize(company) + "\x1f" + normalize(title) + "\x1f" + normalize(description) + "\x1f" + strconv.FormatInt(listedAt, 10)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// IsDuplicateEnriched reports whether any stored job with this content hash has
// already been enriched. Used by ingest to skip a redundant LLM call on a
// re-fetch or cross-source duplicate (R3, R7).
func (s *Store) IsDuplicateEnriched(hash string) bool {
	if hash == "" {
		return false
	}
	j, err := s.FindByContentHash(hash)
	if err != nil || j == nil {
		return false
	}
	return j.IsEnriched()
}

func normalize(s string) string {
	s = strings.ToLower(s)
	s = htmlTagRE.ReplaceAllString(s, " ")
	return collapseWS(s)
}

func collapseWS(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range strings.TrimSpace(s) {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			if !inSpace {
				b.WriteRune(' ')
				inSpace = true
			}
		default:
			b.WriteRune(r)
			inSpace = false
		}
	}
	return b.String()
}


// StructuralHash fingerprints the employer/title/description while deliberately
// excluding posting timestamps. It is used to recognize a new LinkedIn job ID
// that republishes the same underlying vacancy.
func StructuralHash(company, title, description string) string {
	company = normalize(company)
	title = normalize(title)
	description = normalize(description)
	// Structural comparison is only meaningful with a real employer, title,
	// and fetched description. Avoid collapsing incomplete fetches together.
	if company == "" || title == "" || description == "" {
		return ""
	}
	s := company + "\x1f" + title + "\x1f" + description
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ClassifyStructuralDuplicate compares a fully fetched candidate with stored
// jobs. It never deletes the new candidate; it returns a classification and
// the earlier job ID so the caller can persist provenance.
func (s *Store) ClassifyStructuralDuplicate(j *models.JobPosting) (classification, duplicateOf string, err error) {
	if j == nil || j.ID == "" {
		return DuplicateNew, "", nil
	}
	if existing, err := s.Get(j.ID); err != nil {
		return "", "", err
	} else if existing != nil {
		return DuplicateSameJobID, existing.ID, nil
	}

	hash := j.StructuralHash
	if hash == "" {
		hash = StructuralHash(j.Company, j.Title, j.Description)
	}
	if hash == "" {
		return DuplicateNew, "", nil
	}
	existing, err := s.FindByStructuralHash(hash, j.ID)
	if err != nil {
		return "", "", err
	}
	if existing == nil {
		return DuplicateNew, "", nil
	}

	incomingDate := postedDateKey(j.PostedAt)
	existingDate := postedDateKey(existing.PostedAt)
	if incomingDate != "" && existingDate != "" && incomingDate != existingDate {
		return DuplicateLikelyRepost, existing.ID, nil
	}
	return DuplicateExact, existing.ID, nil
}

func postedDateKey(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
