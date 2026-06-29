// Package auth manages the persistent Chromium session that li-assist uses for
// all voyager API calls. Auth = a real browser bound to a persistent
// user-data-dir (default ~/.config/li-assist/chrome/). The session persists
// across invocations via the profile dir, not a cookie-replay blob.
package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

const (
	// profileSubDir is appended to the user's home to get the Chrome profile dir.
	profileSubDir = ".config/li-assist/chrome"

	// gracefulCloseTimeout is the maximum time we wait for Chrome to flush its
	// cookie store and exit cleanly. 10 s is long enough for the OS to fsync
	// the SQLite Cookies DB without risking a hung process.
	gracefulCloseTimeout = 10 * time.Second
)

// DefaultProfileDir returns the canonical persistent Chrome profile directory.
func DefaultProfileDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, profileSubDir), nil
}

// Session wraps a chromedp browser context that is bound to a persistent
// Chrome user-data-dir. The browser instance lives for the lifetime of the
// Session; call Close when done.
type Session struct {
	cancelAlloc context.CancelFunc
	cancelCtx   context.CancelFunc
	ctx         context.Context
	profileDir  string
	originReady bool
	closeOnce   sync.Once
}

// linkedInOriginURL is a lightweight LinkedIn page used only to put the browser
// on a www.linkedin.com origin so in-page fetch() calls to /voyager/api are
// same-origin (and carry the session cookies). Without this, a fetch from the
// blank start page fails with a CORS "Failed to fetch".
const linkedInOriginURL = "https://www.linkedin.com/feed/"

// EnsureLinkedInOrigin navigates the browser to a www.linkedin.com page once,
// so subsequent in-page voyager fetch() calls execute same-origin. Idempotent
// for the session's lifetime. Not safe for concurrent use (the CLI is single
// threaded: one command, one fetch sequence).
func (s *Session) EnsureLinkedInOrigin() error {
	if s.originReady {
		return nil
	}
	if err := chromedp.Run(s.ctx, chromedp.Navigate(linkedInOriginURL)); err != nil {
		return fmt.Errorf("navigate to linkedin origin: %w", err)
	}
	s.originReady = true
	return nil
}

// Open launches Chrome against the persistent profile at profileDir
// (defaulting to DefaultProfileDir if empty) and returns a ready Session.
//
// headless=true is correct for normal API calls (the profile already holds
// the login cookies). headless=false is required for the interactive Login
// flow so the user can type credentials.
//
// Open returns ErrChromeNotFound (a typed sentinel) when no Chrome or Chromium
// binary can be located on the system, with an actionable install message.
func Open(parent context.Context, headless bool) (*Session, error) {
	if _, found := FindChrome(); !found {
		return nil, ErrChromeNotFound
	}

	profileDir, err := DefaultProfileDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("create chrome profile dir: %w", err)
	}

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)

	// Trigger actual browser launch with a no-op run so callers get a
	// launch error immediately rather than on the first real action.
	if err := chromedp.Run(ctx); err != nil {
		cancelCtx()
		cancelAlloc()
		return nil, fmt.Errorf("launch chrome (profile=%s): %w", profileDir, err)
	}

	return &Session{
		cancelAlloc: cancelAlloc,
		cancelCtx:   cancelCtx,
		ctx:         ctx,
		profileDir:  profileDir,
	}, nil
}

// Close gracefully shuts down the Chrome browser so the on-disk Cookies store
// is flushed before the process exits, then releases the allocator.
//
// chromedp.Cancel sends Browser.close via CDP and waits for the process to
// stop — that is what allows Chrome to fsync its SQLite Cookies DB. A plain
// context cancel (s.cancelCtx) does NOT flush the store.
//
// Close is idempotent: subsequent calls are no-ops.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		// Step 1: graceful close — sends Browser.close, waits for exit, flushes
		// the cookie store. We derive the timeout context from s.ctx so that a
		// parent cancellation (e.g. Ctrl-C) is still honoured, while giving
		// Chrome up to gracefulCloseTimeout to finish.
		tctx, tcancel := context.WithTimeout(s.ctx, gracefulCloseTimeout)
		defer tcancel()

		if err := chromedp.Cancel(tctx); err != nil {
			// Graceful close failed: either the browser is already gone, or s.ctx
			// was already cancelled (e.g. Ctrl-C) so Browser.close could not be
			// sent. In that case cookies will NOT flush in this path — expected on
			// a hard abort. Steps 2-3 below ensure the process is never leaked.
			_ = err
		}

		// Step 2: cancel the chromedp context (no-op when Cancel already closed
		// the browser, but necessary as a safety net in error paths).
		s.cancelCtx()

		// Step 3: release the allocator (cleans up the exec-allocator goroutine).
		s.cancelAlloc()
	})
}

// Context returns the chromedp browser context. Pass this to chromedp.Run or
// to Transport operations.
func (s *Session) Context() context.Context {
	return s.ctx
}

// ProfileDir returns the path to the persistent Chrome user-data-dir.
func (s *Session) ProfileDir() string {
	return s.profileDir
}

// Cookies returns all browser cookies as a name->value map. The operation runs
// on the session's own browser context; bound the lifetime via Close, not a ctx.
func (s *Session) Cookies() (map[string]string, error) {
	rawCookies, err := getAllCookies(s.ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rawCookies))
	for _, c := range rawCookies {
		m[c.Name] = c.Value
	}
	return m, nil
}

// CSRF derives the CSRF token from the JSESSIONID cookie: the cookie value
// with surrounding double-quotes stripped. Returns an error if JSESSIONID is
// absent (the session is not logged in, or the cookie has not been set yet).
func (s *Session) CSRF() (string, error) {
	cookies, err := getAllCookies(s.ctx)
	if err != nil {
		return "", err
	}
	for _, c := range cookies {
		if c.Name == "JSESSIONID" {
			return strings.Trim(c.Value, `"`), nil
		}
	}
	return "", fmt.Errorf("JSESSIONID cookie not found -- session may not be logged in")
}

// LoggedIn reports whether the li_at session cookie is present in the browser.
func (s *Session) LoggedIn() bool {
	cookies, err := getAllCookies(s.ctx)
	if err != nil {
		return false
	}
	for _, c := range cookies {
		if c.Name == "li_at" {
			return true
		}
	}
	return false
}

// LiAtExpiry reads the li_at cookie's nominal expiry from the live browser
// session. The bool is false when the cookie is absent or is a session cookie
// (Expires <= 0 in CDP, meaning the browser discards it on close rather than
// storing a real expiry). When true, the returned time is the cookie's
// stated expiry in UTC.
//
// NOTE: the staleness policy is age-based (14 days from CapturedAt), not
// expiry-based. This value is captured at login and stored for display only.
func (s *Session) LiAtExpiry() (time.Time, bool) {
	cookies, err := getAllCookies(s.ctx)
	if err != nil {
		return time.Time{}, false
	}
	for _, c := range cookies {
		if c.Name == "li_at" && c.Expires > 0 {
			return time.Unix(int64(c.Expires), 0).UTC(), true
		}
	}
	return time.Time{}, false
}

// getAllCookies retrieves all browser cookies via the Storage CDP domain.
func getAllCookies(ctx context.Context) ([]*network.Cookie, error) {
	var cookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = storage.GetCookies().Do(ctx)
		return err
	}))
	if err != nil {
		return nil, fmt.Errorf("get cookies: %w", err)
	}
	return cookies, nil
}
