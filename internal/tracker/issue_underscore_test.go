package tracker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIssueTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// captureWarnings points the loader's warnings at a buffer for one test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := warnOut
	warnOut = &buf
	t.Cleanup(func() { warnOut = old })
	return &buf
}

// Evidence folders (_evidence/) under the issues dir hold READMEs and
// reports, with or without frontmatter: never issues, and no warning.
func TestLoadIssuesSkipsUnderscoreFolders(t *testing.T) {
	dir := t.TempDir()
	warnings := captureWarnings(t)
	writeIssueTree(t, dir, map[string]string{
		"Factory/real.md":                      "---\ntitle: \"Real\"\n---\n",
		"_evidence/company-floor-v2/README.md": "# Mockup notes, no frontmatter\n",
		"_evidence/lore/README.md":             "---\ntitle: Lore callouts evidence\n---\n",
		"Factory/_shots/notes.md":              "plain notes\n",
		".reports/gtm/2026-W44.md":             "# no frontmatter either\n",
	})
	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Slug != "factory/real" {
		var slugs []string
		for _, is := range issues {
			slugs = append(slugs, is.Slug)
		}
		t.Fatalf("issues = %v, want only factory/real", slugs)
	}
	if warnings.Len() != 0 {
		t.Errorf("warnings = %q, want none", warnings.String())
	}
}

// A stray markdown file without frontmatter outside those folders still
// warns, but once per file per process, however often the board loads.
func TestLoadIssuesWarnsOncePerFile(t *testing.T) {
	dir := t.TempDir()
	warnings := captureWarnings(t)
	writeIssueTree(t, dir, map[string]string{
		"Factory/real.md":  "---\ntitle: \"Real\"\n---\n",
		"Factory/stray.md": "no frontmatter\n",
	})
	for i := 0; i < 3; i++ {
		if _, err := LoadIssues(dir); err != nil {
			t.Fatal(err)
		}
	}
	got := warnings.String()
	if n := strings.Count(got, "warning: skipping"); n != 1 {
		t.Fatalf("stray.md warned %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "warning: skipping "+filepath.Join(dir, "Factory", "stray.md")) {
		t.Errorf("warning = %q, want the file's path", got)
	}
}
