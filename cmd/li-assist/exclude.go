package main

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// defaultExcludePath returns the default path for the company exclusion list:
// ~/.config/li-assist/excluded-companies.txt.
// If the home directory cannot be resolved (e.g. HOME unset), the path is
// returned as an empty string and callers should skip loading.
func defaultExcludePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "li-assist", "excluded-companies.txt")
}

// loadExcludedCompanies reads a company exclusion file and returns the list of
// company terms to exclude. The file format is one term per line; lines that
// are blank or start with '#' are ignored; leading and trailing whitespace is
// trimmed from each entry.
//
// A missing file is not an error -- the file is optional. Returns nil, nil
// when the file does not exist.
func loadExcludedCompanies(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var companies []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		companies = append(companies, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return companies, nil
}
