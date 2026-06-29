package auth

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrChromeNotFound is returned by Open when no Chrome or Chromium binary can
// be located on the system. Install Google Chrome from
// https://www.google.com/chrome/ (or a Chromium package) and re-run.
var ErrChromeNotFound = errors.New(
	"Chrome or Chromium is required but was not found: " +
		"install Google Chrome from https://www.google.com/chrome/ " +
		"(or a Chromium package such as 'chromium' / 'chromium-browser'), " +
		"then re-run",
)

// chromeCandidates returns the ordered list of paths / names to probe for
// Chrome/Chromium on the current OS. It mirrors chromedp's own findExecPath
// probe list exactly, minus the "google-chrome" fallback (which is there only
// to surface a usable error message — we want to detect absence instead).
func chromeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
	case "windows":
		profile := os.Getenv("USERPROFILE")
		return []string{
			"chrome",
			"chrome.exe",
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			filepath.Join(profile, `AppData\Local\Google\Chrome\Application\chrome.exe`),
			filepath.Join(profile, `AppData\Local\Chromium\Application\chrome.exe`),
		}
	default: // Linux and other Unix-like systems
		return []string{
			"headless_shell",
			"headless-shell",
			"chromium",
			"chromium-browser",
			"google-chrome",
			"google-chrome-stable",
			"google-chrome-beta",
			"google-chrome-unstable",
			"/usr/bin/google-chrome",
			"/usr/local/bin/chrome",
			"/snap/bin/chromium",
			"chrome",
		}
	}
}

// FindChrome probes the OS-specific candidate list for a Chrome or Chromium
// binary using exec.LookPath. It returns the resolved path and found=true on
// the first hit; if nothing is found it returns ("", false).
//
// The probe list mirrors chromedp's own findExecPath so li-assist detects
// exactly the same binaries chromedp would use — without silently falling
// back to an absent "google-chrome".
func FindChrome() (path string, found bool) {
	for _, candidate := range chromeCandidates() {
		resolved, err := exec.LookPath(candidate)
		if err == nil {
			return resolved, true
		}
	}
	return "", false
}
