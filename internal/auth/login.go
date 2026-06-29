package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	linkedInLoginURL  = "https://www.linkedin.com/login"
	loginPollInterval = 2 * time.Second
	loginTimeout      = 5 * time.Minute

	// persistExpiry is the fallback cookie lifetime we assign when LinkedIn
	// issued a session cookie (CDP Expires <= 0). This is generous: LinkedIn's
	// server remains the real authority on validity; our staleness policy is
	// age-based at 14 days anyway.
	persistExpiry = 365 * 24 * time.Hour

	// authCookieNames lists the auth cookies we re-persist as persistent after
	// a successful login so they survive Chrome's close-and-reopen boundary.
	// JSESSIONID is the CSRF token source; li_at is the primary auth credential.
)

var authCookieNames = map[string]bool{
	"li_at":      true,
	"JSESSIONID": true,
}

// Login performs the interactive LinkedIn login flow:
//  1. Opens a non-headless Chrome on the persistent profile.
//  2. Navigates to the LinkedIn login page.
//  3. Polls for the li_at cookie for up to 5 minutes (the user authenticates
//     manually in the browser window).
//  4. Re-sets li_at and JSESSIONID as PERSISTENT cookies (if LinkedIn issued
//     them as session cookies) so they flush to Chrome's on-disk Cookies store.
//  5. Saves a Credentials record with CapturedAt=now so callers can see when
//     the session was last established.
//  6. Closes the browser gracefully (flushing cookies) and prints a confirmation.
//
// After Login returns nil, subsequent Open(parent, true) calls will find the
// cookies in the persistent profile and operate headlessly.
func Login(parent context.Context) error {
	sess, err := Open(parent, false) // visible window so the user can log in
	if err != nil {
		return fmt.Errorf("open browser for login: %w", err)
	}
	defer sess.Close()

	fmt.Printf("Chrome profile dir: %s\n", sess.ProfileDir())
	fmt.Println("Navigating to LinkedIn login page...")

	if err := chromedp.Run(sess.ctx, chromedp.Navigate(linkedInLoginURL)); err != nil {
		return fmt.Errorf("navigate to login page: %w", err)
	}

	fmt.Println("Waiting for login (up to 5 min) -- please sign in to LinkedIn in the browser window...")
	if err := waitForLogin(sess.ctx); err != nil {
		return fmt.Errorf("login wait: %w", err)
	}
	fmt.Println("Login detected (li_at cookie present).")

	// Re-set auth cookies as PERSISTENT so they survive the headful→headless
	// process boundary. This must happen BEFORE the deferred Close() flushes
	// Chrome, while the browser is still alive.
	if persistErr := persistAuthCookies(sess.ctx); persistErr != nil {
		// Non-fatal: log a warning so the user knows; the login is still good
		// (the cookies are present in-process) and the metadata save still runs.
		fmt.Printf("Warning: could not persist session cookies to disk: %v\n", persistErr)
		fmt.Println("  Tip: if the next command reports 'not logged in', re-run 'li-assist auth login'")
		fmt.Println("  and make sure to tick 'Keep me logged in' in the LinkedIn login form.")
	}

	// Capture li_at expiry best-effort (may be zero for session cookies that
	// were not yet re-persisted, or now set to our generous fallback).
	liAtExpiry, _ := sess.LiAtExpiry()

	// Persist a small Credentials record so callers can inspect the session age.
	creds := Credentials{
		CapturedAt: time.Now().UTC(),
		LiAtExpiry: liAtExpiry,
	}
	credPath := DefaultPath()
	if saveErr := Save(creds, credPath); saveErr != nil {
		// Non-fatal: the real session state is in the Chrome profile dir.
		fmt.Printf("Warning: could not save login metadata to %s: %v\n", credPath, saveErr)
	}

	fmt.Printf("Logged in; session saved to %s\n", sess.ProfileDir())
	return nil
}

// persistAuthCookies re-sets li_at and JSESSIONID as PERSISTENT cookies
// (non-zero Expires) so Chrome writes them to its on-disk Cookies store when
// the browser closes. LinkedIn sometimes issues these as session cookies
// (CDP Expires <= 0), particularly after 2FA or when "Keep me logged in" is
// unchecked. A session cookie is discarded on browser exit and never reaches
// disk, which is the root cause of the "not logged in" failure on the next
// headless invocation.
//
// When a cookie already has a real server-set expiry (Expires > 0), that value
// is preserved. When it is a session cookie (Expires <= 0), a generous one-year
// expiry is assigned as a local safeguard; LinkedIn's server validity remains
// authoritative (the 14-day staleness policy also applies independently).
func persistAuthCookies(ctx context.Context) error {
	cookies, err := getAllCookies(ctx)
	if err != nil {
		return fmt.Errorf("read cookies: %w", err)
	}

	var lastErr error
	for _, c := range cookies {
		if !authCookieNames[c.Name] {
			continue
		}
		params := BuildPersistParams(c)
		if doErr := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return params.Do(ctx)
		})); doErr != nil {
			// Accumulate but continue — failing to persist JSESSIONID should
			// not prevent li_at from being persisted. Join so that if BOTH fail
			// the warning surfaces both, not just the last.
			lastErr = errors.Join(lastErr, fmt.Errorf("set persistent %s: %w", c.Name, doErr))
		}
	}
	return lastErr
}

// BuildPersistParams converts a CDP cookie into a SetCookieParams that will
// re-write the cookie as a persistent (non-session) cookie. It preserves
// Domain, Path, Secure, HTTPOnly, and SameSite. If the existing cookie already
// has a real server-set expiry (Expires > 0, seconds since epoch), that expiry
// is reused; otherwise a future fallback expiry is used.
//
// This function is pure (no I/O) so it can be directly unit-tested.
func BuildPersistParams(c *network.Cookie) *network.SetCookieParams {
	var expiryTime time.Time
	if c.Expires > 0 {
		expiryTime = time.Unix(int64(c.Expires), 0).UTC()
	} else {
		expiryTime = time.Now().UTC().Add(persistExpiry)
	}
	ts := cdp.TimeSinceEpoch(expiryTime)

	params := network.SetCookie(c.Name, c.Value).
		WithDomain(c.Domain).
		WithPath(c.Path).
		WithSecure(c.Secure).
		WithHTTPOnly(c.HTTPOnly).
		WithExpires(&ts).
		WithPriority(c.Priority)

	if c.SameSite != "" {
		params = params.WithSameSite(c.SameSite)
	}
	// Forward the source scheme/port so the re-set cookie keeps the same
	// schemeful-SameSite identity as the original. Without these, a future
	// Chrome that enforces Schemeful SameSite on CDP-injected cookies could
	// treat the re-set cookie as a different one and drop it on close.
	if c.SourceScheme != "" {
		params = params.WithSourceScheme(c.SourceScheme)
	}
	if c.SourcePort != 0 {
		params = params.WithSourcePort(c.SourcePort)
	}
	return params
}

// waitForLogin polls for the li_at cookie every loginPollInterval until it
// appears or loginTimeout elapses. It returns promptly if ctx is cancelled
// (e.g. the user hits Ctrl-C) rather than sleeping out the interval.
func waitForLogin(ctx context.Context) error {
	deadline := time.Now().Add(loginTimeout)
	for time.Now().Before(deadline) {
		cookies, err := getAllCookies(ctx)
		if err == nil {
			for _, c := range cookies {
				if c.Name == "li_at" {
					return nil
				}
			}
		}
		fmt.Println("  still waiting for login...")
		timer := time.NewTimer(loginPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("timed out after %v waiting for li_at cookie", loginTimeout)
}
