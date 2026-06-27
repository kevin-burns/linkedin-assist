package usecase

import (
	"context"
	"fmt"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// DefaultEnrichCap is the cap applied when Cap <= 0 or when the caller needs
// a default value to pass to enrichMaxPerRun.
const DefaultEnrichCap = 25

// EnrichNewJobsResult summarises an EnrichNewJobs.Execute run. All fields are
// always populated so callers can emit an auditable one-liner without
// inspecting slices.
type EnrichNewJobsResult struct {
	// Enriched is the count of jobs for which enrichment succeeded (including
	// enrich-once cache hits — the insights were already present).
	Enriched int
	// SkippedByCap is the count of jobs not attempted because the per-run cap
	// was reached. It is always > 0 only when len(jobs) > Cap.
	SkippedByCap int
	// Errors is the count of jobs where GetJob or EnrichJob failed. Per-job
	// errors are non-fatal: the run continues and each failure is logged by the
	// caller. The top-level Execute always returns a nil error.
	Errors int
}

// EnrichNewJobs orchestrates the per-NEW-job enrichment pipeline for a sweep
// run. It composes existing GetJob and EnrichJob use-cases — no cache-first or
// enrich-once logic is duplicated here.
//
// For each job (up to Cap):
//  1. GetJob.Execute — cache-first detail fetch (supplies the description).
//  2. EnrichJob.Execute — enrich-once LLM call + persist insights.
//
// Errors from either step are counted but do not abort processing.
// All per-run accounting is returned in EnrichNewJobsResult.
type EnrichNewJobs struct {
	// GetJob is the detail-fetch use-case wired with the rate-limited voyager
	// transport. Routing through it guarantees the existing rate limiter is
	// respected for every detail fetch.
	GetJob GetJob
	// EnrichJob is the LLM enrichment use-case wired with the same cache used
	// for job storage so the enrich-once policy is honoured.
	EnrichJob EnrichJob
	// Cap is the maximum number of jobs to attempt per run. Jobs beyond Cap are
	// counted as SkippedByCap. A value <= 0 is treated as the default (25);
	// unlimited calls to the rate-limited voyager API are not a reachable mode.
	Cap int
	// ErrLogger is called for each per-job error so callers can emit a stderr
	// note per failure. It must not be nil when any job may fail.
	// Signature: func(urn domain.URN, stage string, err error).
	ErrLogger func(urn domain.URN, stage string, err error)
}

// Execute enriches each job in jobs up to Cap. It always returns nil as its
// error; per-job failures are counted in EnrichNewJobsResult.Errors and
// reported via ErrLogger (if set). On context cancellation the loop breaks
// cleanly; cancelled-but-not-attempted jobs are not counted as errors.
func (uc EnrichNewJobs) Execute(ctx context.Context, jobs []domain.Job) (EnrichNewJobsResult, error) {
	cap := uc.Cap
	if cap <= 0 {
		cap = DefaultEnrichCap
	}

	var res EnrichNewJobsResult

	for i, j := range jobs {
		if i >= cap {
			// We've hit the cap; count all remaining as skipped.
			res.SkippedByCap = len(jobs) - cap
			break
		}

		// Break cleanly on cancellation; do not count un-attempted jobs as errors.
		if ctx.Err() != nil {
			res.SkippedByCap = len(jobs) - i
			break
		}

		urn := j.URN()

		// Step 1: cache-first detail fetch — supplies the description.
		detail, err := uc.GetJob.Execute(ctx, urn)
		if err != nil {
			if uc.ErrLogger != nil {
				uc.ErrLogger(urn, "get", fmt.Errorf("detail fetch: %w", err))
			}
			res.Errors++
			continue
		}

		// Step 2: enrich-once LLM call + persist insights.
		_, err = uc.EnrichJob.Execute(ctx, detail)
		if err != nil {
			if uc.ErrLogger != nil {
				uc.ErrLogger(urn, "enrich", fmt.Errorf("enrich: %w", err))
			}
			res.Errors++
			continue
		}

		res.Enriched++
	}

	return res, nil
}
