package auth_test

import (
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"

	"github.com/kevin-burns/linkedin-assist/internal/auth"
)

// TestBuildPersistParams exercises the pure cookie→SetCookieParams mapping.
//
// Pairwise coverage across the key input dimensions:
//
//	Expires   : session cookie (≤0) | persistent cookie (>0)
//	Domain    : empty | populated
//	Secure    : true | false
//	HTTPOnly  : true | false
//	SameSite  : "" | "Strict" | "Lax" | "None"
//
// The test verifies:
//   - Session cookies get a future Expires (> now).
//   - Persistent cookies keep their original Expires.
//   - Domain, Path, Secure, HTTPOnly, SameSite are all forwarded correctly.
//   - Name and Value are preserved.
func TestBuildPersistParams(t *testing.T) {
	now := time.Now().UTC()
	futureEpoch := float64(now.Add(30 * 24 * time.Hour).Unix()) // 30 days from now

	type want struct {
		name         string
		value        string
		domain       string
		path         string
		secure       bool
		httpOnly     bool
		sameSite     network.CookieSameSite
		priority     network.CookiePriority
		sourceScheme network.CookieSourceScheme
		sourcePort   int64
		expiresGT    time.Time // Expires must be strictly after this
		expiresLE    time.Time // Expires must be on or before this (zero → unchecked)
	}

	tests := []struct {
		name  string
		input *network.Cookie
		want  want
	}{
		{
			name: "session_cookie_gets_future_expiry",
			input: &network.Cookie{
				Name:     "li_at",
				Value:    "AQEDABCD",
				Domain:   ".linkedin.com",
				Path:     "/",
				Secure:   true,
				HTTPOnly: true,
				SameSite: network.CookieSameSiteNone,
				Expires:  0, // session cookie
			},
			want: want{
				name:      "li_at",
				value:     "AQEDABCD",
				domain:    ".linkedin.com",
				path:      "/",
				secure:    true,
				httpOnly:  true,
				sameSite:  network.CookieSameSiteNone,
				expiresGT: now,
				expiresLE: now.Add(366 * 24 * time.Hour),
			},
		},
		{
			name: "negative_expires_treated_as_session_cookie",
			input: &network.Cookie{
				Name:    "JSESSIONID",
				Value:   `"abc123"`,
				Domain:  "www.linkedin.com",
				Path:    "/",
				Secure:  true,
				Expires: -1,
			},
			want: want{
				name:      "JSESSIONID",
				value:     `"abc123"`,
				domain:    "www.linkedin.com",
				path:      "/",
				secure:    true,
				httpOnly:  false,
				sameSite:  "",
				expiresGT: now,
				expiresLE: now.Add(366 * 24 * time.Hour),
			},
		},
		{
			name: "persistent_cookie_keeps_original_expiry",
			input: &network.Cookie{
				Name:     "li_at",
				Value:    "AQEDXYZ",
				Domain:   ".linkedin.com",
				Path:     "/",
				Secure:   true,
				HTTPOnly: true,
				SameSite: network.CookieSameSiteNone,
				Expires:  futureEpoch,
			},
			want: want{
				name:      "li_at",
				value:     "AQEDXYZ",
				domain:    ".linkedin.com",
				path:      "/",
				secure:    true,
				httpOnly:  true,
				sameSite:  network.CookieSameSiteNone,
				expiresGT: time.Unix(int64(futureEpoch)-1, 0).UTC(),
				expiresLE: time.Unix(int64(futureEpoch)+1, 0).UTC(),
			},
		},
		{
			name: "strict_samesite_forwarded",
			input: &network.Cookie{
				Name:     "li_at",
				Value:    "token",
				Domain:   ".linkedin.com",
				Path:     "/",
				Secure:   false,
				HTTPOnly: false,
				SameSite: network.CookieSameSiteStrict,
				Expires:  0,
			},
			want: want{
				name:      "li_at",
				value:     "token",
				domain:    ".linkedin.com",
				path:      "/",
				secure:    false,
				httpOnly:  false,
				sameSite:  network.CookieSameSiteStrict,
				expiresGT: now,
				expiresLE: now.Add(366 * 24 * time.Hour),
			},
		},
		{
			name: "lax_samesite_forwarded",
			input: &network.Cookie{
				Name:     "JSESSIONID",
				Value:    `"sess"`,
				Domain:   "www.linkedin.com",
				Path:     "/",
				Secure:   true,
				HTTPOnly: false,
				SameSite: network.CookieSameSiteLax,
				Expires:  futureEpoch,
			},
			want: want{
				name:      "JSESSIONID",
				value:     `"sess"`,
				domain:    "www.linkedin.com",
				path:      "/",
				secure:    true,
				httpOnly:  false,
				sameSite:  network.CookieSameSiteLax,
				expiresGT: time.Unix(int64(futureEpoch)-1, 0).UTC(),
				expiresLE: time.Unix(int64(futureEpoch)+1, 0).UTC(),
			},
		},
		{
			name: "empty_samesite_not_set",
			input: &network.Cookie{
				Name:     "li_at",
				Value:    "v",
				Domain:   ".linkedin.com",
				Path:     "/",
				Secure:   true,
				HTTPOnly: true,
				SameSite: "",
				Expires:  0,
			},
			want: want{
				name:      "li_at",
				value:     "v",
				domain:    ".linkedin.com",
				path:      "/",
				secure:    true,
				httpOnly:  true,
				sameSite:  "", // must not be set when input is empty
				expiresGT: now,
				expiresLE: now.Add(366 * 24 * time.Hour),
			},
		},
		{
			name: "source_scheme_port_and_priority_forwarded",
			input: &network.Cookie{
				Name:         "li_at",
				Value:        "tok",
				Domain:       ".linkedin.com",
				Path:         "/",
				Secure:       true,
				HTTPOnly:     true,
				SameSite:     network.CookieSameSiteNone,
				Priority:     network.CookiePriorityMedium,
				SourceScheme: network.CookieSourceSchemeSecure,
				SourcePort:   443,
				Expires:      0,
			},
			want: want{
				name:         "li_at",
				value:        "tok",
				domain:       ".linkedin.com",
				path:         "/",
				secure:       true,
				httpOnly:     true,
				sameSite:     network.CookieSameSiteNone,
				priority:     network.CookiePriorityMedium,
				sourceScheme: network.CookieSourceSchemeSecure,
				sourcePort:   443,
				expiresGT:    now,
				expiresLE:    now.Add(366 * 24 * time.Hour),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := auth.BuildPersistParams(tc.input)

			if params.Name != tc.want.name {
				t.Errorf("Name: got %q, want %q", params.Name, tc.want.name)
			}
			if params.Value != tc.want.value {
				t.Errorf("Value: got %q, want %q", params.Value, tc.want.value)
			}
			if params.Domain != tc.want.domain {
				t.Errorf("Domain: got %q, want %q", params.Domain, tc.want.domain)
			}
			if params.Path != tc.want.path {
				t.Errorf("Path: got %q, want %q", params.Path, tc.want.path)
			}
			if params.Secure != tc.want.secure {
				t.Errorf("Secure: got %v, want %v", params.Secure, tc.want.secure)
			}
			if params.HTTPOnly != tc.want.httpOnly {
				t.Errorf("HTTPOnly: got %v, want %v", params.HTTPOnly, tc.want.httpOnly)
			}
			if params.SameSite != tc.want.sameSite {
				t.Errorf("SameSite: got %q, want %q", params.SameSite, tc.want.sameSite)
			}
			if params.Priority != tc.want.priority {
				t.Errorf("Priority: got %q, want %q", params.Priority, tc.want.priority)
			}
			if params.SourceScheme != tc.want.sourceScheme {
				t.Errorf("SourceScheme: got %q, want %q", params.SourceScheme, tc.want.sourceScheme)
			}
			if params.SourcePort != tc.want.sourcePort {
				t.Errorf("SourcePort: got %d, want %d", params.SourcePort, tc.want.sourcePort)
			}

			if params.Expires == nil {
				t.Fatal("Expires must not be nil (cookie must become persistent)")
			}
			got := params.Expires.Time()
			if !got.After(tc.want.expiresGT) {
				t.Errorf("Expires %v is not after lower bound %v", got, tc.want.expiresGT)
			}
			if !tc.want.expiresLE.IsZero() && got.After(tc.want.expiresLE) {
				t.Errorf("Expires %v exceeds upper bound %v", got, tc.want.expiresLE)
			}
		})
	}
}
