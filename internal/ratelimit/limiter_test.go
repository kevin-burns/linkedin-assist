package ratelimit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestLimiter returns a Limiter with injectable seams for deterministic
// unit tests. The fake clock is fixed at the given time, sleep is a no-op
// that records the requested duration, and jitter always returns 0 (so sleep
// duration equals MinGap).
func newTestLimiter(opts Options, now time.Time) (*Limiter, *fakeSleep) {
	fs := &fakeSleep{}
	l := &Limiter{
		opts:     opts,
		now:      func() time.Time { return now },
		sleep:    fs.sleep,
		randIntn: func(n int) int { return 0 }, // no jitter
	}
	return l, fs
}

type fakeSleep struct {
	last  time.Duration
	calls int
}

func (f *fakeSleep) sleep(_ context.Context, d time.Duration) error {
	f.last = d
	f.calls++
	return nil
}

// baseOpts returns a minimal Options backed by a temp directory.
func baseOpts(t *testing.T) Options {
	t.Helper()
	return Options{
		MinGap:      100 * time.Millisecond,
		MaxGap:      500 * time.Millisecond,
		DailyCap:    3,
		CounterPath: filepath.Join(t.TempDir(), "usage.json"),
	}
}

// TestWait_failsSafeWhenCounterUnwritable verifies that if the counter cannot
// be persisted, Wait returns an error (blocking the call) rather than failing
// open and letting an unbounded number of calls through.
func TestWait_failsSafeWhenCounterUnwritable(t *testing.T) {
	dir := t.TempDir()
	// Put a regular file where a directory is needed, so saveCount's MkdirAll
	// of the parent fails on every platform (no chmod needed).
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := baseOpts(t)
	opts.CounterPath = filepath.Join(blocker, "usage.json")
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	l, _ := newTestLimiter(opts, now)

	err := l.Wait(context.Background())
	if err == nil {
		t.Fatal("expected Wait to error when the counter cannot be persisted, got nil")
	}
	if errors.Is(err, ErrDailyCapExceeded) {
		t.Errorf("write failure should not surface as ErrDailyCapExceeded: %v", err)
	}
}

// TestWait_sleepsWithinGap verifies that Wait sleeps a duration in [MinGap, MaxGap].
func TestWait_sleepsWithinGap(t *testing.T) {
	opts := baseOpts(t)
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	l, fs := newTestLimiter(opts, now)

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait returned unexpected error: %v", err)
	}

	if fs.last < opts.MinGap || fs.last > opts.MaxGap {
		t.Errorf("sleep duration %v not in [%v, %v]", fs.last, opts.MinGap, opts.MaxGap)
	}
}

// TestWait_jitterRange verifies that with random jitter in [0, window) the
// sleep stays within [MinGap, MaxGap].
func TestWait_jitterRange(t *testing.T) {
	opts := baseOpts(t)
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	window := int(opts.MaxGap - opts.MinGap) // 400ms in units of ns

	// Use maximum jitter (window-1 nanoseconds).
	fs := &fakeSleep{}
	l := &Limiter{
		opts:     opts,
		now:      func() time.Time { return now },
		sleep:    fs.sleep,
		randIntn: func(n int) int { return n - 1 },
	}

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait returned unexpected error: %v", err)
	}

	maxExpected := opts.MinGap + time.Duration(window-1)
	if fs.last < opts.MinGap || fs.last > maxExpected {
		t.Errorf("sleep duration %v not in [%v, %v]", fs.last, opts.MinGap, maxExpected)
	}
}

// TestWait_dailyCap verifies that the (DailyCap+1)th call returns ErrDailyCapExceeded.
func TestWait_dailyCap(t *testing.T) {
	opts := baseOpts(t) // DailyCap = 3
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	l, _ := newTestLimiter(opts, now)

	for i := 0; i < opts.DailyCap; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait %d returned unexpected error: %v", i+1, err)
		}
	}

	err := l.Wait(context.Background())
	if !errors.Is(err, ErrDailyCapExceeded) {
		t.Errorf("expected ErrDailyCapExceeded, got: %v", err)
	}
}

