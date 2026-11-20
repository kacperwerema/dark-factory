package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSharedDepsLine: the briefing names worktree deps --own only for a
// project whose main checkout has node_modules (its worktrees link it).
func TestSharedDepsLine(t *testing.T) {
	dir := t.TempDir()
	proj := &Project{Name: "p", Slug: "p", WorkDir: dir, IssueDir: filepath.Join(dir, "issues")}
	if got := SharedDepsLine(proj); got != "" {
		t.Fatalf("no node_modules: %q, want none", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := SharedDepsLine(proj); !strings.Contains(got, "issue-cli --project p worktree deps --own") {
		t.Fatalf("line %q lacks the --own command", got)
	}
	if !strings.Contains(WorktreePromptSection(proj, &Issue{Slug: "a/b"}, filepath.Join(dir, ".worktrees", "a", "b"), "work/a/b"), "worktree deps --own") {
		t.Error("the Worktree section lacks the shared node_modules line")
	}
}
