package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/usecase"
)

// fakeCache is a test double for usecase.JobCache.
type fakeCache struct {
	store map[string]domain.Job
	// upserted tracks URNs upserted during the test.
	upserted []string
	// insights stores enrichment results keyed by URN string.
	insights map[string]domain.Insights
}

func newFakeCache() *fakeCache {
	return &fakeCache{
		store:    make(map[string]domain.Job),
		insights: make(map[string]domain.Insights),
	}
}

func (f *fakeCache) Has(id string) bool {
	_, ok := f.store[id]
	return ok
}

func (f *fakeCache) Get(id string) (domain.Job, bool, bool) {
	j, ok := f.store[id]
	if !ok {
		return domain.Job{}, false, false
	}
	hasDesc := j.Posting().Description() != ""
	return j, hasDesc, true
}

func (f *fakeCache) Upsert(j domain.Job, _ string, _ time.Time) {
	f.store[string(j.URN())] = j
	f.upserted = append(f.upserted, string(j.URN()))
}

func (f *fakeCache) Len() int { return len(f.store) }

func (f *fakeCache) GetInsights(id string) (domain.Insights, bool) {
	ins, ok := f.insights[id]
	return ins, ok
}

func (f *fakeCache) PutInsights(id string, ins domain.Insights) {
	if f.insights == nil {
		f.insights = make(map[string]domain.Insights)
	}
	f.insights[id] = ins
}

// --- Sweep tests ---

func TestSweepJobs_FirstRun_AllNew(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:101", "Platform Engineer", "Berlin", "Corp A")
	job2 := makeJob(t, "urn:li:job:102", "SRE", "Remote", "Corp B")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1, job2}}
	cache := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: cache}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{Keyword: "engineer"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.NewCount != 2 {
		t.Errorf("NewCount = %d; want 2", result.NewCount)
	}
	if result.SeenCount != 0 {
		t.Errorf("SeenCount = %d; want 0", result.SeenCount)
	}
	if result.ExcludedCount != 0 {
		t.Errorf("ExcludedCount = %d; want 0", result.ExcludedCount)
	}
	if len(result.New) != 2 {
		t.Errorf("len(New) = %d; want 2", len(result.New))
	}
	if result.New == nil {
		t.Error("New must not be nil")
	}
	if result.Seen == nil {
		t.Error("Seen must not be nil")
	}
}

func TestSweepJobs_SecondRun_AllSeen(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:201", "Platform Engineer", "Berlin", "Corp A")
	job2 := makeJob(t, "urn:li:job:202", "SRE", "Remote", "Corp B")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1, job2}}
	cache := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: cache}

	req := usecase.JobSearchRequest{Keyword: "engineer"}

	// First run: populates cache.
	_, err := uc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	// Second run: same results, all cached now.
	result, err := uc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if result.NewCount != 0 {
		t.Errorf("NewCount = %d; want 0 on second run", result.NewCount)
	}
	if result.SeenCount != 2 {
		t.Errorf("SeenCount = %d; want 2 on second run", result.SeenCount)
	}
}

func TestSweepJobs_NewJobAppearsOnSecondRun(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:301", "Platform Engineer", "Berlin", "Corp A")
	job2 := makeJob(t, "urn:li:job:302", "SRE", "Remote", "Corp B")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1}}
	cache := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: cache}

	req := usecase.JobSearchRequest{Keyword: "engineer"}

	// First run: only job1.
	result1, err := uc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if result1.NewCount != 1 {
		t.Errorf("first run NewCount = %d; want 1", result1.NewCount)
	}

	// Second run: job1 + new job2 appears.
	repo.jobs = []domain.Job{job1, job2}
	result2, err := uc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if result2.NewCount != 1 {
		t.Errorf("second run NewCount = %d; want 1 (only job2 is new)", result2.NewCount)
	}
	if result2.SeenCount != 1 {
		t.Errorf("second run SeenCount = %d; want 1 (job1 is seen)", result2.SeenCount)
	}
	if result2.New[0].URN() != job2.URN() {
		t.Errorf("New[0] URN = %q; want %q", result2.New[0].URN(), job2.URN())
	}
}

func TestSweepJobs_ExcludedCountFlowsThrough(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:401", "Engineer", "Berlin", "Deloitte")
	job2 := makeJob(t, "urn:li:job:402", "SRE", "Remote", "Corp B")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1, job2}}
	cache := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: cache}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{
		Keyword:          "engineer",
		ExcludeCompanies: []string{"deloitte"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.ExcludedCount != 1 {
		t.Errorf("ExcludedCount = %d; want 1", result.ExcludedCount)
	}
	if result.NewCount != 1 {
		t.Errorf("NewCount = %d; want 1 (only Corp B)", result.NewCount)
	}
}

func TestSweepJobs_AllJobsUpsertedIntoCache(t *testing.T) {
	job1 := makeJob(t, "urn:li:job:501", "Engineer", "Berlin", "Corp A")
	job2 := makeJob(t, "urn:li:job:502", "SRE", "Remote", "Corp B")

	repo := &fakeJobsRepo{jobs: []domain.Job{job1, job2}}
	cache := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: cache}

	_, err := uc.Execute(context.Background(), usecase.JobSearchRequest{Keyword: "engineer"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if cache.Len() != 2 {
		t.Errorf("cache Len = %d; want 2 (all results upserted)", cache.Len())
	}
	if !cache.Has("urn:li:job:501") {
		t.Error("expected urn:li:job:501 in cache after sweep")
	}
	if !cache.Has("urn:li:job:502") {
		t.Error("expected urn:li:job:502 in cache after sweep")
	}
}

func TestSweepJobs_EmptyKeywordError(t *testing.T) {
	repo := &fakeJobsRepo{}
	cache := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: cache}

	_, err := uc.Execute(context.Background(), usecase.JobSearchRequest{Keyword: ""})
	if err == nil {
		t.Fatal("expected error for empty keyword, got nil")
	}
}

func TestSweepJobs_ResultSlicesNeverNil(t *testing.T) {
	repo := &fakeJobsRepo{jobs: []domain.Job{}}
	cache := newFakeCache()
	uc := usecase.SweepJobs{Repo: repo, Cache: cache}

	result, err := uc.Execute(context.Background(), usecase.JobSearchRequest{Keyword: "engineer"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.New == nil {
		t.Error("New must not be nil")
	}
	if result.Seen == nil {
		t.Error("Seen must not be nil")
	}
}
