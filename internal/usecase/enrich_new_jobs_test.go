package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/usecase"
)

// makeJobWithDesc builds a domain.Job that has a non-empty description so the
// cache's hasDescription check passes for the GetJob cache-hit path.
func makeJobWithDesc(t *testing.T, urnStr domain.URN, title string) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN(string(urnStr))
	if err != nil {
		t.Fatalf("ParseURN(%q): %v", urnStr, err)
	}
	return domain.NewJob(
		urn,
		title,
		"Remote",
		domain.NewCompany("", "Acme"),
		time.Time{},
		domain.NewPosting("Some description text", "https://example.com/apply", 0),
	)
}

// fakeJobGetterForEnrich is a controllable JobGetter for EnrichNewJobs tests.
// Each URN string maps to a configured result (job + error). If not configured,
// Get synthesises a job whose URN matches the request (convenience default).
type fakeJobGetterForEnrich struct {
	results map[string]struct {
		job domain.Job
		err error
	}
	calls []domain.URN
}

func (f *fakeJobGetterForEnrich) Get(_ context.Context, urn domain.URN) (domain.Job, error) {
	f.calls = append(f.calls, urn)
	if f.results != nil {
		if r, ok := f.results[string(urn)]; ok {
			return r.job, r.err
		}
	}
	// Convenience default: synthesise a job with a description.
	j := domain.NewJob(
		urn,
		"Fake Title",
		"Remote",
		domain.NewCompany("", "Acme"),
		time.Time{},
		domain.NewPosting("desc", "https://example.com", 0),
	)
	return j, nil
}

// fakeEnricherForSweep is a controllable Enricher for EnrichNewJobs tests.
// errors maps urn-string → error; if absent, Enrich returns wantInsights().
type fakeEnricherForSweep struct {
	errors map[string]error
	calls  []domain.URN
}

func (f *fakeEnricherForSweep) Enrich(_ context.Context, job domain.Job) (domain.Insights, error) {
	f.calls = append(f.calls, job.URN())
	if f.errors != nil {
		if err, ok := f.errors[string(job.URN())]; ok {
			return domain.Insights{}, err
		}
	}
	return wantInsights(), nil
}

// --- EnrichNewJobs tests ---

// TestEnrichNewJobs_AllEnriched: NEW=3, cap=25 → all 3 enriched, 0 skipped.
func TestEnrichNewJobs_AllEnriched(t *testing.T) {
	jobs := []domain.Job{
		makeJobWithDesc(t, "urn:li:fsd_jobPosting:1001", "Eng A"),
		makeJobWithDesc(t, "urn:li:fsd_jobPosting:1002", "Eng B"),
		makeJobWithDesc(t, "urn:li:fsd_jobPosting:1003", "Eng C"),
	}

	getter := &fakeJobGetterForEnrich{}
	enricher := &fakeEnricherForSweep{}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 25}

	result, err := uc.Execute(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Enriched != 3 {
		t.Errorf("Enriched = %d; want 3", result.Enriched)
	}
	if result.SkippedByCap != 0 {
		t.Errorf("SkippedByCap = %d; want 0", result.SkippedByCap)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d; want 0", result.Errors)
	}
}

// TestEnrichNewJobs_CapReached: NEW=40, cap=25 → 25 enriched, 15 skipped.
func TestEnrichNewJobs_CapReached(t *testing.T) {
	jobs := make([]domain.Job, 40)
	for i := 0; i < 40; i++ {
		urnStr := domain.URN(fmt.Sprintf("urn:li:fsd_jobPosting:2%03d", i))
		jobs[i] = makeJobWithDesc(t, urnStr, "Job")
	}

	getter := &fakeJobGetterForEnrich{}
	enricher := &fakeEnricherForSweep{}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 25}

	result, err := uc.Execute(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Enriched != 25 {
		t.Errorf("Enriched = %d; want 25", result.Enriched)
	}
	if result.SkippedByCap != 15 {
		t.Errorf("SkippedByCap = %d; want 15", result.SkippedByCap)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d; want 0", result.Errors)
	}
	// Confirm the getter was only called for the first 25.
	if len(getter.calls) != 25 {
		t.Errorf("getter called %d times; want 25 (capped)", len(getter.calls))
	}
}

