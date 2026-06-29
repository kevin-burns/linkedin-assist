package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kevin-burns/linkedin-assist/internal/auth"
	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/ratelimit"
	"github.com/kevin-burns/linkedin-assist/internal/voyager"
)

// checkResult holds the outcome of a single doctor check.
type checkResult struct {
	name    string
	verdict string // "PASS", "WARN", "FAIL", "SKIP"
	detail  string
}

func (r checkResult) String() string {
	s := fmt.Sprintf("[%s] %s", r.verdict, r.name)
	if r.detail != "" {
		s += ": " + r.detail
	}
	return s
}

// newDoctorCmd returns the "doctor" command.
func newDoctorCmd() *cobra.Command {
	var checkLoginFlow bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks on the li-assist configuration and LinkedIn session",
		Long: `Runs a sequence of checks and prints a PASS/WARN/FAIL report.

Checks performed:
  1. Chrome/Chromium binary present on the system.
  2. Credentials present + staleness (STALE is WARN, missing is FAIL).
  3. Login health: opens headless Chrome and verifies li_at cookie is present.
     If credentials exist but li_at is absent, a specific diagnosis is printed
     (session cookie was not persisted -- see 'li-assist auth login' tip below).
  4. Voyager probe: performs a single jobs search to confirm API reachability.

Exits non-zero if any check FAILs.

Use --check-login-flow to also verify the LinkedIn login page renders
(catches Chrome or network breakage before a real re-auth is needed).`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			var results []checkResult
			anyFail := false

			// ------------------------------------------------------------------
			// Check 1: Chrome/Chromium binary present
			// ------------------------------------------------------------------
			chromePath, chromeFound := auth.FindChrome()
			if !chromeFound {
				results = append(results, checkResult{
					name:    "chrome binary",
					verdict: "FAIL",
					detail:  "Chrome or Chromium not found -- install Google Chrome from https://www.google.com/chrome/ (or a Chromium package) and re-run",
				})
				anyFail = true
			} else {
				results = append(results, checkResult{
					name:    "chrome binary",
					verdict: "PASS",
					detail:  chromePath,
				})
			}

			// ------------------------------------------------------------------
			// Check 2: credentials present + staleness
			// ------------------------------------------------------------------
			credPath := auth.DefaultPath()
			reauthDays := auth.ReauthDays()
			now := time.Now().UTC()

			var creds auth.Credentials
			var credsErr error
			creds, credsErr = auth.Load(credPath)
			if credsErr != nil {
				results = append(results, checkResult{
					name:    "credentials",
					verdict: "FAIL",
					detail:  fmt.Sprintf("no credentials.json found at %s -- run li-assist auth login", credPath),
				})
				anyFail = true
			} else {
				_, stale := auth.SessionStaleness(creds.CapturedAt, now, reauthDays)
				age, _ := auth.SessionStaleness(creds.CapturedAt, now, reauthDays)
				ageDays := int(age.Hours() / 24)
				if stale {
					results = append(results, checkResult{
						name:    "credentials",
						verdict: "WARN",
						detail:  fmt.Sprintf("session is %d days old (policy: %d) -- consider running li-assist auth login", ageDays, reauthDays),
					})
				} else {
					results = append(results, checkResult{
						name:    "credentials",
						verdict: "PASS",
						detail:  fmt.Sprintf("session is %d days old (policy: %d)", ageDays, reauthDays),
					})
				}
			}

			// ------------------------------------------------------------------
			// Check 3: login health (headless Chrome, li_at present)
			// ------------------------------------------------------------------
			var sess *auth.Session
			var openErr error
			if !chromeFound {
				// Chrome is absent — skip the session checks so we don't emit a
				// confusing secondary error for the same root cause.
				results = append(results, checkResult{
					name:    "login health",
					verdict: "SKIP",
					detail:  "skipped because Chrome binary was not found (see check 1)",
				})
			} else {
				sess, openErr = auth.Open(ctx, true)
				if openErr != nil {
					results = append(results, checkResult{
						name:    "login health",
						verdict: "FAIL",
						detail:  fmt.Sprintf("could not open headless Chrome: %v", openErr),
					})
					anyFail = true
				} else {
					defer sess.Close()
				}
			}

			loginHealthPassed := false
			if sess != nil {
				if !sess.LoggedIn() {
					// Distinguish between "never logged in" and "cookie not persisted".
					// If a fresh credentials.json exists (within the staleness window)
					// but li_at is absent in the headless session, the login succeeded
					// but the session cookie was not written to disk.
					detail := "li_at cookie absent -- run 'li-assist auth login' first"
					if credsErr == nil {
						age, stale := auth.SessionStaleness(creds.CapturedAt, now, reauthDays)
						ageDays := int(age.Hours() / 24)
						if !stale {
							detail = fmt.Sprintf(
								"login metadata is recent (%d day(s) old) but li_at cookie was not found "+
									"in the Chrome profile -- the session cookie did not persist to disk; "+
									"re-run 'li-assist auth login', complete any 2FA prompt, and tick "+
									"\"Keep me logged in\" in the LinkedIn login form",
								ageDays,
							)
						}
					}
					results = append(results, checkResult{
						name:    "login health",
						verdict: "FAIL",
						detail:  detail,
					})
					anyFail = true
				} else {
					loginHealthPassed = true
					results = append(results, checkResult{
						name:    "login health",
						verdict: "PASS",
						detail:  "li_at present in browser session",
					})
				}
			}

			// ------------------------------------------------------------------
			// Check 4: voyager probe (only if login health passed)
			// ------------------------------------------------------------------
			if loginHealthPassed {
				limiter := ratelimit.NewLimiter(ratelimit.OptionsFromEnv())
				transport := voyager.NewTransport(sess, limiter)
				jobsClient := voyager.NewJobsClient(transport)

				jobs, probeErr := jobsClient.Search(ctx, voyager.JobSearchParams{
					Keywords: "engineer",
					Count:    1,
				})
				if probeErr != nil {
					verdict := "FAIL"
					detail := fmt.Sprintf("probe error: %v", probeErr)
					if errors.Is(probeErr, domain.ErrAuth) {
						detail = "401 authentication failure -- run li-assist auth login"
					} else if errors.Is(probeErr, domain.ErrSchema) {
						detail = "response schema mismatch -- LinkedIn API may have changed (schema drift)"
					}
					results = append(results, checkResult{
						name:    "voyager probe",
						verdict: verdict,
						detail:  detail,
					})
					anyFail = true
				} else {
					results = append(results, checkResult{
						name:    "voyager probe",
						verdict: "PASS",
						detail:  fmt.Sprintf("search returned %d result(s)", len(jobs)),
					})
				}
			} else {
				skipDetail := "skipped because login health check failed"
				if !chromeFound {
					skipDetail = "skipped because the Chrome binary was not found (see check 1)"
				}
				results = append(results, checkResult{
					name:    "voyager probe",
					verdict: "SKIP",
					detail:  skipDetail,
				})
			}

			// ------------------------------------------------------------------
			// Optional: --check-login-flow
			// ------------------------------------------------------------------
			if checkLoginFlow {
				// Verify Chrome/chromedp can still render a LinkedIn page (catches
				// Chrome auto-update breakage before a re-auth is needed). Reuse the
				// session already opened for check 3 -- opening a second Chrome on the
				// same persistent profile dir would deadlock on Chromium's singleton
				// profile lock.
				if !chromeFound || openErr != nil {
					results = append(results, checkResult{
						name:    "chrome render",
						verdict: "FAIL",
						detail:  "Chrome could not be opened (see checks above)",
					})
					anyFail = true
				} else if navErr := sess.EnsureLinkedInOrigin(); navErr != nil {
					results = append(results, checkResult{
						name:    "chrome render",
						verdict: "FAIL",
						detail:  fmt.Sprintf("chromedp could not render a LinkedIn page: %v", navErr),
					})
					anyFail = true
				} else {
					results = append(results, checkResult{
						name:    "chrome render",
						verdict: "PASS",
						detail:  "chromedp rendered a LinkedIn page successfully",
					})
				}
			}

			// ------------------------------------------------------------------
			// Print report
			// ------------------------------------------------------------------
			fmt.Fprintln(os.Stdout, "li-assist doctor report:")
			fmt.Fprintln(os.Stdout, "--------------------")
			for _, r := range results {
				fmt.Fprintln(os.Stdout, r.String())
			}
			fmt.Fprintln(os.Stdout, "--------------------")
			if anyFail {
				fmt.Fprintln(os.Stdout, "overall: FAIL")
				return fmt.Errorf("one or more checks failed")
			}
			fmt.Fprintln(os.Stdout, "overall: PASS")
			return nil
		},
	}

	cmd.Flags().BoolVar(&checkLoginFlow, "check-login-flow", false,
		"Also open a visible Chrome window to verify the LinkedIn login page renders (integration-only)")

	return cmd
}
