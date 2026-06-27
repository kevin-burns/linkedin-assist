package enrich_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/enrich"
)

// makeTestJob builds a minimal domain.Job for testing.
func makeTestJob(t *testing.T) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN("urn:li:fsd_jobPosting:9999")
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}
	return domain.NewJob(
		urn,
		"Senior Synergy Architect",
		"Remote",
		domain.NewCompany("", "BoilerplateCo"),
		time.Time{},
		domain.NewPosting(
			"We are seeking a passionate, world-class, ninja rockstar to join our fast-paced disruptive team...",
			"https://example.com/apply",
			0,
		),
	)
}

// insightsPayload builds a JSON response matching domain.Insights.
func insightsJSON() string {
	return `{"real_summary":"Backend engineer role","top_skills":["Go","Kubernetes"],"salary_range":"$120k-$150k","seniority":"Senior","condensed_description":"Platform engineering role","notes":"JD uses excessive buzzwords"}`
}

// --- OpenAI-compatible path tests ---

func TestEnrich_OpenAICompat_ParsesInsights(t *testing.T) {
	// Serve a canned OpenAI chat/completions response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := fmt.Sprintf(`{
			"choices": [{
				"message": {
					"content": %q
				}
			}]
		}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewOpenAICompat(srv.URL, "test-key", "test-model")
	job := makeTestJob(t)

	insights, err := e.Enrich(context.Background(), job)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	if insights.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q; want %q", insights.RealSummary, "Backend engineer role")
	}
	if len(insights.TopSkills) != 2 {
		t.Errorf("TopSkills len = %d; want 2", len(insights.TopSkills))
	}
	if insights.TopSkills[0] != "Go" {
		t.Errorf("TopSkills[0] = %q; want %q", insights.TopSkills[0], "Go")
	}
	if insights.SalaryRange != "$120k-$150k" {
		t.Errorf("SalaryRange = %q; want %q", insights.SalaryRange, "$120k-$150k")
	}
	if insights.Seniority != "Senior" {
		t.Errorf("Seniority = %q; want %q", insights.Seniority, "Senior")
	}
	if insights.CondensedDescription != "Platform engineering role" {
		t.Errorf("CondensedDescription = %q; want %q", insights.CondensedDescription, "Platform engineering role")
	}
	if insights.Notes != "JD uses excessive buzzwords" {
		t.Errorf("Notes = %q; want %q", insights.Notes, "JD uses excessive buzzwords")
	}
}

func TestEnrich_OpenAICompat_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewOpenAICompat(srv.URL, "sk-mykey", "test-model")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if gotAuth != "Bearer sk-mykey" {
		t.Errorf("Authorization header = %q; want %q", gotAuth, "Bearer sk-mykey")
	}
}

func TestEnrich_OpenAICompat_NoKeyOmitsAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	// No API key (Ollama-style).
	e := enrich.NewOpenAICompat(srv.URL, "", "test-model")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header = %q; want empty (no key supplied)", gotAuth)
	}
}

func TestEnrich_OpenAICompat_JSONObjectResponseFormat(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		resp := fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewOpenAICompat(srv.URL, "key", "test-model")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	// When a key is provided, response_format must be set.
	rf, ok := gotBody["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %v", gotBody["response_format"])
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %q; want json_object", rf["type"])
	}
}

func TestEnrich_OpenAICompat_FencedJSON(t *testing.T) {
	// Model wraps JSON in markdown fences — robust parser must strip them.
	fenced := "```json\n" + insightsJSON() + "\n```"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, fenced)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewOpenAICompat(srv.URL, "key", "test-model")
	insights, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich with fenced JSON: %v", err)
	}
	if insights.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q; want %q", insights.RealSummary, "Backend engineer role")
	}
}

func TestEnrich_OpenAICompat_LeadingProseJSON(t *testing.T) {
	// Model emits prose then JSON — extract first balanced {...}.
	withProse := "Here is the analysis:\n" + insightsJSON() + "\nDone."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, withProse)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewOpenAICompat(srv.URL, "key", "test-model")
	insights, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich with leading prose: %v", err)
	}
	if insights.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q; want %q", insights.RealSummary, "Backend engineer role")
	}
}

func TestEnrich_OpenAICompat_BadJSON_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{"choices":[{"message":{"content":"not json at all"}}]}`
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewOpenAICompat(srv.URL, "key", "test-model")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err == nil {
		t.Fatal("expected error for unparseable content, got nil")
	}
}

func TestEnrich_OpenAICompat_HTTPError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	e := enrich.NewOpenAICompat(srv.URL, "key", "test-model")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err == nil {
		t.Fatal("expected error for HTTP 429, got nil")
	}
}

// --- Anthropic native path tests ---

func TestEnrich_Anthropic_ParsesInsights(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewAnthropic(srv.URL, "ant-key", "claude-haiku-4-5")
	job := makeTestJob(t)

	insights, err := e.Enrich(context.Background(), job)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if insights.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q; want %q", insights.RealSummary, "Backend engineer role")
	}
}

func TestEnrich_Anthropic_RequiredHeaders(t *testing.T) {
	var gotXAPIKey, gotVersion, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("content-type")
		resp := fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewAnthropic(srv.URL, "ant-mykey", "claude-haiku-4-5")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if gotXAPIKey != "ant-mykey" {
		t.Errorf("x-api-key = %q; want %q", gotXAPIKey, "ant-mykey")
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("anthropic-version = %q; want %q", gotVersion, "2023-06-01")
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("content-type = %q; want application/json", gotContentType)
	}
}

func TestEnrich_Anthropic_FencedJSON(t *testing.T) {
	fenced := "```json\n" + insightsJSON() + "\n```"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, fenced)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewAnthropic(srv.URL, "ant-key", "claude-haiku-4-5")
	insights, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich with fenced JSON: %v", err)
	}
	if insights.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q; want %q", insights.RealSummary, "Backend engineer role")
	}
}

func TestEnrich_Anthropic_HTTPError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := enrich.NewAnthropic(srv.URL, "bad-key", "claude-haiku-4-5")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err == nil {
		t.Fatal("expected error for HTTP 401, got nil")
	}
}

func TestEnrich_Anthropic_NoTextBlock_ReturnsError(t *testing.T) {
	// Response with only a non-text content block (e.g. "thinking") and no text block.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{"content":[{"type":"thinking","thinking":"let me ponder..."}]}`
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewAnthropic(srv.URL, "ant-key", "claude-haiku-4-5")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err == nil {
		t.Fatal("expected error when no text block present, got nil")
	}
	if !errors.Is(err, domain.ErrSchema) {
		t.Errorf("err = %v; want wrapping domain.ErrSchema", err)
	}
}

