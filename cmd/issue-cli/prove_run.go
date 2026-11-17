package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
// links pass: go test -run in a Go module, else the project's ci: commands.
// It refuses what would make the report wrong: no worktree, neither a Go
// module nor a ci: command, uncommitted code, or mem-guard saying no.
func runProveTests(ctx *Context, issue *tracker.Issue) (*proof.TestRun, error) {
	root := projectRoot(ctx.Project)
	comments, _ := tracker.LoadComments(issue.FilePath)
	report := proof.Collect(root, issue, comments)
	names := proof.TestNames(report)
	manual := fmt.Sprintf("issue-cli comment %s --text \"tests: ...\"", issue.Slug)
	if len(names) == 0 {
		return nil, fmt.Errorf("%s has no test links to run: add a `Proves: AC1 <TestName>` trailer on the commit that adds the test (or issue-cli prove %s AC1 --test <TestName>), then run prove --run again", issue.Slug, issue.Slug)
	}
	stats, _ := tracker.LoadStats(issue.FilePath)
	at := proof.FindIssueRef(root, issue, stats.Transitions)
	worktree := at.Worktree
	if st, err := os.Stat(worktree); err != nil || !st.IsDir() {
		why := "no branch with a worktree found"
		if at.Miss != "" {
			why = at.Miss
		} else if tracker.BranchHead(root, at.Name) != "" {
			why = at.Name + " has no worktree (git worktree add " + filepath.Join(root, ".worktrees", issue.Slug) + " " + at.Name + ")"
		}
		return nil, fmt.Errorf("no worktree at %s: prove --run runs the tests in the issue's worktree.\n%s\nOr run them yourself, then: %s", worktree, why, manual)
	}
	// Without a go.mod the project's own tests run: its default-tier ci:
	// commands, as local CI runs them.
	var suite *tracker.CIConfig
	if !proof.IsGoModule(worktree) {
		if wf := ctx.Project.LoadWorkflow(); len(wf.CI.Commands("")) > 0 {
			suite = wf.CI
		} else {
			return nil, fmt.Errorf("prove --run runs go test in a Go module, else the project's ci: commands (workflow.yaml ci: run:), and %s has no go.mod and the project no ci: command. Add one, or run the tests yourself, then: %s", worktree, manual)
		}
	}
	dirty, err := proof.DirtyCode(worktree)
	if err != nil {
		return nil, fmt.Errorf("git status in %s: %w", worktree, err)
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("uncommitted code in the worktree (%s): commit it first, then run prove --run (a code commit after the tests: comment makes it stale)", strings.Join(dirty, ", "))
	}
	var run proof.TestRun
	if suite != nil {
		run = proof.PlanSuiteReportRun(worktree, report, suiteCommand(suite))
	} else {
		run = proof.PlanReportRun(worktree, report)
	}
	run.Head = proof.ShortHead(worktree)
	if len(run.Packages) == 0 {
		return &run, nil
	}
	if mem := autostart.ReadMemory(); mem.Found && !mem.CanStart {
		return nil, fmt.Errorf("mem-guard says no test run now (%s); try again when memory is lower", mem.Note)
	}
	if suite != nil {
		if err := runSuite(ctx, worktree, suite, &run); err != nil {
			return nil, err
		}
		return postIfPassed(ctx, issue, &run)
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
	return postIfPassed(ctx, issue, &run)
}

// postIfPassed posts the tests: comment when every named test passed.
func postIfPassed(ctx *Context, issue *tracker.Issue, run *proof.TestRun) (*proof.TestRun, error) {
	if run.Passed() {
		if err := tracker.AddCommentBy(issue.FilePath, 0, run.Comment(), "cli", commentAuthor(ctx.Project, issue)); err != nil {
			return nil, fmt.Errorf("posting the tests: comment: %w", err)
		}
		run.Posted = true
	}
	return run, nil
}

// suiteCommand is how a run of the project's default-tier ci: commands is
// named in its output and tests: comment.
func suiteCommand(c *tracker.CIConfig) string {
	var cmds []string
	for _, cmd := range c.Commands("") {
		cmds = append(cmds, cmd.Cmd)
	}
	return strings.Join(cmds, " && ")
}

// runSuite runs the default-tier ci: commands in order in the worktree, as
// local CI runs them (sh -c, its nice level, timeout and environment), and
// records the outcome on run: green when every command exited 0, else each
// named test fails with the output's tail. The first red command stops it.
func runSuite(ctx *Context, worktree string, c *tracker.CIConfig, run *proof.TestRun) error {
	results, err := os.MkdirTemp("", "prove-run-results-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(results)
	limit := c.TimeoutDuration()
	cctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	fmt.Fprintf(ctx.Stderr, "prove --run: no go.mod, running the project's ci: commands in %s (up to %s)\n", worktree, limit)
	var out bytes.Buffer
	passed := true
	for _, cmd := range c.Commands("") {
		fmt.Fprintf(&out, "$ %s\n", cmd.Cmd)
		exit, timedOut, startErr := ci.RunCommand(cctx, worktree, cmd.Cmd, c.NiceLevel(), results, &out)
		if startErr != nil {
			return fmt.Errorf("running %q: %w", cmd.Cmd, startErr)
		}
		if timedOut {
			fmt.Fprintf(&out, "%s: timed out after %s (ci: timeout)\n", cmd.Cmd, limit)
		} else if exit != 0 {
			fmt.Fprintf(&out, "%s: exit %d\n", cmd.Cmd, exit)
		}
		if exit != 0 || timedOut {
			passed = false
			break
		}
	}
	run.SuiteDone(passed, lastLines(out.String(), 20))
	return nil
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
		case proof.RunSuperseded:
			fmt.Fprintf(w, "– %s — superseded by %s: not in the worktree, only an older trailer names it; skipped\n", res.Name, res.SupersededBy)
		case proof.RunNotFound:
			if run.Suite {
				fmt.Fprintf(w, "✗ %s — not found: no tracked test file in the worktree has it as a whole word\n", res.Name)
				break
			}
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
		if res.Outcome != proof.RunPass && res.Outcome != proof.RunSuperseded {
			failed++
		}
	}
	fmt.Fprintf(w, "✗ No tests: comment posted: %d of %d named tests did not pass. Fix them (or the trailer's test name) and run prove --run again\n", failed, len(run.Results))
}
