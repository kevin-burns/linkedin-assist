package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	linkedInLoginURL  = "https://www.linkedin.com/login"
	loginPollInterval = 2 * time.Second
	loginTimeout      = 5 * time.Minute
)

// Login performs the interactive LinkedIn login flow:
//  1. Opens a non-headless Chrome on the persistent profile.
//  2. Navigates to the LinkedIn login page.
//  3. Polls for the li_at cookie for up to 5 minutes (the user authenticates
//     manually in the browser window).
//  4. Saves a Credentials record with CapturedAt=now so callers can see when
//     the session was last established.
//  5. Closes the browser and prints a confirmation message.
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

	// Capture li_at expiry best-effort (may be zero for session cookies).
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
