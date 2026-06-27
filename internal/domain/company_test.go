package domain

import "testing"

func TestCompany_immutableAccessors(t *testing.T) {
	urn, _ := ParseURN("urn:li:fsd_company:99")
	c := NewCompany(urn, "Acme")
	if c.URN() != urn || c.Name() != "Acme" {
		t.Errorf("accessors mismatch")
	}
}
