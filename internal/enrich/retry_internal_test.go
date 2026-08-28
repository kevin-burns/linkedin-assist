package enrich

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// These tests live inside the package so they can shorten enrichBackoffBase.
// Driving the real loop at 1s/2s/4s would cost seven seconds per case.

func retryTestJob(t *testing.T) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN("urn:li:fsd_jobPosting:4242")
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}
	return domain.NewJob(urn, "Platform Engineer", "Remote",
		domain.NewCompany("", "SlowCo"), time.Time{},
		domain.NewPosting("Build and run the platform.", "https://example.com/apply", 0))
}

const retryTestInsights = `{"real_summary":"Platform role","top_skills":["Go"],"salary_range":"","seniority":"Senior","condensed_description":"Platform work","notes":""}`

func writeInsights(w http.ResponseWriter) {
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, retryTestInsights)
}

// shortenBackoff makes the retry loop cheap for tests.
func shortenBackoff(t *testing.T) {
	t.Helper()
	prev := enrichBackoffBase
	enrichBackoffBase = 5 * time.Millisecond
	t.Cleanup(func() { enrichBackoffBase = prev })
}

// The regression this whole change exists for: the client's own timeout is a
// transport error, so it carries no status code and the old loop returned
// before retryableStatus was ever consulted.
func TestEnrich_RetriesClientTimeout(t *testing.T) {
	shortenBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			time.Sleep(400 * time.Millisecond) // exceeds the client timeout below
			return
		}
		writeInsights(w)
	}))
	defer srv.Close()

	t.Setenv(envEnrichTimeout, "150ms")
	e := NewOpenAICompat(srv.URL, "k", "m")

	got, err := e.Enrich(context.Background(), retryTestJob(t))
	if err != nil {
		t.Fatalf("expected the retry to recover the timeout, got: %v", err)
	}
	if got.Seniority != "Senior" {
		t.Errorf("seniority = %q, want Senior", got.Seniority)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("server saw %d requests, want 2 (one timed out, one succeeded)", n)
	}
}

// The failure that was actually observed in the field, and the one the first
// version of this fix missed: the provider answers 200 promptly and then streams
// the completion slowly, so http.Client.Timeout fires while READING THE BODY.
// Do() has already returned success by then, and a body read outside the retry
// loop is unreachable from it.
func TestEnrich_RetriesSlowBody(t *testing.T) {
	shortenBackoff(t)
	var calls int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush() // headers land; the client's Do() succeeds
			<-release                // ... and then the body never arrives in time
			return
		}
		writeInsights(w)
	}))
	defer func() { close(release); srv.Close() }()

	t.Setenv(envEnrichTimeout, "150ms")
	e := NewOpenAICompat(srv.URL, "k", "m")

	got, err := e.Enrich(context.Background(), retryTestJob(t))
	if err != nil {
		t.Fatalf("expected the retry to recover a slow body, got: %v", err)
	}
	if got.Seniority != "Senior" {
		t.Errorf("seniority = %q, want Senior", got.Seniority)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("server saw %d requests, want 2", n)
	}
}

