package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/connections"
	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// resolveConnectionsPath returns the path to the Connections.csv file using
// the following precedence (highest to lowest):
//
//  1. flagVal     — the --connections flag value
//  2. env         — LI_ASSIST_CONNECTIONS_CSV environment variable
//  3. cfg         — connections_path in config.json
//  4. default     — ~/.config/li-assist/connections.csv
func resolveConnectionsPath(flagVal string, cfg appConfig) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("LI_ASSIST_CONNECTIONS_CSV"); v != "" {
		return v
	}
	if cfg.ConnectionsPath != "" {
		return cfg.ConnectionsPath
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "li-assist", "connections.csv")
}

// isInsideGitRepo returns true when path (or one of its parent directories)
// contains a .git entry (directory or file — the file form is used by git
// worktrees). This is a heuristic to warn users that their Connections.csv is
// inside a version-controlled tree and at risk of being committed. It walks up
// to the filesystem root; an error at any step is treated as "not inside a git
// repo" to avoid false positives.
func isInsideGitRepo(path string) bool {
	dir := filepath.Dir(filepath.Clean(path))
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			break
		}
		dir = parent
	}
	return false
}

// staleThreshold is the age beyond which we warn that a Connections.csv
// export may have stale employer data (a connection may have changed jobs).
const staleThreshold = 45 * 24 * time.Hour

// loadConnectionsForIntros loads the Connections.csv at connPath and returns
// the parsed connections plus a boolean indicating whether intros can be used.
//
// Failure modes (never fatal — always continue without intros):
//   - File missing: prints a friendly "how to export" note.
//   - Other load error: prints error to stderr.
//   - Malformed rows skipped: prints a one-line count note.
//   - File inside a git repo: prints a WARNING (only when the file exists).
//   - Export older than staleThreshold: prints a staleness WARNING.
func loadConnectionsForIntros(connPath string) ([]domain.Connection, bool) {
	// Guard: only warn about git-repo placement when the file exists.
	// If it doesn't exist, the file-not-found message below is sufficient.
	if _, statErr := os.Stat(connPath); statErr == nil && isInsideGitRepo(connPath) {
		fmt.Fprintf(os.Stderr,
			"WARNING: connections file %q appears to be inside a git repository "+
				"and should not be committed (it contains real people's names)\n",
			connPath,
		)
	}

	conns, skipped, exportedAt, err := connections.Load(connPath)
	if err != nil {
		if errors.Is(err, connections.ErrConnectionsFileNotFound) {
			fmt.Fprintln(os.Stderr,
				"intros: connections file not found -- to export: "+
					"LinkedIn → Settings → Data Privacy → Get a copy of your data → Connections")
		} else {
			fmt.Fprintf(os.Stderr, "intros: failed to load connections: %v; continuing without intros\n", err)
		}
		return nil, false
	}

	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "intros: skipped %d malformed row(s) in connections file\n", skipped)
	}

	// Compute age once to avoid a subtle race where two time.Since calls
	// straddle a second boundary and produce inconsistent day counts.
	if !exportedAt.IsZero() {
		age := time.Since(exportedAt)
		if age > staleThreshold {
			days := int(age.Hours() / 24)
			fmt.Fprintf(os.Stderr,
				"WARNING: connections export is %d days old; intros may be stale "+
					"(a connection may have changed employer)\n",
				days,
			)
		}
	}

	return conns, true
}
