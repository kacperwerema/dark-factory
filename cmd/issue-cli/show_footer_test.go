package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Lint's show-footer check reads show's own lines: a heading or hint show
// changes must change there too, or a pasted footer goes unnamed. == Shared
// files == needs another branch, so TestIsShowFooterLine holds its form.
func TestShowOutputIsTheFooterLintReads(t *testing.T) {
	file := "---\ntitle: \"A\"\nstatus: \"idea\"\n---\n\nbody\n\n## Idea\n- [ ] box\n\n## Test Plan\n### Automated\nx\n"
	proj := typedProject(t, typedWorkflow, map[string]string{"a": file})
	if err := tracker.AddComment(filepath.Join(proj.IssueDir, "a.md"), 0, "a comment", "test"); err != nil {
		t.Fatal(err)
	}
	decide, out, _ := newTestContext(proj, false)
	if err := runDecide(decide, []string{"a", "--provisional", "--text", "a decision"}); err != nil {
		t.Fatalf("decide: %v\n%s", err, out)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{"a"}); err != nil {
		t.Fatalf("show: %v", err)
	}
	blocks := []string{"== Body", "== Checklist", "== Decisions in force", "== Test Plan", "== Comments", "Tick done boxes", "Box text wrong", "Disagree with one"}
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		for _, block := range blocks {
			if strings.HasPrefix(line, block) {
				seen[block] = true
				if !lint.IsShowFooterLine(line) {
					t.Errorf("show prints %q and lint's show-footer doesn't know it", line)
				}
			}
		}
	}
	for _, block := range blocks {
		if !seen[block] {
			t.Errorf("show printed no %q line, so this test no longer reaches it:\n%s", block, stdout)
		}
	}
}
