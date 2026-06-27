package usecase_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
	"github.com/kevin-burns/linkedin-assist/internal/usecase"
)

// makeIntroJob builds a domain.Job with the given company name for intro testing.
func makeIntroJob(t *testing.T, companyName string) domain.Job {
	t.Helper()
	urn, _ := domain.ParseURN("urn:li:fsd_jobPosting:9001")
	return domain.NewJob(
		urn,
		"Engineer",
		"Remote",
		domain.NewCompany("urn:li:company:1", companyName),
		time.Time{},
		domain.NewPosting("", "", 0),
	)
}

// makeConn builds a domain.Connection for testing.
func makeConn(firstName, lastName, company string, connectedOn time.Time) domain.Connection {
	return domain.Connection{
		FirstName:   firstName,
		LastName:    lastName,
		ProfileURL:  "https://www.linkedin.com/in/" + strings.ToLower(firstName+"-"+lastName),
		Company:     company,
		Position:    "Engineer",
		ConnectedOn: connectedOn,
	}
}

// TestMatchIntros_ExactNormalizedMatch: job company "Foo Inc" matches
// connection company "Foo Inc" (same string) and "Foo" (suffix stripped).
func TestMatchIntros_ExactNormalizedMatch(t *testing.T) {
	job := makeIntroJob(t,"Foo Inc")
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	conns := []domain.Connection{
		makeConn("Alice", "Alpha", "Foo Inc", now),
		makeConn("Bob", "Beta", "Foo", now.Add(-24*time.Hour)),
	}

	intros := usecase.MatchIntros(job, conns)
	if len(intros) != 2 {
		t.Fatalf("len(intros) = %d; want 2", len(intros))
	}
	for _, intro := range intros {
		if intro.MatchConfidence != "exact" {
			t.Errorf("MatchConfidence = %q; want %q", intro.MatchConfidence, "exact")
		}
	}
}

// TestMatchIntros_LegalSuffixStripped: "Widgets GmbH" normalises to "widgets"
// and matches a connection company of "Widgets".
func TestMatchIntros_LegalSuffixStripped(t *testing.T) {
	job := makeIntroJob(t,"Widgets GmbH")
	conn := makeConn("Carol", "Chord", "Widgets", time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))

	intros := usecase.MatchIntros(job, []domain.Connection{conn})
	if len(intros) != 1 {
		t.Fatalf("len(intros) = %d; want 1", len(intros))
	}
	if intros[0].Name != "Carol Chord" {
		t.Errorf("Name = %q; want %q", intros[0].Name, "Carol Chord")
	}
}

// TestMatchIntros_NoCompanyJob: job with empty company name → nil.
func TestMatchIntros_NoCompanyJob(t *testing.T) {
	job := makeIntroJob(t,"")
	conn := makeConn("Dave", "Delta", "Acme", time.Time{})

	intros := usecase.MatchIntros(job, []domain.Connection{conn})
	if intros != nil {
		t.Errorf("expected nil intros for no-company job, got %v", intros)
	}
}

// TestMatchIntros_NoFalseMatch: different company names do not match.
func TestMatchIntros_NoFalseMatch(t *testing.T) {
	job := makeIntroJob(t,"Alpha Corp")
	conns := []domain.Connection{
		makeConn("Eve", "Echo", "Beta Corp", time.Time{}),
		makeConn("Frank", "Foxtrot", "Gamma Inc", time.Time{}),
	}

	intros := usecase.MatchIntros(job, conns)
	if len(intros) != 0 {
		t.Errorf("expected 0 intros, got %d: %v", len(intros), intros)
	}
}

// TestMatchIntros_RecencyOrdering: intros are sorted by ConnectedOn descending
// (most-recent first).
func TestMatchIntros_RecencyOrdering(t *testing.T) {
	job := makeIntroJob(t,"SameCo")
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	conns := []domain.Connection{
		makeConn("Old", "Pal", "SameCo", older),
		makeConn("New", "Pal", "SameCo", newer),
	}

	intros := usecase.MatchIntros(job, conns)
	if len(intros) != 2 {
		t.Fatalf("len(intros) = %d; want 2", len(intros))
	}
	if !intros[0].ConnectedOn.Equal(newer) {
		t.Errorf("intros[0].ConnectedOn = %v; want %v (most recent first)", intros[0].ConnectedOn, newer)
	}
	if !intros[1].ConnectedOn.Equal(older) {
		t.Errorf("intros[1].ConnectedOn = %v; want %v", intros[1].ConnectedOn, older)
	}
}

// TestMatchIntros_ZeroConnectedOnToleratedInSort: connections with zero
// ConnectedOn are included and sort after dated connections.
func TestMatchIntros_ZeroConnectedOnToleratedInSort(t *testing.T) {
	job := makeIntroJob(t,"SameCo")
	known := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	conns := []domain.Connection{
		makeConn("Zero", "Date", "SameCo", time.Time{}),
		makeConn("Known", "Date", "SameCo", known),
	}

	intros := usecase.MatchIntros(job, conns)
	if len(intros) != 2 {
		t.Fatalf("len(intros) = %d; want 2", len(intros))
	}
	// The dated one should come first.
	if intros[0].Name != "Known Date" {
		t.Errorf("intros[0].Name = %q; want %q", intros[0].Name, "Known Date")
	}
}

