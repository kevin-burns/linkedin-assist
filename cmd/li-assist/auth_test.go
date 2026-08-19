package main

import (
	"encoding/json"
	"testing"
)

// TestStatusJSONAlwaysCarriesAgeDays pins a field that disappeared in the
// wild. `age_days` was tagged `omitempty` on a float64, and the value is
// rounded to one decimal -- so every session younger than ~1.2h rounded to
// 0.0 and encoding/json dropped the key entirely. Observed 2026-08-19
// straight after `auth login`: a logged-in, perfectly healthy session
// reported no age_days at all, and a consumer computing time-to-re-auth
// from it silently computed nothing.
//
// A zero age is a real, common answer here, not an absent one.
func TestStatusJSONAlwaysCarriesAgeDays(t *testing.T) {
	blob, err := json.Marshal(statusJSON{LoggedIn: true, AgeDays: 0})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	age, ok := got["age_days"]
	if !ok {
		t.Fatalf("age_days missing for a freshly captured session; payload: %s", blob)
	}
	if age != float64(0) {
		t.Errorf("age_days = %v, want 0", age)
	}
}
