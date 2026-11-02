package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The shipping → done dry run, run before the fast-forward, fails with the
// commit to add while no commit on the branch references the issue, and is
// ready once one has a Refs: line.
func TestShippingDryRunWantsACommitReferencingTheIssue(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	git("checkout", "-q", "-b", "work/cli/sample")
	git("commit", "-q", "--allow-empty", "-m", "feat: the change")
	git("checkout", "-q", "main")

	workflowPath := filepath.Join(dir, "workflow.yaml")
	write(workflowPath, `statuses:
  - name: "shipping"
  - name: "done"
transitions:
  - from: "shipping"
    to: "done"
    actions:
      - type: validate
        rule: "commits_reference_issue"
`)
	issuesDir := filepath.Join(dir, "issues")
	write(filepath.Join(issuesDir, "CLI", "sample.md"), "---\ntitle: \"sample\"\nstatus: \"shipping\"\nsystem: \"CLI\"\n---\n\nBody.\n")
	proj := &tracker.Project{Name: "test", Slug: "test", IssueDir: issuesDir, WorkflowFile: workflowPath}

	ctx, out, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"cli/sample", "--to", "done", "--dry-run"})
	if err == nil {
		t.Fatalf("dry run passed with no commit referencing the issue:\n%s", out)
	}
	got := out.String() + err.Error()
	assertContains(t, got, "no commit on work/cli/sample references the issue")
	assertContains(t, got, `git commit --allow-empty -m "chore: reference the issue" -m "Refs: cli/sample"`)

	git("checkout", "-q", "work/cli/sample")
	git("commit", "-q", "--allow-empty", "-m", "docs(changelog): entry", "-m", "Refs: cli/sample")
	git("checkout", "-q", "main")
	ctx, out, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done", "--dry-run"}); err != nil {
		t.Fatalf("dry run with a Refs: line: %v\n%s", err, out)
	}
	assertContains(t, out.String(), "✓ Ready: shipping → done")
}