// TestWait_dailyCap_noSleepOnExceeded verifies that Wait does NOT sleep when
// the cap is exceeded (fail-fast).
func TestWait_dailyCap_noSleepOnExceeded(t *testing.T) {
	opts := Options{
		MinGap:      100 * time.Millisecond,
		MaxGap:      500 * time.Millisecond,
		DailyCap:    1,
		CounterPath: filepath.Join(t.TempDir(), "usage.json"),
	}
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	l, fs := newTestLimiter(opts, now)

	// First call should succeed and sleep.
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait returned unexpected error: %v", err)
	}
	callsAfterFirst := fs.calls

	// Second call should fail without sleeping.
	err := l.Wait(context.Background())
	if !errors.Is(err, ErrDailyCapExceeded) {
		t.Errorf("expected ErrDailyCapExceeded, got: %v", err)
	}
	if fs.calls != callsAfterFirst {
		t.Errorf("sleep was called after cap exceeded (calls went from %d to %d)", callsAfterFirst, fs.calls)
	}
}

// TestWait_counterResetsNewDay verifies that after the fake clock advances to
// a new day, Wait treats the count as 0 again.
func TestWait_counterResetsNewDay(t *testing.T) {
	opts := Options{
		MinGap:      10 * time.Millisecond,
		MaxGap:      10 * time.Millisecond,
		DailyCap:    2,
		CounterPath: filepath.Join(t.TempDir(), "usage.json"),
	}

	day1 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)

	currentTime := day1
	fs := &fakeSleep{}
	l := &Limiter{
		opts:     opts,
		now:      func() time.Time { return currentTime },
		sleep:    fs.sleep,
		randIntn: func(n int) int { return 0 },
	}

	// Exhaust the cap on day 1.
	for i := 0; i < opts.DailyCap; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("day1 Wait %d: %v", i+1, err)
		}
	}
	if err := l.Wait(context.Background()); !errors.Is(err, ErrDailyCapExceeded) {
		t.Fatalf("expected cap exceeded on day 1, got: %v", err)
	}

	// Advance clock to day 2 -- counter should reset.
	currentTime = day2
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("day2 Wait returned unexpected error: %v", err)
	}
}

// TestWait_counterPersistsAcrossInstances verifies that the counter file is
// shared: a second Limiter reading the same path sees the count from the first.
func TestWait_counterPersistsAcrossInstances(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "usage.json")
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	opts := Options{
		MinGap:      10 * time.Millisecond,
		MaxGap:      10 * time.Millisecond,
		DailyCap:    2,
		CounterPath: counterPath,
	}

	// First instance makes 1 call.
	l1, _ := newTestLimiter(opts, now)
	if err := l1.Wait(context.Background()); err != nil {
		t.Fatalf("l1 Wait: %v", err)
	}

	// Second instance (same path) should see count=1 and allow one more call.
	l2, _ := newTestLimiter(opts, now)
	if err := l2.Wait(context.Background()); err != nil {
		t.Fatalf("l2 Wait: %v", err)
	}

	// Third instance should see count=2 = cap => fail.
	l3, _ := newTestLimiter(opts, now)
	err := l3.Wait(context.Background())
	if !errors.Is(err, ErrDailyCapExceeded) {
		t.Errorf("expected ErrDailyCapExceeded from l3, got: %v", err)
	}
}

// TestWait_ctxCancelled verifies that Wait returns ctx.Err() when the context
// is cancelled during the sleep, and does NOT increment the counter.
func TestWait_ctxCancelled(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "usage.json")
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	opts := Options{
		MinGap:      10 * time.Second, // long enough that we cancel before it expires
		MaxGap:      10 * time.Second,
		DailyCap:    5,
		CounterPath: counterPath,
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Real context-aware sleep that respects cancellation.
	realSleep := func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}

	l := &Limiter{
		opts:     opts,
		now:      func() time.Time { return now },
		sleep:    realSleep,
		randIntn: func(n int) int { return 0 },
	}

	// Cancel immediately.
	cancel()

	err := l.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	// Counter must NOT have been incremented.
	today := now.Format("2006-01-02")
	count, loadErr := l.loadCount(today)
	if loadErr == nil && count != 0 {
		t.Errorf("counter was incremented after ctx cancel: count=%d", count)
	}
	// If loadErr != nil the file was never written -- also correct.
}

