package enrich_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/enrich"
)

// TestEnsureOllama_AlreadyUp verifies that when the probe returns 200, start
// is never called and the function returns nil.
func TestEnsureOllama_AlreadyUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var startCalled atomic.Bool
	stub := func() error {
		startCalled.Store(true)
		return nil
	}

	err := enrich.EnsureOllama(srv.URL, true, 5*time.Second, stub)
	if err != nil {
		t.Fatalf("EnsureOllama: expected nil, got %v", err)
	}
	if startCalled.Load() {
		t.Error("start must NOT be called when ollama is already up")
	}
}

// TestEnsureOllama_DownAutostartFalse verifies that when the probe fails and
// autostart is false, start is never called and an error is returned.
func TestEnsureOllama_DownAutostartFalse(t *testing.T) {
	// Use a server that always 503s to simulate "down".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	var startCalled atomic.Bool
	stub := func() error {
		startCalled.Store(true)
		return nil
	}

	err := enrich.EnsureOllama(srv.URL, false, 5*time.Second, stub)
	if err == nil {
		t.Fatal("expected error when down and autostart=false, got nil")
	}
	if startCalled.Load() {
		t.Error("start must NOT be called when autostart=false")
	}
}

// TestEnsureOllama_DownAutostartTrue_StartsAndBecomesReady verifies the happy
// path: probe fails, start flips the fake server to 200, polling returns nil.
func TestEnsureOllama_DownAutostartTrue_StartsAndBecomesReady(t *testing.T) {
	var ready atomic.Bool // toggled by the stub start function

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" && ready.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	var startCalled atomic.Bool
	stub := func() error {
		startCalled.Store(true)
		ready.Store(true) // immediately ready after "start"
		return nil
	}

	err := enrich.EnsureOllama(srv.URL, true, 5*time.Second, stub)
	if err != nil {
		t.Fatalf("EnsureOllama: expected nil, got %v", err)
	}
	if !startCalled.Load() {
		t.Error("start must be called exactly once when ollama is down and autostart=true")
	}
}

// TestEnsureOllama_DownAutostartTrue_NeverReady verifies that when start is
// called but the server never becomes ready, a timeout error is returned.
func TestEnsureOllama_DownAutostartTrue_NeverReady(t *testing.T) {
	// Server always returns 503 regardless.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	stub := func() error { return nil } // start "succeeds" but server never comes up

	// Use a very short timeout so the test completes quickly.
	err := enrich.EnsureOllama(srv.URL, true, 300*time.Millisecond, stub)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("error should mention readiness timeout; got: %v", err)
	}
}

// TestEnsureOllama_DownAutostartTrue_StartFails verifies that when start()
// itself returns an error, EnsureOllama surfaces a non-nil error that mentions
// it could not start ollama.
func TestEnsureOllama_DownAutostartTrue_StartFails(t *testing.T) {
	// Server always returns non-200 (ollama is not running).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	startErr := fmt.Errorf("no ollama binary on PATH")
	stub := func() error { return startErr }

	err := enrich.EnsureOllama(srv.URL, true, 5*time.Second, stub)
	if err == nil {
		t.Fatal("expected error when start() fails, got nil")
	}
	if !strings.Contains(err.Error(), "could not start ollama") {
		t.Errorf("error should mention 'could not start ollama'; got: %v", err)
	}
}

// TestNewFromEnv_ForceOllama_AutostartFalse_OllamaDown verifies that when
// LI_ASSIST_ENRICH_PROVIDER=ollama, LI_ASSIST_OLLAMA_AUTOSTART=false, and
// the Ollama host is unreachable, NewFromEnv returns an error (not nil).
func TestNewFromEnv_ForceOllama_AutostartFalse_OllamaDown(t *testing.T) {
	// Use a server that always 503s to simulate "down".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "ollama")
	t.Setenv("LI_ASSIST_OLLAMA_HOST", srv.URL)
	t.Setenv("LI_ASSIST_OLLAMA_AUTOSTART", "false")

	_, err := enrich.NewFromEnv()
	if err == nil {
		t.Fatal("expected error when ollama is down and autostart disabled, got nil")
	}
}

// TestNewFromEnv_ForceOllama_AutostartTrue_OllamaAlreadyUp verifies that when
// LI_ASSIST_ENRICH_PROVIDER=ollama and Ollama is already running, NewFromEnv
// succeeds without trying to spawn anything.
func TestNewFromEnv_ForceOllama_AutostartTrue_OllamaAlreadyUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "ollama")
	t.Setenv("LI_ASSIST_OLLAMA_HOST", srv.URL)
	t.Setenv("LI_ASSIST_OLLAMA_AUTOSTART", "true")

	e, err := enrich.NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: expected nil error, got %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil Enricher")
	}
}
