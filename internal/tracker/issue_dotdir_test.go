package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

// The work files' logs and reports sit in dot folders under the issues
// dir (.goals, .outputs, .jobs, .reports): a markdown report there is
// never read as an issue.
func TestLoadIssuesSkipsDotFolders(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SEO/page.md", "---\ntitle: \"Page\"\n---\n")
	write(".reports/gtm/2026-W44.md", "---\ntitle: \"Not an issue\"\n---\n# GTM · week 2026-W44\n")
	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Slug != "seo/page" {
		t.Fatalf("issues = %d, want only seo/page", len(issues))
	}
}
