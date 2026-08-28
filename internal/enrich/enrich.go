// Package enrich provides a provider-agnostic LLM enrichment adapter.
// It satisfies the usecase.Enricher interface via Go duck typing.
//
// Dependency constraint: this package may ONLY import internal/domain and
// stdlib. It must NOT import internal/usecase, internal/voyager,
// internal/cache, or any other internal package. The check-deps make target
// enforces this.
//
// Provider priority (auto-detect): Ollama → OpenAI → Gemini → Anthropic.
// OpenRouter is supported but NOT auto-detected -- see the note in autoDetect.
// Set LI_ASSIST_ENRICH_PROVIDER to force a specific provider.
// Set LI_ASSIST_ENRICH_MODEL to override the model name.
//
// Ollama auto-start (forced provider only):
// When LI_ASSIST_ENRICH_PROVIDER=ollama and the server is not running, the
// CLI will start "ollama serve" automatically (detached, new process group)
// and wait for it to become ready before proceeding. This behaviour only
// applies to the explicit "ollama" provider — auto-detect is side-effect-free.
//
//	LI_ASSIST_OLLAMA_AUTOSTART     — default "true"; set "false" or "0" to
//	                                 disable automatic start.
//	LI_ASSIST_OLLAMA_START_TIMEOUT — readiness wait; default "15s". Accepts
//	                                 Go duration strings ("30s", "1m") or
//	                                 plain seconds ("20").
//	LI_ASSIST_OLLAMA_HOST          — Ollama base URL; default
//	                                 "http://localhost:11434".
//
// Request timeout (all providers):
//
//	LI_ASSIST_ENRICH_TIMEOUT       — per-request HTTP timeout; default "120s".
//	                                 Accepts Go duration strings ("3m") or plain
//	                                 seconds ("180"). Transient failures --
//	                                 timeouts, resets, 429/5xx -- are retried
//	                                 three times with a 1s/2s/4s backoff.
package enrich

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// ErrNoProvider is returned by NewFromEnv when no LLM provider is reachable or
// configured. Callers should treat this as a soft skip, not a hard failure.
var ErrNoProvider = errors.New("enrich: no provider configured")

// IsErrNoProvider reports whether err is or wraps ErrNoProvider.
func IsErrNoProvider(err error) bool {
	return errors.Is(err, ErrNoProvider)
}

// Enricher enriches a domain.Job with LLM-generated insights. It satisfies the
// usecase.Enricher interface structurally (duck typing); this package does not
// import usecase.
type Enricher struct {
	impl enricherImpl
}

// enricherImpl is the internal strategy interface.
type enricherImpl interface {
	enrich(ctx context.Context, job domain.Job) (domain.Insights, error)
}

// Enrich calls the underlying provider implementation.
func (e *Enricher) Enrich(ctx context.Context, job domain.Job) (domain.Insights, error) {
	return e.impl.enrich(ctx, job)
}

// ---- Prompt ----

func buildPrompt(job domain.Job) string {
	return fmt.Sprintf(`You are a no-nonsense recruiter-BS filter. Read the job description below and return ONLY a valid JSON object — no prose, no markdown fences, no commentary before or after — matching exactly this schema:

{
  "real_summary": "<1-2 plain sentences, no marketing language, honest summary of what the role actually is>",
  "top_skills": ["<skill1>", "<skill2>", ...],
  "salary_range": "<stated compensation verbatim, or empty string>",
  "seniority": "<Junior|Mid|Senior|Staff|Principal|Lead or empty if genuinely unclear>",
  "condensed_description": "<short factual paragraph — the role minus the hype>",
  "notes": "<flag if JD appears AI-generated, boilerplate, or buzzword-heavy; else empty string>"
}

Field rules — follow these exactly:

top_skills:
- Include ONLY genuinely required / must-have skills. Exclude any skill described as "nice to have", "bonus", "preferred", "a plus", or otherwise optional.
- Do not split a combined skill into separate items. Keep compound skills as one array element (e.g. "SQL/Postgres", "CI/CD", "Prometheus/Grafana" each stay a single item).
- Cap at 8 items, most important first.

salary_range:
- Copy the stated compensation verbatim, including any equity, bonus, or OTE wording (e.g. "$150,000-$185,000 + equity").
- If no compensation is stated in the description, return an empty string "".
- Do not invent or infer a range that is not explicitly stated.

seniority:
- A short label inferred from the description (e.g. Junior, Mid, Senior, Staff, Principal, Lead).
- Return an empty string if genuinely unclear.

real_summary:
- 1-2 plain sentences, no marketing language.

condensed_description:
- A short factual paragraph — the role minus the hype.

notes:
- Flag if the description reads as AI-generated / boilerplate / buzzword-heavy, else return an empty string.

Job title: %s
Company: %s
Location: %s

Job description:
%s`,
		job.Title(),
		job.Company().Name(),
		job.Location(),
		job.Posting().Description(),
	)
}

