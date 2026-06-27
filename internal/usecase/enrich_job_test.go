package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/usecase"
)

// fakeEnricher is a test double for the usecase.Enricher port.
type fakeEnricher struct {
	insights domain.Insights
	err      error
	calls    int
}

func (f *fakeEnricher) Enrich(_ context.Context, _ domain.Job) (domain.Insights, error) {
	f.calls++
	return f.insights, f.err
}

func makeEnrichJob(t *testing.T) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN("urn:li:fsd_jobPosting:7777")
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}
	return domain.NewJob(
		urn,
		"Platform Engineer",
		"Remote",
		domain.NewCompany("", "Acme"),
		time.Time{},
		domain.NewPosting("Build stuff", "https://example.com/apply", 0),
	)
}

func wantInsights() domain.Insights {
	return domain.Insights{
		RealSummary:          "Platform engineering role",
		TopSkills:            []string{"Go", "Kubernetes"},
		SalaryRange:          "$120k",
		Seniority:            "Senior",
		CondensedDescription: "Build and maintain platform.",
		Notes:                "",
	}
}

func TestEnrichJob_Execute_CallsEnricherAndReturnsInsights(t *testing.T) {
	enricher := &fakeEnricher{insights: wantInsights()}
	uc := usecase.EnrichJob{Enricher: enricher}

	job := makeEnrichJob(t)
	ins, err := uc.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if ins.RealSummary != wantInsights().RealSummary {
		t.Errorf("RealSummary = %q; want %q", ins.RealSummary, wantInsights().RealSummary)
	}
	if enricher.calls != 1 {
		t.Errorf("enricher called %d times; want 1", enricher.calls)
	}
}

func TestEnrichJob_Execute_CachesInsights(t *testing.T) {
	enricher := &fakeEnricher{insights: wantInsights()}
	cache := newFakeCache()
	uc := usecase.EnrichJob{Enricher: enricher, Cache: cache}

	job := makeEnrichJob(t)
	_, err := uc.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, ok := cache.GetInsights(string(job.URN()))
	if !ok {
		t.Fatal("expected insights to be persisted in cache after enrichment")
	}
	if stored.RealSummary != wantInsights().RealSummary {
		t.Errorf("cached RealSummary = %q; want %q", stored.RealSummary, wantInsights().RealSummary)
	}
}

func TestEnrichJob_Execute_EnrichOnce_CacheHitSkipsEnricher(t *testing.T) {
	// Pre-populate cache with insights.
	cache := newFakeCache()
	job := makeEnrichJob(t)
	cached := domain.Insights{
		RealSummary:          "Cached summary",
		CondensedDescription: "Already done.",
	}
	cache.PutInsights(string(job.URN()), cached)

	enricher := &fakeEnricher{insights: wantInsights()}
	uc := usecase.EnrichJob{Enricher: enricher, Cache: cache}

	ins, err := uc.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if enricher.calls != 0 {
		t.Errorf("enricher called %d times; want 0 (cache hit should skip enricher)", enricher.calls)
	}
	if ins.RealSummary != "Cached summary" {
		t.Errorf("RealSummary = %q; want cached %q", ins.RealSummary, "Cached summary")
	}
}

func TestEnrichJob_Execute_EnrichOnce_SecondCallSkipsEnricher(t *testing.T) {
	// The first Execute stores insights; the second must not call enricher.
	enricher := &fakeEnricher{insights: wantInsights()}
	cache := newFakeCache()
	uc := usecase.EnrichJob{Enricher: enricher, Cache: cache}

	job := makeEnrichJob(t)
	for i := 0; i < 3; i++ {
		_, err := uc.Execute(context.Background(), job)
		if err != nil {
			t.Fatalf("Execute (call %d): %v", i+1, err)
		}
	}
	if enricher.calls != 1 {
		t.Errorf("enricher called %d times across 3 executions; want exactly 1", enricher.calls)
	}
}

func TestEnrichJob_Execute_EmptyURN_Error(t *testing.T) {
	enricher := &fakeEnricher{insights: wantInsights()}
	uc := usecase.EnrichJob{Enricher: enricher}

	// Construct a job with empty URN by using the zero value.
	var zeroJob domain.Job
	_, err := uc.Execute(context.Background(), zeroJob)
	if err == nil {
		t.Fatal("expected error for empty URN, got nil")
	}
}

func TestEnrichJob_Execute_EnricherError_Propagated(t *testing.T) {
	enrichErr := errors.New("llm call failed")
	enricher := &fakeEnricher{err: enrichErr}
	uc := usecase.EnrichJob{Enricher: enricher}

	job := makeEnrichJob(t)
	_, err := uc.Execute(context.Background(), job)
	if err == nil {
		t.Fatal("expected error from enricher, got nil")
	}
	if !errors.Is(err, enrichErr) {
		t.Errorf("err = %v; want to wrap %v", err, enrichErr)
	}
}

func TestEnrichJob_Execute_NilCache_DoesNotPanic(t *testing.T) {
	enricher := &fakeEnricher{insights: wantInsights()}
	uc := usecase.EnrichJob{Enricher: enricher, Cache: nil}

	job := makeEnrichJob(t)
	ins, err := uc.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute with nil cache: %v", err)
	}
	if ins.RealSummary != wantInsights().RealSummary {
		t.Errorf("RealSummary = %q; want %q", ins.RealSummary, wantInsights().RealSummary)
	}
}
