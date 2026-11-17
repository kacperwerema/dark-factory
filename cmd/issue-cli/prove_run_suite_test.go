package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// makeSuiteFixture is makeProofFixture plus the issue's worktree on
// work/cli/sample holding a JavaScript project (no go.mod) whose test file
// declares it('TestCount') in a commit with "Proves: AC1 TestCount", and a
// ci: block running ciCmd. mem-guard reads as absent.
func makeSuiteFixture(t *testing.T, ciCmd string) (*tracker.Project, string, string) {
	t.Helper()
	proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
	root := filepath.Dir(proj.IssueDir)
	if ciCmd != "" {
		wf := readFile(t, proj.WorkflowFile) + "ci:\n  run:\n    - " + strconvQuote(ciCmd) + "\n"
		if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	worktree := filepath.Join(root, ".worktrees", "elsewhere")
	cmd := exec.Command("git", "-C", root, "worktree", "add", "-q", "-b", "work/cli/sample", worktree)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(worktree, "test"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitInWorktree(t, worktree, map[string]string{
		"package.json":       `{"name":"sample","scripts":{"test":"node --test"}}` + "\n",
		"count.js":           "export const count = () => 2\n",
		"test/count.test.js": "import { it } from 'node:test'\nit('TestCount', () => {})\n",
	}, "feat: count\n\nProves: AC1 TestCount")
	old := autostart.ReadMemory
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{} }
	t.Cleanup(func() { autostart.ReadMemory = old })
	return proj, issuePath, worktree
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A project without go.mod: prove --run runs its ci: command in the issue's
// worktree (found through git worktree list, not at .worktrees/<slug>) and
// posts the tests: comment naming the test it found in a test file.
func TestProveRunRunsTheProjectsCICommandWithoutGoMod(t *testing.T) {
	proj, issuePath, _ := makeSuiteFixture(t, "test -f test/count.test.js && echo suite ok")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err != nil {
		t.Fatalf("prove --run: %v\n%s", err, stdout.String())
	}
	out := stdout.String()
	assertContains(t, out, ": test -f test/count.test.js && echo suite ok ==")
	assertContains(t, out, "✓ TestCount (test/count.test.js)")
	assertContains(t, out, "AC1 [x] proven")
	comments, _ := tracker.LoadComments(issuePath)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Text, "tests: prove --run at ") || !strings.HasSuffix(comments[0].Text, ": test -f test/count.test.js && echo suite ok — 1 passed: TestCount") {
		t.Errorf("comments = %+v, want one tests: comment naming the ci: command and TestCount", comments)
	}
}

// A red ci: command posts nothing and shows its output.
func TestProveRunPostsNothingWhenTheCICommandFails(t *testing.T) {
	proj, issuePath, _ := makeSuiteFixture(t, "echo 'not ok 1 - TestCount'; exit 3")
	ctx, stdout, _ := newTestContext(proj, true)
	err := runProve(ctx, []string{"cli/sample", "--run", "--json"})
	if err == nil || !strings.Contains(err.Error(), "no tests: comment posted") {
		t.Fatalf("prove --run with a red ci: command = %v, want the not-posted error", err)
	}
	var out proveOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if out.Run == nil || out.Run.Posted || !out.Run.Suite || len(out.Run.Results) != 1 || out.Run.Results[0].Outcome != proof.RunFail {
		t.Fatalf("run = %+v, want TestCount failed, a suite run, not posted", out.Run)
	}
	got := strings.Join(out.Run.Results[0].Output, "\n")
	if !strings.Contains(got, "not ok 1 - TestCount") || !strings.Contains(got, "exit 3") {
		t.Errorf("output = %q, want the command's output and its exit code", got)
	}
	if comments, _ := tracker.LoadComments(issuePath); len(comments) != 0 {
		t.Errorf("comments = %+v, want none", comments)
	}
}

// A name no test file holds is not found, and the run posts nothing even
// when the suite is green: a green suite says nothing about a missing test.
func TestProveRunSuiteNeedsEachNameInATestFile(t *testing.T) {
	proj, issuePath, worktree := makeSuiteFixture(t, "true")
	commitInWorktree(t, worktree, map[string]string{"count.js": "export const count = () => 2 // TestTotal\n"}, "feat: total\n\nProves: AC2 TestTotal")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err == nil {
		t.Fatalf("prove --run with a name in no test file posted:\n%s", stdout.String())
	}
	assertContains(t, stdout.String(), "✗ TestTotal — not found: no tracked test file in the worktree has it as a whole word")
	if comments, _ := tracker.LoadComments(issuePath); len(comments) != 0 {
		t.Errorf("comments = %+v, want none", comments)
	}
}

// Neither go.mod nor a ci: command: refused, naming both ways out.
func TestProveRunWithoutGoModOrCICommand(t *testing.T) {
	proj, _, _ := makeSuiteFixture(t, "")
	ctx, _, _ := newTestContext(proj, false)
	err := runProve(ctx, []string{"cli/sample", "--run"})
	if err == nil || !strings.Contains(err.Error(), "has no go.mod and the project no ci: command. Add one, or run the tests yourself") {
		t.Errorf("prove --run without go.mod or ci: = %v, want the refusal naming ci:", err)
	}
}

// The proof's fix hint names prove --run in a project with a ci: command.
func TestStaleTestLinkHintNamesProveRunWithACICommand(t *testing.T) {
	proj, _, _ := makeSuiteFixture(t, "true")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--list"}); err != nil {
		t.Fatalf("prove --list: %v", err)
	}
	assertContains(t, stdout.String(), "— no tests: report\n      → issue-cli prove cli/sample --run (runs the named tests and posts the tests: comment)")
}
