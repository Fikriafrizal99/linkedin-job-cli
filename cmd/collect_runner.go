package cmd

import (
	"fmt"
	"strings"

	"linkedin-jobs/internal/linkedin"
	"linkedin-jobs/internal/models"
	"linkedin-jobs/internal/store"
)

type collectRequest struct {
	Keywords       string
	Location       string
	PostedWithin   string
	Top            int
	Remote         bool
	Hybrid         bool
	Onsite         bool
	ForceOverwrite bool
	WithSession    bool
}

type collectRunResult struct {
	SearchRuns       int      `json:"search_runs"`
	Searched         int      `json:"searched"`
	NewCandidates    int      `json:"new_candidates"`
	Persisted        int      `json:"persisted"`
	ExactDuplicates  int      `json:"exact_duplicates"`
	LikelyReposts    int      `json:"likely_reposts"`
	JobIDs           []string `json:"job_ids,omitempty"`
	Jobs             []*models.JobPosting `json:"-"`
}

type collectProgressFunc func(stage string, done, total int)

func runCollect(req collectRequest, st *store.Store, progress collectProgressFunc) (*collectRunResult, error) {
	req.Keywords = strings.TrimSpace(req.Keywords)
	req.Location = strings.TrimSpace(req.Location)
	req.PostedWithin = strings.TrimSpace(req.PostedWithin)
	if req.Keywords == "" {
		return nil, fmt.Errorf("keywords are required")
	}
	if req.Top < 0 {
		return nil, fmt.Errorf("top must be 0 or greater")
	}
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}

	postedWithin, err := resolvePostedWithin(req.PostedWithin)
	if err != nil {
		return nil, err
	}
	c, err := newClient(req.WithSession)
	if err != nil {
		return nil, err
	}

	if progress != nil {
		progress("search", 0, 0)
	}
	jobs, err := c.Search(linkedin.SearchParams{
		Keywords:     req.Keywords,
		Location:     req.Location,
		WorkType:     resolveWorkType(req.Remote, req.Hybrid, req.Onsite),
		PostedWithin: postedWithin,
		MaxJobs:      req.Top,
	})
	if err != nil {
		return nil, fmt.Errorf("collect search failed: %w", err)
	}
	for _, j := range jobs {
		j.Source = "linkedin"
	}

	result := &collectRunResult{SearchRuns: 1, Searched: len(jobs)}
	target := jobs
	if !req.ForceOverwrite {
		target, err = filterNewJobsWithStore(st, jobs)
		if err != nil {
			return nil, fmt.Errorf("filter existing jobs: %w", err)
		}
	}
	result.NewCandidates = len(target)
	result.Jobs = target
	if len(target) == 0 {
		return result, nil
	}

	if progress != nil {
		progress("details", 0, len(target))
	}
	c.FetchDetailsBatch(target, resolveDetailDelay(), func(done, total int) {
		if progress != nil {
			progress("details", done, total)
		}
	})

	for i, j := range target {
		j.ContentHash = store.ContentHash(j.Company, j.Title, j.Description, j.ListedAt)
		j.StructuralHash = store.StructuralHash(j.Company, j.Title, j.Description)

		classification, duplicateOf, classifyErr := st.ClassifyStructuralDuplicate(j)
		if classifyErr != nil {
			classification = store.DuplicateNew
		}
		if classification == store.DuplicateSameJobID {
			if existing, getErr := st.Get(j.ID); getErr == nil && existing != nil {
				classification = existing.DuplicateClassification
				duplicateOf = existing.DuplicateOfJobID
			}
			if classification == "" {
				classification = store.DuplicateNew
			}
		}
		j.DuplicateClassification = classification
		j.DuplicateOfJobID = duplicateOf

		switch classification {
		case store.DuplicateExact:
			result.ExactDuplicates++
		case store.DuplicateLikelyRepost:
			result.LikelyReposts++
		}

		if err := st.Upsert(j); err != nil {
			continue
		}
		result.Persisted++
		result.JobIDs = append(result.JobIDs, j.ID)
		if progress != nil {
			progress("persist", i+1, len(target))
		}
	}
	return result, nil
}

func filterNewJobsWithStore(st *store.Store, jobs []*models.JobPosting) ([]*models.JobPosting, error) {
	if len(jobs) == 0 {
		return jobs, nil
	}
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		if j != nil && strings.TrimSpace(j.ID) != "" {
			ids = append(ids, j.ID)
		}
	}
	existing, err := st.ExistingIDs(ids)
	if err != nil {
		return nil, err
	}
	out := make([]*models.JobPosting, 0, len(jobs))
	for _, j := range jobs {
		if j == nil || existing[j.ID] {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}
