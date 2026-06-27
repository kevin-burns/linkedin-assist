package main

import (
	"testing"
)

func TestResolveLocation_AnywhereFlag(t *testing.T) {
	loc, err := resolveLocation(true, "", "Aachen, Germany")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loc != "" {
		t.Errorf("with --anywhere, want empty location, got %q", loc)
	}
}

func TestResolveLocation_LocationFlagOverridesDefault(t *testing.T) {
	loc, err := resolveLocation(false, "Berlin", "Aachen, Germany")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loc != "Berlin" {
		t.Errorf("want %q, got %q", "Berlin", loc)
	}
}

func TestResolveLocation_FallsBackToHome(t *testing.T) {
	loc, err := resolveLocation(false, "", "Aachen, Germany")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loc != "Aachen, Germany" {
		t.Errorf("want %q, got %q", "Aachen, Germany", loc)
	}
}

func TestResolveLocation_NoHomeNoFlag(t *testing.T) {
	loc, err := resolveLocation(false, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loc != "" {
		t.Errorf("want empty location, got %q", loc)
	}
}

func TestResolveLocation_AnywhereAndLocationConflict(t *testing.T) {
	_, err := resolveLocation(true, "Berlin", "Aachen, Germany")
	if err == nil {
		t.Error("expected error when --anywhere and --location are both set, got nil")
	}
}
