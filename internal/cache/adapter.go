package cache

import (
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// UCAdapter wraps *Store and implements the usecase.JobCache port.
// It lives in this package (alongside Store) so cmd can import just
// internal/cache and pass a *UCAdapter to the use-cases.
type UCAdapter struct {
	s *Store
}

// NewUCAdapter wraps a Store to satisfy the usecase.JobCache interface.
func NewUCAdapter(s *Store) *UCAdapter {
	return &UCAdapter{s: s}
}

// Has reports whether a job with the given URN string is cached.
func (a *UCAdapter) Has(id string) bool {
	return a.s.Has(id)
}

// Get returns the cached domain.Job, whether it has a non-empty description,
// and whether the record exists at all.
func (a *UCAdapter) Get(id string) (domain.Job, bool, bool) {
	rec, ok := a.s.Get(id)
	if !ok {
		return domain.Job{}, false, false
	}
	j, err := rec.ToDomain()
	if err != nil {
		return domain.Job{}, false, false
	}
	return j, rec.Description != "", true
}

// Upsert inserts or updates the cached record for j.
func (a *UCAdapter) Upsert(j domain.Job, keyword string, now time.Time) {
	a.s.Upsert(j, keyword, now)
}

// Len returns the number of records in the underlying Store.
func (a *UCAdapter) Len() int {
	return a.s.Len()
}

// GetInsights retrieves the LLM-generated insights for the given URN.
func (a *UCAdapter) GetInsights(id string) (domain.Insights, bool) {
	return a.s.GetInsights(id)
}

// PutInsights persists insights for the given URN. No-op if URN not cached.
func (a *UCAdapter) PutInsights(id string, ins domain.Insights) {
	a.s.PutInsights(id, ins)
}

// Flush persists the underlying Store to disk. Should be called when done
// with the adapter (e.g. via defer).
func (a *UCAdapter) Flush() error {
	return a.s.Flush()
}
