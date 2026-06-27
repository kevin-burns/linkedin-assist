// Package ratelimit provides a configurable, jittered rate limiter for pacing
// LinkedIn voyager API calls. It enforces a per-call random sleep in
// [MinGap, MaxGap] and a per-day call cap tracked in a JSON counter file.
//
// # Environment variables
//
//   - LI_ASSIST_MIN_GAP_MS  -- minimum inter-call gap in milliseconds (default 3000)
//   - LI_ASSIST_MAX_GAP_MS  -- maximum inter-call gap in milliseconds (default 6000)
//   - LI_ASSIST_DAILY_CAP   -- maximum voyager calls per calendar day (default 100)
//
// Invalid or zero values fall back to the built-in defaults. If MaxGap parses
// to a value smaller than MinGap it is coerced up to MinGap (zero jitter).
//
// The counter file (default ~/.config/li-assist/usage.json) holds a small JSON
// object:
//
//	{ "date": "YYYY-MM-DD", "count": N }
//
// A missing file, an unreadable file, or a date that does not match today is
// treated as count=0. Writes are atomic (tmp + rename, mode 0600).
package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ErrDailyCapExceeded is returned by Wait when the daily call cap has been
// reached. Callers can test with errors.Is.
var ErrDailyCapExceeded = errors.New("daily cap exceeded")

// Options configures the Limiter.
type Options struct {
	// MinGap is the minimum sleep duration between calls.
	MinGap time.Duration
	// MaxGap is the maximum sleep duration between calls.
	// If MaxGap < MinGap it is coerced to MinGap.
	MaxGap time.Duration
	// DailyCap is the maximum number of successful Wait calls allowed per day.
	DailyCap int
	// CounterPath is the path to the JSON usage counter file.
	CounterPath string
}

const (
	defaultMinGapMS = 3000
	defaultMaxGapMS = 6000
	defaultDailyCap = 100
)

// defaultCounterPath returns ~/.config/li-assist/usage.json, falling back to
// the current directory on HOME resolution failure.
func defaultCounterPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "li-assist", "usage.json")
}

// OptionsFromEnv builds Options from environment variables, falling back to
// safe defaults for missing or invalid values.
func OptionsFromEnv() Options {
	minGap := parseMSDuration("LI_ASSIST_MIN_GAP_MS", defaultMinGapMS)
	maxGap := parseMSDuration("LI_ASSIST_MAX_GAP_MS", defaultMaxGapMS)
	dailyCap := parseInt("LI_ASSIST_DAILY_CAP", defaultDailyCap)

	// Coerce: MaxGap must be >= MinGap.
	if maxGap < minGap {
		maxGap = minGap
	}

	return Options{
		MinGap:      minGap,
		MaxGap:      maxGap,
		DailyCap:    dailyCap,
		CounterPath: defaultCounterPath(),
	}
}

// parseMSDuration reads an env var as milliseconds and returns a Duration.
// Returns the fallback if the var is unset, empty, or <= 0.
func parseMSDuration(env string, defaultMS int) time.Duration {
	v := os.Getenv(env)
	if v == "" {
		return time.Duration(defaultMS) * time.Millisecond
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return time.Duration(defaultMS) * time.Millisecond
	}
	return time.Duration(n) * time.Millisecond
}

// parseInt reads an env var as a positive integer, returning the fallback on
// failure or a non-positive value.
func parseInt(env string, fallback int) int {
	v := os.Getenv(env)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// usageRecord is the JSON shape of the counter file.
type usageRecord struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// Limiter paces calls according to Options. Create one with NewLimiter.
// The zero value is not valid; always use NewLimiter.
type Limiter struct {
	opts     Options
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	randIntn func(n int) int
}

// NewLimiter returns a Limiter wired to real time functions. All injectable
// seams default to stdlib implementations.
func NewLimiter(opts Options) *Limiter {
	// Coerce MaxGap here as well, in case the caller constructs Options directly.
	if opts.MaxGap < opts.MinGap {
		opts.MaxGap = opts.MinGap
	}
	if opts.CounterPath == "" {
		opts.CounterPath = defaultCounterPath()
	}
	return &Limiter{
		opts: opts,
		now:  time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
		randIntn: rand.Intn,
	}
}

// Wait enforces the rate limit. It:
//  1. Loads today's call count from CounterPath.
//  2. Returns ErrDailyCapExceeded (wrapped) if count >= DailyCap.
//  3. Sleeps a jittered duration in [MinGap, MaxGap], respecting ctx cancellation.
//  4. Increments and atomically persists the counter.
//  5. Returns nil on success, or ctx.Err() if cancelled during sleep.
func (l *Limiter) Wait(ctx context.Context) error {
	today := l.now().Format("2006-01-02")

	count, err := l.loadCount(today)
	if err != nil {
		// A corrupt/unreadable counter must not silently disable the cap: warn
		// so the operator notices, and treat as 0 (the next saveCount heals it).
		fmt.Fprintf(os.Stderr, "ratelimit: counter reset (%v)\n", err)
		count = 0
	}

	if count >= l.opts.DailyCap {
		return fmt.Errorf("%w: %d calls already made today (cap is %d)", ErrDailyCapExceeded, count, l.opts.DailyCap)
	}

	// Compute jittered sleep duration in [MinGap, MaxGap].
	gap := l.opts.MinGap
	window := int(l.opts.MaxGap - l.opts.MinGap)
	if window > 0 {
		gap += time.Duration(l.randIntn(window))
	}

	if err := l.sleep(ctx, gap); err != nil {
		return err
	}

	return l.saveCount(today, count+1)
}

// loadCount returns today's call count. A missing file or a stale date (daily
// rollover) yields 0 with no error -- both are normal. Only a genuinely
// unreadable or corrupt (unparseable) file returns an error, so the caller can
// surface it rather than silently disabling the cap.
func (l *Limiter) loadCount(today string) (int, error) {
	data, err := os.ReadFile(l.opts.CounterPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read usage file: %w", err)
	}
	var rec usageRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return 0, fmt.Errorf("parse usage file: %w", err)
	}
	if rec.Date != today {
		return 0, nil // daily rollover -- not an error
	}
	return rec.Count, nil
}

// saveCount atomically writes the counter file (tmp + rename, mode 0600).
func (l *Limiter) saveCount(today string, count int) error {
	rec := usageRecord{Date: today, Count: count}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}

	dir := filepath.Dir(l.opts.CounterPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir for usage file: %w", err)
	}

	tmp := l.opts.CounterPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write usage tmp: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the primary error is returned
		return fmt.Errorf("chmod usage tmp: %w", err)
	}
	if err := os.Rename(tmp, l.opts.CounterPath); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the primary error is returned
		return fmt.Errorf("rename usage tmp: %w", err)
	}
	return nil
}
