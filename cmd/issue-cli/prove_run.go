package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/autostart"
	"github.com/michal-franc/issue-viewer/internal/proof"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// goTestRunner runs go with args in dir and returns its stdout (go test
// -json events) and stderr. A var so tests need not run go.
var goTestRunner = func(dir string, args []string) (stdout, stderr []byte, err error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	return out, []byte(errBuf.String()), err
}

// runProveTests runs the tests the issue's test links name in its worktree
// and, when every one passed, posts the tests: comment that makes those
// links pass. It refuses what would make the report wrong: no worktree, not
// a Go module, uncommitted code, or mem-guard saying no.
func runProveTests(ctx *Context, issue *tracker.Issue) (*proof.TestRun, error) {
	root := projectRoot(ctx.Project)
	comments, _ := tracker.LoadComments(issue.FilePath)
	names := proof.TestNames(proof.Collect(root, issue, comments))
	manual := fmt.Sprintf("issue-cli comment %s --text \"tests: ...\"", issue.Slug)
	if len(names) == 0 {
		return nil, fmt.Errorf("%s has no test links to run: add a `Proves: AC1 <TestName>` trailer on the commit that adds the test (or issue-cli prove %s AC1 --test <TestName>), then run prove --run again", issue.Slug, issue.Slug)
	}
	worktree := filepath.Join(root, ".worktrees", issue.Slug)
	if st, err := os.Stat(worktree); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("no worktree at %s: prove --run runs the tests in the issue's worktree. Run them yourself, then: %s", worktree, manual)
	}
	if !proof.IsGoModule(worktree) {
		return nil, fmt.Errorf("prove --run runs go test, and %s has no go.mod. Run the tests yourself, then: %s", worktree, manual)
	}
	dirty, err := proof.DirtyCode(worktree)
	if err != nil {
		return nil, fmt.Errorf("git status in %s: %w", worktree, err)
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("uncommitted code in the worktree (%s): commit it first, then run prove --run (a code commit after the tests: comment makes it stale)", strings.Join(dirty, ", "))
	}
	run := proof.PlanGoRun(worktree, names)
	run.Head = proof.ShortHead(worktree)
	if len(run.Packages) == 0 {
		return &run, nil
	}
	if mem := autostart.ReadMemory(); mem.Found && !mem.CanStart {
		return nil, fmt.Errorf("mem-guard says no test run now (%s); try again when memory is lower", mem.Note)
	}
	var tops []string
	for _, res := range run.Results {
		if t, _, _ := strings.Cut(res.Name, "/"); res.Outcome == proof.RunNotRun && !containsStr(tops, t) {
			tops = append(tops, t)
		}
	}
	stdout, stderr, err := goTestRunner(worktree, proof.GoTestArgs(tops, run.Packages))
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return nil, fmt.Errorf("go test: %w", err)
	}
	proof.ReadGoTestJSON(&run, stdout)
	for i, res := range run.Results {
		if res.Outcome == proof.RunNotRun && len(res.Output) == 0 {
			run.Results[i].Output = lastLines(string(stderr), 20)
		}
	}
	if run.Passed() {
		if err := tracker.AddCommentBy(issue.FilePath, 0, run.Comment(), "cli", commentAuthor(ctx.Project, issue)); err != nil {
			return nil, fmt.Errorf("posting the tests: comment: %w", err)
		}
		run.Posted = true
	}
	return &run, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// writeTestRun prints what prove --run ran, each name's outcome and whether
// it posted the tests: comment.
func writeTestRun(w io.Writer, slug string, run *proof.TestRun) {
	at := ""
	if run.Head != "" {
		at = " at " + run.Head
	}
	if len(run.Packages) > 0 {
		fmt.Fprintf(w, "== Test run%s: %s ==\n", at, run.Command)
	} else {
		fmt.Fprintf(w, "== Test run%s: nothing to run ==\n", at)
	}
	for _, res := range run.Results {
		switch res.Outcome {
		case proof.RunPass:
			fmt.Fprintf(w, "✓ %s (%s)\n", res.Name, res.Package)
		case proof.RunNotFound:
			fmt.Fprintf(w, "✗ %s — not found: no tracked _test.go in the worktree declares it as a Go test (Test…, Example… or Fuzz…)\n", res.Name)
		default:
			fmt.Fprintf(w, "✗ %s (%s) — %s\n", res.Name, res.Package, res.Outcome)
			for _, l := range res.Output {
				fmt.Fprintf(w, "    %s\n", l)
			}
		}
	}
	if run.Posted {
		fmt.Fprintf(w, "✓ Posted to %s: %s\n", slug, run.Comment())
		return
	}
	failed := 0
	for _, res := range run.Results {
		if res.Outcome != proof.RunPass {
			failed++
		}
	}
	fmt.Fprintf(w, "✗ No tests: comment posted: %d of %d named tests did not pass. Fix them (or the trailer's test name) and run prove --run again\n", failed, len(run.Results))
}
