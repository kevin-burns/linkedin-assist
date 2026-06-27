// Package cache provides a local JSONL-backed store for job data. It is
// the only package allowed to own the on-disk representation. usecase/ talks
// to it only through the port defined in usecase/ports.go.
//
// File layout: ~/.config/li-assist/cache/jobs.jsonl
// One JSON object per line, keyed by job URN id. Lines are never deleted
// in-place; the file is rewritten atomically on Flush.
//
// Size cap: default 10 MB (env LI_ASSIST_CACHE_MAX_BYTES). When over cap,
// least-recently-seen records are evicted until the file would fit.
//
// Resilience: a corrupt line or completely corrupt file is logged to stderr
// and treated as empty. The next Flush heals it.
package cache

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

const (
	defaultMaxBytes = 10 * 1024 * 1024 // 10 MB
	envMaxBytes     = "LI_ASSIST_CACHE_MAX_BYTES"
)

// CachedJob is the persisted representation of a job in the cache.
// JSON tags are stable wire format -- do not rename without migration.
type CachedJob struct {
	URN      string     `json:"urn"`
	Title    string     `json:"title"`
	Company  companyRec `json:"company"`
	Location string     `json:"location"`
	PostedAt time.Time  `json:"posted_at"`
	// SalaryText is reserved for future enrichment; populated by caller if known.
	SalaryText  string    `json:"salary_text,omitempty"`
	ApplyURL    string    `json:"apply_url,omitempty"`
	Description string    `json:"description,omitempty"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	SurfacedBy  []string  `json:"surfaced_by,omitempty"`
	// Insights is a nullable opaque JSON blob reserved for a future enrichment
	// layer. Never populated by this package; preserved across merges.
	Insights json.RawMessage `json:"insights,omitempty"`
}

// companyRec is the JSON representation of a company inside a CachedJob.
type companyRec struct {
	URN  string `json:"urn"`
	Name string `json:"name"`
}

// jobToDomain converts a CachedJob back to a domain.Job.
func (c CachedJob) ToDomain() (domain.Job, error) {
	urn, err := domain.ParseURN(c.URN)
	if err != nil {
		return domain.Job{}, fmt.Errorf("cache: bad urn %q: %w", c.URN, err)
	}
	var coURN domain.URN
	if c.Company.URN != "" {
		coURN, err = domain.ParseURN(c.Company.URN)
		if err != nil {
			// Non-fatal: company URN may be absent in older records.
			coURN = ""
		}
	}
	return domain.NewJob(
		urn,
		c.Title,
		c.Location,
		domain.NewCompany(coURN, c.Company.Name),
		c.PostedAt,
		domain.NewPosting(c.Description, c.ApplyURL, 0),
	), nil
}

// fromDomain converts a domain.Job to a CachedJob. Caller supplies FirstSeen
// and LastSeen; SurfacedBy and Insights are set separately.
func fromDomain(j domain.Job) CachedJob {
	return CachedJob{
		URN:   string(j.URN()),
		Title: j.Title(),
		Company: companyRec{
			URN:  string(j.Company().URN()),
			Name: j.Company().Name(),
		},
		Location:    j.Location(),
		PostedAt:    j.PostedAt(),
		ApplyURL:    j.Posting().ApplyURL(),
		Description: j.Posting().Description(),
	}
}

// Store is a JSONL-backed job cache. Load once with Open (or New for testing),
// operate in memory, Flush to disk. Not safe for concurrent use from multiple
// goroutines without external synchronisation.
type Store struct {
	path     string
	maxBytes int64
	records  map[string]CachedJob // keyed by URN string
}

// DefaultPath returns the canonical JSONL cache path.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "li-assist", "cache", "jobs.jsonl")
}

// maxBytesFromEnv reads LI_ASSIST_CACHE_MAX_BYTES, falling back to defaultMaxBytes.
func maxBytesFromEnv() int64 {
	v := os.Getenv(envMaxBytes)
	if v == "" {
		return defaultMaxBytes
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return defaultMaxBytes
	}
	return n
}

// Open loads the cache from path (creating parent directories and an empty
// file if absent). A corrupt line is logged and skipped; a corrupt file is
// logged and treated as empty. The caller owns the returned *Store and must
// call Flush when done.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("cache: mkdir: %w", err)
	}

	s := &Store{
		path:     path,
		maxBytes: maxBytesFromEnv(),
		records:  make(map[string]CachedJob),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil // empty store; file created on first Flush
		}
		fmt.Fprintf(os.Stderr, "WARNING: cache: cannot read %s: %v -- treating as empty\n", path, err)
		return s, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Raise the per-line limit well above the default 64KB: an enriched record
	// (long description + insights) can exceed it, and the default would
	// silently drop the whole line. 1MB comfortably covers any single record
	// under the 10MB file cap.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec CachedJob
		if err := json.Unmarshal(line, &rec); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: cache: skipping corrupt line %d in %s: %v\n", lineNo, path, err)
			continue
		}
		if rec.URN == "" {
			fmt.Fprintf(os.Stderr, "WARNING: cache: skipping line %d with empty URN\n", lineNo)
			continue
		}
		s.records[rec.URN] = rec
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: cache: error reading %s: %v -- partial data loaded\n", path, err)
	}

	return s, nil
}

// Has reports whether a job with the given URN id is in the cache.
func (s *Store) Has(id string) bool {
	_, ok := s.records[id]
	return ok
}

// Get returns the cached record for id. Returns the zero CachedJob and false
// if not found.
func (s *Store) Get(id string) (CachedJob, bool) {
	rec, ok := s.records[id]
	return rec, ok
}

// Upsert inserts or updates a record derived from j. On insert, FirstSeen and
// LastSeen are both set to now. On update:
//   - FirstSeen is preserved.
//   - LastSeen is updated to now.
//   - SurfacedBy gains keyword (deduped).
//   - Description and ApplyURL are updated only if the incoming value is non-empty.
//   - Insights is preserved (never overwritten by this method).
func (s *Store) Upsert(j domain.Job, keyword string, now time.Time) {
	key := string(j.URN())
	incoming := fromDomain(j)

	existing, exists := s.records[key]
	if !exists {
		incoming.FirstSeen = now
		incoming.LastSeen = now
		if keyword != "" {
			incoming.SurfacedBy = []string{keyword}
		}
		s.records[key] = incoming
		return
	}

	// Merge: preserve FirstSeen; update the rest selectively.
	incoming.FirstSeen = existing.FirstSeen
	incoming.LastSeen = now
	incoming.Insights = existing.Insights

	// Preserve existing non-empty description/applyURL/salary over empty incoming
	// (a search-sourced upsert has no description; enrichment may set salary).
	if incoming.Description == "" {
		incoming.Description = existing.Description
	}
	if incoming.ApplyURL == "" {
		incoming.ApplyURL = existing.ApplyURL
	}
	if incoming.SalaryText == "" {
		incoming.SalaryText = existing.SalaryText
	}

	// Merge SurfacedBy (deduplicate).
	seen := make(map[string]struct{}, len(existing.SurfacedBy)+1)
	for _, k := range existing.SurfacedBy {
		seen[k] = struct{}{}
	}
	merged := append([]string(nil), existing.SurfacedBy...)
	if keyword != "" {
		if _, dup := seen[keyword]; !dup {
			merged = append(merged, keyword)
		}
	}
	incoming.SurfacedBy = merged

	s.records[key] = incoming
}

// GetInsights returns the deserialized domain.Insights for the given URN,
// and whether the record exists and carries non-nil insights.
func (s *Store) GetInsights(id string) (domain.Insights, bool) {
	rec, ok := s.records[id]
	if !ok || len(rec.Insights) == 0 {
		return domain.Insights{}, false
	}
	var ins domain.Insights
	if err := json.Unmarshal(rec.Insights, &ins); err != nil {
		return domain.Insights{}, false
	}
	return ins, true
}

// PutInsights persists insights for the given URN. It marshals ins to JSON and
// stores it in the Insights field of the existing record. If the URN is not in
// the cache, the call is a no-op.
func (s *Store) PutInsights(id string, ins domain.Insights) {
	rec, ok := s.records[id]
	if !ok {
		return
	}
	b, err := json.Marshal(ins)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: cache: cannot marshal insights for %s: %v\n", id, err)
		return
	}
	rec.Insights = json.RawMessage(b)
	s.records[id] = rec
}

// All returns all cached records in an unspecified order.
func (s *Store) All() []CachedJob {
	out := make([]CachedJob, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out
}

// Len returns the number of records in the cache.
func (s *Store) Len() int {
	return len(s.records)
}

// Flush serialises all records to the JSONL file atomically (tmp + rename,
// 0600). If the serialised size would exceed maxBytes, least-recently-seen
// records are evicted (oldest LastSeen first) until the payload fits, and the
// eviction count is logged to stderr.
func (s *Store) Flush() error {
	payload, evicted := s.buildPayload()
	if evicted > 0 {
		fmt.Fprintf(os.Stderr, "cache: evicted %d record(s) to stay under size cap\n", evicted)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cache: mkdir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0600); err != nil {
		return fmt.Errorf("cache: write tmp: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cache: chmod tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cache: rename tmp: %w", err)
	}
	return nil
}

// buildPayload serialises records, applying LRU eviction if needed.
// Returns the final payload and the number of evicted records.
func (s *Store) buildPayload() ([]byte, int) {
	// Sort records by LastSeen descending (newest first) for stable eviction.
	recs := s.All()
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].LastSeen.After(recs[j].LastSeen)
	})

	var buf bytes.Buffer
	evicted := 0

	// Try all records first; if over cap, drop from the tail (oldest).
	for {
		buf.Reset()
		for _, r := range recs {
			line, err := json.Marshal(r)
			if err != nil {
				// Should never happen with our clean structs.
				fmt.Fprintf(os.Stderr, "WARNING: cache: skipping unmarshalable record %q: %v\n", r.URN, err)
				continue
			}
			buf.Write(line)
			buf.WriteByte('\n')
		}
		if int64(buf.Len()) <= s.maxBytes || len(recs) == 0 {
			break
		}
		// Evict the last (oldest LastSeen) record.
		recs = recs[:len(recs)-1]
		evicted++
	}

	// Rebuild the in-memory map to match the survivors, so Len() reflects what
	// was actually persisted and a repeated Flush stays under the cap (idempotent).
	if evicted > 0 {
		survivors := make(map[string]CachedJob, len(recs))
		for _, r := range recs {
			survivors[r.URN] = r
		}
		s.records = survivors
	}

	return buf.Bytes(), evicted
}
