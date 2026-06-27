package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommandShowsVersion(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--version"})
	cmd.SetOut(buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), version) {
		t.Fatalf("version string %q not found in output: %q", version, buf.String())
	}
}

var _ = cobra.Command{} // ensure cobra import alive
