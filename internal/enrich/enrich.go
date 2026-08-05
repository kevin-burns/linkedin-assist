// Package enrich provides a provider-agnostic LLM enrichment adapter.
// It satisfies the usecase.Enricher interface via Go duck typing.
//
// Dependency constraint: this package may ONLY import internal/domain and
// stdlib. It must NOT import internal/usecase, internal/voyager,
// internal/cache, or any other internal package. The check-deps make target
// enforces this.
//
// Provider priority (auto-detect): Ollama → OpenAI → Gemini → Anthropic.
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
package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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
		client:  &http.Client{Timeout: 120 * time.Second},
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

	resp, err := o.client.Do(req)
	if err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return domain.Insights{}, fmt.Errorf("enrich: provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return domain.Insights{}, fmt.Errorf("enrich: decode response: %w", err)
	}
	if len(result.Choices) == 0 {
		return domain.Insights{}, fmt.Errorf("enrich: provider returned no choices")
	}

	return parseInsights([]byte(result.Choices[0].Message.Content))
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
		client:  &http.Client{Timeout: 120 * time.Second},
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
