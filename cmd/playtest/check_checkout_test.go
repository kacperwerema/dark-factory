package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/liveboard"
	"github.com/kacperwerema/dark-factory/internal/standin"
)

// checkout is a temp repo with a board, a linked worktree of it with no
// issues/ (as a dispatched worker has) and a session that names one step
// of the board's only issue.
type checkout struct {
	primary, worktree string
	sessions          string
}

func newCheckout(t *testing.T, stepID string) checkout {
	t.Helper()
	root := t.TempDir()
	c := checkout{primary: filepath.Join(root, "engine"), sessions: filepath.Join(root, "sessions")}
	c.worktree = filepath.Join(c.primary, ".worktrees", "x")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustMkdir(t, c.primary)
	mustWrite(t, filepath.Join(c.primary, "go.mod"), "module x\n")
	git(root, "init", "-q", c.primary)
	git(c.primary, "add", "go.mod")
	git(c.primary, "commit", "-q", "-m", "init")
	git(c.primary, "worktree", "add", "-q", c.worktree)
	// On the primary checkout only, untracked, as the real board is.
	writeBoard(t, filepath.Join(c.primary, "issues"), 1)
	mustMkdir(t, c.sessions)
	mustWrite(t, filepath.Join(c.sessions, "s.yaml"), fmt.Sprintf(`session: s
issue: cli/real
scenario: basic
steps:
  - id: %s
    title: a step
    checks: [{ visible: board.search }]
`, stepID))
	t.Setenv("PLAYTEST_PROJECT", "")
	t.Setenv("ISSUE_VIEWER_PROJECT", "")
	t.Setenv(liveboard.Env, "")
	t.Setenv("PLAYTEST_ISSUE_CLI", filepath.Join(root, "no-such-cli"))
	return c
}

// writeBoard writes one issue with boxes test plan boxes, TP1 to TPn: ids
// go by position.
func writeBoard(t *testing.T, dir string, boxes int) {
	t.Helper()
	mustMkdir(t, filepath.Join(dir, "CLI"))
	mustWrite(t, filepath.Join(dir, "CLI", "real.md"), "---\ntitle: \"Real\"\nstatus: \"in progress\"\n---\n\n## Test Plan\n\n"+strings.Repeat("- [ ] a box\n", boxes))
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// check runs playtest check from dir, the way make does.
func (c checkout) check(t *testing.T, scenarios, dir string, extra ...string) (int, string, string) {
	t.Helper()
	t.Chdir(dir)
	var out, errw strings.Builder
	args := append([]string{"check", "-sessions", c.sessions, "-scenarios", scenarios}, extra...)
	code := run(args, &out, &errw)
	return code, out.String(), errw.String()
}

// A linked worktree has no issues/, and with no issue-cli to name the
// project the board is still the primary checkout's: a stale step id fails,
// a real one passes with no note.
func TestCheckInAWorktreeReadsThePrimaryBoard(t *testing.T) {
	scenarios := filepath.Join(repoDir(t), "playtest", "scenarios")

	stale := newCheckout(t, "TP7")
	code, out, errw := stale.check(t, scenarios, stale.worktree)
	if code == 0 || !strings.Contains(errw, `step id "TP7" is not a checkbox in cli/real`) {
		t.Fatalf("stale id from the worktree: code %d\nout: %s\nerr: %s", code, out, errw)
	}

	real := newCheckout(t, "TP1")
	code, out, errw = real.check(t, scenarios, real.worktree)
	if code != 0 || strings.Contains(out, "note:") {
		t.Fatalf("real id from the worktree: code %d\nout: %s\nerr: %s", code, out, errw)
	}
}

// issue-cli run in a folder with an issues/ answers that folder as its one
// project, so asked from the primary checkout it never lists the
// configured one. The stand-in does the same: a project named from there
// must still be found.
func TestCheckInThePrimaryCheckoutReadsItsBoard(t *testing.T) {
	scenarios := filepath.Join(repoDir(t), "playtest", "scenarios")
	c := newCheckout(t, "TP7")
	stub := filepath.Join(t.TempDir(), "issue-cli")
	t.Setenv("STUB_ISSUES", filepath.Join(c.primary, "issues"))
	standin.Write(t, stub, `if [ -d issues ]; then
  printf '[{"slug":"engine-folder","issue_dir":"./issues"}]\n'
else
  printf '[{"slug":"engine","issue_dir":"%s"}]\n' "$STUB_ISSUES"
fi
`)
	t.Setenv("PLAYTEST_ISSUE_CLI", stub)

	for name, env := range map[string]string{"nothing named": "", "named": "engine"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PLAYTEST_PROJECT", env)
			code, out, errw := c.check(t, scenarios, c.primary)
			if code == 0 || !strings.Contains(errw, `step id "TP7" is not a checkbox in cli/real`) {
				t.Fatalf("code %d\nout: %s\nerr: %s", code, out, errw)
			}
		})
	}
}

// LIVE_BOARD wins over the repo's board and over a project that lists
// another one, as it does for tests.
func TestCheckLiveBoardOverrides(t *testing.T) {
	scenarios := filepath.Join(repoDir(t), "playtest", "scenarios")
	c := newCheckout(t, "TP7") // no TP7 on the repo's board
	other := filepath.Join(t.TempDir(), "other")
	writeBoard(t, other, 7)
	t.Setenv(liveboard.Env, other)

	code, out, errw := c.check(t, scenarios, c.worktree)
	if code != 0 || strings.Contains(out, "note:") {
		t.Fatalf("from the worktree: code %d\nout: %s\nerr: %s", code, out, errw)
	}

	stub := filepath.Join(t.TempDir(), "issue-cli")
	t.Setenv("STUB_ISSUES", filepath.Join(c.primary, "issues"))
	standin.Write(t, stub, "printf '[{\"slug\":\"engine\",\"issue_dir\":\"%s\"}]\\n' \"$STUB_ISSUES\"\n")
	t.Setenv("PLAYTEST_ISSUE_CLI", stub)
	t.Setenv("PLAYTEST_PROJECT", "engine")
	code, out, errw = c.check(t, scenarios, c.worktree)
	if code != 0 || strings.Contains(out, "note:") {
		t.Fatalf("over a listed project: code %d\nout: %s\nerr: %s", code, out, errw)
	}

	// A LIVE_BOARD with no board in it says so; it doesn't fall back.
	t.Setenv(liveboard.Env, filepath.Join(t.TempDir(), "empty"))
	code, out, errw = c.check(t, scenarios, c.worktree)
	if code != 0 || !strings.Contains(out, "no board read") {
		t.Fatalf("empty override: code %d\nout: %s\nerr: %s", code, out, errw)
	}
}

// A project named but not listed reads no other board: the note says so,
// even with the repo's board right there.
func TestCheckNamedProjectNeverFallsBack(t *testing.T) {
	scenarios := filepath.Join(repoDir(t), "playtest", "scenarios")
	c := newCheckout(t, "TP7")
	code, out, errw := c.check(t, scenarios, c.worktree, "-project", "nowhere")
	if code != 0 || !strings.Contains(out, `no board read for project "nowhere"`) {
		t.Fatalf("code %d\nout: %s\nerr: %s", code, out, errw)
	}
}