// ---- OpenAI-compatible implementation ----

// openAICompatImpl drives any OpenAI-compatible endpoint (Ollama, OpenAI, Gemini).
type openAICompatImpl struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAICompat returns an Enricher backed by an OpenAI-compatible endpoint.
// Pass apiKey="" for keyless endpoints (e.g. local Ollama).
func NewOpenAICompat(baseURL, apiKey, model string) *Enricher {
	return &Enricher{impl: &openAICompatImpl{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: enrichHTTPTimeout()},
	}}
}

func (o *openAICompatImpl) enrich(ctx context.Context, job domain.Job) (domain.Insights, error) {
	prompt := buildPrompt(job)

	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type responseFormat struct {
		Type string `json:"type"`
	}
	type reqBody struct {
		Model          string          `json:"model"`
		Messages       []message       `json:"messages"`
		ResponseFormat *responseFormat `json:"response_format,omitempty"`
	}

	body := reqBody{
		Model: o.model,
		Messages: []message{
			{Role: "system", Content: "You are a recruiter-BS filter. Return ONLY valid JSON matching the schema provided. No prose, no markdown fences."},
			{Role: "user", Content: prompt},
		},
	}

	// Include response_format only when a key is present (i.e. non-Ollama keyless).
	// Ollama also supports it, but for safety we only enforce it with keyed providers.
	if o.apiKey != "" {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	// Retry the transient set with backoff. The status list and the reasoning are
	// ported from claude-skills/terragrunt-skill/evals/run_model.py, which already
	// drives OpenRouter and carries the comment "429 is rate limiting, which THE
	// FREE TIERS DO CONSTANTLY". Without this a single 429 fails an enrichment that
	// would have succeeded a second later, which on a :free model is the common case
	// rather than the exceptional one.
	var (
		resp     *http.Response
		respBody []byte
	)
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			// Rebuild the body: a Reader is consumed by the previous attempt.
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		lastAttempt := attempt >= maxEnrichRetries

		// failure is non-nil when this attempt should be repeated after a backoff.
		var failure error

		resp, err = o.client.Do(req)
		switch {
		case err != nil:
			// A transport error carries no status code, so retryableStatus never
			// sees it. Before this branch existed, a connection that never opened
			// failed the enrichment outright.
			if lastAttempt || !retryableError(ctx, err) {
				return domain.Insights{}, fmt.Errorf("enrich: http: %w", err)
			}
			failure = err

		case retryableStatus(resp.StatusCode) && !lastAttempt:
			_ = resp.Body.Close()
			failure = fmt.Errorf("HTTP %d", resp.StatusCode)

		default:
			// Read the body INSIDE the loop. http.Client.Timeout covers the body
			// read as well as the connection, so a provider that returns its
			// headers promptly and then streams a slow completion fails here --
			// not at Do. That is where the deepseek-v4-flash timeouts actually
			// landed: "context deadline exceeded ... while reading body", after a
			// 200. Reading outside the loop put the most common timeout out of
			// the retry's reach.
			respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxEnrichRespBytes))
			_ = resp.Body.Close()
			if err != nil {
				if lastAttempt || !retryableError(ctx, err) {
					return domain.Insights{}, fmt.Errorf("enrich: read response: %w", err)
				}
				failure = err
				break
			}
			if resp.StatusCode != http.StatusOK {
				return domain.Insights{}, fmt.Errorf("enrich: provider returned HTTP %d: %s", resp.StatusCode, truncateForError(respBody))
			}
		}
		if failure == nil {
			break
		}
		// 1s, 2s, 4s. Bounded on purpose -- enrichment is interactive and a caller
		// waiting a minute for a free model is worse than a soft skip.
		if werr := backoffWait(ctx, attempt); werr != nil {
			return domain.Insights{}, werr
		}
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: decode response: %w", err)
	}
	if len(result.Choices) == 0 {
		return domain.Insights{}, fmt.Errorf("enrich: provider returned no choices")
	}

	return parseInsights([]byte(result.Choices[0].Message.Content))
}

