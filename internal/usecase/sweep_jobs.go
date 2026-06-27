package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// SweepResult is the output of a SweepJobs execution. Counts are always
// populated so callers can emit an auditable summary without inspecting slices.
type SweepResult struct {
	// New contains jobs not previously seen in the cache.
	New []domain.Job
	// Seen contains jobs already present in the cache.
	Seen []domain.Job
	// NewCount is len(New).
	NewCount int
	// SeenCount is len(Seen).
	SeenCount int
	// ExcludedCount is the number of results filtered by company exclusion.
	ExcludedCount int
}

// SweepJobs runs a search and classifies results into new vs already-seen,
// using the local job cache to determine what is new.
type SweepJobs struct {
	Repo  JobsRepo
	Cache JobCache
}

// Execute runs the sweep. It:
//  1. Searches via Repo (applying ExcludeCompanies exclusion via SearchJobs).
//  2. Classifies each non-excluded job as NEW or SEEN based on the Cache.
//  3. Upserts all non-excluded jobs into the Cache (updates LastSeen/SurfacedBy).
//
// ExcludedCount reflects jobs removed by company-exclusion rules.
// All counts are always set; New and Seen are never nil.
func (uc SweepJobs) Execute(ctx context.Context, req JobSearchRequest) (SweepResult, error) {
	if req.Keyword == "" {
		return SweepResult{}, fmt.Errorf("keyword must not be empty")
	}

	// Reuse SearchJobs for normalisation, limit defaulting, and exclusion logic.
	searchUC := SearchJobs{Repo: uc.Repo}
	searchResult, err := searchUC.Execute(ctx, req)
	if err != nil {
		return SweepResult{}, fmt.Errorf("sweep search: %w", err)
	}

	now := time.Now().UTC()
	keyword := strings.TrimSpace(req.Keyword)

	newJobs := make([]domain.Job, 0)
	seenJobs := make([]domain.Job, 0)

	for _, j := range searchResult.Jobs {
		id := string(j.URN())
		if uc.Cache.Has(id) {
			seenJobs = append(seenJobs, j)
		} else {
			newJobs = append(newJobs, j)
		}
		uc.Cache.Upsert(j, keyword, now)
	}

	return SweepResult{
		New:           newJobs,
		Seen:          seenJobs,
		NewCount:      len(newJobs),
		SeenCount:     len(seenJobs),
		ExcludedCount: searchResult.Hidden,
	}, nil
}
