package voyager

import (
	jsonv1 "encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParse_Golden pins what parseJobsSearch and parseJobDetail produce
// from real Voyager responses. The golden file was written by the
// encoding/json (v1) implementation before the move to encoding/json/v2:
// v2 matches member names case-sensitively and rejects duplicate names, so
// a field that only decoded through v1's case-insensitive match would
// silently stay empty. Any such change shows up here as a golden diff.
//
// The golden values are serialised with v1 on purpose, so the snapshot
// format does not move when the code under test does.
//
// Corpus: the tracked fixtures under testdata/voyager/. Set
// VOYAGER_EXTRA_CORPUS to a directory of further captures (for example the
// gitignored _capture/ tree) and VOYAGER_EXTRA_GOLDEN to a golden path
// OUTSIDE the repo to check those too: captures hold private data and must
// not be committed, nor must anything derived from them.
//
// Regenerate with UPDATE_GOLDEN=1.
func TestParse_Golden(t *testing.T) {
	check(t, "testdata/voyager", filepath.Join("testdata", "parse.golden.json"), true)
	if dir, golden := os.Getenv("VOYAGER_EXTRA_CORPUS"), os.Getenv("VOYAGER_EXTRA_GOLDEN"); dir != "" && golden != "" {
		check(t, dir, golden, false)
	}
}

// parseResult holds %#v renderings, not the values: domain.Job keeps its
// fields unexported, so JSON would serialise every job as {} and the golden
// would pass whatever the parser did.
type parseResult struct {
	SearchJobs string `json:"search_jobs,omitempty"`
	SearchErr  string `json:"search_err,omitempty"`
	DetailJob  string `json:"detail_job,omitempty"`
	DetailErr  string `json:"detail_err,omitempty"`
}

func check(t *testing.T, root, goldenPath string, skipCapture bool) {
	t.Helper()
	got := map[string]parseResult{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// _capture/ is gitignored private data: never part of the tracked corpus.
		if d.IsDir() && d.Name() == "_capture" && skipCapture {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".golden.json") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var r parseResult
		if jobs, err := parseJobsSearch(body); err != nil {
			r.SearchErr = err.Error()
		} else {
			r.SearchJobs = fmt.Sprintf("%#v", jobs)
		}
		if job, err := parseJobDetail(body); err != nil {
			r.DetailErr = err.Error()
		} else {
			r.DetailJob = fmt.Sprintf("%#v", job)
		}
		rel, _ := filepath.Rel(root, p)
		got[filepath.ToSlash(rel)] = r
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatalf("no .json files under %s", root)
	}
	gotJSON, err := jsonv1.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, append(gotJSON, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d files)", goldenPath, len(got))
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 first): %v", err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(gotJSON)) {
		t.Fatalf("parse output differs from %s over %d files; rerun with UPDATE_GOLDEN=1 into a scratch path and diff", goldenPath, len(got))
	}
}
