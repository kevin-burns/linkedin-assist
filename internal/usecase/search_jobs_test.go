package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/usecase"
)

// fakeJobsRepo is a test double that implements usecase.JobsRepo without any
// voyager or auth import.
type fakeJobsRepo struct {
	jobs []domain.Job
	err  error
	// lastReq captures the last request for assertion.
	lastReq usecase.JobSearchRequest
}

func (f *fakeJobsRepo) Search(ctx context.Context, req usecase.JobSearchRequest) ([]domain.Job, error) {
	f.lastReq = req
	return f.jobs, f.err
}

func makeJob(t *testing.T, urnStr, title, location, company string) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN(urnStr)
	if err != nil {
		t.Fatalf("ParseURN(%q): %v", urnStr, err)
	}
	return domain.NewJob(
		urn,
		title,
		location,
		domain.NewCompany("", company),
		time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		domain.NewPosting("", "", 0),
	)
}

func TestSearchJobs_Execute_PassesThrough(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:1001", "Software Engineer", "San Francisco, CA", "Acme Corp")
	job2 := makeJob(t, "urn:li:job:1002", "Staff Engineer", "Remote", "Widgets Inc")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1, job2}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:  "engineer",
		Location: "San Francisco",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d; want 2", result.Count)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("len(Jobs) = %d; want 2", len(result.Jobs))
	}
	if result.Jobs[0].Title() != "Software Engineer" {
		t.Errorf("Jobs[0].Title = %q; want %q", result.Jobs[0].Title(), "Software Engineer")
	}
	if result.Jobs[1].Title() != "Staff Engineer" {
		t.Errorf("Jobs[1].Title = %q; want %q", result.Jobs[1].Title(), "Staff Engineer")
	}
}

func TestSearchJobs_Execute_CountMatchesLen(t *testing.T) {
	jobs := []domain.Job{
		makeJob(t, "urn:li:job:2001", "Backend Engineer", "New York", "Corp A"),
		makeJob(t, "urn:li:job:2002", "Frontend Engineer", "Austin", "Corp B"),
		makeJob(t, "urn:li:job:2003", "DevOps Engineer", "Seattle", "Corp C"),
	}
	repo := &fakeJobsRepo{jobs: jobs}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword: "engineer",
		Limit:   25,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != len(jobs) {
		t.Errorf("Count = %d; want %d", result.Count, len(jobs))
	}
}

func TestSearchJobs_Execute_DefaultLimit(t *testing.T) {
	repo := &fakeJobsRepo{jobs: []domain.Job{}}
	uc := usecase.SearchJobs{Repo: repo}

	_, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword: "go developer",
		Limit:   0, // should default to 25
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.lastReq.Limit != 25 {
		t.Errorf("Limit passed to repo = %d; want 25", repo.lastReq.Limit)
	}
}

func TestSearchJobs_Execute_EmptyKeywordError(t *testing.T) {
	repo := &fakeJobsRepo{jobs: []domain.Job{}}
	uc := usecase.SearchJobs{Repo: repo}

	_, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword: "",
	})
	if err == nil {
		t.Fatal("expected error for empty keyword, got nil")
	}
}

func TestSearchJobs_Execute_RepoError(t *testing.T) {
	repoErr := errors.New("network timeout")
	repo := &fakeJobsRepo{err: repoErr}
	uc := usecase.SearchJobs{Repo: repo}

	_, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword: "engineer",
	})
	if err == nil {
		t.Fatal("expected error from repo, got nil")
	}
	if !errors.Is(err, repoErr) {
		t.Errorf("error %v; want to wrap %v", err, repoErr)
	}
}

