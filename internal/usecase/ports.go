package usecase

import (
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// JobCache is the port the sweep (and get) use-cases drive for local caching.
// It is defined here so usecase/ has no dependency on internal/cache.
// The implementation lives in internal/cache and is wired in cmd/.
type JobCache interface {
	// Has reports whether a job with the given URN string is cached.
	Has(id string) bool
	// Get returns the cached domain.Job and whether the cache held a
	// non-empty description for that job. If id is not cached, ok is false.
	Get(id string) (j domain.Job, hasDescription bool, ok bool)
	// Upsert inserts or updates the cached record for j. keyword is the
	// search term that surfaced the job (may be ""). now is the timestamp
	// to record for FirstSeen (insert) / LastSeen (update).
	Upsert(j domain.Job, keyword string, now time.Time)
	// Len returns the number of records currently in the cache.
	Len() int

	// GetInsights retrieves the LLM-generated insights for the given URN,
	// returning (insights, true) when present or (zero, false) when absent.
	GetInsights(id string) (domain.Insights, bool)
	// PutInsights persists insights for the given URN. It is a no-op when
	// the underlying record does not exist (i.e., Upsert the job first).
	PutInsights(id string, ins domain.Insights)
}

// noopCache is a JobCache that never stores anything. Used when the real
// cache could not be opened so use-cases degrade gracefully.
type noopCache struct{}

func (noopCache) Has(_ string) bool                          { return false }
func (noopCache) Get(_ string) (domain.Job, bool, bool)      { return domain.Job{}, false, false }
func (noopCache) Upsert(_ domain.Job, _ string, _ time.Time) {}
func (noopCache) Len() int                                   { return 0 }
func (noopCache) GetInsights(_ string) (domain.Insights, bool) {
	return domain.Insights{}, false
}
func (noopCache) PutInsights(_ string, _ domain.Insights) {}

// NoopCache returns a JobCache that discards all writes and reports every
// lookup as a miss. Pass it when the real cache is unavailable.
func NoopCache() JobCache { return noopCache{} }
