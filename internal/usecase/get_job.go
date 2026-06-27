package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// JobGetter is the port the GetJob use-case drives. It is defined here (domain-only
// types) so the use-case layer has no dependency on any infrastructure package.
// The adapter that wires voyager.JobsClient to this interface lives in cmd/.
type JobGetter interface {
	Get(ctx context.Context, urn domain.URN) (domain.Job, error)
}

// GetJob is the job-detail use-case. When Cache is non-nil, it checks the
// cache before calling Repo. Set Refresh to true to bypass the cache and
// always fetch from Repo (the --refresh flag).
type GetJob struct {
	Repo    JobGetter
	Cache   JobCache // optional; if nil, always fetches from Repo
	Refresh bool     // if true, bypass cache and force re-fetch
}

// Execute fetches the full detail for the job identified by urn.
//
// Cache-first logic (when Cache is non-nil and Refresh is false):
//   - If the cache holds a record with a non-empty description, return it
//     without a Repo call.
//   - On a cache miss (or empty description), call Repo.Get, then upsert the
//     result into the cache.
//
// When Refresh is true, Repo.Get is always called and the result is upserted.
func (uc GetJob) Execute(ctx context.Context, urn domain.URN) (domain.Job, error) {
	if urn == "" {
		return domain.Job{}, fmt.Errorf("urn must not be empty")
	}

	id := string(urn)

	// Cache hit: return without a Repo call (unless --refresh).
	if uc.Cache != nil && !uc.Refresh {
		if j, hasDesc, ok := uc.Cache.Get(id); ok && hasDesc {
			return j, nil
		}
	}

	// Cache miss or refresh: call the repo.
	job, err := uc.Repo.Get(ctx, urn)
	if err != nil {
		return domain.Job{}, fmt.Errorf("jobs repo get: %w", err)
	}

	// Upsert into cache on successful fetch.
	if uc.Cache != nil {
		uc.Cache.Upsert(job, "", time.Now().UTC())
	}

	return job, nil
}
