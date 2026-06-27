package enrich

import (
	"errors"
	"testing"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// insightsJSONInternal returns a canonical JSON payload for parseInsights tests.
func insightsJSONInternal() string {
	return `{"real_summary":"Backend engineer role","top_skills":["Go","Kubernetes"],"salary_range":"$120k-$150k","seniority":"Senior","condensed_description":"Platform engineering role","notes":"JD uses excessive buzzwords"}`
}

func TestParseInsights_PlainJSON(t *testing.T) {
	ins, err := parseInsights([]byte(insightsJSONInternal()))
	if err != nil {
		t.Fatalf("parseInsights: %v", err)
	}
	if ins.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q", ins.RealSummary)
	}
}

func TestParseInsights_FencedMarkdown(t *testing.T) {
	fenced := []byte("```json\n" + insightsJSONInternal() + "\n```")
	ins, err := parseInsights(fenced)
	if err != nil {
		t.Fatalf("parseInsights with fences: %v", err)
	}
	if ins.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q", ins.RealSummary)
	}
}

func TestParseInsights_LeadingAndTrailingProse(t *testing.T) {
	prose := []byte("Sure! Here you go:\n\n" + insightsJSONInternal() + "\n\nI hope this helps!")
	ins, err := parseInsights(prose)
	if err != nil {
		t.Fatalf("parseInsights with prose: %v", err)
	}
	if ins.RealSummary != "Backend engineer role" {
		t.Errorf("RealSummary = %q", ins.RealSummary)
	}
}

func TestParseInsights_NoJSON_ReturnsError(t *testing.T) {
	_, err := parseInsights([]byte("no json here"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, domain.ErrSchema) {
		t.Errorf("err = %v; want wrapping domain.ErrSchema", err)
	}
}