// TestEnrichNewJobs_PerJobErrorContinues: one bad detail-fetch doesn't abort the sweep.
func TestEnrichNewJobs_PerJobErrorContinues(t *testing.T) {
	badURN := domain.URN("urn:li:fsd_jobPosting:3001")
	goodURN := domain.URN("urn:li:fsd_jobPosting:3002")
	good2URN := domain.URN("urn:li:fsd_jobPosting:3003")

	jobs := []domain.Job{
		makeJobWithDesc(t, badURN, "Bad Job"),
		makeJobWithDesc(t, goodURN, "Good Job"),
		makeJobWithDesc(t, good2URN, "Good Job 2"),
	}

	fetchErr := errors.New("detail fetch timeout")
	getter := &fakeJobGetterForEnrich{
		results: map[string]struct {
			job domain.Job
			err error
		}{
			string(badURN): {err: fetchErr},
			string(goodURN): {
				job: makeJobWithDesc(t, goodURN, "Good Job"),
			},
			string(good2URN): {
				job: makeJobWithDesc(t, good2URN, "Good Job 2"),
			},
		},
	}
	enricher := &fakeEnricherForSweep{}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 25}

	result, err := uc.Execute(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Execute must not return an error even when individual jobs fail: %v", err)
	}

	if result.Errors != 1 {
		t.Errorf("Errors = %d; want 1 (the bad detail-fetch)", result.Errors)
	}
	if result.Enriched != 2 {
		t.Errorf("Enriched = %d; want 2 (the two good jobs)", result.Enriched)
	}
	if result.SkippedByCap != 0 {
		t.Errorf("SkippedByCap = %d; want 0", result.SkippedByCap)
	}
}

// TestEnrichNewJobs_EnrichErrorContinues: an enrich (LLM) error per-job is also non-fatal.
func TestEnrichNewJobs_EnrichErrorContinues(t *testing.T) {
	badURN := domain.URN("urn:li:fsd_jobPosting:5001")
	goodURN := domain.URN("urn:li:fsd_jobPosting:5002")

	jobs := []domain.Job{
		makeJobWithDesc(t, badURN, "Bad Enrich Job"),
		makeJobWithDesc(t, goodURN, "Good Job"),
	}

	enrichErr := errors.New("llm timed out")
	getter := &fakeJobGetterForEnrich{}
	enricher := &fakeEnricherForSweep{
		errors: map[string]error{
			string(badURN): enrichErr,
		},
	}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 25}

	result, err := uc.Execute(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Execute must not return an error on per-job enrich failure: %v", err)
	}

	if result.Errors != 1 {
		t.Errorf("Errors = %d; want 1 (the bad enrich)", result.Errors)
	}
	if result.Enriched != 1 {
		t.Errorf("Enriched = %d; want 1 (the good job)", result.Enriched)
	}
}

// TestEnrichNewJobs_EnrichOnce: a NEW job that already has cached insights is not re-enriched.
func TestEnrichNewJobs_EnrichOnce(t *testing.T) {
	cachedURN := domain.URN("urn:li:fsd_jobPosting:4001")
	freshURN := domain.URN("urn:li:fsd_jobPosting:4002")

	cachedJob := makeJobWithDesc(t, cachedURN, "Cached Job")
	freshJob := makeJobWithDesc(t, freshURN, "Fresh Job")

	cache := newFakeCache()
	// Pre-populate the job and its insights in cache so EnrichJob returns immediately.
	cache.Upsert(cachedJob, "", time.Now())
	cache.PutInsights(string(cachedURN), domain.Insights{RealSummary: "already enriched"})

	getter := &fakeJobGetterForEnrich{
		results: map[string]struct {
			job domain.Job
			err error
		}{
			string(cachedURN): {job: cachedJob},
			string(freshURN):  {job: freshJob},
		},
	}
	enricher := &fakeEnricherForSweep{}

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 25}

	jobs := []domain.Job{cachedJob, freshJob}
	result, err := uc.Execute(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Both counted as "enriched" (one from cache, one freshly).
	if result.Enriched != 2 {
		t.Errorf("Enriched = %d; want 2", result.Enriched)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d; want 0", result.Errors)
	}

	// The enricher must NOT have been called for cachedJob (enrich-once).
	for _, urn := range enricher.calls {
		if urn == cachedURN {
			t.Errorf("enricher was called for already-cached URN %q; enrich-once should skip it", cachedURN)
		}
	}

	// Enricher should have been called exactly once — for the fresh job only.
	if len(enricher.calls) != 1 {
		t.Errorf("enricher called %d times; want exactly 1 (freshJob only)", len(enricher.calls))
	}
}

