package auth

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// StatusInput holds the data needed to render a human-readable auth status.
// All fields are plain values so the function is pure and fully testable
// without a browser.
type StatusInput struct {
	// CredsLoaded is true when a credentials.json was successfully read.
	CredsLoaded bool
	// Creds is the loaded credentials (only meaningful when CredsLoaded=true).
	Creds Credentials
	// ProfileDir is the path to the persistent Chrome user-data-dir.
	ProfileDir string
	// Now is the current time used for age calculations (injectable for tests).
	Now time.Time
	// ReauthDays is the staleness threshold (from ReauthDays() or a test value).
	ReauthDays int
}

// FormatStatus returns a human-readable auth status report string.
// It is a pure function: no I/O, no browser, suitable for unit tests.
func FormatStatus(in StatusInput) string {
	var b strings.Builder

	if !in.CredsLoaded {
		fmt.Fprintln(&b, "status: not logged in -- run li-assist auth login")
		fmt.Fprintf(&b, "profile dir: %s\n", in.ProfileDir)
		return b.String()
	}

	fmt.Fprintln(&b, "status: credentials present")

	// Captured-at + age.
	capturedAt := in.Creds.CapturedAt
	if capturedAt.IsZero() {
		fmt.Fprintln(&b, "captured at: unknown")
	} else {
		age, _ := SessionStaleness(capturedAt, in.Now, in.ReauthDays)
		ageDays := math.Round(age.Hours() / 24)
		fmt.Fprintf(&b, "captured at: %s (%.0f days ago)\n", capturedAt.Format(time.RFC3339), ageDays)
	}

	// Staleness verdict.
	_, stale := SessionStaleness(capturedAt, in.Now, in.ReauthDays)
	if stale {
		age, _ := SessionStaleness(capturedAt, in.Now, in.ReauthDays)
		ageDays := int(math.Round(age.Hours() / 24))
		fmt.Fprintf(&b, "staleness:   STALE -- re-run li-assist auth login (%d days old, policy %d)\n", ageDays, in.ReauthDays)
	} else {
		fmt.Fprintln(&b, "staleness:   OK")
	}

	// li_at nominal expiry.
	if in.Creds.LiAtExpiry.IsZero() {
		fmt.Fprintln(&b, "li_at expiry: unknown (session cookie or not captured)")
	} else {
		remainingDays := int(math.Round(in.Creds.LiAtExpiry.Sub(in.Now).Hours() / 24))
		if remainingDays < 0 {
			fmt.Fprintf(&b, "li_at expiry: %s (EXPIRED %d days ago)\n",
				in.Creds.LiAtExpiry.Format(time.RFC3339), -remainingDays)
		} else {
			fmt.Fprintf(&b, "li_at expiry: %s (%d days remaining)\n",
				in.Creds.LiAtExpiry.Format(time.RFC3339), remainingDays)
		}
	}

	fmt.Fprintf(&b, "profile dir: %s\n", in.ProfileDir)

	return b.String()
}
