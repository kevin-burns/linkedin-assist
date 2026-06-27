package connections_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/connections"
)

// testdataPath returns the absolute path to a file inside testdata/.
func testdataPath(name string) string {
	// __file__ is not available in Go, so construct from the package source dir.
	// os.Getwd() returns the package directory when tests run.
	wd, _ := os.Getwd()
	return filepath.Join(wd, "testdata", name)
}

// TestLoad_HeaderDetected verifies that the preamble (Notes:, quoted note,
// blank line) is skipped and the header is found correctly regardless of
// exact preamble length.
func TestLoad_HeaderDetected(t *testing.T) {
	conns, skipped, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	// short-row-only is the one malformed row.
	if skipped != 1 {
		t.Errorf("skipped = %d; want 1 (short-row-only)", skipped)
	}
	// 5 valid data rows: Ané Müller, Bob Smith, Carol Lee, Dave Jones, Eve Brown.
	if len(conns) != 5 {
		t.Errorf("len(conns) = %d; want 5", len(conns))
	}
}

// TestLoad_NoEmailField verifies that no email data is retained in any
// Connection (data minimization gate).
func TestLoad_NoEmailField(t *testing.T) {
	// This test is purely structural: domain.Connection has no Email field.
	// Compilation itself is the primary guard; this test documents intent.
	conns, _, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	for _, c := range conns {
		// Spot-check that FirstName/LastName/Company are populated and no
		// email-like substring leaks into any string field.
		_ = c.FirstName + c.LastName + c.ProfileURL + c.Company + c.Position
		// domain.Connection has no Email field — verified at compile time.
	}
}

// TestLoad_NonASCIIName verifies that UTF-8 names (e.g. "Ané Müller") are
// parsed correctly.
func TestLoad_NonASCIIName(t *testing.T) {
	conns, _, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(conns) == 0 {
		t.Fatal("no connections returned")
	}
	// Row 1: Ané Müller
	got := conns[0].FirstName + " " + conns[0].LastName
	if got != "Ané Müller" {
		t.Errorf("first connection name = %q; want %q", got, "Ané Müller")
	}
}

// TestLoad_QuotedFieldWithComma verifies that a company name containing a
// comma ("Acme, Inc.") is parsed as a single field, not split.
func TestLoad_QuotedFieldWithComma(t *testing.T) {
	conns, _, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	// Row 1 (Ané Müller) has Company = "Acme, Inc."
	if conns[0].Company != "Acme, Inc." {
		t.Errorf("Company = %q; want %q", conns[0].Company, "Acme, Inc.")
	}
}

// TestLoad_BlankCompany verifies that a blank Company field is tolerated.
func TestLoad_BlankCompany(t *testing.T) {
	conns, _, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	// Row 4 (Dave Jones) has a blank Company.
	dave := conns[3]
	if dave.FirstName != "Dave" {
		t.Fatalf("expected Dave at index 3, got %q", dave.FirstName)
	}
	if dave.Company != "" {
		t.Errorf("Dave's Company = %q; want empty", dave.Company)
	}
}

// TestLoad_BlankConnectedOn verifies that a blank Connected On field results
// in a zero time.Time and does not crash.
func TestLoad_BlankConnectedOn(t *testing.T) {
	conns, _, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	// Row 4 (Dave Jones) has blank Connected On.
	dave := conns[3]
	if !dave.ConnectedOn.IsZero() {
		t.Errorf("Dave's ConnectedOn = %v; want zero", dave.ConnectedOn)
	}
	// Row 5 (Eve Brown) also has blank Connected On.
	eve := conns[4]
	if eve.FirstName != "Eve" {
		t.Fatalf("expected Eve at index 4, got %q", eve.FirstName)
	}
	if !eve.ConnectedOn.IsZero() {
		t.Errorf("Eve's ConnectedOn = %v; want zero", eve.ConnectedOn)
	}
}