func TestEnrich_Anthropic_FirstTextBlock_UsedWhenMixed(t *testing.T) {
	// Response with a non-text block first, then a text block — the text block must be used.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := fmt.Sprintf(`{"content":[{"type":"thinking","thinking":"deliberating"},{"type":"text","text":%q}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewAnthropic(srv.URL, "ant-key", "claude-haiku-4-5")
	insights, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if insights.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q; want %q", insights.RealSummary, "Backend engineer role")
	}
}

func TestEnrich_Anthropic_NoThinkingParam(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		resp := fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	e := enrich.NewAnthropic(srv.URL, "ant-key", "claude-haiku-4-5")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if _, hasThinking := gotBody["thinking"]; hasThinking {
		t.Error("request body must not include 'thinking' parameter")
	}
	// Must have max_tokens.
	if _, hasMaxTokens := gotBody["max_tokens"]; !hasMaxTokens {
		t.Error("request body must include 'max_tokens'")
	}
}

// --- Provider auto-detect tests ---

func TestNewFromEnv_NoProvider_ReturnsErrNoProvider(t *testing.T) {
	// Clear all provider env vars.
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "none")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := enrich.NewFromEnv()
	if err == nil {
		t.Fatal("expected error when no provider configured, got nil")
	}
	if !enrich.IsErrNoProvider(err) {
		t.Errorf("err = %v; want ErrNoProvider sentinel", err)
	}
}

func TestNewFromEnv_ForceAnthropicProvider(t *testing.T) {
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "ant-testkey")
	t.Setenv("LI_ASSIST_ENRICH_MODEL", "claude-haiku-4-5")

	e, err := enrich.NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil Enricher")
	}
}

func TestNewFromEnv_ForceOpenAIProvider(t *testing.T) {
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "sk-testkey")

	e, err := enrich.NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil Enricher")
	}
}

func TestNewFromEnv_ForceGeminiProvider(t *testing.T) {
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "gemini")
	t.Setenv("GEMINI_API_KEY", "gem-testkey")

	e, err := enrich.NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil Enricher")
	}
}

func TestNewFromEnv_ForceUnknownProvider_ReturnsError(t *testing.T) {
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "mymagicprovider")

	_, err := enrich.NewFromEnv()
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestNewFromEnv_AutoDetect_OpenAIWhenKeySet(t *testing.T) {
	// Force auto by not setting LI_ASSIST_ENRICH_PROVIDER.
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "")
	t.Setenv("OPENAI_API_KEY", "sk-autodetect")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	// Point Ollama probe at a server that will refuse (port 0 means no server).
	t.Setenv("LI_ASSIST_OLLAMA_HOST", "http://127.0.0.1:0")

	e, err := enrich.NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv auto with OPENAI_API_KEY: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil Enricher")
	}
}

func TestNewFromEnv_AutoDetect_AnthropicWhenOnlyKeySet(t *testing.T) {
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "ant-only")
	t.Setenv("LI_ASSIST_OLLAMA_HOST", "http://127.0.0.1:0")

	e, err := enrich.NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv auto with ANTHROPIC_API_KEY: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil Enricher")
	}
}

func TestNewFromEnv_AutoDetect_NoProvider_ErrNoProvider(t *testing.T) {
	t.Setenv("LI_ASSIST_ENRICH_PROVIDER", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	// Point Ollama probe at a port that won't respond.
	t.Setenv("LI_ASSIST_OLLAMA_HOST", "http://127.0.0.1:0")

	_, err := enrich.NewFromEnv()
	if !enrich.IsErrNoProvider(err) {
		t.Errorf("err = %v; want ErrNoProvider sentinel", err)
	}
}

// --- OpenAI-compat keyless path: response_format must be absent ---

func TestEnrich_OpenAICompat_KeylessOmitsResponseFormat(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		resp := fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, insightsJSON())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	// No API key: Ollama-style keyless path.
	e := enrich.NewOpenAICompat(srv.URL, "", "test-model")
	_, err := e.Enrich(context.Background(), makeTestJob(t))
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	// response_format must be entirely absent from the marshaled request body.
	if _, present := gotBody["response_format"]; present {
		t.Errorf("response_format key must not be present for keyless (Ollama) path; got: %v", gotBody["response_format"])
	}
}
