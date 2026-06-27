package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExcludedCompanies_ParsesFile(t *testing.T) {
	content := `# This is a comment
Deloitte
  Amazon Web Services (AWS)

# Another comment
McKinsey
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "excluded-companies.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	got, err := loadExcludedCompanies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"Deloitte", "Amazon Web Services (AWS)", "McKinsey"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d; want %d; got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}

func TestLoadExcludedCompanies_MissingFile_ReturnsNilNil(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "does-not-exist.txt")

	got, err := loadExcludedCompanies(path)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil slice for missing file, got: %v", got)
	}
}

func TestLoadExcludedCompanies_BlankLinesAndComments(t *testing.T) {
	content := `
# start comment

# mid comment

`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "excluded-companies.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	got, err := loadExcludedCompanies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice for file with only blanks/comments; got %v", got)
	}
}

func TestLoadExcludedCompanies_TrimsWhitespace(t *testing.T) {
	content := "  Acme Corp  \n\t Widgets Inc \t\n"
	tmp := t.TempDir()
	path := filepath.Join(tmp, "excluded-companies.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	got, err := loadExcludedCompanies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"Acme Corp", "Widgets Inc"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d; want %d; got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}
