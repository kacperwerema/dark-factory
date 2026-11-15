package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The call that lost a round in the post-mortem: a "## Clarify" block on an
// issue with a "### Clarify" pointer under Design is its own section, the one
// issue-cli clarify reads.
func TestAppendBodyFileReachesTheSectionAtItsLevel(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data := readFile(t, issuePath) + "\n## Design\nPlan\n\n### Clarify\nSee below\n\n## Notes\nN\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(t.TempDir(), "block.md")
	if err := os.WriteFile(block, []byte("## Clarify\n- first question\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, _, _ := newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug, "--body-file", block}); err != nil {
		t.Fatalf("append: %v", err)
	}
	body := issueBody(t, issuePath)
	found := false
	for _, s := range tracker.SplitSections(body) {
		if s.Title == "Clarify" {
			found = true
			if !strings.Contains(s.Text, "first question") {
				t.Errorf("## Clarify = %q, want the block", s.Text)
			}
		}
		if s.Title == "Design" && strings.Contains(s.Text, "first question") {
			t.Errorf("the block landed under Design:\n%s", s.Text)
		}
	}
	if !found {
		t.Fatalf("no ## Clarify section in\n%s", body)
	}

	// The same block again now finds its own section.
	if err := runAppend(ctx, []string{slug, "--body", "## Clarify\n- second question"}); err != nil {
		t.Fatalf("second append: %v", err)
	}
	body = issueBody(t, issuePath)
	if n := strings.Count(body, "## Clarify\n") - strings.Count(body, "### Clarify\n"); n != 1 {
		t.Fatalf("%d ## Clarify headings in\n%s", n, body)
	}
	if !strings.Contains(body, "- second question") {
		t.Fatalf("second block missing in\n%s", body)
	}
}