func TestSearchJobs_Execute_EmptyResults(t *testing.T) {
	repo := &fakeJobsRepo{jobs: []domain.Job{}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:  "unicorn position",
		Location: "Mars",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 0 {
		t.Errorf("Count = %d; want 0", result.Count)
	}
	if result.Jobs == nil {
		t.Error("Jobs should be non-nil empty slice, got nil")
	}
}

// --- Company exclusion tests ---

func TestSearchJobs_Execute_ExcludeCompanies_RemovesMatches(t *testing.T) {
	deloitte := makeJob(t, "urn:li:job:3001", "Consultant", "Chicago", "Deloitte")
	aws := makeJob(t, "urn:li:job:3002", "Cloud Engineer", "Remote", "Amazon Web Services (AWS)")
	acme := makeJob(t, "urn:li:job:3003", "Backend Engineer", "Austin", "Acme Corp")

	repo := &fakeJobsRepo{jobs: []domain.Job{deloitte, aws, acme}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:          "engineer",
		ExcludeCompanies: []string{"deloitte", "aws"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 2 {
		t.Errorf("Hidden = %d; want 2", result.Hidden)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d; want 1", result.Count)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("len(Jobs) = %d; want 1", len(result.Jobs))
	}
	if result.Jobs[0].Company().Name() != "Acme Corp" {
		t.Errorf("remaining job company = %q; want %q", result.Jobs[0].Company().Name(), "Acme Corp")
	}
}

func TestSearchJobs_Execute_ExcludeCompanies_CaseInsensitive(t *testing.T) {
	deloitte := makeJob(t, "urn:li:job:4001", "Analyst", "NYC", "Deloitte")
	other := makeJob(t, "urn:li:job:4002", "Engineer", "Remote", "Other Co")

	repo := &fakeJobsRepo{jobs: []domain.Job{deloitte, other}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:          "analyst",
		ExcludeCompanies: []string{"DELOITTE"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 1 {
		t.Errorf("Hidden = %d; want 1", result.Hidden)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d; want 1", result.Count)
	}
}

func TestSearchJobs_Execute_ExcludeCompanies_EmptyAndWhitespaceTermsIgnored(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:5001", "Engineer", "Remote", "Acme")
	job2 := makeJob(t, "urn:li:job:5002", "Designer", "NYC", "Widgets")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1, job2}}
	uc := usecase.SearchJobs{Repo: repo}

	// Empty string, whitespace-only -- these must not filter anything.
	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:          "engineer",
		ExcludeCompanies: []string{"", "   ", "\t"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 0 {
		t.Errorf("Hidden = %d; want 0 (blank terms must not filter)", result.Hidden)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d; want 2", result.Count)
	}
}

func TestSearchJobs_Execute_ExcludeCompanies_NilExcludeList_NoFiltering(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:6001", "Engineer", "Remote", "Deloitte")
	job2 := makeJob(t, "urn:li:job:6002", "Manager", "NYC", "AWS")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1, job2}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:          "engineer",
		ExcludeCompanies: nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 0 {
		t.Errorf("Hidden = %d; want 0 (nil exclusion list must not filter)", result.Hidden)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d; want 2", result.Count)
	}
}

func TestSearchJobs_Execute_ExcludeCompanies_ResultNonNilWhenAllFiltered(t *testing.T) {
	deloitte := makeJob(t, "urn:li:job:7001", "Consultant", "Chicago", "Deloitte")

	repo := &fakeJobsRepo{jobs: []domain.Job{deloitte}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:          "consultant",
		ExcludeCompanies: []string{"deloitte"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 1 {
		t.Errorf("Hidden = %d; want 1", result.Hidden)
	}
	if result.Count != 0 {
		t.Errorf("Count = %d; want 0", result.Count)
	}
	if result.Jobs == nil {
		t.Error("Jobs must be non-nil even when all results are filtered out")
	}
}

// --- Title exclusion tests ---

func TestSearchJobs_Execute_ExcludeTitleTerms_DropsMatchingJobs(t *testing.T) {
	recruiter := makeJob(t, "urn:li:job:8001", "Technical Recruiter", "NYC", "StaffCo")
	engineer := makeJob(t, "urn:li:job:8002", "Platform Engineer", "Remote", "Acme")
	mgr := makeJob(t, "urn:li:job:8003", "Engineering Manager", "Austin", "WidgetCo")

	repo := &fakeJobsRepo{jobs: []domain.Job{recruiter, engineer, mgr}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:           "engineer",
		ExcludeTitleTerms: []string{"recruiter", "manager"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 2 {
		t.Errorf("Hidden = %d; want 2", result.Hidden)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d; want 1", result.Count)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("len(Jobs) = %d; want 1", len(result.Jobs))
	}
	if result.Jobs[0].Title() != "Platform Engineer" {
		t.Errorf("remaining job title = %q; want %q", result.Jobs[0].Title(), "Platform Engineer")
	}
}

func TestSearchJobs_Execute_ExcludeTitleTerms_CaseInsensitive(t *testing.T) {
	recruiter := makeJob(t, "urn:li:job:8101", "SENIOR RECRUITER", "NYC", "StaffCo")
	engineer := makeJob(t, "urn:li:job:8102", "Backend Engineer", "Remote", "Acme")

	repo := &fakeJobsRepo{jobs: []domain.Job{recruiter, engineer}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:           "engineer",
		ExcludeTitleTerms: []string{"recruiter"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 1 {
		t.Errorf("Hidden = %d; want 1 (case-insensitive title match)", result.Hidden)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d; want 1", result.Count)
	}
}

func TestSearchJobs_Execute_ExcludeTitleTerms_WorksAlongsideCompanyExclusion(t *testing.T) {
	// Excluded by title.
	recruiter := makeJob(t, "urn:li:job:8201", "Technical Recruiter", "NYC", "Corp A")
	// Excluded by company.
	deloitte := makeJob(t, "urn:li:job:8202", "Engineer", "Chicago", "Deloitte")
	// Kept.
	engineer := makeJob(t, "urn:li:job:8203", "Platform Engineer", "Remote", "Acme")

	repo := &fakeJobsRepo{jobs: []domain.Job{recruiter, deloitte, engineer}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:           "engineer",
		ExcludeCompanies:  []string{"deloitte"},
		ExcludeTitleTerms: []string{"recruiter"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both filters feed Hidden.
	if result.Hidden != 2 {
		t.Errorf("Hidden = %d; want 2 (company + title both count)", result.Hidden)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d; want 1", result.Count)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("len(Jobs) = %d; want 1", len(result.Jobs))
	}
	if result.Jobs[0].Title() != "Platform Engineer" {
		t.Errorf("remaining job title = %q; want %q", result.Jobs[0].Title(), "Platform Engineer")
	}
}

func TestSearchJobs_Execute_ExcludeTitleTerms_NonMatchingTermsKeepJobs(t *testing.T) {
	engineer := makeJob(t, "urn:li:job:8301", "Platform Engineer", "Remote", "Acme")
	sre := makeJob(t, "urn:li:job:8302", "SRE", "NYC", "Corp B")

	repo := &fakeJobsRepo{jobs: []domain.Job{engineer, sre}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:           "engineer",
		ExcludeTitleTerms: []string{"recruiter", "manager"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 0 {
		t.Errorf("Hidden = %d; want 0 (no title matches)", result.Hidden)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d; want 2", result.Count)
	}
}

func TestSearchJobs_Execute_ExcludeTitleTerms_EmptySlice_NoOp(t *testing.T) {
	engineer := makeJob(t, "urn:li:job:8401", "Platform Engineer", "Remote", "Acme")

	repo := &fakeJobsRepo{jobs: []domain.Job{engineer}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:           "engineer",
		ExcludeTitleTerms: []string{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 0 {
		t.Errorf("Hidden = %d; want 0 (empty ExcludeTitleTerms must be a no-op)", result.Hidden)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d; want 1", result.Count)
	}
}

func TestSearchJobs_Execute_ExcludeTitleTerms_NilSlice_NoOp(t *testing.T) {
	engineer := makeJob(t, "urn:li:job:8501", "Platform Engineer", "Remote", "Acme")

	repo := &fakeJobsRepo{jobs: []domain.Job{engineer}}
	uc := usecase.SearchJobs{Repo: repo}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:           "engineer",
		ExcludeTitleTerms: nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hidden != 0 {
		t.Errorf("Hidden = %d; want 0 (nil ExcludeTitleTerms must be a no-op)", result.Hidden)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d; want 1", result.Count)
	}
}

// --- Sweep title exclusion tests ---

func TestSweepJobs_ExcludeTitleTerms_ReflectedInExcludedCount(t *testing.T) {
	recruiter := makeJob(t, "urn:li:job:9001", "Technical Recruiter", "NYC", "StaffCo")
	engineer := makeJob(t, "urn:li:job:9002", "Platform Engineer", "Remote", "Acme")

	repo := &fakeJobsRepo{jobs: []domain.Job{recruiter, engineer}}
	fc := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: fc}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:           "engineer",
		ExcludeTitleTerms: []string{"recruiter"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.ExcludedCount != 1 {
		t.Errorf("ExcludedCount = %d; want 1 (title-excluded job counted)", result.ExcludedCount)
	}
	if result.NewCount != 1 {
		t.Errorf("NewCount = %d; want 1 (only engineer is new)", result.NewCount)
	}
}
