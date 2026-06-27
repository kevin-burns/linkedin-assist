// Package usecase contains application use-cases. It may import only
// internal/domain; it must not import voyager, auth, or any other
// infrastructure package.
package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// JobSearchRequest holds the inputs for a job-search use-case invocation.
type JobSearchRequest struct {
	Keyword           string
	Location          string
	Limit             int
	ExcludeCompanies  []string
	ExcludeTitleTerms []string
}

// JobSearchResult holds the output of a job-search use-case invocation.
type JobSearchResult struct {
	Jobs   []domain.Job
	Count  int
	Hidden int
}

// JobsRepo is the port the use-case drives. It is defined here (domain-only
// types) so the use-case layer has no dependency on any infrastructure package.
// The adapter that wires voyager.JobsClient to this interface lives in cmd/.
type JobsRepo interface {
	Search(ctx context.Context, req JobSearchRequest) ([]domain.Job, error)
}

// SearchJobs is the jobs-search use-case.
type SearchJobs struct {
	Repo JobsRepo
}

// Execute performs the job search and returns results. It returns an error if
// Repo.Search fails, or if the request is invalid.
func (uc SearchJobs) Execute(ctx context.Context, req JobSearchRequest) (JobSearchResult, error) {
	if req.Keyword == "" {
		return JobSearchResult{}, fmt.Errorf("keyword must not be empty")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 25
	}
	req.Limit = limit

	jobs, err := uc.Repo.Search(ctx, req)
	if err != nil {
		return JobSearchResult{}, fmt.Errorf("jobs repo search: %w", err)
	}
	// Guarantee a non-nil slice so callers (and the JSON presenter) get "[]"
	// rather than "null" on an empty result.
	if jobs == nil {
		jobs = []domain.Job{}
	}

	// Build normalised company exclusion terms (lower-case, trimmed, non-empty).
	var companyTerms []string
	for _, t := range req.ExcludeCompanies {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			companyTerms = append(companyTerms, t)
		}
	}

	// Build normalised title exclusion terms (lower-case, trimmed, non-empty).
	var titleTerms []string
	for _, t := range req.ExcludeTitleTerms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			titleTerms = append(titleTerms, t)
		}
	}

	// Filter jobs whose company name contains any company exclusion term, or
	// whose title contains any title exclusion term. Both filters feed Hidden.
	filtered := make([]domain.Job, 0, len(jobs))
	for _, j := range jobs {
		excluded := false

		// Company exclusion: case-insensitive substring match.
		if !excluded {
			name := strings.ToLower(j.Company().Name())
			for _, term := range companyTerms {
				if strings.Contains(name, term) {
					excluded = true
					break
				}
			}
		}

		// Title exclusion: case-insensitive substring match.
		if !excluded {
			title := strings.ToLower(j.Title())
			for _, term := range titleTerms {
				if strings.Contains(title, term) {
					excluded = true
					break
				}
			}
		}

		if !excluded {
			filtered = append(filtered, j)
		}
	}

	hidden := len(jobs) - len(filtered)
	return JobSearchResult{
		Jobs:   filtered,
		Count:  len(filtered),
		Hidden: hidden,
	}, nil
}
