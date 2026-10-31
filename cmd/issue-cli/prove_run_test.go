package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/autostart"
	"github.com/michal-franc/issue-viewer/internal/proof"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// makeRunFixture is makeProofFixture plus the issue's worktree on
// work/cli/sample: a Go module whose test commit carries
// "Proves: AC1 TestCount". mem-guard reads as absent.
func makeRunFixture(t *testing.T) (*tracker.Project, string, string) {
	t.Helper()
	proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
	root := filepath.Dir(proj.IssueDir)
	worktree := filepath.Join(root, ".worktrees", "cli", "sample")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "worktree", "add", "-q", "-b", "work/cli/sample", worktree)
	files := map[string]string{
		"go.mod":        "module sample\n\ngo 1.21\n",
		"count.go":      "package sample\n\nfunc Count() int { return 2 }\n",
		"count_test.go": "package sample\n\nimport \"testing\"\n\nfunc TestCount(t *testing.T) {\n\tif Count() != 2 {\n\t\tt.Fatal(\"count\")\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(worktree, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(worktree, "add", ".")
	git(worktree, "commit", "-q", "-m", "feat: count\n\nProves: AC1 TestCount")
	old := autostart.ReadMemory
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{} }
	t.Cleanup(func() { autostart.ReadMemory = old })
	return proj, issuePath, worktree
}

func stubGoTest(t *testing.T, events string) *[]string {
	t.Helper()
	var calls []string
	old := goTestRunner
	goTestRunner = func(dir string, args []string) ([]byte, []byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte(events), nil, nil
	}
	t.Cleanup(func() { goTestRunner = old })
	return &calls
}

func TestProveRunPostsTheTestsCommentWhenAllPass(t *testing.T) {
	proj, issuePath, _ := makeRunFixture(t)
	calls := stubGoTest(t, `{"Action":"run","Package":"sample","Test":"TestCount"}
{"Action":"pass","Package":"sample","Test":"TestCount"}
`)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err != nil {
		t.Fatalf("prove --run: %v\n%s", err, stdout.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "test -json -run ^(TestCount)$ ." {
		t.Errorf("go calls = %q, want one go test -run ^(TestCount)$ .", *calls)
	}
	out := stdout.String()
	assertContains(t, out, "== Test run at ")
	assertContains(t, out, "✓ TestCount (.)")
	assertContains(t, out, "✓ Posted to cli/sample: tests: prove --run at ")
	assertContains(t, out, "✓ test TestCount (trailer on ")
	assertContains(t, out, "— reported after the last change")
	assertContains(t, out, "AC1 [x] proven")

	comments, _ := tracker.LoadComments(issuePath)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Text, "tests: prove --run at ") || !strings.HasSuffix(comments[0].Text, "— 1 passed: TestCount") {
		t.Errorf("comments = %+v, want one tests: comment naming TestCount", comments)
	}
}

func TestProveRunPostsNothingWhenATestFails(t *testing.T) {
	proj, issuePath, _ := makeRunFixture(t)
	stubGoTest(t, `{"Action":"run","Package":"sample","Test":"TestCount"}
{"Action":"output","Package":"sample","Test":"TestCount","Output":"    count_test.go:7: count\n"}
{"Action":"fail","Package":"sample","Test":"TestCount"}
`)
	ctx, stdout, _ := newTestContext(proj, true)
	err := runProve(ctx, []string{"cli/sample", "--run", "--json"})
	if err == nil || !strings.Contains(err.Error(), "no tests: comment posted") {
		t.Fatalf("prove --run with a failing test = %v, want the not-posted error", err)
	}
	var out proveOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if out.Run == nil || out.Run.Posted || len(out.Run.Results) != 1 || out.Run.Results[0].Outcome != proof.RunFail || !strings.Contains(strings.Join(out.Run.Results[0].Output, "\n"), "count_test.go:7") {
		t.Errorf("run = %+v, want TestCount failed with its output, not posted", out.Run)
	}
	if comments, _ := tracker.LoadComments(issuePath); len(comments) != 0 {
		t.Errorf("comments = %+v, want none", comments)
	}
}

func TestProveRunRefusals(t *testing.T) {
	proj, _, worktree := makeRunFixture(t)
	stubGoTest(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cli/sample", "AC1", "--run"}, "use it alone"},
		{[]string{"cli/sample", "--run", "--value", "x"}, "use it alone"},
	} {
		ctx, _, _ := newTestContext(proj, false)
		if err := runProve(ctx, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("prove %v = %v, want %q", tc.args, err, tc.want)
		}
	}

	// A changed doc is fine; changed code is refused.
	if err := os.WriteFile(filepath.Join(worktree, "NOTES.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "count.go"), []byte("package sample\n\nfunc Count() int { return 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err == nil || !strings.Contains(err.Error(), "uncommitted code in the worktree (count.go)") {
		t.Errorf("prove --run with changed code = %v, want the uncommitted-code refusal", err)
	}

	old := autostart.ReadMemory
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Note: "BLOCKED: memory used 80%"} }
	defer func() { autostart.ReadMemory = old }()
	if err := os.WriteFile(filepath.Join(worktree, "count.go"), []byte("package sample\n\nfunc Count() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err == nil || !strings.Contains(err.Error(), "mem-guard says no test run now (BLOCKED: memory used 80%)") {
		t.Errorf("prove --run under mem-guard BLOCKED = %v, want the mem-guard refusal", err)
	}

	if err := os.Remove(filepath.Join(worktree, "go.mod")); err != nil {
		t.Fatal(err)
	}
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{} }
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err == nil || !strings.Contains(err.Error(), "has no go.mod") {
		t.Errorf("prove --run without go.mod = %v, want the not-a-Go-module refusal", err)
	}
}

func TestProveRunWithoutTestLinks(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	ctx, _, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err == nil || !strings.Contains(err.Error(), "has no test links to run") {
		t.Errorf("prove --run without test links = %v, want the no-test-links error", err)
	}
}

// The real go test, once: the tiny module builds in a second or two.
func TestProveRunRunsGoTestForReal(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH")
	}
	proj, issuePath, _ := makeRunFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err != nil {
		t.Fatalf("prove --run: %v\n%s", err, stdout.String())
	}
	assertContains(t, stdout.String(), "✓ TestCount (.)")
	if comments, _ := tracker.LoadComments(issuePath); len(comments) != 1 {
		t.Errorf("comments = %+v, want the tests: comment", comments)
	}
}

func TestStaleTestLinkHintNamesProveRun(t *testing.T) {
	proj, _, _ := makeRunFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--list"}); err != nil {
		t.Fatalf("prove --list: %v", err)
	}
	assertContains(t, stdout.String(), "— no tests: report\n      → issue-cli prove cli/sample --run (runs the named tests and posts the tests: comment)")
}

