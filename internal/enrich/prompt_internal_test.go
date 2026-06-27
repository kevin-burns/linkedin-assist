package enrich

import (
	"strings"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// makePromptTestJob builds a minimal domain.Job for prompt-content tests.
func makePromptTestJob(t *testing.T) domain.Job {
	t.Helper()
	urn, err := domain.ParseURN("urn:li:fsd_jobPosting:1234")
	if err != nil {
		t.Fatalf("ParseURN: %v", err)
	}
	return domain.NewJob(
		urn,
		"Staff Software Engineer",
		"San Francisco, CA",
		domain.NewCompany("", "Acme Corp"),
		time.Time{},
		domain.NewPosting(
			"We are looking for a Staff Software Engineer who knows Go, SQL/Postgres, and CI/CD. Nice to have: Kubernetes. Salary: $160,000-$200,000 + equity.",
			"https://example.com/apply",
			0,
		),
	)
}

// TestBuildPrompt_JSONOnly verifies the prompt instructs the model to return
// ONLY a JSON object with no surrounding prose or markdown fences.
func TestBuildPrompt_JSONOnly(t *testing.T) {
	job := makePromptTestJob(t)
	p := buildPrompt(job)

	mustContain(t, p, "ONLY", "prompt must instruct model to return ONLY JSON")
	mustContain(t, p, "no prose", "prompt must forbid prose in output")
	mustContain(t, p, "no markdown", "prompt must forbid markdown fences")
}

// TestBuildPrompt_TopSkills_RequiredOnly verifies the prompt restricts
// top_skills to required/must-have skills and excludes nice-to-have signals.
func TestBuildPrompt_TopSkills_RequiredOnly(t *testing.T) {
	job := makePromptTestJob(t)
	p := buildPrompt(job)

	mustContainAny(t, p,
		[]string{"required", "must-have", "must have"},
		"prompt must instruct that top_skills includes only required/must-have skills",
	)
	mustContainAny(t, p,
		[]string{"nice to have", "nice-to-have"},
		"prompt must explicitly mention excluding nice-to-have skills",
	)
	mustContainAny(t, p,
		[]string{"bonus", "preferred"},
		"prompt must explicitly mention excluding bonus/preferred skills",
	)
}

// TestBuildPrompt_TopSkills_NoSplit verifies the prompt instructs the model
// not to split compound skills like SQL/Postgres or CI/CD.
func TestBuildPrompt_TopSkills_NoSplit(t *testing.T) {
	job := makePromptTestJob(t)
	p := buildPrompt(job)

	mustContainAny(t, p,
		[]string{"do not split", "Do not split", "NOT split", "not split"},
		"prompt must instruct the model not to split compound skills",
	)
}

// TestBuildPrompt_TopSkills_Cap verifies the prompt caps top_skills at 8 items.
func TestBuildPrompt_TopSkills_Cap(t *testing.T) {
	job := makePromptTestJob(t)
	p := buildPrompt(job)

	// The cap must appear as "8" with nearby context about items/skills/cap.
	mustContain(t, p, "8", "prompt must cap top_skills at 8 items")
}

// TestBuildPrompt_SalaryRange_Verbatim verifies the prompt requires verbatim
// copying of the stated compensation, including equity/bonus/OTE.
func TestBuildPrompt_SalaryRange_Verbatim(t *testing.T) {
	job := makePromptTestJob(t)
	p := buildPrompt(job)

	mustContainAny(t, p,
		[]string{"verbatim", "exactly as stated", "word for word"},
		"prompt must instruct verbatim copying of salary/compensation",
	)
	mustContainAny(t, p,
		[]string{"equity", "bonus", "OTE"},
		"prompt must mention preserving equity/bonus/OTE in salary_range",
	)
}

// TestBuildPrompt_SalaryRange_EmptyIfAbsent verifies the prompt instructs
// returning an empty string when no compensation is stated.
func TestBuildPrompt_SalaryRange_EmptyIfAbsent(t *testing.T) {
	job := makePromptTestJob(t)
	p := buildPrompt(job)

	mustContainAny(t, p,
		[]string{"empty string", "empty if", "return \"\"", "return ''"},
		"prompt must instruct empty string when no salary is stated",
	)
	mustContainAny(t, p,
		[]string{"do not invent", "do not infer", "Do not invent", "Do not infer", "never invent", "never infer"},
		"prompt must instruct the model not to invent or infer a salary range",
	)
}

// TestBuildPrompt_JobFieldsPresent verifies the job's title, company, location,
// and description are interpolated into the prompt.
func TestBuildPrompt_JobFieldsPresent(t *testing.T) {
	job := makePromptTestJob(t)
	p := buildPrompt(job)

	mustContain(t, p, "Staff Software Engineer", "prompt must include job title")
	mustContain(t, p, "Acme Corp", "prompt must include company name")
	mustContain(t, p, "San Francisco, CA", "prompt must include location")
	mustContain(t, p, "SQL/Postgres", "prompt must include job description content")
}

// ---- helpers ----

func mustContain(t *testing.T, s, substr, msg string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%s: substring %q not found in prompt", msg, substr)
	}
}

func mustContainAny(t *testing.T, s string, candidates []string, msg string) {
	t.Helper()
	for _, c := range candidates {
		if strings.Contains(s, c) {
			return
		}
	}
	t.Errorf("%s: none of %v found in prompt", msg, candidates)
}