// TestMatchIntros_IntroFields: verifies Name, ProfileURL, Company, Position,
// ConnectedOn, and MatchConfidence are set correctly.
func TestMatchIntros_IntroFields(t *testing.T) {
	job := makeIntroJob(t,"TechCo Inc")
	connectedOn := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	conn := domain.Connection{
		FirstName:   "Grace",
		LastName:    "Gamma",
		ProfileURL:  "https://www.linkedin.com/in/grace-gamma",
		Company:     "TechCo Inc",
		Position:    "Staff Engineer",
		ConnectedOn: connectedOn,
	}

	intros := usecase.MatchIntros(job, []domain.Connection{conn})
	if len(intros) != 1 {
		t.Fatalf("len(intros) = %d; want 1", len(intros))
	}
	got := intros[0]
	if got.Name != "Grace Gamma" {
		t.Errorf("Name = %q; want %q", got.Name, "Grace Gamma")
	}
	if got.ProfileURL != "https://www.linkedin.com/in/grace-gamma" {
		t.Errorf("ProfileURL = %q; want %q", got.ProfileURL, "https://www.linkedin.com/in/grace-gamma")
	}
	if got.Company != "TechCo Inc" {
		t.Errorf("Company = %q; want %q", got.Company, "TechCo Inc")
	}
	if got.Position != "Staff Engineer" {
		t.Errorf("Position = %q; want %q", got.Position, "Staff Engineer")
	}
	if !got.ConnectedOn.Equal(connectedOn) {
		t.Errorf("ConnectedOn = %v; want %v", got.ConnectedOn, connectedOn)
	}
	if got.MatchConfidence != "exact" {
		t.Errorf("MatchConfidence = %q; want %q", got.MatchConfidence, "exact")
	}
}

// TestMatchIntros_BlankConnectionCompany: connections with blank Company never
// match any job.
func TestMatchIntros_BlankConnectionCompany(t *testing.T) {
	job := makeIntroJob(t,"Acme")
	conn := makeConn("Henry", "Hotel", "", time.Time{})

	intros := usecase.MatchIntros(job, []domain.Connection{conn})
	if len(intros) != 0 {
		t.Errorf("expected 0 intros when connection has blank company, got %d", len(intros))
	}
}

// TestMatchIntros_AllLegalSuffixes: verifies all supported legal suffixes are
// stripped so cross-suffix matches work.
func TestMatchIntros_AllLegalSuffixes(t *testing.T) {
	suffixes := []struct {
		jobCompany  string
		connCompany string
	}{
		{"Foo Inc", "Foo"},
		{"Foo Ltd", "Foo"},
		{"Foo LLC", "Foo"},
		{"Foo GmbH", "Foo"},
		{"Foo BV", "Foo"},
		{"Foo AG", "Foo"},
		{"Foo Limited", "Foo"},
		{"Foo Co", "Foo"},
		{"Foo Corp", "Foo"},
		{"Foo PLC", "Foo"},
	}

	for _, tc := range suffixes {
		t.Run(tc.jobCompany+"→"+tc.connCompany, func(t *testing.T) {
			job := makeIntroJob(t,tc.jobCompany)
			conn := makeConn("Test", "User", tc.connCompany, time.Time{})
			intros := usecase.MatchIntros(job, []domain.Connection{conn})
			if len(intros) != 1 {
				t.Errorf("len(intros) = %d; want 1 (jobCompany=%q, connCompany=%q)",
					len(intros), tc.jobCompany, tc.connCompany)
			}
		})
	}
}

// TestMatchIntros_LeadingThe: "The Acme Corp" normalises to "acme" and matches
// "Acme".
func TestMatchIntros_LeadingThe(t *testing.T) {
	job := makeIntroJob(t,"The Acme Corp")
	conn := makeConn("Ivan", "Iota", "Acme", time.Time{})

	intros := usecase.MatchIntros(job, []domain.Connection{conn})
	if len(intros) != 1 {
		t.Fatalf("len(intros) = %d; want 1", len(intros))
	}
}

// TestMatchIntros_NilConns: nil connection slice returns nil intros.
func TestMatchIntros_NilConns(t *testing.T) {
	job := makeIntroJob(t, "SomeCo")
	intros := usecase.MatchIntros(job, nil)
	if intros != nil {
		t.Errorf("expected nil, got %v", intros)
	}
}

// TestMatchIntros_StableSortTieBreak: when two connections have the same
// ConnectedOn, ordering is deterministic (Name ascending) regardless of input
// slice order.
func TestMatchIntros_StableSortTieBreak(t *testing.T) {
	job := makeIntroJob(t, "SameCo")
	sameDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// Provide in reversed alphabetical order to confirm the sort is stable/
	// deterministic, not input-order-dependent.
	conns := []domain.Connection{
		makeConn("Zeta", "Z", "SameCo", sameDate),
		makeConn("Alpha", "A", "SameCo", sameDate),
		makeConn("Mu", "M", "SameCo", sameDate),
	}

	intros := usecase.MatchIntros(job, conns)
	if len(intros) != 3 {
		t.Fatalf("len(intros) = %d; want 3", len(intros))
	}
	// With equal ConnectedOn, tie-break is Name ascending.
	if intros[0].Name != "Alpha A" {
		t.Errorf("intros[0].Name = %q; want %q", intros[0].Name, "Alpha A")
	}
	if intros[1].Name != "Mu M" {
		t.Errorf("intros[1].Name = %q; want %q", intros[1].Name, "Mu M")
	}
	if intros[2].Name != "Zeta Z" {
		t.Errorf("intros[2].Name = %q; want %q", intros[2].Name, "Zeta Z")
	}
}
