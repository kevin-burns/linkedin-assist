//go:build integration

// Package voyager integration tests require a live LinkedIn browser session.
// Gate on LI_ASSIST_INTEGRATION=1 and a logged-in Chrome profile.
//
// Run with:
//
//	LI_ASSIST_INTEGRATION=1 go test -tags=integration ./internal/voyager/ -v -run TestTransport
package voyager

import (
	"context"
	"os"
	"testing"

	"github.com/kevin-burns/linkedin-assist/internal/auth"
)

func TestTransport_JobsSearch(t *testing.T) {
	if os.Getenv("LI_ASSIST_INTEGRATION") != "1" {
		t.Skip("set LI_ASSIST_INTEGRATION=1 to run browser integration tests")
	}
	ctx := context.Background()
	sess, err := auth.Open(ctx, true) // headless -- profile must already be logged in
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	defer sess.Close()

	if !sess.LoggedIn() {
		t.Fatal("session is not logged in -- run 'li-assist auth login' first")
	}

	transport := NewTransport(sess, nil)
	client := NewJobsClient(transport)

	jobs, err := client.Search(ctx, JobSearchParams{
		Keywords: "platform engineer",
		Location: "Berlin",
		Start:    0,
		Count:    25,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(jobs) == 0 {
		t.Error("Search returned 0 jobs")
	}
	t.Logf("Search returned %d jobs; first: %s", len(jobs), jobs[0].Title())
}
