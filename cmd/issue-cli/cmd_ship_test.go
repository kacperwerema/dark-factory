package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// shipFixture is cli/sample's project in a git repository whose main
// checkout is on main, with the issue's worktree on work/cli/sample one
// commit ahead and main one commit ahead of the cut.
func shipFixture(t *testing.T) (proj *tracker.Project, issuePath, root, tree string) {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(k, v)
	}
	proj, issuePath = makeGateFixture(t, "in progress", "", true)
	root = filepath.Dir(proj.IssueDir)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("issues/\n.worktrees/\nworkflow.yaml\n"), 0o644)
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	tree = filepath.Join(root, ".worktrees", "cli", "sample")
	git(t, root, "worktree", "add", "-q", "-b", "work/cli/sample", tree)
	os.WriteFile(filepath.Join(tree, "feature.txt"), []byte("f\n"), 0o644)
	git(t, tree, "add", "feature.txt")
	git(t, tree, "commit", "-q", "-m", "feat: the feature")
	os.WriteFile(filepath.Join(root, "other.txt"), []byte("o\n"), 0o644)
	git(t, root, "add", "other.txt")
	git(t, root, "commit", "-q", "-m", "feat: another branch landed")
	withCwd(t, tree)
	t.Setenv(tracker.OwnSlugEnv, "cli/sample")
	return proj, issuePath, root, tree
}

// gitLine is git's output with the trailing newline dropped.
func gitLine(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(git(t, dir, args...))
}

// AC1: ship merges main, runs the check, fast-forwards main, posts the
// shas on the issue and ends with the LANDED line.
func TestShipCommandLandsTheIssueBranch(t *testing.T) {
	proj, issuePath, root, tree := shipFixture(t)
	ctx, stdout, stderr := newTestContext(proj, false)
	if err := runShip(ctx, []string{"--check", "echo 'validate: PASS'"}); err != nil {
		t.Fatalf("ship: %v\n%s%s", err, stdout, stderr)
	}
	head := gitLine(t, tree, "rev-parse", "HEAD")
	if got := gitLine(t, root, "rev-parse", "main"); got != head {
		t.Fatalf("main at %s, branch at %s", got, head)
	}
	if subj := gitLine(t, tree, "log", "-1", "--format=%s"); subj != "chore(merge): main into cli/sample" {
		t.Errorf("merge subject %q", subj)
	}
	if got := lastLine(stdout.String()); got != "ship: LANDED "+head[:7] {
		t.Errorf("last line %q", got)
	}
	body := mustRead(t, issuePath)
	if !strings.Contains(string(body), "progress: ship: check PASS at "+head[:7]) {
		t.Errorf("no progress comment naming the check's sha:\n%s", body)
	}
}

// AC3: a refused fast-forward ends with NOT LANDED on the last stdout line
// and exit code 1; main stays where it was.
func TestShipCommandRefusedFastForwardExitsOne(t *testing.T) {
	proj, issuePath, root, _ := shipFixture(t)
	// An uncommitted feature.txt in the main checkout blocks the fast-forward.
	os.WriteFile(filepath.Join(root, "feature.txt"), []byte("dirty\n"), 0o644)
	git(t, root, "add", "feature.txt")
	before := gitLine(t, root, "rev-parse", "main")
	issueBefore := mustRead(t, issuePath)
	ctx, stdout, _ := newTestContext(proj, false)
	err := runShip(ctx, []string{"--check", "echo 'validate: PASS'"})
	var code *exitCodeError
	if !errors.As(err, &code) || code.Code != 1 {
		t.Fatalf("err = %v, want exit code 1", err)
	}
	if got := lastLine(stdout.String()); !strings.HasPrefix(got, "ship: NOT LANDED (ff refused: ") {
		t.Errorf("last line %q", got)
	}
	if got := gitLine(t, root, "rev-parse", "main"); got != before {
		t.Error("main moved")
	}
	if got := mustRead(t, issuePath); string(got) != string(issueBefore) {
		t.Error("a refused ship changed the issue")
	}
}

// AC4: --dry-run prints the plan and changes nothing.
func TestShipCommandDryRunChangesNothing(t *testing.T) {
	proj, _, root, tree := shipFixture(t)
	mainBefore, headBefore := gitLine(t, root, "rev-parse", "main"), gitLine(t, tree, "rev-parse", "HEAD")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runShip(ctx, []string{"--dry-run", "--check", "touch ran"}); err != nil {
		t.Fatalf("ship --dry-run: %v", err)
	}
	for _, want := range []string{"(dry run, nothing runs)", `merge main into work/cli/sample: "chore(merge): main into cli/sample"`, "check: touch ran"} {
		assertContains(t, stdout.String(), want)
	}
	if gitLine(t, root, "rev-parse", "main") != mainBefore || gitLine(t, tree, "rev-parse", "HEAD") != headBefore {
		t.Error("the dry run moved a branch")
	}
	if _, err := os.Stat(filepath.Join(tree, "ran")); err == nil {
		t.Error("the dry run ran the check")
	}
}
