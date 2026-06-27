//go:build integration

// Package auth integration tests require a real Chrome binary and a live
// LinkedIn session. Gate on LI_ASSIST_INTEGRATION=1.
//
// Run with:
//
//	LI_ASSIST_INTEGRATION=1 go test -tags=integration ./internal/auth/ -v -run TestSession
package auth

import (
	"context"
	"os"
	"testing"
)

func TestSession_OpenAndClose(t *testing.T) {
	if os.Getenv("LI_ASSIST_INTEGRATION") != "1" {
		t.Skip("set LI_ASSIST_INTEGRATION=1 to run browser integration tests")
	}
	ctx := context.Background()
	sess, err := Open(ctx, true) // headless=true; assumes profile already logged in
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sess.Close()

	if sess.Context() == nil {
		t.Error("Context() returned nil")
	}
	t.Logf("profile dir: %s", sess.ProfileDir())
}

func TestSession_LoggedIn(t *testing.T) {
	if os.Getenv("LI_ASSIST_INTEGRATION") != "1" {
		t.Skip("set LI_ASSIST_INTEGRATION=1 to run browser integration tests")
	}
	ctx := context.Background()
	sess, err := Open(ctx, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sess.Close()

	if !sess.LoggedIn() {
		t.Error("LoggedIn returned false -- run 'li-assist auth login' first")
	}
}

func TestSession_CSRF(t *testing.T) {
	if os.Getenv("LI_ASSIST_INTEGRATION") != "1" {
		t.Skip("set LI_ASSIST_INTEGRATION=1 to run browser integration tests")
	}
	ctx := context.Background()
	sess, err := Open(ctx, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sess.Close()

	csrf, err := sess.CSRF()
	if err != nil {
		t.Fatalf("CSRF: %v (is the session logged in?)", err)
	}
	if csrf == "" {
		t.Error("CSRF returned empty string")
	}
	t.Logf("CSRF token (first 8 chars): %.8s...", csrf)
}
