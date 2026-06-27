package domain

import "testing"

func TestPerson_immutableAccessors(t *testing.T) {
	urn, _ := ParseURN("urn:li:fsd_profile:abc")
	p := NewPerson(urn, "kevin-burns", "Kevin Burns", "Engineer", "Berlin")
	if p.URN() != urn {
		t.Errorf("URN mismatch")
	}
	if p.Slug() != "kevin-burns" {
		t.Errorf("Slug mismatch")
	}
	if p.DisplayName() != "Kevin Burns" {
		t.Errorf("DisplayName mismatch")
	}
}
