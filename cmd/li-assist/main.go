package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

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
