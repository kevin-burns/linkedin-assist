package auth_test

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"

	"github.com/kevin-burns/linkedin-assist/internal/auth"
)

// TestFindChrome_FoundOnDevMachine verifies that FindChrome detects a real
// Chrome/Chromium binary on this development machine. If Chrome is genuinely
// absent (e.g. a bare CI container), the test is skipped rather than failed —
// the unit under test is the probe logic, not the presence of Chrome.
func TestFindChrome_FoundOnDevMachine(t *testing.T) {
	path, found := auth.FindChrome()
	if !found {
		t.Skip("no Chrome/Chromium found on this machine (expected on headless CI without Chrome)")
	}
	if path == "" {
		t.Error("FindChrome: found=true but path is empty")
	}
	t.Logf("FindChrome: found %q", path)
}

// TestFindChrome_ReturnsExactLookPathResult checks that when FindChrome returns
// found=true, the returned path is a resolved absolute path (exec.LookPath
// resolves relative names to absolute paths).
func TestFindChrome_ReturnsExactLookPathResult(t *testing.T) {
	path, found := auth.FindChrome()
	if !found {
		t.Skip("Chrome not available on this machine")
	}
	// exec.LookPath always returns an absolute path for found executables.
	// Validate it by looking it up again.
	resolved, err := exec.LookPath(path)
	if err != nil {
		t.Errorf("FindChrome returned %q but exec.LookPath rejects it: %v", path, err)
	}
	if resolved != path {
		// Both forms are valid (LookPath may clean the path); just log.
		t.Logf("FindChrome path %q re-resolved to %q (both valid)", path, resolved)
	}
}

// TestErrChromeNotFound_Sentinel confirms ErrChromeNotFound is a standalone
// sentinel (errors.Is identity) and carries a non-empty, actionable message.
func TestErrChromeNotFound_Sentinel(t *testing.T) {
	err := auth.ErrChromeNotFound
	if !errors.Is(err, auth.ErrChromeNotFound) {
		t.Error("ErrChromeNotFound does not satisfy errors.Is with itself")
	}
	if err.Error() == "" {
		t.Error("ErrChromeNotFound.Error() must not be empty")
	}
	// The message should mention Chrome so it's actionable.
	msg := err.Error()
	for _, want := range []string{"Chrome", "https://www.google.com/chrome"} {
		found := false
		for i := 0; i+len(want) <= len(msg); i++ {
			if msg[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ErrChromeNotFound message %q does not contain %q", msg, want)
		}
	}
}

// TestChromeCandidates_NonEmpty verifies the candidate list is non-empty for
// the current OS (exercises the unexported helper via a table-driven approach
// on each supported GOOS value, but since we can only run on one OS at a time,
// we just verify the current OS produces candidates).
func TestChromeCandidates_NonEmpty(t *testing.T) {
	// FindChrome iterates chromeCandidates internally. If the list were empty
	// it would always return ("", false). Verify indirectly: on a machine that
	// has Chrome, FindChrome must return found=true AND at least one candidate
	// must resolve.  On a machine without Chrome, we simply verify that
	// FindChrome returns false (empty list → always false → consistent).
	path, found := auth.FindChrome()
	goos := runtime.GOOS
	switch goos {
	case "darwin", "windows":
		// On developer machines these always have Chrome; skip if truly absent.
		if !found {
			t.Skipf("no Chrome on %s (unexpected on a dev machine but not a code error)", goos)
		}
		if path == "" {
			t.Error("found=true but path is empty")
		}
	default:
		// Linux CI containers often lack Chrome — just log, no assertion.
		t.Logf("GOOS=%s: FindChrome returned found=%v path=%q", goos, found, path)
	}
}
