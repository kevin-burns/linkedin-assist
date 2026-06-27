package domain

import (
	"errors"
	"testing"
)

func TestParseURN_validJob(t *testing.T) {
	u, err := ParseURN("urn:li:job:1234567890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := u.Entity(), "job"; got != want {
		t.Errorf("Entity() = %q, want %q", got, want)
	}
	if got, want := u.ID(), "1234567890"; got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}
}

func TestParseURN_validCompany(t *testing.T) {
	u, err := ParseURN("urn:li:fsd_company:1441")
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Entity(); got != "fsd_company" {
		t.Errorf("Entity() = %q", got)
	}
}

func TestParseURN_compoundID(t *testing.T) {
	// Real voyager URNs carry colons/parens in the id segment (compound keys,
	// nested URNs). The id is everything after "urn:li:<entity>:".
	cases := []struct {
		in     string
		entity string
		id     string
	}{
		{"urn:li:fsd_jobPosting:(123456,456)", "fsd_jobPosting", "(123456,456)"},
		{"urn:li:activity:(urn:li:organization:123,456)", "activity", "(urn:li:organization:123,456)"},
	}
	for _, c := range cases {
		u, err := ParseURN(c.in)
		if err != nil {
			t.Fatalf("ParseURN(%q) unexpected error: %v", c.in, err)
		}
		if got := u.Entity(); got != c.entity {
			t.Errorf("ParseURN(%q).Entity() = %q, want %q", c.in, got, c.entity)
		}
		if got := u.ID(); got != c.id {
			t.Errorf("ParseURN(%q).ID() = %q, want %q", c.in, got, c.id)
		}
	}
}

func TestParseURN_invalid(t *testing.T) {
	cases := []string{
		"",
		"not-a-urn",
		"urn:li:",
		"urn:li:job",
		"urn:li:job:",
		"urn:foo:job:123",
	}
	for _, c := range cases {
		_, err := ParseURN(c)
		if !errors.Is(err, ErrInvalidURN) {
			t.Errorf("ParseURN(%q) error = %v, want ErrInvalidURN", c, err)
		}
	}
}