// maxEnrichRespBytes caps the response we will buffer. A chat completion of this
// shape is a few kilobytes; anything approaching the cap is a misconfigured
// endpoint, not an answer.
const maxEnrichRespBytes = 1 << 20 // 1 MiB

// truncateForError keeps an error message readable when a provider answers with
// an HTML error page rather than JSON.
func truncateForError(b []byte) string {
	const cap = 4096
	s := strings.TrimSpace(string(b))
	if len(s) > cap {
		return s[:cap] + "… (truncated)"
	}
	return s
}

// maxEnrichRetries bounds the backoff loop above: 3 retries = 1s + 2s + 4s.
const maxEnrichRetries = 3

// retryableStatus reports whether a status is worth a second attempt. Same set as
// run_model.py's RETRY_STATUS.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429 -- the free-tier case
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	}
	return false
}

// retryableError reports whether a transport-level failure is worth another
// attempt. Statuses and transport errors are different populations: a timeout or
// a dropped connection is transient and usually clears on the next attempt, while
// a cancelled context, a bad certificate or an unresolvable host repeats
// identically and would only burn the backoff.
func retryableError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// The caller asked to stop, or their own deadline has already passed. Neither
	// is ours to retry -- a further attempt on a dead context fails instantly.
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	// Deterministic failures: the same request will fail the same way.
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// NXDOMAIN is permanent; a SERVFAIL or a timed-out resolver is not.
		return !dnsErr.IsNotFound && (dnsErr.IsTemporary || dnsErr.IsTimeout)
	}
	// http.Client.Timeout lands here, as does a per-request deadline.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// Connection reset/refused, or a response truncated mid-flight.
	return errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

// enrichBackoffBase is the first backoff interval; each attempt doubles it.
// A variable rather than a constant only so tests can drive the whole loop
// without spending seven real seconds in it.
var enrichBackoffBase = time.Second

// backoffWait sleeps 1s, 2s, 4s ... but abandons the wait if the caller's context
// is done. A plain time.Sleep here would ignore a cancellation for up to 4s.
func backoffWait(ctx context.Context, attempt int) error {
	t := time.NewTimer(time.Duration(1<<attempt) * enrichBackoffBase)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("enrich: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

// envEnrichTimeout overrides the per-request HTTP timeout.
const envEnrichTimeout = "LI_ASSIST_ENRICH_TIMEOUT"

// defaultEnrichTimeout is generous because a large posting on a loaded provider
// legitimately takes tens of seconds. Measured on a 19k-char posting: gemini
// answers in ~4s, a discounted flash model has ranged from 5s to 96s.
const defaultEnrichTimeout = 120 * time.Second

// enrichHTTPTimeout reads LI_ASSIST_ENRICH_TIMEOUT. Accepts Go duration strings
// ("180s", "3m") or bare seconds ("180"), same grammar as
// LI_ASSIST_OLLAMA_START_TIMEOUT. A model that reliably needs more than the
// default is otherwise unusable with no way to find that out except by hitting
// the wall.
func enrichHTTPTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv(envEnrichTimeout))
	if v == "" {
		return defaultEnrichTimeout
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return defaultEnrichTimeout
}

// ---- Anthropic native implementation ----

// anthropicImpl drives the Anthropic Messages API natively (not OpenAI-compat).
type anthropicImpl struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewAnthropic returns an Enricher backed by the Anthropic Messages API.
// baseURL is used in tests to point at an httptest.Server; in production use
// "https://api.anthropic.com".
func NewAnthropic(baseURL, apiKey, model string) *Enricher {
	return &Enricher{impl: &anthropicImpl{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: enrichHTTPTimeout()},
	}}
}

func (a *anthropicImpl) enrich(ctx context.Context, job domain.Job) (domain.Insights, error) {
	prompt := buildPrompt(job)

	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type reqBody struct {
		Model     string    `json:"model"`
		MaxTokens int       `json:"max_tokens"`
		Messages  []message `json:"messages"`
	}

	body := reqBody{
		Model:     a.model,
		MaxTokens: 1024,
		Messages:  []message{{Role: "user", Content: prompt}},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(req)
	if err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return domain.Insights{}, fmt.Errorf("enrich: provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: decode response: %w", err)
	}
	// Iterate to find the first text-type block; Anthropic may include other
	// types (e.g. "thinking") before the text content.
	var textBlock string
	for _, block := range result.Content {
		if block.Type == "text" {
			textBlock = block.Text
			break
		}
	}
	if textBlock == "" {
		return domain.Insights{}, fmt.Errorf("%w: anthropic response contained no text content block", domain.ErrSchema)
	}

	return parseInsights([]byte(textBlock))
}

