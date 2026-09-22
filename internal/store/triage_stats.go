package store

import (
	"fmt"
	"strings"

	"linkedin-jobs/internal/models"
)

type ReviewReasonCount struct {
	Reason string
	Count  int
}

func (s *Store) TopJobReviewReasons(state string, limit int) ([]ReviewReasonCount, error) {
	state, ok := models.NormalizeJobReviewState(state)
	if !ok {
		return nil, fmt.Errorf("invalid job review state %q", state)
	}
	q := `
SELECT review_reason, COUNT(*)
FROM jobs
WHERE review_state=? AND COALESCE(TRIM(review_reason),'')<>''
GROUP BY review_reason
ORDER BY COUNT(*) DESC, review_reason ASC`
	args := []interface{}{state}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewReasonCount
	for rows.Next() {
		var row ReviewReasonCount
		if err := rows.Scan(&row.Reason, &row.Count); err != nil {
			return nil, err
		}
		row.Reason = strings.TrimSpace(row.Reason)
		out = append(out, row)
	}
	return out, rows.Err()
}
