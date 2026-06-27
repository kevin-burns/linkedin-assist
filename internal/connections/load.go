// Package connections parses the official LinkedIn Connections.csv data export.
// It may import only internal/domain and stdlib.
package connections

import (
	"bufio"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kevin-burns/linkedin-assist/internal/domain"
)

// ErrConnectionsFileNotFound is returned by Load when the file does not exist.
// Callers can detect it with errors.Is to print a friendly "how to export" note.
var ErrConnectionsFileNotFound = errors.New("connections file not found")

// connectedOnLayout is the Go time layout for LinkedIn's "DD Mon YYYY" format
// (e.g. "19 Jun 2026"). The day is always zero-padded.
const connectedOnLayout = "02 Jan 2006"

// utf8BOM is the UTF-8 byte order mark. Some LinkedIn exports are re-saved via
// Excel which prepends a BOM; stripping it lets the "First Name," header prefix
// detection work correctly on those files.
const utf8BOM = "\xEF\xBB\xBF"

// Load parses a LinkedIn Connections.csv export and returns the connections it
// contains.
//
// Design decisions:
//   - Preamble detection: the real file has a variable-length preamble before
//     the header. We detect the header as the first line starting with "First Name,"
//     (case-sensitive, as LinkedIn uses). Hardcoding "skip N lines" is fragile.
//   - Column mapping: columns are mapped by name so re-ordered exports don't
//     silently corrupt data.
//   - Email dropped: Email Address is never read into any struct (data minimization;
//     it is ~97% blank and is PII).
//   - Malformed rows: rows with fewer columns than the header are skipped and
//     counted; they never cause an error.
//   - exportedAt: set to the file's modification time (the CSV has no export-date
//     column). Callers use it to warn when the export is stale.
//
// Returns ErrConnectionsFileNotFound when the file does not exist.
func Load(path string) (conns []domain.Connection, skipped int, exportedAt time.Time, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, 0, time.Time{}, ErrConnectionsFileNotFound
		}
		return nil, 0, time.Time{}, statErr
	}
	exportedAt = info.ModTime()

	f, openErr := os.Open(path) //nolint:gosec // path comes from resolved user config, not raw input
	if openErr != nil {
		return nil, 0, time.Time{}, openErr
	}
	defer func() { _ = f.Close() }()

	// --- Phase 1: locate the header line ---
	// Scan lines until we find one starting with "First Name,"; feed the
	// remaining content (header + data) to encoding/csv.
	scanner := bufio.NewScanner(f)
	var headerLine string
	var afterHeader strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		// Strip a leading UTF-8 BOM if present (e.g. files re-saved via Excel).
		line = strings.TrimPrefix(line, utf8BOM)
		if strings.HasPrefix(line, "First Name,") {
			headerLine = line
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, exportedAt, err
	}
	if headerLine == "" {
		// No recognisable header found — return empty, no error.
		return nil, 0, exportedAt, nil
	}

	// Collect lines after the header into a buffer for the CSV reader.
	afterHeader.WriteString(headerLine + "\n")
	for scanner.Scan() {
		afterHeader.WriteString(scanner.Text() + "\n")
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, exportedAt, err
	}

	// --- Phase 2: parse with encoding/csv ---
	r := csv.NewReader(strings.NewReader(afterHeader.String()))
	r.FieldsPerRecord = -1 // allow variable field counts (we skip short rows)
	r.LazyQuotes = true    // tolerate imperfect quoting in real exports

	// Read the header row to build the column-name → index map.
	header, csvErr := r.Read()
	if csvErr != nil {
		return nil, 0, exportedAt, csvErr
	}
	colIdx := make(map[string]int, len(header))
	for i, name := range header {
		colIdx[strings.TrimSpace(name)] = i
	}

	// Required columns (Email Address is intentionally excluded).
	requiredCols := []string{"First Name", "Last Name", "URL", "Company", "Position", "Connected On"}
	for _, col := range requiredCols {
		if _, ok := colIdx[col]; !ok {
			// Missing a required column in the header — return empty.
			return nil, 0, exportedAt, nil
		}
	}

	idxFirst := colIdx["First Name"]
	idxLast := colIdx["Last Name"]
	idxURL := colIdx["URL"]
	idxCompany := colIdx["Company"]
	idxPosition := colIdx["Position"]
	idxConnected := colIdx["Connected On"]
	minCols := maxIdx(idxFirst, idxLast, idxURL, idxCompany, idxPosition, idxConnected) + 1

	// Read data rows.
	for {
		row, readErr := r.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			// csv parse error on this row — skip and count.
			skipped++
			continue
		}
		if len(row) < minCols {
			skipped++
			continue
		}

		conn := domain.Connection{
			FirstName:  strings.TrimSpace(row[idxFirst]),
			LastName:   strings.TrimSpace(row[idxLast]),
			ProfileURL: strings.TrimSpace(row[idxURL]),
			Company:    strings.TrimSpace(row[idxCompany]),
			Position:   strings.TrimSpace(row[idxPosition]),
			// Email is intentionally not read.
		}

		if raw := strings.TrimSpace(row[idxConnected]); raw != "" {
			if t, parseErr := time.Parse(connectedOnLayout, raw); parseErr == nil {
				conn.ConnectedOn = t.UTC()
			}
			// Unparseable Connected On: leave zero (tolerate without crash).
		}

		conns = append(conns, conn)
	}

	return conns, skipped, exportedAt, nil
}

// maxIdx returns the maximum of the given indices.
func maxIdx(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