// ---- parseInsights ----

// parseInsights extracts domain.Insights from raw LLM output. It strips
// markdown fences, finds the first balanced { ... } block, and unmarshals it.
// On failure it wraps domain.ErrSchema.
func parseInsights(raw []byte) (domain.Insights, error) {
	s := string(raw)

	// Strip leading/trailing whitespace.
	s = strings.TrimSpace(s)

	// Strip ```json ... ``` or ``` ... ``` fences.
	if strings.HasPrefix(s, "```") {
		// Find first newline after the fence opener.
		nl := strings.IndexByte(s, '\n')
		if nl != -1 {
			s = s[nl+1:]
		}
		// Strip trailing fence.
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}

	// Find the first balanced { ... } block.
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return domain.Insights{}, fmt.Errorf("%w: no JSON object found in LLM output", domain.ErrSchema)
	}

	depth := 0
	end := -1
	inStr := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inStr {
			escaped = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if end == -1 {
		return domain.Insights{}, fmt.Errorf("%w: unbalanced JSON object in LLM output", domain.ErrSchema)
	}

	jsonBlob := s[start : end+1]
	var ins domain.Insights
	if err := json.Unmarshal([]byte(jsonBlob), &ins); err != nil {
		return domain.Insights{}, fmt.Errorf("%w: %v", domain.ErrSchema, err)
	}
	return ins, nil
}

// ---- Provider auto-detect / NewFromEnv ----

const (
	envProvider   = "LI_ASSIST_ENRICH_PROVIDER"
	envModel      = "LI_ASSIST_ENRICH_MODEL"
	envOllamaHost = "LI_ASSIST_OLLAMA_HOST"

	defaultOllamaHost = "http://localhost:11434"

	// Model defaults current as of 2026-06; overridable via LI_ASSIST_ENRICH_MODEL
	// (model IDs drift — the env override is the safety net).
	defaultOllamaModel      = "llama3.2"
	defaultOpenAIModel      = "gpt-5.4-mini"
	defaultGeminiModel      = "gemini-2.5-flash"
	defaultAnthropicModel   = "claude-haiku-4-5"
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	openAIBaseURL           = "https://api.openai.com/v1"
	geminiBaseURL           = "https://generativelanguage.googleapis.com/v1beta/openai"
	openRouterBaseURL       = "https://openrouter.ai/api/v1"
	// Deliberately a PAID model, not a :free one. Benchmarked 2026-08-28 on three
	// real cached postings, five attempts each on the same posting:
	//
	//	openai gpt-5.4-mini          5/5   1,945ms   (paid, the default path)
	//	google/gemini-3.7-flash      5/5   6,460ms   $0.375/$1.875 per 1M tok
	//	minimax/minimax-m3:free      4/5   9,550ms
	//	google/gemma-4-26b-a4b-it:free 0/3  HTTP 429 every attempt
	//	z-ai/glm-5.2:free            0/3   HTTP 429 every attempt
	//
	// The free tier is a false economy here. Two of three candidates were hard
	// rate-limited on a shared upstream pool ("temporarily rate-limited upstream",
	// routed via Google AI Studio) and the one that worked was both slower and less
	// reliable than a paid model costing fractions of a cent per posting.
	// Enrichment is opt-in and per-posting, so the bill is bounded by hand.
	//
	// Gemini 3.7 Flash also gave the most specific output of the three that ran --
	// naming prompt injection and data leaks where gpt-5.4-mini said "threat
	// modeling" -- and carries a 1M context, which job ads never approach but
	// removes truncation as a failure mode entirely.
	//
	// COST, measured from the real cache rather than guessed: an average posting is
	// ~4,400 chars of description (~1,350 prompt tokens) and yields ~1,200 chars of
	// insights (~300 output tokens). At the rates above that is $0.00107 per
	// posting -- 11 cents per 100, $1.07 per 1,000. Enriching a whole 3,500-row
	// cache would cost under $4. The listed rate is itself a promotional discount
	// (Google AI Studio list is ~$1.50 in), so treat it as a floor that may rise.
	defaultOpenRouterModel = "google/gemini-3.7-flash"
)

