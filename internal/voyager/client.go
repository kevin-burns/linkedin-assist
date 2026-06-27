package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/kevin-burns/linkedin-assist/internal/auth"
	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/ratelimit"
)

const linkedInBase = "https://www.linkedin.com"

// Transport implements rawGetter by issuing each voyager GET as a fetch()
// executed inside a live LinkedIn page context (the browser-resident transport
// from Design Revision 1). It satisfies the rawGetter interface defined in
// jobs.go, so JobsClient can drive it without a chromedp import.
type Transport struct {
	sess    *auth.Session
	limiter *ratelimit.Limiter
}

// NewTransport constructs a Transport backed by the given authenticated Session
// and an optional rate limiter. If limiter is nil, calls are not throttled.
func NewTransport(s *auth.Session, limiter *ratelimit.Limiter) *Transport {
	return &Transport{sess: s, limiter: limiter}
}

// Get performs a voyager GET request via fetch() executed inside the browser
// page and returns the response body on HTTP 2xx, or a domain error otherwise.
//
// URL construction:
//   - The `query` param is a LinkedIn restli tuple string whose structural
//     characters ( ) : , must appear LITERAL on the wire (not percent-encoded).
//     Spaces inside the value become %20. Other params use standard encoding.
//   - See encodeVoyagerURL for the full encoding contract.
func (t *Transport) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	// The in-page fetch must run from a www.linkedin.com origin or it is blocked
	// as cross-origin ("Failed to fetch"). Land on a LinkedIn page first.
	if err := t.sess.EnsureLinkedInOrigin(); err != nil {
		return nil, fmt.Errorf("prepare session origin: %w", err)
	}

	csrf, err := t.sess.CSRF()
	if err != nil {
		return nil, fmt.Errorf("%w: could not read CSRF token: %v", domain.ErrAuth, err)
	}

	fullURL := encodeVoyagerURL(path, query)

	// Enforce rate limit before issuing the actual network call.
	if t.limiter != nil {
		if err := t.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
	}

	// Run on the session's browser context (which carries the chromedp executor)
	// but cancel it if the caller's ctx is cancelled (e.g. Ctrl-C), so an
	// in-flight request does not hang past the user's interrupt.
	runCtx, cancel := context.WithCancel(t.sess.Context())
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	status, ok, body, err := fetchInPage(runCtx, fullURL, csrf)
	if err != nil {
		return nil, fmt.Errorf("fetch-in-page %s: %w", path, err)
	}

	switch {
	case status == 200 && ok:
		return []byte(body), nil
	case status == 0:
		// fetch() threw in-page (CORS, network, abort); body holds "ERROR: ...".
		return nil, fmt.Errorf("fetch-in-page %s failed: %s", path, snippet(body))
	case status == 401:
		return nil, fmt.Errorf("%w: re-run li-assist auth login", domain.ErrAuth)
	case status == 429:
		return nil, fmt.Errorf("%w: voyager %s", domain.ErrRateLimit, path)
	case status >= 400:
		return nil, fmt.Errorf("voyager %s returned HTTP %d: %s", path, status, snippet(body))
	default:
		return nil, fmt.Errorf("voyager %s: unexpected status %d (ok=%v): %s", path, status, ok, snippet(body))
	}
}

