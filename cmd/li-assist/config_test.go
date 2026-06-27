package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -- appConfig load/save/clear tests -----------------------------------------

func TestLoadConfig_MissingFile_ReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "does-not-exist.json")

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg.HomeLocation != "" {
		t.Errorf("expected empty HomeLocation for missing file, got %q", cfg.HomeLocation)
	}
}

func TestLoadConfig_MalformedJSON_WarnsAndReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Capture stderr to verify warning is printed.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	cfg, err := loadConfig(path)

	_ = w.Close()
	os.Stderr = oldStderr
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	stderrOut := string(buf[:n])

	if err != nil {
		t.Fatalf("expected nil error for malformed JSON, got: %v", err)
	}
	if cfg.HomeLocation != "" {
		t.Errorf("expected empty HomeLocation for malformed JSON, got %q", cfg.HomeLocation)
	}
	if !strings.Contains(stderrOut, "WARNING") {
		t.Errorf("expected WARNING in stderr, got: %q", stderrOut)
	}
}

func TestLoadConfig_ValidFile_ReturnsValues(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	data := `{"home_location":"Aachen, Germany"}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HomeLocation != "Aachen, Germany" {
		t.Errorf("HomeLocation = %q; want %q", cfg.HomeLocation, "Aachen, Germany")
	}
}

func TestSaveConfig_WritesCorrectJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	cfg := appConfig{HomeLocation: "Berlin, Germany"}
	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got appConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.HomeLocation != "Berlin, Germany" {
		t.Errorf("HomeLocation = %q; want %q", got.HomeLocation, "Berlin, Germany")
	}

	// File must be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o; want 0600", info.Mode().Perm())
	}
}

func TestSaveConfig_CreatesDirectoryIfMissing(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "subdir", "config.json")

	cfg := appConfig{HomeLocation: "Munich"}
	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected file to exist at %s", path)
	}
}

func TestSaveConfig_ClearLocation(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	// Write initial value.
	if err := saveConfig(path, appConfig{HomeLocation: "Hamburg"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	// Clear it.
	if err := saveConfig(path, appConfig{HomeLocation: ""}); err != nil {
		t.Fatalf("saveConfig clear: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.HomeLocation != "" {
		t.Errorf("HomeLocation after clear = %q; want empty", cfg.HomeLocation)
	}
}

// -- defaultConfigPath --------------------------------------------------------

func TestDefaultConfigPath_NotEmpty(t *testing.T) {
	p := defaultConfigPath()
	if p == "" {
		t.Error("defaultConfigPath returned empty string")
	}
	if !strings.HasSuffix(p, "config.json") {
		t.Errorf("expected path to end with config.json, got %q", p)
	}
}

// -- connections_path config round-trip ---------------------------------------

func TestConnectionsPath_SetShowClear(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	// Set.
	want := "/home/user/Connections.csv"
	if err := saveConfig(path, appConfig{ConnectionsPath: want}); err != nil {
		t.Fatalf("saveConfig (set): %v", err)
	}

	// Show.
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig (show): %v", err)
	}
	if cfg.ConnectionsPath != want {
		t.Errorf("ConnectionsPath = %q; want %q", cfg.ConnectionsPath, want)
	}

	// Clear.
	cfg.ConnectionsPath = ""
	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("saveConfig (clear): %v", err)
	}
	cfg2, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig (after clear): %v", err)
	}
	if cfg2.ConnectionsPath != "" {
		t.Errorf("ConnectionsPath after clear = %q; want empty", cfg2.ConnectionsPath)
	}
}

func TestConnectionsPath_IndependentOfHomeLocation(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	// Save both fields.
	if err := saveConfig(path, appConfig{
		HomeLocation:    "Aachen, Germany",
		ConnectionsPath: "/tmp/Connections.csv",
	}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.HomeLocation != "Aachen, Germany" {
		t.Errorf("HomeLocation = %q; want %q", cfg.HomeLocation, "Aachen, Germany")
	}
	if cfg.ConnectionsPath != "/tmp/Connections.csv" {
		t.Errorf("ConnectionsPath = %q; want %q", cfg.ConnectionsPath, "/tmp/Connections.csv")
	}
}

// -- resolveConnectionsPath ---------------------------------------------------

func TestResolveConnectionsPath_FlagWins(t *testing.T) {
	t.Setenv("LI_ASSIST_CONNECTIONS_CSV", "/env/path.csv")
	cfg := appConfig{ConnectionsPath: "/config/path.csv"}
	got := resolveConnectionsPath("/flag/path.csv", cfg)
	if got != "/flag/path.csv" {
		t.Errorf("got %q; want flag path", got)
	}
}

func TestResolveConnectionsPath_EnvWinsOverConfig(t *testing.T) {
	t.Setenv("LI_ASSIST_CONNECTIONS_CSV", "/env/path.csv")
	cfg := appConfig{ConnectionsPath: "/config/path.csv"}
	got := resolveConnectionsPath("", cfg)
	if got != "/env/path.csv" {
		t.Errorf("got %q; want env path", got)
	}
}

func TestResolveConnectionsPath_ConfigWinsOverDefault(t *testing.T) {
	t.Setenv("LI_ASSIST_CONNECTIONS_CSV", "")
	cfg := appConfig{ConnectionsPath: "/config/path.csv"}
	got := resolveConnectionsPath("", cfg)
	if got != "/config/path.csv" {
		t.Errorf("got %q; want config path", got)
	}
}

func TestResolveConnectionsPath_DefaultFallback(t *testing.T) {
	t.Setenv("LI_ASSIST_CONNECTIONS_CSV", "")
	cfg := appConfig{}
	got := resolveConnectionsPath("", cfg)
	if got == "" {
		t.Error("expected non-empty default path")
	}
	if !strings.HasSuffix(got, "connections.csv") {
		t.Errorf("default path %q does not end with connections.csv", got)
	}
}

// -- isInsideGitRepo ----------------------------------------------------------

func TestIsInsideGitRepo_TempDir_False(t *testing.T) {
	// A temp dir should not be inside a git repo.
	tmp := t.TempDir()
	p := filepath.Join(tmp, "file.csv")
	if isInsideGitRepo(p) {
		t.Errorf("expected false for temp dir path %q", p)
	}
}

// TestIsInsideGitRepo_GitDir_True: a file beneath a directory that contains a
// .git DIRECTORY (the normal git repo case) must return true.
func TestIsInsideGitRepo_GitDir_True(t *testing.T) {
	tmp := t.TempDir()
	// Create a .git directory to simulate a repo root.
	gitDir := filepath.Join(tmp, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("Mkdir .git: %v", err)
	}
	// The target file is nested a couple of levels inside the repo.
	subDir := filepath.Join(tmp, "data", "exports")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	p := filepath.Join(subDir, "Connections.csv")
	if !isInsideGitRepo(p) {
		t.Errorf("expected true for path %q inside a git repo (with .git dir at %q)", p, gitDir)
	}
}

// TestIsInsideGitRepo_GitFile_True: a file beneath a directory that contains a
// .git FILE (the git worktree case) must also return true. This guards the PII
// warning from silently breaking when the user runs li-assist from a worktree.
func TestIsInsideGitRepo_GitFile_True(t *testing.T) {
	tmp := t.TempDir()
	// Create a .git FILE (not a directory) to simulate a worktree.
	gitFile := filepath.Join(tmp, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: ../.git/worktrees/wt1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile .git: %v", err)
	}
	p := filepath.Join(tmp, "Connections.csv")
	if !isInsideGitRepo(p) {
		t.Errorf("expected true for path %q inside a worktree (with .git file at %q)", p, gitFile)
	}
}
