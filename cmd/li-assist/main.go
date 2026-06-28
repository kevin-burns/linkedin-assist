package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"
)

// Build-stamped by GoReleaser via -ldflags -X. Release binaries carry a precise
// version+commit+date; other builds keep the dev defaults below.
var (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	// `go install <module>@vX.Y.Z` builds from source and does NOT apply
	// GoReleaser's -X ldflags, so `version` would stay "0.0.0-dev". Fall back to
	// the module version Go embeds in the build info (e.g. "v0.1.2") so installed
	// builds still report something meaningful. "(devel)" — a plain `go build` in
	// a checkout — keeps the dev default.
	if version == "0.0.0-dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "li-assist",
		Short: "Personal LinkedIn CLI",
		Version: fmt.Sprintf("%s (commit=%s, built=%s)",
			version, commit, date),
	}
	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newJobsCmd())
	cmd.AddCommand(newDoctorCmd())
	return cmd
}

func main() {
	// Propagate SIGINT / SIGTERM so in-flight voyager fetches and Chrome
	// sessions are cancelled cleanly rather than killed by the OS.
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
