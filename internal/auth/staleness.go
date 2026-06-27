package auth

import (
	"os"
	"strconv"
	"time"
)

const (
	defaultReauthDays = 14
	reauthDaysEnvVar  = "LI_ASSIST_REAUTH_DAYS"
)

// ReauthDays returns the configured re-authentication cadence in days.
// It reads LI_ASSIST_REAUTH_DAYS from the environment, falling back to 14 for
// missing, invalid, or non-positive values.
func ReauthDays() int {
	v := os.Getenv(reauthDaysEnvVar)
	if v == "" {
		return defaultReauthDays
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultReauthDays
	}
	return n
}

// SessionStaleness reports how old a session is and whether it exceeds the
// reauthDays policy. capturedAt is the time the session was established.
// A zero capturedAt is always considered stale.
//
// stale is true when age >= reauthDays (i.e. the boundary itself is stale).
func SessionStaleness(capturedAt time.Time, now time.Time, reauthDays int) (age time.Duration, stale bool) {
	if capturedAt.IsZero() {
		return 0, true
	}
	age = now.Sub(capturedAt)
	if age < 0 {
		age = 0
	}
	threshold := time.Duration(reauthDays) * 24 * time.Hour
	return age, age >= threshold
}