// snippet returns a short, single-line excerpt of a response body for error
// messages, so a multi-kilobyte HTML error page does not flood the terminal.
func snippet(body string) string {
	const max = 200
	s := strings.ReplaceAll(strings.TrimSpace(body), "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// encodeVoyagerURL builds the full request URL for a voyager endpoint.
//
// Encoding contract (matches M0 captured URL, the working oracle):
//   - The "query" param value (a restli tuple like "(origin:JOB_SEARCH_PAGE_OTHER_ENTRY,...)")
//     and the "variables" param value (a restli tuple like "(cardSectionTypes:List(...),...)") are
//     both written with their structural punctuation -- ( ) : , -- LITERAL on the wire.
//     Spaces within the value become %20. No other transformation is applied.
//   - All other params (decorationId, count, q, start, queryId, etc.) are encoded with
//     standard url.QueryEscape rules.
//
// This is a pure function; it is unit-tested in client_test.go.
func encodeVoyagerURL(path string, query url.Values) string {
	// restliKeys holds the set of param names that carry restli tuple values
	// and must use literal structural punctuation rather than percent-encoding.
	restliKeys := map[string]struct{}{"query": {}, "variables": {}}

	// Separate restli params from standard params.
	restliParams := map[string]string{}
	other := url.Values{}
	for k, vs := range query {
		if _, ok := restliKeys[k]; ok {
			if len(vs) > 0 {
				restliParams[k] = vs[0]
			}
			continue
		}
		other[k] = vs
	}

	var sb strings.Builder
	sb.WriteString(linkedInBase)
	sb.WriteString(path)

	// Encode the standard parameters using url.Values.Encode (sorts by key).
	standardEncoded := other.Encode()

	if len(restliParams) == 0 {
		// No restli params: standard encoding is sufficient.
		if standardEncoded != "" {
			sb.WriteByte('?')
			sb.WriteString(standardEncoded)
		}
		return sb.String()
	}

	// Append the standard params first, then the restli params in stable order.
	sb.WriteByte('?')
	if standardEncoded != "" {
		sb.WriteString(standardEncoded)
		sb.WriteByte('&')
	}

	// Emit restli params in a deterministic order: "query" before "variables".
	first := true
	for _, k := range []string{"query", "variables"} {
		v, ok := restliParams[k]
		if !ok {
			continue
		}
		if !first {
			sb.WriteByte('&')
		}
		first = false
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(encodeRestliValue(v))
	}

	return sb.String()
}

// encodeRestliValue percent-encodes a restli tuple value for embedding in a
// URL query string while keeping the restli structural characters literal:
//
//	( ) : ,  -- kept as-is (structural, not encoded)
//	space    -- encoded as %20
//	everything else -- standard URL percent-encoding via url.PathEscape
//
// url.PathEscape encodes everything except unreserved chars and '/', ':', '@',
// '!', '$', '&', '\”, '(', ')', '*', '+', ',', ';', '='. Of those, the ones
// we must keep literal are ( ) : , -- which PathEscape already leaves alone.
// Spaces become %20 (PathEscape also does this). The net effect is that the
// restli tuple structural chars survive and ordinary values are safely encoded.
//
// Crucially, this function is NOT double-encoding-safe by itself -- callers
// must pass the raw (not already-encoded) restli string. restliEscape in
// jobs.go pre-escapes user-supplied keywords/location BEFORE embedding them
// in the tuple, so the tuple arriving here contains %XX sequences that must
// not be re-encoded. url.PathEscape would encode the '%' in those sequences.
// We handle this by splitting on pre-existing %XX sequences and encoding only
// the plain segments between them.
func encodeRestliValue(s string) string {
	var sb strings.Builder
	i := 0
	for i < len(s) {
		// Look for a pre-existing percent-encoded triplet like %20 or %2C.
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			// Pass the %XX through verbatim (avoid double-encoding).
			sb.WriteByte('%')
			sb.WriteByte(s[i+1])
			sb.WriteByte(s[i+2])
			i += 3
			continue
		}
		// Encode this single byte.
		b := s[i]
		switch {
		case b == ' ':
			sb.WriteString("%20")
		case isRestliSafe(b):
			sb.WriteByte(b)
		default:
			fmt.Fprintf(&sb, "%%%02X", b)
		}
		i++
	}
	return sb.String()
}

// isRestliSafe reports whether a byte may appear literally in a restli query
// value embedded in a URL query string. Safe = unreserved chars plus the
// restli structural chars and a handful of sub-delimiters that LinkedIn keeps
// literal in practice.
func isRestliSafe(b byte) bool {
	// Unreserved: A-Z a-z 0-9 - _ . ~
	if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') {
		return true
	}
	switch b {
	case '-', '_', '.', '~': // unreserved
		return true
	case '(', ')', ':', ',': // restli structural -- MUST stay literal
		return true
	}
	return false
}

// isHex reports whether b is a hexadecimal digit (for %XX detection).
func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// fetchInPage issues a voyager GET via fetch() inside the page (the proven
// M0 transport) and returns the HTTP status, ok flag, and the full response
// body string. csrf must already have surrounding quotes stripped.
func fetchInPage(ctx context.Context, fullURL, csrf string) (status int, ok bool, body string, err error) {
	var raw []byte
	opt := func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}
	if runErr := chromedp.Run(ctx, chromedp.Evaluate(buildFetchExpr(fullURL, csrf), &raw, opt)); runErr != nil {
		return 0, false, "", runErr
	}
	var r struct {
		Status int    `json:"status"`
		OK     bool   `json:"ok"`
		Body   string `json:"body"`
	}
	if jsonErr := json.Unmarshal(raw, &r); jsonErr != nil {
		return 0, false, "", fmt.Errorf("parse fetch result (raw=%.200s): %w", string(raw), jsonErr)
	}
	return r.Status, r.OK, r.Body, nil
}

// buildFetchExpr returns a JS expression that performs a voyager GET with the
// 4 required headers and resolves to {status, ok, body}. This is the identical
// pattern from cmd/spike-jobs/main.go (the proven M0 oracle).
func buildFetchExpr(fullURL, csrf string) string {
	urlJSON, _ := json.Marshal(fullURL)
	csrfJSON, _ := json.Marshal(csrf)
	return fmt.Sprintf(`(async () => {
  try {
    const resp = await fetch(%s, {
      method: 'GET',
      credentials: 'include',
      headers: {
        'accept': 'application/vnd.linkedin.normalized+json+2.1',
        'csrf-token': %s,
        'x-li-lang': 'en_US',
        'x-restli-protocol-version': '2.0.0',
      },
    });
    const body = await resp.text();
    return {status: resp.status, ok: resp.ok, body: body};
  } catch (e) {
    return {status: 0, ok: false, body: 'ERROR: ' + (e && e.message ? e.message : String(e))};
  }
})()`, string(urlJSON), string(csrfJSON))
}