// A non-retryable status must still surface the provider's own message, which
// now comes from the body buffered inside the loop.
func TestEnrich_NonRetryableStatusKeepsProviderMessage(t *testing.T) {
	shortenBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"no credit remaining"}`)
	}))
	defer srv.Close()

	e := NewOpenAICompat(srv.URL, "k", "m")
	_, err := e.Enrich(context.Background(), retryTestJob(t))
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	if !strings.Contains(err.Error(), "no credit remaining") || !strings.Contains(err.Error(), "401") {
		t.Errorf("error lost the provider message: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("server saw %d requests, want 1 — 401 is not retryable", n)
	}
}

// The status path, which shipped in v0.3.0 with no test of its own.
func TestEnrich_RetriesRetryableStatus(t *testing.T) {
	shortenBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeInsights(w)
	}))
	defer srv.Close()

	e := NewOpenAICompat(srv.URL, "k", "m")
	if _, err := e.Enrich(context.Background(), retryTestJob(t)); err != nil {
		t.Fatalf("expected 429,429,200 to succeed: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("server saw %d requests, want 3", n)
	}
}

// The body is a consumed Reader after the first attempt; if it is not rebuilt
// the retry posts an empty request and the provider rejects it.
func TestEnrich_RetryResendsBody(t *testing.T) {
	shortenBackoff(t)
	var calls int32
	var lens []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lens = append(lens, len(b))
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeInsights(w)
	}))
	defer srv.Close()

	e := NewOpenAICompat(srv.URL, "k", "m")
	if _, err := e.Enrich(context.Background(), retryTestJob(t)); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if len(lens) != 2 || lens[0] == 0 || lens[0] != lens[1] {
		t.Errorf("request body lengths = %v, want two equal non-zero lengths", lens)
	}
}

// Bounded on purpose: a caller waiting on a dead provider is worse than a soft skip.
func TestEnrich_GivesUpAfterMaxRetries(t *testing.T) {
	shortenBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	e := NewOpenAICompat(srv.URL, "k", "m")
	if _, err := e.Enrich(context.Background(), retryTestJob(t)); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if want := int32(maxEnrichRetries + 1); atomic.LoadInt32(&calls) != want {
		t.Errorf("server saw %d requests, want %d", calls, want)
	}
}

// A cancelled context is the caller asking to stop. Retrying it burns the
// backoff and fails identically every time.
func TestEnrich_CancelledContextIsNotRetried(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// Hold the request open until the test releases it, so the cancellation
		// below is what ends the call. Released explicitly rather than via
		// r.Context(): a blocked handler would otherwise stall srv.Close().
		<-release
		writeInsights(w)
	}))
	defer func() { close(release); srv.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	e := NewOpenAICompat(srv.URL, "k", "m")
	start := time.Now()
	if _, err := e.Enrich(ctx, retryTestJob(t)); err == nil {
		t.Fatal("expected an error on a cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v — a cancelled context should not have entered the backoff", elapsed)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("server saw %d requests, want 1", n)
	}
}

func TestRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"client timeout", &net.OpError{Err: timeoutErr{}}, true},
		{"deadline exceeded", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), true},
		{"connection reset", fmt.Errorf("read: %w", syscall.ECONNRESET), true},
		{"connection refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"resolver timeout", &net.DNSError{IsTimeout: true}, true},

		{"cancelled", fmt.Errorf("wrapped: %w", context.Canceled), false},
		{"NXDOMAIN", &net.DNSError{IsNotFound: true}, false},
		{"bad certificate", &tls.CertificateVerificationError{}, false},
		{"tls record header", tls.RecordHeaderError{Msg: "first record does not look like TLS"}, false},
		{"plain error", errors.New("malformed request"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableError(context.Background(), tc.err); got != tc.want {
				t.Errorf("retryableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// Whatever the error, a context that is already done is never worth retrying.
func TestRetryableError_DoneContextOverridesEverything(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if retryableError(ctx, context.DeadlineExceeded) {
		t.Error("a done context must not be retried even on a normally-retryable error")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestEnrichHTTPTimeout(t *testing.T) {
	tests := []struct {
		env  string
		want time.Duration
	}{
		{"", defaultEnrichTimeout},
		{"3m", 3 * time.Minute},
		{"180s", 180 * time.Second},
		{"180", 180 * time.Second},
		{" 45 ", 45 * time.Second},
		{"nonsense", defaultEnrichTimeout},
		{"0", defaultEnrichTimeout},
		{"-30s", defaultEnrichTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv(envEnrichTimeout, tc.env)
			if got := enrichHTTPTimeout(); got != tc.want {
				t.Errorf("enrichHTTPTimeout() with %q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func TestBackoffWait_ReturnsOnContextCancel(t *testing.T) {
	prev := enrichBackoffBase
	enrichBackoffBase = 10 * time.Second
	t.Cleanup(func() { enrichBackoffBase = prev })

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	start := time.Now()
	err := backoffWait(ctx, 0)
	if err == nil {
		t.Fatal("expected backoffWait to return the context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want a wrapped context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("backoffWait took %v — it ignored the cancellation", elapsed)
	}
}
