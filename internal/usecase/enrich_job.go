package usecase

import (
	"context"
	"fmt"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// Enricher is the port the EnrichJob use-case drives. It is defined here (with
// only domain types in its signature) so the use-case layer has no dependency
// on the internal/enrich infrastructure package. The adapter in internal/enrich
// satisfies this interface via Go duck typing; it does NOT import usecase.
type Enricher interface {
	Enrich(ctx context.Context, job domain.Job) (domain.Insights, error)
}

// EnrichJob enriches a job posting with LLM-generated insights. It applies an
// enrich-once policy: if insights are already cached for the given job, they
// are returned immediately without calling the Enricher.
type EnrichJob struct {
	Enricher Enricher
	Cache    JobCache // optional; if nil, insights are never persisted
}

// Execute enriches job and returns the resulting domain.Insights.
//
// Enrich-once: if Cache is non-nil and already holds insights for job.URN(),
// those cached insights are returned immediately and the Enricher is not called.
//
// Otherwise, the Enricher is called once. If Cache is non-nil, the resulting
// insights are persisted before returning.
func (uc EnrichJob) Execute(ctx context.Context, job domain.Job) (domain.Insights, error) {
	id := string(job.URN())
	if id == "" {
		return domain.Insights{}, fmt.Errorf("enrich job: job URN must not be empty")
	}

	// Enrich-once: return cached insights if available.
	if uc.Cache != nil {
		if ins, ok := uc.Cache.GetInsights(id); ok {
			return ins, nil
		}
	}

	ins, err := uc.Enricher.Enrich(ctx, job)
	if err != nil {
		return domain.Insights{}, fmt.Errorf("enrich job: %w", err)
	}

	// Persist insights so subsequent calls skip the LLM.
	if uc.Cache != nil {
		uc.Cache.PutInsights(id, ins)
	}

	return ins, nil
}
