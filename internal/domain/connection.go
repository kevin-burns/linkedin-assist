package domain

import "time"

// Connection is a single 1st-degree LinkedIn connection parsed from the
// official Connections.csv data export.
//
// Email is intentionally absent: it is ~97% blank, is PII, and is not needed
// for any v1 feature. It must never be added to this struct.
type Connection struct {
	FirstName   string
	LastName    string
	ProfileURL  string
	Company     string
	Position    string
	ConnectedOn time.Time // zero when the Connected On field is absent or unparseable
}

// Intro is a match result produced by MatchIntros: a 1st-degree connection
// who appears to work at the same company as a job.
//
// MatchConfidence is always "exact" in v1; the field is included so callers
// can filter or display confidence without a breaking change when stronger /
// fuzzy matching is added.
//
// Deliberately deferred (v1 out of scope):
//   - Seniority / role-family ranking (requires offline precision eval).
//   - Fuzzy company matching.
//   - LLM "why this person" annotation.
type Intro struct {
	Name            string    // "First Last"
	ProfileURL      string
	Company         string    // the connection's raw Company field
	Position        string
	ConnectedOn     time.Time // zero when absent
	MatchConfidence string    // "exact" in v1
}
