package auth

import (
	"strings"
	"testing"
	"time"
)

// TestFormatStatus_notLoggedIn verifies the not-logged-in message.
func TestFormatStatus_notLoggedIn(t *testing.T) {
	result := FormatStatus(StatusInput{
		CredsLoaded: false,
		ProfileDir:  "/home/user/.config/li-assist/chrome",
		Now:         time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		ReauthDays:  14,
	})

	if !strings.Contains(result, "not logged in") {
		t.Errorf("expected 'not logged in' in output, got:\n%s", result)
	}
	if !strings.Contains(result, "li-assist auth login") {
		t.Errorf("expected 'li-assist auth login' hint in output, got:\n%s", result)
	}
}

// TestFormatStatus_fresh verifies output for a fresh (non-stale) session.
func TestFormatStatus_fresh(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-5 * 24 * time.Hour)

	result := FormatStatus(StatusInput{
		CredsLoaded: true,
		Creds: Credentials{
			CapturedAt: capturedAt,
		},
		ProfileDir: "/home/user/.config/li-assist/chrome",
		Now:        now,
		ReauthDays: 14,
	})

	if !strings.Contains(result, "OK") {
		t.Errorf("expected 'OK' staleness verdict for 5-day-old session, got:\n%s", result)
	}
	if strings.Contains(result, "STALE") {
		t.Errorf("did not expect 'STALE' for 5-day-old session, got:\n%s", result)
	}
	// auth status is metadata-only; it must point the user at doctor for the
	// real (browser-backed) session check.
	if !strings.Contains(result, "li-assist doctor") {
		t.Errorf("expected a 'li-assist doctor' hint in the status output, got:\n%s", result)
	}
}

// TestFormatStatus_stale verifies output for a stale session.
func TestFormatStatus_stale(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-20 * 24 * time.Hour)

	result := FormatStatus(StatusInput{
		CredsLoaded: true,
		Creds: Credentials{
			CapturedAt: capturedAt,
		},
		ProfileDir: "/home/user/.config/li-assist/chrome",
		Now:        now,
		ReauthDays: 14,
	})

	if !strings.Contains(result, "STALE") {
		t.Errorf("expected 'STALE' in output for 20-day-old session, got:\n%s", result)
	}
	if !strings.Contains(result, "li-assist auth login") {
		t.Errorf("expected re-login hint for stale session, got:\n%s", result)
	}
}

// TestFormatStatus_withLiAtExpiry verifies li_at expiry is shown when present.
func TestFormatStatus_withLiAtExpiry(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-5 * 24 * time.Hour)
	expiry := now.Add(360 * 24 * time.Hour) // ~1 year out

	result := FormatStatus(StatusInput{
		CredsLoaded: true,
		Creds: Credentials{
			CapturedAt: capturedAt,
			LiAtExpiry: expiry,
		},
		ProfileDir: "/home/user/.config/li-assist/chrome",
		Now:        now,
		ReauthDays: 14,
	})

	if !strings.Contains(result, "li_at expiry") {
		t.Errorf("expected li_at expiry info in output, got:\n%s", result)
	}
}

// TestFormatStatus_noLiAtExpiry verifies output when li_at expiry is unknown.
func TestFormatStatus_noLiAtExpiry(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-5 * 24 * time.Hour)

	result := FormatStatus(StatusInput{
		CredsLoaded: true,
		Creds: Credentials{
			CapturedAt: capturedAt,
			// LiAtExpiry is zero
		},
		ProfileDir: "/home/user/.config/li-assist/chrome",
		Now:        now,
		ReauthDays: 14,
	})

	if !strings.Contains(result, "unknown") {
		t.Errorf("expected 'unknown' for missing li_at expiry, got:\n%s", result)
	}
}
