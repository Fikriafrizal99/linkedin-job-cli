package cmd

import (
	"fmt"
	"strings"

	"linkedin-jobs/internal/store"
)

const (
	maxCollectQueries      = 20
	maxCollectLocations    = 10
	maxCollectCombinations = 50
	maxCollectValueLength  = 160
)

type collectPlan struct {
	Queries   []string
	Locations []string
	Requests  []collectRequest
}

func splitCollectValues(values []string, field string, maxItems int, required bool) ([]string, error) {
	out := make([]string, 0)
	seen := map[string]bool{}
	for _, raw := range values {
		raw = strings.ReplaceAll(raw, "\r\n", "\n")
		raw = strings.ReplaceAll(raw, "\r", "\n")
		for _, part := range strings.Split(raw, "\n") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			if len(value) > maxCollectValueLength {
				return out, fmt.Errorf("%s must be %d characters or fewer per line", field, maxCollectValueLength)
			}
			key := strings.ToLower(value)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, value)
			if len(out) > maxItems {
				return out, fmt.Errorf("use at most %d %s", maxItems, field)
			}
		}
	}
	if required && len(out) == 0 {
		return out, fmt.Errorf("%s are required", field)
	}
	return out, nil
}

func buildCollectPlan(base collectRequest, queryValues, locationValues []string) (collectPlan, error) {
	plan := collectPlan{}
	queries, err := splitCollectValues(queryValues, "queries", maxCollectQueries, true)
	if err != nil {
		plan.Queries = queries
		return plan, err
	}
	locations, err := splitCollectValues(locationValues, "locations", maxCollectLocations, false)
	if err != nil {
		plan.Queries, plan.Locations = queries, locations
		return plan, err
	}
	if len(locations) == 0 {
		locations = []string{""}
	}
	plan.Queries, plan.Locations = queries, locations
	if len(queries)*len(locations) > maxCollectCombinations {
		return plan, fmt.Errorf("too many search combinations: %d queries × %d locations = %d; maximum is %d", len(queries), len(locations), len(queries)*len(locations), maxCollectCombinations)
	}
	for _, query := range queries {
		for _, location := range locations {
			req := base
			req.Keywords = query
			req.Location = location
			plan.Requests = append(plan.Requests, req)
		}
	}
	return plan, nil
}

func collectLocationsText(locations []string) string {
	values := make([]string, 0, len(locations))
	for _, location := range locations {
		if strings.TrimSpace(location) != "" {
			values = append(values, location)
		}
	}
	return strings.Join(values, "\n")
}

func runCollectBatch(plan collectPlan, st *store.Store, progress collectProgressFunc) (*collectRunResult, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if len(plan.Requests) == 0 {
		return nil, fmt.Errorf("at least one search combination is required")
	}
	postedWithin := ""
	if len(plan.Requests) > 0 {
		postedWithin = plan.Requests[0].PostedWithin
	}
	runID, err := st.CreateCollectionRun(plan.Queries, plan.Locations, postedWithin)
	if err != nil {
		return nil, fmt.Errorf("create collection run: %w", err)
	}
	result := &collectRunResult{RunID: runID, SearchRuns: len(plan.Requests)}
	finishRun := func(status string, runErr error) {
		errText := ""
		if runErr != nil {
			errText = runErr.Error()
		}
		_ = st.FinishCollectionRun(runID, store.CollectionRunFinish{
			SearchRuns: result.SearchRuns,
			SearchedCount: result.Searched,
			NewCount: result.NewCandidates,
			PersistedCount: result.Persisted,
			ExactDuplicateCount: result.ExactDuplicates,
			LikelyRepostCount: result.LikelyReposts,
			Status: status,
			Error: errText,
		})
	}
	seenJobs := map[string]bool{}
	for i, req := range plan.Requests {
		if progress != nil {
			progress("batch", i, len(plan.Requests))
		}
		part, err := runCollect(req, st, progress)
		if err != nil {
			location := strings.TrimSpace(req.Location)
			if location == "" {
				location = "all locations"
			}
			runErr := fmt.Errorf("search %d/%d failed for %q @ %q: %w", i+1, len(plan.Requests), req.Keywords, location, err)
			finishRun("FAILED", runErr)
			return result, runErr
		}
		result.Searched += part.Searched
		result.NewCandidates += part.NewCandidates
		result.Persisted += part.Persisted
		result.ExactDuplicates += part.ExactDuplicates
		result.LikelyReposts += part.LikelyReposts
		result.JobIDs = append(result.JobIDs, part.JobIDs...)
		for _, id := range part.JobIDs {
			classification := ""
			for _, job := range part.Jobs {
				if job != nil && job.ID == id {
					classification = job.DuplicateClassification
					break
				}
			}
			if err := st.AddCollectionRunJob(runID, id, classification, true); err != nil {
				runErr := fmt.Errorf("record collection run job %s: %w", id, err)
				finishRun("FAILED", runErr)
				return result, runErr
			}
		}
		for _, job := range part.Jobs {
			if job == nil || seenJobs[job.ID] {
				continue
			}
			seenJobs[job.ID] = true
			result.Jobs = append(result.Jobs, job)
		}
		if progress != nil {
			progress("batch", i+1, len(plan.Requests))
		}
	}
	if err := st.FinishCollectionRun(runID, store.CollectionRunFinish{
		SearchRuns: result.SearchRuns,
		SearchedCount: result.Searched,
		NewCount: result.NewCandidates,
		PersistedCount: result.Persisted,
		ExactDuplicateCount: result.ExactDuplicates,
		LikelyRepostCount: result.LikelyReposts,
		Status: "COMPLETED",
	}); err != nil {
		return result, fmt.Errorf("finish collection run: %w", err)
	}
	return result, nil
}