// NewFromEnv constructs an Enricher by reading environment variables.
// Provider selection order:
//  1. LI_ASSIST_ENRICH_PROVIDER (forced; "none" → ErrNoProvider immediately)
//  2. auto: Ollama reachable? → ollama. OPENAI_API_KEY? → openai. GEMINI_API_KEY? → gemini. ANTHROPIC_API_KEY? → anthropic. → ErrNoProvider.
//
// LI_ASSIST_ENRICH_MODEL overrides the default model for whichever provider is chosen.
func NewFromEnv() (*Enricher, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv(envProvider)))
	model := strings.TrimSpace(os.Getenv(envModel))
	ollamaHost := strings.TrimRight(strings.TrimSpace(os.Getenv(envOllamaHost)), "/")
	if ollamaHost == "" {
		ollamaHost = defaultOllamaHost
	}

	switch provider {
	case "", "auto":
		return autoDetect(ollamaHost, model)
	case "none":
		return nil, ErrNoProvider
	case "ollama":
		if err := ensureOllamaRunning(ollamaHost); err != nil {
			return nil, err
		}
		m := model
		if m == "" {
			m = defaultOllamaModel
		}
		return NewOpenAICompat(ollamaHost+"/v1", "", m), nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		m := model
		if m == "" {
			m = defaultOpenAIModel
		}
		return NewOpenAICompat(openAIBaseURL, key, m), nil
	case "gemini":
		key := os.Getenv("GEMINI_API_KEY")
		m := model
		if m == "" {
			m = defaultGeminiModel
		}
		return NewOpenAICompat(geminiBaseURL, key, m), nil
	case "openrouter":
		// OPENROUTER_API_KEY is absent from non-interactive shells -- it lives in
		// ~/.config/dotfiles/env.sh. Fail here rather than letting the request go
		// out unauthenticated and come back as a confusing 401.
		//
		// The wrap is ErrNoProvider on purpose, so a missing key stays a SOFT SKIP
		// like every other absent provider. The consequence, verified rather than
		// assumed: callers in cmd/li-assist/jobs.go match on IsErrNoProvider and
		// print their own message, so the detail below never reaches a user. That
		// generic message now names OPENROUTER_API_KEY too, which is what actually
		// tells someone what to set.
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("enrich: OPENROUTER_API_KEY is not set "+
				"(it is absent from non-interactive shells; "+
				"run: source ~/.config/dotfiles/env.sh): %w", ErrNoProvider)
		}
		m := model
		if m == "" {
			m = defaultOpenRouterModel
		}
		return NewOpenAICompat(openRouterBaseURL, key, m), nil
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		m := model
		if m == "" {
			m = defaultAnthropicModel
		}
		return NewAnthropic(defaultAnthropicBaseURL, key, m), nil
	default:
		return nil, fmt.Errorf("enrich: unknown provider %q (valid: ollama, openai, gemini, anthropic, none, auto)", provider)
	}
}

// autoDetect runs the provider priority chain without a forced selection.
func autoDetect(ollamaHost, model string) (*Enricher, error) {
	// 1. Ollama: probe /api/tags with a short timeout (1s, side-effect-free).
	if ollamaReachable(ollamaHost, 1*time.Second) {
		m := model
		if m == "" {
			m = defaultOllamaModel
		}
		return NewOpenAICompat(ollamaHost+"/v1", "", m), nil
	}

	// OpenRouter is deliberately ABSENT from auto-detect. It was added here briefly
	// on 2026-08-28 and reverted the same day: auto-detect exists to pick something
	// that works without being asked, and inserting OpenRouter ahead of OpenAI would
	// have made every enrichment slower (6,460ms vs 1,945ms measured) to no benefit,
	// while silently moving traffic to a different vendor.
	//
	// Reach for it explicitly with LI_ASSIST_ENRICH_PROVIDER=openrouter, which is
	// the honest interface for "use this other vendor" -- there is no way to infer
	// that intent from the mere presence of a key.
	//
	// 2. OpenAI.
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		m := model
		if m == "" {
			m = defaultOpenAIModel
		}
		return NewOpenAICompat(openAIBaseURL, key, m), nil
	}

	// 3. Gemini.
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		m := model
		if m == "" {
			m = defaultGeminiModel
		}
		return NewOpenAICompat(geminiBaseURL, key, m), nil
	}

	// 4. Anthropic.
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		m := model
		if m == "" {
			m = defaultAnthropicModel
		}
		return NewAnthropic(defaultAnthropicBaseURL, key, m), nil
	}

	return nil, ErrNoProvider
}
