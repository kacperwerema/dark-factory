package viewer

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/testids"
)

// TestTemplateTestIDs keeps templates and internal/testids in step: every
// data-testid a template declares must be listed, and every listed id must
// be used by some template. Playtest sessions validate their targets
// against the list, so drift here would let a session point at nothing.
func TestTemplateTestIDs(t *testing.T) {
	files, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	var used []string
	for _, f := range files {
		src, err := fs.ReadFile(templateFS, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range testids.ScanTemplate(string(src)) {
			used = append(used, id)
			if !testids.Known(id) {
				t.Errorf("%s: data-testid %q is not listed in internal/testids/ (one file per page)", f, id)
			}
		}
	}
	for _, entry := range testids.IDs {
		found := false
		for _, id := range used {
			if testids.Compatible(entry, id) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("internal/testids lists %q but no template uses it", entry)
		}
	}
}

// TestFixtureTestIDs does the same for testids.FixtureIDs against the
// scenario project pages (playtest/scenarios/*/root/**/*.html), which
// sessions check by opening the raw page.
func TestFixtureTestIDs(t *testing.T) {
	var used []string
	err := filepath.WalkDir(repoPath("playtest", "scenarios"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") || !strings.Contains(filepath.ToSlash(path), "/root/") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, id := range testids.ScanTemplate(string(src)) {
			used = append(used, id)
			if !slices.Contains(testids.FixtureIDs, id) {
				t.Errorf("%s: data-testid %q is not listed in testids.FixtureIDs", path, id)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range testids.FixtureIDs {
		if !slices.Contains(used, entry) {
			t.Errorf("testids.FixtureIDs lists %q but no scenario page uses it", entry)
		}
	}
}

// TestStubTestIDs checks testids.StubIDs against the playtest stubs that
// draw them (setAttribute('data-testid', …) in cmd/playtest/assets/stubs).
func TestStubTestIDs(t *testing.T) {
	files, err := filepath.Glob(repoPath("cmd", "playtest", "assets", "stubs", "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no stubs: %v", err)
	}
	drawn := regexp.MustCompile(`setAttribute\('data-testid', '([^']+)'\)`)
	var used []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range drawn.FindAllStringSubmatch(string(src), -1) {
			used = append(used, m[1])
			if !slices.Contains(testids.StubIDs, m[1]) {
				t.Errorf("%s draws data-testid %q, not listed in testids.StubIDs", f, m[1])
			}
		}
	}
	for _, entry := range testids.StubIDs {
		if !slices.Contains(used, entry) {
			t.Errorf("testids.StubIDs lists %q but no stub draws it", entry)
		}
	}
}
