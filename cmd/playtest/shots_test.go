package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadmeShotList checks the README's shot list without a browser.
func TestReadmeShotList(t *testing.T) {
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	shots, err := LoadShots(filepath.Join(repo, "docs", "images", "shots.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range shots {
		if _, err := os.Stat(filepath.Join(repo, "docs", "images", s.File)); err != nil {
			t.Errorf("%s is listed but not in docs/images: run make readme-shots", s.File)
		}
	}
}

func TestLoadShotsRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"empty":      "[]",
		"no png":     "- {file: a.jpg, path: /}",
		"a path":     "- {file: ../a.png, path: /}",
		"twice":      "- {file: a.png, path: /}\n- {file: a.png, path: /x}",
		"bad page":   "- {file: a.png, path: p/demo}",
		"not a list": "file: a.png",
	} {
		path := filepath.Join(t.TempDir(), "shots.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadShots(path); err == nil {
			t.Errorf("%s: LoadShots accepted %q", name, strings.TrimSpace(body))
		}
	}
}