// commitInWorktree writes files in the worktree and commits them with msg.
func commitInWorktree(t *testing.T, worktree string, files map[string]string, msg string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(worktree, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// An older commit's trailer names TestCount; a later commit renamed it to
// TestCountCard and says so in its own trailer. Old commits can't be
// reworded, so the old name is superseded: shown, skipped, and the run
// posts when the current tests pass.
func TestProveRunSkipsATestALaterTrailerSuperseded(t *testing.T) {
	proj, issuePath, worktree := makeRunFixture(t)
	commitInWorktree(t, worktree, map[string]string{
		"count_test.go": "package sample\n\nimport \"testing\"\n\nfunc TestCountCard(t *testing.T) {\n\tif Count() != 2 {\n\t\tt.Fatal(\"count\")\n\t}\n}\n",
	}, "test: rename after D-1\n\nProves: AC1 TestCountCard")
	calls := stubGoTest(t, `{"Action":"run","Package":"sample","Test":"TestCountCard"}
{"Action":"pass","Package":"sample","Test":"TestCountCard"}
`)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err != nil {
		t.Fatalf("prove --run: %v\n%s", err, stdout.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "test -json -run ^(TestCountCard)$ ." {
		t.Errorf("go calls = %q, want only TestCountCard run", *calls)
	}
	out := stdout.String()
	assertContains(t, out, "✓ TestCountCard (.)")
	assertContains(t, out, "– TestCount — superseded by TestCountCard (trailer on ")
	assertContains(t, out, "✗ test TestCount (trailer on ")
	assertContains(t, out, ") — superseded by TestCountCard (trailer on ")
	assertContains(t, out, "✓ Posted to cli/sample: tests: prove --run at ")
	comments, _ := tracker.LoadComments(issuePath)
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "— 1 passed: TestCountCard; 1 superseded: TestCount") {
		t.Errorf("comments = %+v, want one tests: comment naming TestCountCard and the superseded TestCount", comments)
	}
}

// The newest trailer naming a test that isn't there is a mistake, not a
// rename: an older found test doesn't excuse it.
func TestProveRunStillBlocksOnTheNewestTrailersMissingTest(t *testing.T) {
	proj, issuePath, worktree := makeRunFixture(t)
	commitInWorktree(t, worktree, map[string]string{"NOTES.md": "x"}, "docs: notes\n\nProves: AC1 TestCountTypo")
	stubGoTest(t, `{"Action":"run","Package":"sample","Test":"TestCount"}
{"Action":"pass","Package":"sample","Test":"TestCount"}
`)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "--run"}); err == nil || !strings.Contains(err.Error(), "no tests: comment posted") {
		t.Fatalf("prove --run = %v, want the not-posted error\n%s", err, stdout.String())
	}
	assertContains(t, stdout.String(), "✗ TestCountTypo — not found")
	if comments, _ := tracker.LoadComments(issuePath); len(comments) != 0 {
		t.Errorf("comments = %+v, want none", comments)
	}
}
