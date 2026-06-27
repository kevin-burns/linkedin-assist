package cache_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/cache"
	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// helpers

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "jobs.jsonl")
}

func makeJobURN(t *testing.T, urnStr, title, company string) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN(urnStr)
	if err != nil {
		t.Fatalf("ParseURN(%q): %v", urnStr, err)
	}
	return domain.NewJob(
		urn,
		title,
		"Berlin, Germany",
		domain.NewCompany("", company),
		time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		domain.NewPosting("desc for "+title, "https://example.com/apply", 0),
	)
}

var now = time.Date(2025, 6, 10, 12, 0, 0, 0, time.UTC)

// --- Has / Get / Upsert ---

func TestStore_HasReturnsFalseWhenEmpty(t *testing.T) {
	s, err := cache.Open(tmpPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Has("urn:li:fsd_jobPosting:9999") {
		t.Error("expected Has to return false for missing record")
	}
}

func TestStore_UpsertAndGet(t *testing.T) {
	path := tmpPath(t)
	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	job := makeJobURN(t, "urn:li:fsd_jobPosting:1001", "Platform Engineer", "Acme")
	s.Upsert(job, "platform engineer", now)

	if !s.Has("urn:li:fsd_jobPosting:1001") {
		t.Fatal("expected Has true after Upsert")
	}
	rec, ok := s.Get("urn:li:fsd_jobPosting:1001")
	if !ok {
		t.Fatal("expected Get to find record")
	}
	if rec.Title != "Platform Engineer" {
		t.Errorf("Title = %q; want %q", rec.Title, "Platform Engineer")
	}
	if rec.FirstSeen != now {
		t.Errorf("FirstSeen = %v; want %v", rec.FirstSeen, now)
	}
	if rec.LastSeen != now {
		t.Errorf("LastSeen = %v; want %v", rec.LastSeen, now)
	}
	if len(rec.SurfacedBy) != 1 || rec.SurfacedBy[0] != "platform engineer" {
		t.Errorf("SurfacedBy = %v; want [platform engineer]", rec.SurfacedBy)
	}
}

func TestStore_UpsertPreservesFirstSeen(t *testing.T) {
	s, err := cache.Open(tmpPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	job := makeJobURN(t, "urn:li:fsd_jobPosting:2001", "Backend Engineer", "Corp")
	first := now
	second := now.Add(24 * time.Hour)

	s.Upsert(job, "backend", first)
	s.Upsert(job, "engineer", second)

	rec, _ := s.Get("urn:li:fsd_jobPosting:2001")
	if rec.FirstSeen != first {
		t.Errorf("FirstSeen = %v; want %v (must be preserved)", rec.FirstSeen, first)
	}
	if rec.LastSeen != second {
		t.Errorf("LastSeen = %v; want %v", rec.LastSeen, second)
	}
}

func TestStore_UpsertDedupesSurfacedBy(t *testing.T) {
	s, err := cache.Open(tmpPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	job := makeJobURN(t, "urn:li:fsd_jobPosting:3001", "SRE", "Corp")
	s.Upsert(job, "sre", now)
	s.Upsert(job, "sre", now.Add(time.Hour)) // same keyword again
	s.Upsert(job, "devops", now.Add(2*time.Hour))

	rec, _ := s.Get("urn:li:fsd_jobPosting:3001")
	if len(rec.SurfacedBy) != 2 {
		t.Errorf("SurfacedBy len = %d; want 2 (deduped)", len(rec.SurfacedBy))
	}
}

func TestStore_UpsertPreservesNonEmptyDescription(t *testing.T) {
	s, err := cache.Open(tmpPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	richURN, _ := domain.ParseURN("urn:li:fsd_jobPosting:4001")
	richJob := domain.NewJob(richURN, "DevOps", "Berlin", domain.NewCompany("", "X"),
		time.Time{}, domain.NewPosting("Detailed description here", "https://apply.example.com", 0))
	s.Upsert(richJob, "devops", now)

	// Second upsert with empty description (e.g. from a search result that lacks detail)
	sparseURN, _ := domain.ParseURN("urn:li:fsd_jobPosting:4001")
	sparseJob := domain.NewJob(sparseURN, "DevOps", "Berlin", domain.NewCompany("", "X"),
		time.Time{}, domain.NewPosting("", "", 0))
	s.Upsert(sparseJob, "devops", now.Add(time.Hour))

	rec, _ := s.Get("urn:li:fsd_jobPosting:4001")
	if rec.Description != "Detailed description here" {
		t.Errorf("Description = %q; want preserved rich description", rec.Description)
	}
	if rec.ApplyURL != "https://apply.example.com" {
		t.Errorf("ApplyURL = %q; want preserved URL", rec.ApplyURL)
	}
}

func TestStore_UpsertPreservesInsights(t *testing.T) {
	// Seed a cache file where one record already carries insights (as a future
	// enrichment layer would have written), then upsert the SAME job from a
	// search (which has no insights) and confirm the insights survive the merge
	// and a flush/reopen round-trip.
	path := tmpPath(t)
	seed := `{"urn":"urn:li:fsd_jobPosting:5001","title":"ML Engineer",` +
		`"first_seen":"2026-06-01T00:00:00Z","last_seen":"2026-06-01T00:00:00Z",` +
		`"insights":{"top_skills":["MLOps","MLflow"]}}` + "\n"
	if err := os.WriteFile(path, []byte(seed), 0600); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	job := makeJobURN(t, "urn:li:fsd_jobPosting:5001", "ML Engineer", "AI Corp")
	s.Upsert(job, "ml", now) // search-sourced upsert: no insights on the incoming record
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	reopened, err := cache.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Get("urn:li:fsd_jobPosting:5001")
	if !ok {
		t.Fatal("record missing after round-trip")
	}
	if len(got.Insights) == 0 || !strings.Contains(string(got.Insights), "MLflow") {
		t.Errorf("insights not preserved across upsert+round-trip: got %q", string(got.Insights))
	}
}

// --- Flush + round-trip ---

func TestStore_FlushAndReopen(t *testing.T) {
	path := tmpPath(t)

	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	job1 := makeJobURN(t, "urn:li:fsd_jobPosting:6001", "Job A", "Corp A")
	job2 := makeJobURN(t, "urn:li:fsd_jobPosting:6002", "Job B", "Corp B")
	s.Upsert(job1, "keyword", now)
	s.Upsert(job2, "keyword", now)

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	s2, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open after flush: %v", err)
	}
	if s2.Len() != 2 {
		t.Errorf("Len = %d after reopen; want 2", s2.Len())
	}
	if !s2.Has("urn:li:fsd_jobPosting:6001") {
		t.Error("record 6001 not found after reopen")
	}
	if !s2.Has("urn:li:fsd_jobPosting:6002") {
		t.Error("record 6002 not found after reopen")
	}
}

// --- Corrupt file resilience ---

func TestStore_CorruptFile_TreatedAsEmpty(t *testing.T) {
	path := tmpPath(t)
	// Write a totally corrupt file.
	if err := os.WriteFile(path, []byte("not valid json at all\n{also bad"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Open must not crash; it should warn and return an empty store.
	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open on corrupt file returned error: %v", err)
	}
	if s.Len() != 0 {
		t.Errorf("Len = %d; want 0 (corrupt file treated as empty)", s.Len())
	}
}

func TestStore_CorruptLine_SkippedGoodLinesKept(t *testing.T) {
	path := tmpPath(t)

	// Write one valid JSONL line and one corrupt line.
	goodLine, _ := json.Marshal(cache.CachedJob{
		URN:       "urn:li:fsd_jobPosting:7001",
		Title:     "Good Job",
		FirstSeen: now,
		LastSeen:  now,
	})
	content := append(goodLine, '\n')
	content = append(content, []byte("THIS IS CORRUPT JSON\n")...)

	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Len() != 1 {
		t.Errorf("Len = %d; want 1 (only valid line kept)", s.Len())
	}
	if !s.Has("urn:li:fsd_jobPosting:7001") {
		t.Error("expected good record to survive corrupt neighbour")
	}
}

func TestStore_FlushHealsCorruptFile(t *testing.T) {
	path := tmpPath(t)
	if err := os.WriteFile(path, []byte("garbage\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	job := makeJobURN(t, "urn:li:fsd_jobPosting:8001", "Healed Job", "Corp")
	s.Upsert(job, "heal", now)

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	s2, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open after heal: %v", err)
	}
	if s2.Len() != 1 {
		t.Errorf("Len = %d after heal; want 1", s2.Len())
	}
}

// --- Size cap / LRU eviction ---

func TestStore_EvictionUnderSizeCap(t *testing.T) {
	path := tmpPath(t)

	// Override the env var to a tiny cap so we can trigger eviction easily.
	t.Setenv("LI_ASSIST_CACHE_MAX_BYTES", "500")

	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Insert enough records that they clearly exceed 500 bytes when serialised.
	base := now
	urns := []string{
		"urn:li:fsd_jobPosting:9001",
		"urn:li:fsd_jobPosting:9002",
		"urn:li:fsd_jobPosting:9003",
		"urn:li:fsd_jobPosting:9004",
		"urn:li:fsd_jobPosting:9005",
		"urn:li:fsd_jobPosting:9006",
		"urn:li:fsd_jobPosting:9007",
		"urn:li:fsd_jobPosting:9008",
		"urn:li:fsd_jobPosting:9009",
		"urn:li:fsd_jobPosting:9010",
	}
	for i, u := range urns {
		job := makeJobURN(t, u, "Job "+u, "Corp")
		// Stagger LastSeen so we have a deterministic eviction order.
		s.Upsert(job, "keyword", base.Add(time.Duration(i)*time.Hour))
	}

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// The file must exist and be <= 500 bytes.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() > 500 {
		t.Errorf("file size = %d; want <= 500 (cap enforced)", info.Size())
	}

	// After eviction, the newest records should survive.
	s2, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open after eviction: %v", err)
	}
	// The newest record (9010) must be present.
	if !s2.Has("urn:li:fsd_jobPosting:9010") {
		t.Error("newest record 9010 must survive LRU eviction")
	}
	// The oldest record (9001) should have been evicted.
	if s2.Has("urn:li:fsd_jobPosting:9001") {
		t.Error("oldest record 9001 should have been evicted")
	}
}

// --- Insights round-trip ---

func TestStore_InsightsRoundTrip(t *testing.T) {
	// Build a fully-populated Insights with ALL fields (including omitempty ones).
	want := domain.Insights{
		RealSummary:          "Honest backend role summary",
		TopSkills:            []string{"Go", "Kubernetes", "Postgres"},
		SalaryRange:          "$130k-$160k",
		Seniority:            "Senior",
		CondensedDescription: "Platform engineering focused on reliability and scale.",
		Notes:                "JD appears AI-generated; unusually heavy on buzzwords.",
	}

	path := tmpPath(t)
	s, err := cache.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Upsert a job so the URN exists in the store.
	job := makeJobURN(t, "urn:li:fsd_jobPosting:10001", "Platform Engineer", "ReliCo")
	s.Upsert(job, "platform", now)

	// Write insights.
	s.PutInsights("urn:li:fsd_jobPosting:10001", want)

	// Flush to disk, then reopen to simulate a separate CLI invocation.
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	s2, err := cache.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	got, ok := s2.GetInsights("urn:li:fsd_jobPosting:10001")
	if !ok {
		t.Fatal("GetInsights returned false after round-trip; insights not persisted")
	}

	if got.RealSummary != want.RealSummary {
		t.Errorf("RealSummary = %q; want %q", got.RealSummary, want.RealSummary)
	}
	if got.SalaryRange != want.SalaryRange {
		t.Errorf("SalaryRange = %q; want %q (omitempty field must survive round-trip)", got.SalaryRange, want.SalaryRange)
	}
	if got.Seniority != want.Seniority {
		t.Errorf("Seniority = %q; want %q (omitempty field must survive round-trip)", got.Seniority, want.Seniority)
	}
	if got.Notes != want.Notes {
		t.Errorf("Notes = %q; want %q (omitempty field must survive round-trip)", got.Notes, want.Notes)
	}
	if got.CondensedDescription != want.CondensedDescription {
		t.Errorf("CondensedDescription = %q; want %q", got.CondensedDescription, want.CondensedDescription)
	}
	if len(got.TopSkills) != len(want.TopSkills) {
		t.Fatalf("TopSkills len = %d; want %d", len(got.TopSkills), len(want.TopSkills))
	}
	for i, skill := range want.TopSkills {
		if got.TopSkills[i] != skill {
			t.Errorf("TopSkills[%d] = %q; want %q", i, got.TopSkills[i], skill)
		}
	}
}

func TestStore_DefaultPath_ContainsOmLi(t *testing.T) {
	p := cache.DefaultPath()
	if p == "" {
		t.Error("DefaultPath must not be empty")
	}
	// Basic sanity: must include the config subdir.
	if filepath.Base(filepath.Dir(p)) != "cache" {
		t.Errorf("DefaultPath = %q; expected .../cache/jobs.jsonl", p)
	}
}
