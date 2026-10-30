package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mdLink is a markdown link or image target: ](target) or ](target "title").
var mdLink = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// TestReadmeLinksResolve keeps the README from rotting: every relative link
// and image it names exists in the repo.
func TestReadmeLinksResolve(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mdLink.FindAllStringSubmatch(string(b), -1) {
		target := m[1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		target, _, _ = strings.Cut(target, "#")
		if _, err := os.Stat(target); err != nil {
			t.Errorf("README.md links %s, which does not exist (an image: run make readme-shots, or drop it)", m[1])
		}
	}
}

// TestDocImagesReferenced keeps docs/images/ to what the docs show: every
// file there is named by some markdown file (shots.yaml lists the README's
// screenshots and is not an image).
func TestDocImagesReferenced(t *testing.T) {
	var docs strings.Builder
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", ".playtest", "node_modules", "issues":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			docs.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	all := docs.String()
	images, err := os.ReadDir(filepath.Join("docs", "images"))
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range images {
		if img.IsDir() || img.Name() == "shots.yaml" {
			continue
		}
		if !strings.Contains(all, "images/"+img.Name()) {
			t.Errorf("docs/images/%s is not shown by any markdown file: delete it or link it", img.Name())
		}
	}
}
