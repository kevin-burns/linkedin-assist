package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/usecase"
)

// fakeJobGetter is a test double for the JobGetter port.
type fakeJobGetter struct {
	job domain.Job
	err error
	// lastURN captures the URN passed to Get for assertion.
	lastURN domain.URN
}

func (f *fakeJobGetter) Get(_ context.Context, urn domain.URN) (domain.Job, error) {
	f.lastURN = urn
	return f.job, f.err
}

func makeDetailJob(t *testing.T) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN("urn:li:fsd_jobPosting:4427011736")
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}
	co, err := domain.ParseURN("urn:li:fsd_company:8738045")
	if err != nil {
		t.Fatalf("ParseURN company: %v", err)
	}
	return domain.NewJob(
		urn,
		"Senior Cloud Platform Engineer (m/w/d) DevOps",
		"Berlin, Germany",
		domain.NewCompany(co, "MaibornWolff GmbH"),
		time.Time{}, // zero -- postedAt is not parsed from postedOnText
		domain.NewPosting("DevOps ist ...", "https://example.com/apply", 0),
	)
}

func TestGetJob_Execute_ReturnsJob(t *testing.T) {
	want := makeDetailJob(t)
	repo := &fakeJobGetter{job: want}
	uc := usecase.GetJob{Repo: repo}

	urn, err := domain.ParseURN("urn:li:fsd_jobPosting:4427011736")
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}

	got, err := uc.Execute(context.Background(), urn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got.URN()) != string(want.URN()) {
		t.Errorf("URN = %q; want %q", got.URN(), want.URN())
	}
	if got.Title() != want.Title() {
		t.Errorf("Title = %q; want %q", got.Title(), want.Title())
	}
	// URN forwarded to repo.
	if string(repo.lastURN) != string(urn) {
		t.Errorf("repo received URN %q; want %q", repo.lastURN, urn)
	}
}

func TestGetJob_Execute_EmptyURNError(t *testing.T) {
	repo := &fakeJobGetter{}
	uc := usecase.GetJob{Repo: repo}

	_, err := uc.Execute(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty URN, got nil")
	}
}

func TestGetJob_Execute_RepoError(t *testing.T) {
	repoErr := errors.New("network timeout")
	repo := &fakeJobGetter{err: repoErr}
	uc := usecase.GetJob{Repo: repo}

	urn, _ := domain.ParseURN("urn:li:fsd_jobPosting:123")
	_, err := uc.Execute(context.Background(), urn)
	if err == nil {
		t.Fatal("expected error from repo, got nil")
	}
	if !errors.Is(err, repoErr) {
		t.Errorf("error %v; want to wrap %v", err, repoErr)
	}
}

// --- Cache-first tests ---

func TestGetJob_CacheHit_RepoNotCalled(t *testing.T) {
	cached := makeDetailJob(t) // has non-empty description
	cache := newFakeCache()
	urn := cached.URN()
	cache.Upsert(cached, "", time.Now())

	repo := &fakeJobGetter{} // job is zero value -- should NOT be called
	uc := usecase.GetJob{Repo: repo, Cache: cache}

	got, err := uc.Execute(context.Background(), urn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.lastURN != "" {
		t.Errorf("repo was called (lastURN=%q); want no repo call on cache hit", repo.lastURN)
	}
	if string(got.URN()) != string(urn) {
		t.Errorf("URN = %q; want %q", got.URN(), urn)
	}
}

func TestGetJob_CacheMiss_RepoCalledAndCached(t *testing.T) {
	want := makeDetailJob(t)
	cache := newFakeCache() // empty
	repo := &fakeJobGetter{job: want}
	uc := usecase.GetJob{Repo: repo, Cache: cache}

	urn, _ := domain.ParseURN("urn:li:fsd_jobPosting:4427011736")
	got, err := uc.Execute(context.Background(), urn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.lastURN != urn {
		t.Errorf("repo.lastURN = %q; want %q", repo.lastURN, urn)
	}
	if string(got.URN()) != string(want.URN()) {
		t.Errorf("URN = %q; want %q", got.URN(), want.URN())
	}
	// Result should now be in cache.
	if !cache.Has(string(urn)) {
		t.Error("expected job to be upserted into cache after miss")
	}
}

func TestGetJob_Refresh_RepoCalledEvenOnCacheHit(t *testing.T) {
	cached := makeDetailJob(t)
	cache := newFakeCache()
	urn := cached.URN()
	cache.Upsert(cached, "", time.Now())

	// repo returns a "refreshed" version (same URN, different title)
	refreshedURN, _ := domain.ParseURN(string(urn))
	refreshed := domain.NewJob(refreshedURN, "Refreshed Title", "Berlin", domain.NewCompany("", "Corp"),
		time.Time{}, domain.NewPosting("Fresh desc", "https://new.example.com", 0))

	repo := &fakeJobGetter{job: refreshed}
	uc := usecase.GetJob{Repo: repo, Cache: cache, Refresh: true}

	got, err := uc.Execute(context.Background(), urn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.lastURN != urn {
		t.Errorf("repo not called on --refresh; lastURN=%q", repo.lastURN)
	}
	if got.Title() != "Refreshed Title" {
		t.Errorf("Title = %q; want %q", got.Title(), "Refreshed Title")
	}
}