// TestEnrichNewJobs_EmptySlice: no-op on empty input.
func TestEnrichNewJobs_EmptySlice(t *testing.T) {
	getter := &fakeJobGetterForEnrich{}
	enricher := &fakeEnricherForSweep{}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 25}

	result, err := uc.Execute(context.Background(), []domain.Job{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Enriched != 0 {
		t.Errorf("Enriched = %d; want 0", result.Enriched)
	}
	if result.SkippedByCap != 0 {
		t.Errorf("SkippedByCap = %d; want 0", result.SkippedByCap)
	}
}

// TestEnrichNewJobs_ZeroCapClampsToDefault: Cap <= 0 must use DefaultEnrichCap,
// not allow unlimited voyager calls. With 30 jobs and Cap=0, only DefaultEnrichCap
// (25) should be attempted.
func TestEnrichNewJobs_ZeroCapClampsToDefault(t *testing.T) {
	jobs := make([]domain.Job, 30)
	for i := 0; i < 30; i++ {
		urnStr := domain.URN(fmt.Sprintf("urn:li:fsd_jobPosting:9%03d", i))
		jobs[i] = makeJobWithDesc(t, urnStr, "Job")
	}

	getter := &fakeJobGetterForEnrich{}
	enricher := &fakeEnricherForSweep{}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	// Cap == 0: must clamp to DefaultEnrichCap (25), not attempt all 30.
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 0}

	result, err := uc.Execute(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Enriched != usecase.DefaultEnrichCap {
		t.Errorf("Enriched = %d; want %d (DefaultEnrichCap)", result.Enriched, usecase.DefaultEnrichCap)
	}
	if result.SkippedByCap != 30-usecase.DefaultEnrichCap {
		t.Errorf("SkippedByCap = %d; want %d", result.SkippedByCap, 30-usecase.DefaultEnrichCap)
	}
	// Confirm Cap=0 did not allow unlimited calls.
	if len(getter.calls) != usecase.DefaultEnrichCap {
		t.Errorf("getter called %d times; want %d (clamped to DefaultEnrichCap)", len(getter.calls), usecase.DefaultEnrichCap)
	}
}

// TestEnrichNewJobs_NegativeCapClampsToDefault: negative Cap behaves same as zero.
func TestEnrichNewJobs_NegativeCapClampsToDefault(t *testing.T) {
	jobs := make([]domain.Job, 30)
	for i := 0; i < 30; i++ {
		urnStr := domain.URN(fmt.Sprintf("urn:li:fsd_jobPosting:8%03d", i))
		jobs[i] = makeJobWithDesc(t, urnStr, "Job")
	}

	getter := &fakeJobGetterForEnrich{}
	enricher := &fakeEnricherForSweep{}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: -5}

	result, err := uc.Execute(context.Background(), jobs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Enriched != usecase.DefaultEnrichCap {
		t.Errorf("Enriched = %d; want %d (DefaultEnrichCap)", result.Enriched, usecase.DefaultEnrichCap)
	}
	if len(getter.calls) != usecase.DefaultEnrichCap {
		t.Errorf("getter called %d times; want %d (clamped)", len(getter.calls), usecase.DefaultEnrichCap)
	}
}

// TestEnrichNewJobs_CtxCancellation: on context cancellation the loop breaks
// cleanly and un-attempted jobs are not counted as errors.
func TestEnrichNewJobs_CtxCancellation(t *testing.T) {
	jobs := make([]domain.Job, 10)
	for i := 0; i < 10; i++ {
		urnStr := domain.URN(fmt.Sprintf("urn:li:fsd_jobPosting:7%03d", i))
		jobs[i] = makeJobWithDesc(t, urnStr, "Job")
	}

	getter := &fakeJobGetterForEnrich{}
	enricher := &fakeEnricherForSweep{}
	cache := newFakeCache()

	getUC := usecase.GetJob{Repo: getter, Cache: cache}
	enrichUC := usecase.EnrichJob{Enricher: enricher, Cache: cache}
	uc := usecase.EnrichNewJobs{GetJob: getUC, EnrichJob: enrichUC, Cap: 25}

	// Cancel before Execute starts — all jobs should be skipped, not errored.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := uc.Execute(ctx, jobs)
	if err != nil {
		t.Fatalf("Execute must not return top-level error on cancellation: %v", err)
	}

	// No jobs should have been processed after the cancelled context.
	if result.Enriched != 0 {
		t.Errorf("Enriched = %d; want 0 (cancelled before any work)", result.Enriched)
	}
	// Cancelled jobs must not inflate the error count.
	if result.Errors != 0 {
		t.Errorf("Errors = %d; want 0 (cancellation must not be counted as errors)", result.Errors)
	}
}