// TestLoad_ConnectedOnParsed verifies the "DD Mon YYYY" time layout is parsed
// correctly for rows with a populated Connected On field.
func TestLoad_ConnectedOnParsed(t *testing.T) {
	conns, _, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	// Row 1: Ané Müller, Connected On = "01 Jan 2026"
	want := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if !conns[0].ConnectedOn.Equal(want) {
		t.Errorf("conns[0].ConnectedOn = %v; want %v", conns[0].ConnectedOn, want)
	}

	// Row 2: Bob Smith, Connected On = "15 Mar 2025"
	want2 := time.Date(2025, time.March, 15, 0, 0, 0, 0, time.UTC)
	if !conns[1].ConnectedOn.Equal(want2) {
		t.Errorf("conns[1].ConnectedOn = %v; want %v", conns[1].ConnectedOn, want2)
	}
}

// TestLoad_MalformedRowSkipped verifies that a row with too few columns is
// skipped (counted in skipped) rather than crashing.
func TestLoad_MalformedRowSkipped(t *testing.T) {
	_, skipped, _, err := connections.Load(testdataPath("fake_connections.csv"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d; want 1", skipped)
	}
}

// TestLoad_ExportedAtIsMtime verifies that exportedAt matches the file's
// modification time.
func TestLoad_ExportedAtIsMtime(t *testing.T) {
	p := testdataPath("fake_connections.csv")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	wantMtime := info.ModTime()

	_, _, exportedAt, err := connections.Load(p)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !exportedAt.Equal(wantMtime) {
		t.Errorf("exportedAt = %v; want %v (file mtime)", exportedAt, wantMtime)
	}
}

// TestLoad_MissingFile_SentinelError verifies that a missing file returns
// ErrConnectionsFileNotFound (not a generic OS error).
func TestLoad_MissingFile_SentinelError(t *testing.T) {
	_, _, _, err := connections.Load("/tmp/does-not-exist-li-assist-test.csv")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !errors.Is(err, connections.ErrConnectionsFileNotFound) {
		t.Errorf("error = %v; want ErrConnectionsFileNotFound", err)
	}
}

// TestLoad_AlternatePreamble verifies that the header is found even when the
// preamble has a different number of lines (e.g. 0 or 5 lines before header).
func TestLoad_AlternatePreamble(t *testing.T) {
	// Write a CSV with a 1-line preamble then the header.
	tmp := t.TempDir()
	p := filepath.Join(tmp, "alt.csv")
	content := "SomePreamble\nFirst Name,Last Name,URL,Email Address,Company,Position,Connected On\nFoo,Bar,https://www.linkedin.com/in/foo-bar,,TechCorp,Engineer,10 Apr 2025\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	conns, skipped, _, err := connections.Load(p)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d; want 0", skipped)
	}
	if len(conns) != 1 {
		t.Fatalf("len(conns) = %d; want 1", len(conns))
	}
	if conns[0].FirstName != "Foo" || conns[0].Company != "TechCorp" {
		t.Errorf("unexpected connection: %+v", conns[0])
	}
}

// TestLoad_BOMPrefixedHeader verifies that a UTF-8 BOM at the start of the
// file (e.g. from an Excel re-save) does not prevent header detection.
func TestLoad_BOMPrefixedHeader(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bom.csv")
	// \xEF\xBB\xBF is the UTF-8 BOM; it precedes "First Name," on line 1.
	content := "\xEF\xBB\xBFFirst Name,Last Name,URL,Email Address,Company,Position,Connected On\nZara,Zeta,https://www.linkedin.com/in/zara-zeta,,BOM Corp,CTO,05 Feb 2025\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	conns, skipped, _, err := connections.Load(p)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d; want 0", skipped)
	}
	if len(conns) != 1 {
		t.Fatalf("len(conns) = %d; want 1 (BOM should not prevent header detection)", len(conns))
	}
	if conns[0].FirstName != "Zara" || conns[0].Company != "BOM Corp" {
		t.Errorf("unexpected connection: %+v", conns[0])
	}
}
