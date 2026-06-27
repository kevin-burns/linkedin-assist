package auth

import (
	"os"
	"testing"
	"time"
)

// TestReauthDays_default checks that the default is 14 when the env var is unset.
func TestReauthDays_default(t *testing.T) {
	t.Setenv("LI_ASSIST_REAUTH_DAYS", "")
	if got := ReauthDays(); got != 14 {
		t.Errorf("ReauthDays() = %d, want 14", got)
	}
}

// TestReauthDays_valid checks that a valid env var is respected.
func TestReauthDays_valid(t *testing.T) {
	t.Setenv("LI_ASSIST_REAUTH_DAYS", "7")
	if got := ReauthDays(); got != 7 {
		t.Errorf("ReauthDays() = %d, want 7", got)
	}
}

// TestReauthDays_invalid checks that an invalid env var falls back to 14.
func TestReauthDays_invalid(t *testing.T) {
	t.Setenv("LI_ASSIST_REAUTH_DAYS", "not-a-number")
	if got := ReauthDays(); got != 14 {
		t.Errorf("ReauthDays() = %d, want 14 for invalid input", got)
	}
}

// TestReauthDays_zero checks that zero falls back to 14 (non-positive is invalid).
func TestReauthDays_zero(t *testing.T) {
	t.Setenv("LI_ASSIST_REAUTH_DAYS", "0")
	if got := ReauthDays(); got != 14 {
		t.Errorf("ReauthDays() = %d, want 14 for zero input", got)
	}
}

// TestReauthDays_negative checks that a negative value falls back to 14.
func TestReauthDays_negative(t *testing.T) {
	t.Setenv("LI_ASSIST_REAUTH_DAYS", "-5")
	if got := ReauthDays(); got != 14 {
		t.Errorf("ReauthDays() = %d, want 14 for negative input", got)
	}
}

// TestSessionStaleness_fresh verifies a fresh session is not stale.
func TestSessionStaleness_fresh(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-5 * 24 * time.Hour) // 5 days ago

	age, stale := SessionStaleness(capturedAt, now, 14)

	want := 5 * 24 * time.Hour
	if age != want {
		t.Errorf("age = %v, want %v", age, want)
	}
	if stale {
		t.Error("expected stale=false for 5-day-old session with 14-day policy")
	}
}

// TestSessionStaleness_exactly14days verifies that exactly 14 days is stale.
func TestSessionStaleness_exactly14days(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-14 * 24 * time.Hour) // exactly 14 days ago

	age, stale := SessionStaleness(capturedAt, now, 14)

	want := 14 * 24 * time.Hour
	if age != want {
		t.Errorf("age = %v, want %v", age, want)
	}
	if !stale {
		t.Error("expected stale=true for exactly-14-day-old session with 14-day policy")
	}
}

// TestSessionStaleness_over14days verifies that over 14 days is stale.
func TestSessionStaleness_over14days(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-20 * 24 * time.Hour) // 20 days ago

	age, stale := SessionStaleness(capturedAt, now, 14)

	if age < 14*24*time.Hour {
		t.Errorf("age = %v, expected >= 14 days", age)
	}
	if !stale {
		t.Error("expected stale=true for 20-day-old session with 14-day policy")
	}
}

// TestSessionStaleness_zeroCapturedAt verifies that a zero capturedAt is stale.
func TestSessionStaleness_zeroCapturedAt(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	var zeroCapturedAt time.Time

	_, stale := SessionStaleness(zeroCapturedAt, now, 14)

	if !stale {
		t.Error("expected stale=true for zero capturedAt")
	}
}

// TestSessionStaleness_customPolicy verifies a custom policy (7 days).
func TestSessionStaleness_customPolicy(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(-8 * 24 * time.Hour) // 8 days ago

	_, stale := SessionStaleness(capturedAt, now, 7)

	if !stale {
		t.Error("expected stale=true for 8-day-old session with 7-day policy")
	}
}

// TestLoad_backwardCompatible verifies that an old credentials JSON (without
// LiAtExpiry) still loads cleanly with a zero LiAtExpiry.
func TestLoad_backwardCompatible(t *testing.T) {
	dir := t.TempDir()
	oldJSON := `{"cookies":{"li_at":"abc"},"headers":{},"captured_at":"2026-06-01T00:00:00Z"}`
	path := dir + "/creds.json"
	if err := os.WriteFile(path, []byte(oldJSON), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	creds, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if creds.LiAtExpiry.IsZero() == false {
		t.Errorf("expected zero LiAtExpiry for old credentials without that field, got %v", creds.LiAtExpiry)
	}
	if creds.Cookies["li_at"] != "abc" {
		t.Errorf("cookies not loaded correctly")
	}
}

// TestSaveAndLoad_withLiAtExpiry verifies the new LiAtExpiry field round-trips.
func TestSaveAndLoad_withLiAtExpiry(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/creds.json"

	expiry := time.Date(2027, 6, 19, 0, 0, 0, 0, time.UTC)
	orig := Credentials{
		Cookies:    map[string]string{"li_at": "tok"},
		CapturedAt: time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC),
		LiAtExpiry: expiry,
	}

	if err := Save(orig, path); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !loaded.LiAtExpiry.Equal(expiry) {
		t.Errorf("LiAtExpiry roundtrip: got %v, want %v", loaded.LiAtExpiry, expiry)
	}
}
