package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kevin-burns/linkedin-assist/internal/auth"
)

// newAuthCmd returns the "auth" parent command.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage LinkedIn authentication",
	}
	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthStatusCmd())
	return cmd
}

// newAuthLoginCmd returns the "auth login" subcommand.
func newAuthLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Open a browser window and log in to LinkedIn",
		Long: `Opens a visible Chrome window pointed at the LinkedIn login page.
Sign in manually; li-assist detects the session cookie and saves the profile.
Subsequent commands run headlessly against the same profile.`,
		// main() prints returned errors once; don't reprint or dump usage.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return auth.Login(cmd.Context())
		},
	}
}

// newAuthStatusCmd returns the "auth status" subcommand.
func newAuthStatusCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current LinkedIn authentication status",
		Long: `Prints the current authentication status without launching a browser.

Reports:
  - whether a credentials record exists,
  - session age and staleness vs the 14-day policy (LI_ASSIST_REAUTH_DAYS),
  - li_at nominal cookie expiry (informational),
  - Chrome profile directory path.

The staleness check is age-based (days since last li-assist auth login), not
based on the cookie's own expiry date.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profileDir, _ := auth.DefaultProfileDir()
			credPath := auth.DefaultPath()
			now := time.Now().UTC()
			reauthDays := auth.ReauthDays()

			creds, err := auth.Load(credPath)
			credsLoaded := err == nil

			if jsonOut {
				return printStatusJSON(creds, credsLoaded, profileDir, now, reauthDays)
			}

			in := auth.StatusInput{
				CredsLoaded: credsLoaded,
				Creds:       creds,
				ProfileDir:  profileDir,
				Now:         now,
				ReauthDays:  reauthDays,
			}
			fmt.Print(auth.FormatStatus(in))
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit status as JSON (for scripting)")
	return cmd
}

// statusJSON is the JSON shape for --json output.
type statusJSON struct {
	LoggedIn   bool       `json:"logged_in"`
	CapturedAt time.Time  `json:"captured_at,omitempty"`
	AgeDays    float64    `json:"age_days,omitempty"`
	Stale      bool       `json:"stale"`
	ReauthDays int        `json:"reauth_days"`
	LiAtExpiry *time.Time `json:"li_at_expiry,omitempty"` // nil when unknown
	ProfileDir string     `json:"profile_dir"`
	CredPath   string     `json:"cred_path"`
}

func printStatusJSON(creds auth.Credentials, credsLoaded bool, profileDir string, now time.Time, reauthDays int) error {
	out := statusJSON{
		LoggedIn:   credsLoaded,
		ReauthDays: reauthDays,
		ProfileDir: profileDir,
		CredPath:   auth.DefaultPath(),
	}
	if credsLoaded {
		out.CapturedAt = creds.CapturedAt
		if !creds.LiAtExpiry.IsZero() {
			out.LiAtExpiry = &creds.LiAtExpiry
		}
		age, stale := auth.SessionStaleness(creds.CapturedAt, now, reauthDays)
		out.AgeDays = math.Round(age.Hours()/24*10) / 10
		out.Stale = stale
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode status JSON: %w", err)
	}
	return nil
}