// TestOptionsFromEnv_defaults verifies defaults when env vars are unset.
func TestOptionsFromEnv_defaults(t *testing.T) {
	unset := func(key string) {
		t.Helper()
		old, ok := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if ok {
				os.Setenv(key, old)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	unset("LI_ASSIST_MIN_GAP_MS")
	unset("LI_ASSIST_MAX_GAP_MS")
	unset("LI_ASSIST_DAILY_CAP")

	opts := OptionsFromEnv()
	if opts.MinGap != 3000*time.Millisecond {
		t.Errorf("MinGap default: got %v, want 3000ms", opts.MinGap)
	}
	if opts.MaxGap != 6000*time.Millisecond {
		t.Errorf("MaxGap default: got %v, want 6000ms", opts.MaxGap)
	}
	if opts.DailyCap != 100 {
		t.Errorf("DailyCap default: got %d, want 100", opts.DailyCap)
	}
	if opts.CounterPath == "" {
		t.Error("CounterPath is empty")
	}
}

// TestOptionsFromEnv_parsesValues verifies that valid env values are parsed.
func TestOptionsFromEnv_parsesValues(t *testing.T) {
	setEnv := func(key, val string) {
		t.Helper()
		old, ok := os.LookupEnv(key)
		os.Setenv(key, val)
		t.Cleanup(func() {
			if ok {
				os.Setenv(key, old)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	setEnv("LI_ASSIST_MIN_GAP_MS", "1000")
	setEnv("LI_ASSIST_MAX_GAP_MS", "2000")
	setEnv("LI_ASSIST_DAILY_CAP", "50")

	opts := OptionsFromEnv()
	if opts.MinGap != 1000*time.Millisecond {
		t.Errorf("MinGap: got %v, want 1000ms", opts.MinGap)
	}
	if opts.MaxGap != 2000*time.Millisecond {
		t.Errorf("MaxGap: got %v, want 2000ms", opts.MaxGap)
	}
	if opts.DailyCap != 50 {
		t.Errorf("DailyCap: got %d, want 50", opts.DailyCap)
	}
}

// TestOptionsFromEnv_invalidFallsToDefault verifies that invalid env values
// produce defaults.
func TestOptionsFromEnv_invalidFallsToDefault(t *testing.T) {
	setEnv := func(key, val string) {
		t.Helper()
		old, ok := os.LookupEnv(key)
		os.Setenv(key, val)
		t.Cleanup(func() {
			if ok {
				os.Setenv(key, old)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	setEnv("LI_ASSIST_MIN_GAP_MS", "not-a-number")
	setEnv("LI_ASSIST_MAX_GAP_MS", "-1")
	setEnv("LI_ASSIST_DAILY_CAP", "abc")

	opts := OptionsFromEnv()
	if opts.MinGap != 3000*time.Millisecond {
		t.Errorf("MinGap should fall back to default, got %v", opts.MinGap)
	}
	if opts.MaxGap != 6000*time.Millisecond {
		t.Errorf("MaxGap should fall back to default, got %v", opts.MaxGap)
	}
	if opts.DailyCap != 100 {
		t.Errorf("DailyCap should fall back to default, got %d", opts.DailyCap)
	}
}

// TestOptionsFromEnv_coercesMaxGap verifies that MaxGap < MinGap is coerced to MinGap.
func TestOptionsFromEnv_coercesMaxGap(t *testing.T) {
	setEnv := func(key, val string) {
		t.Helper()
		old, ok := os.LookupEnv(key)
		os.Setenv(key, val)
		t.Cleanup(func() {
			if ok {
				os.Setenv(key, old)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	setEnv("LI_ASSIST_MIN_GAP_MS", "5000")
	setEnv("LI_ASSIST_MAX_GAP_MS", "1000") // less than MinGap

	opts := OptionsFromEnv()
	if opts.MaxGap != opts.MinGap {
		t.Errorf("MaxGap should be coerced to MinGap=%v, got MaxGap=%v", opts.MinGap, opts.MaxGap)
	}
}

// TestCounterFileMode verifies that the counter file is written with mode 0600.
func TestCounterFileMode(t *testing.T) {
	opts := baseOpts(t)
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	l, _ := newTestLimiter(opts, now)

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	info, err := os.Stat(opts.CounterPath)
	if err != nil {
		t.Fatalf("stat counter file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("counter file mode: got %04o, want 0600", mode)
	}
}
