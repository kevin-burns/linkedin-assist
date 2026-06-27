// Package enrich — Ollama auto-start helper.
//
// ensureOllamaRunning is called only when LI_ASSIST_ENRICH_PROVIDER=ollama
// (the explicit forced path). It is NOT called from autoDetect; auto mode must
// stay side-effect-free.
//
// Additional env vars:
//
//	LI_ASSIST_OLLAMA_AUTOSTART      — default "true"; set to "false" or "0"
//	                                  to disable automatic start of ollama.
//	LI_ASSIST_OLLAMA_START_TIMEOUT  — how long to wait for ollama to become
//	                                  ready after spawning it (default "15s").
//	                                  Accepts Go duration strings ("30s",
//	                                  "1m") or plain seconds ("20").
package enrich

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	envOllamaAutostart     = "LI_ASSIST_OLLAMA_AUTOSTART"
	envOllamaStartTimeout  = "LI_ASSIST_OLLAMA_START_TIMEOUT"
	defaultStartTimeout    = 15 * time.Second
	pollInterval           = 250 * time.Millisecond
	probeTimeout           = 2 * time.Second
)

// ensureOllamaRunning is the internal entry point used by NewFromEnv. It reads
// the LI_ASSIST_OLLAMA_AUTOSTART and LI_ASSIST_OLLAMA_START_TIMEOUT env vars
// and delegates to EnsureOllama with the real spawn function.
func ensureOllamaRunning(host string) error {
	autostart := ollamaAutostartEnabled()
	timeout := ollamaStartTimeout()
	return EnsureOllama(host, autostart, timeout, spawnOllama)
}

// EnsureOllama probes host/api/tags. If it is already up, returns nil
// immediately without calling start. If it is down and autostart is false,
// returns an error. If it is down and autostart is true, calls start() then
// polls until ready or timeout.
//
// start is an injectable function so unit tests never exec a real ollama.
// Exported for white-box testing from the enrich_test package.
func EnsureOllama(host string, autostart bool, timeout time.Duration, start func() error) error {
	if ollamaReachable(host, probeTimeout) {
		return nil
	}

	if !autostart {
		return fmt.Errorf("enrich: ollama is not running at %s; start it with 'ollama serve', or set LI_ASSIST_OLLAMA_AUTOSTART=true to let li-assist start it automatically", host)
	}

	fmt.Fprintln(os.Stderr, "enrich: ollama not running, starting it...")

	if err := start(); err != nil {
		return fmt.Errorf("enrich: could not start ollama: %w", err)
	}

	began := time.Now()
	deadline := began.Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if ollamaReachable(host, min(remaining, probeTimeout)) {
			fmt.Fprintf(os.Stderr, "enrich: ollama ready (%.1fs)\n", time.Since(began).Seconds())
			return nil
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("enrich: ollama did not become ready within %s; check 'ollama serve' output", timeout)
}

// ollamaReachable sends GET <host>/api/tags with the given timeout and returns
// true if the response status is 200 OK. It is the single probe helper used by
// both EnsureOllama (configurable timeout) and autoDetect (1s fast probe).
func ollamaReachable(host string, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(host + "/api/tags") //nolint:noctx,gosec // G704: host is operator-configured (LI_ASSIST_OLLAMA_HOST), not attacker input
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// spawnOllama is the real detached spawn. It:
//  1. Looks up the ollama binary on PATH.
//  2. Starts "ollama serve" in a new process group (Setpgid) so it outlives
//     the CLI. stdout/stderr are redirected to a temp-dir log file.
func spawnOllama() error {
	bin, err := exec.LookPath("ollama")
	if err != nil {
		return fmt.Errorf("ollama not on PATH (%w); start it manually or set LI_ASSIST_OLLAMA_AUTOSTART=false", err)
	}

	logPath := filepath.Join(os.TempDir(), "li-assist-ollama.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		// Non-fatal: fall back to discarding output.
		logFile = nil
	}

	cmd := exec.Command(bin, "serve") //nolint:gosec // G204: bin is resolved via exec.LookPath("ollama") with a fixed "serve" arg; not user input
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return fmt.Errorf("exec ollama serve: %w", err)
	}
	// Close the parent's copy of the log fd; the child process has its own dup.
	if logFile != nil {
		_ = logFile.Close()
	}
	// Do NOT cmd.Wait() — we want it to outlive this process.
	return nil
}

// ollamaAutostartEnabled reads LI_ASSIST_OLLAMA_AUTOSTART.
// Defaults to true; "false" or "0" disables.
func ollamaAutostartEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envOllamaAutostart)))
	if v == "false" || v == "0" {
		return false
	}
	return true
}

// ollamaStartTimeout reads LI_ASSIST_OLLAMA_START_TIMEOUT.
// Accepts Go duration strings ("30s", "1m") or bare seconds ("20").
// Defaults to defaultStartTimeout.
func ollamaStartTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv(envOllamaStartTimeout))
	if v == "" {
		return defaultStartTimeout
	}
	// Try Go duration first.
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	// Fall back to plain seconds.
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return defaultStartTimeout
}
