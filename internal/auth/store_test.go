package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoad_roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")

	orig := Credentials{
		Cookies:    map[string]string{"li_at": "v1", "JSESSIONID": "v2"},
		Headers:    map[string]string{"user-agent": "test-ua"},
		CapturedAt: time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC),
	}

	if err := Save(orig, path); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if loaded.Cookies["li_at"] != "v1" {
		t.Errorf("cookie roundtrip mismatch")
	}
	if loaded.Headers["user-agent"] != "test-ua" {
		t.Errorf("header roundtrip mismatch")
	}
	if !loaded.CapturedAt.Equal(orig.CapturedAt) {
		t.Errorf("CapturedAt roundtrip mismatch")
	}
}

func TestSave_filePermissionsAre0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	c := Credentials{Cookies: map[string]string{"li_at": "x"}}
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}

func TestLoad_missingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/creds.json")
	if err == nil {
		t.Errorf("expected error for missing file")
	}
}
