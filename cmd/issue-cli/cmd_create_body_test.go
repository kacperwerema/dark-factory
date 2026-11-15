package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateWithBody: create takes the body in the same call (--body, or
// --body-file like append), as GTM's week-plan guidance installed before
// aea20652 still tells agents to (issue cli/gtm-weekly-plan-brief-and-…).
func TestCreateWithBody(t *testing.T) {
	proj := newTestProject(t)

	ctx, _, _ := testContext(proj)
	if err := runCreate(ctx, []string{"--title", "Inline body", "--body", "## Brief\\n\\nWhat and why."}); err != nil {
		t.Fatal(err)
	}
	got := readIssueFile(t, filepath.Join(proj.IssueDir, "inline-body.md"))
	if !strings.Contains(got, "---\n\n## Brief\n\nWhat and why.\n") {
		t.Fatalf("body missing or not right after the frontmatter:\n%s", got)
	}

	file := filepath.Join(t.TempDir(), "brief.md")
	mustWrite(t, file, "## Brief\n\nFrom a file, `quoted` $HOME.\n")
	ctx, _, _ = testContext(proj)
	if err := runCreate(ctx, []string{"--title", "File body", "--body-file", file}); err != nil {
		t.Fatal(err)
	}
	if got := readIssueFile(t, filepath.Join(proj.IssueDir, "file-body.md")); !strings.Contains(got, "From a file, `quoted` $HOME.") {
		t.Fatalf("--body-file text missing:\n%s", got)
	}

	ctx, _, _ = testContext(proj)
	ctx.Stdin = strings.NewReader("## Brief\n\nFrom stdin.\n")
	if err := runCreate(ctx, []string{"--title", "Stdin body", "--body-file", "-"}); err != nil {
		t.Fatal(err)
	}
	if got := readIssueFile(t, filepath.Join(proj.IssueDir, "stdin-body.md")); !strings.Contains(got, "From stdin.") {
		t.Fatalf("--body-file - text missing:\n%s", got)
	}

	ctx, _, _ = testContext(proj)
	err := runCreate(ctx, []string{"--title", "Both", "--body", "x", "--body-file", file})
	if err == nil || !strings.Contains(err.Error(), "together with --body-file") {
		t.Fatalf("--body with --body-file: want a refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(proj.IssueDir, "both.md")); statErr == nil {
		t.Fatal("refused create still wrote the issue")
	}
}

func readIssueFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
