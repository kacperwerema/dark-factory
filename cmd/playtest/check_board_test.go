package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// checkBoard is a board, a session against it and a stand-in issue-cli
// that lists one project and appends each call, with the ISSUE_CLI_LOG it
// saw, to calls.
type checkBoard struct {
	sessions, scenarios string
	calls               string
	issue               string
}

func newCheckBoard(t *testing.T, stepID string) checkBoard {
	t.Helper()
	dir := t.TempDir()
	issues := filepath.Join(dir, "issues", "CLI")
	sessions := filepath.Join(dir, "sessions")
	for _, d := range []string{issues, sessions} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(issues, "real.md"), "---\ntitle: \"Real\"\nstatus: \"done\"\n---\n\n## Acceptance Criteria\n\n- [ ] AC1 — a criterion\n\n## Test Plan\n\n- [ ] TP1 — a box\n- [ ] TP2 — a box\n", 0o644)
	write(filepath.Join(sessions, "s.yaml"), fmt.Sprintf(`session: s
issue: cli/real
scenario: basic
steps:
  - id: %s
    title: a step
    checks: [{ visible: board.search }]
`, stepID), 0o644)

	b := checkBoard{sessions: sessions, scenarios: filepath.Join(repoDir(t), "playtest", "scenarios"), calls: filepath.Join(dir, "calls"), issue: filepath.Join(issues, "real.md")}
	stub := filepath.Join(dir, "issue-cli")
	// calls and issues/ sit beside the stand-in, so its body is the same
	// every run (internal/standin).
	standin.Write(t, stub, `dir=${0%/*}
echo "log=$ISSUE_CLI_LOG args=$*" >> "$dir/calls"
case "$*" in
*projects*) printf '[{"slug":"engine","issue_dir":"%s"}]\n' "$dir/issues" ;;
*) echo "Error: issue not found" >&2; exit 1 ;;
esac
`)
	t.Setenv("PLAYTEST_ISSUE_CLI", stub)
	t.Setenv("PLAYTEST_PROJECT", "")
	t.Setenv("ISSUE_VIEWER_PROJECT", "engine")
	t.Setenv("ISSUE_CLI_LOG", filepath.Join(dir, "worker.clilog"))
	return b
}

func (b checkBoard) check(t *testing.T, extra ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errw strings.Builder
	args := append([]string{"check", "-sessions", b.sessions, "-scenarios", b.scenarios}, extra...)
	code = run(args, &out, &errw)
	return code, out.String(), errw.String()
}

// setStatus changes the board's issue's status.
func (b checkBoard) setStatus(t *testing.T, status string) {
	t.Helper()
	body := strings.Replace(mustRead(t, b.issue), `status: "done"`, fmt.Sprintf("status: %q", status), 1)
	if err := os.WriteFile(b.issue, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeBoardSession replaces the board's session.
func writeBoardSession(t *testing.T, b checkBoard, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(b.sessions, "s.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (b checkBoard) readCalls(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(b.calls)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(raw)
}

// check takes its project from the same rule as report and reads that
// board: a stale step id fails it, a real one passes. Before, check asked
// for the old engine board's name and every id passed unread.
func TestCheckReadsTheProjectsBoard(t *testing.T) {
	stale := newCheckBoard(t, "TP7")
	code, out, errw := stale.check(t)
	if code == 0 || !strings.Contains(errw, `step id "TP7" is not a checkbox in cli/real`) {
		t.Fatalf("stale id: code %d\nout: %s\nerr: %s", code, out, errw)
	}

	real := newCheckBoard(t, "TP1")
	if code, out, errw := real.check(t); code != 0 || strings.Contains(out, "note:") {
		t.Fatalf("real id: code %d\nout: %s\nerr: %s", code, out, errw)
	}
}

// A project this machine doesn't have is said once, by name, and no lookup
// is tried: 111-235 failing children per run is what this bug was.
func TestCheckUnknownProjectSaysSoOnce(t *testing.T) {
	b := newCheckBoard(t, "TP7")
	code, out, errw := b.check(t, "-project", "nowhere")
	if code != 0 {
		t.Fatalf("shape-only run must pass: code %d\nerr: %s", code, errw)
	}
	var notes []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "note:") {
			notes = append(notes, l)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], `"nowhere"`) {
		t.Fatalf("want one note naming the project, got %q", notes)
	}
	if calls := b.readCalls(t); strings.Contains(calls, "checklist") {
		t.Fatalf("lookups tried on an unresolved project:\n%s", calls)
	}

	// No issue-cli at all reads the same: one line, a clean pass.
	t.Setenv("PLAYTEST_ISSUE_CLI", filepath.Join(t.TempDir(), "no-such-cli"))
	code, out, errw = b.check(t)
	if code != 0 || strings.Count(out, "note:") != 1 || !strings.Contains(out, `"engine"`) {
		t.Fatalf("no issue-cli: code %d\nout: %s\nerr: %s", code, out, errw)
	}
}

// Issues missing from the board are the sessions' history, not a failure
// to log: the board is read once, with no issue-cli checklist child per
// session, and the one child check starts doesn't get the worker's clilog.
func TestCheckChildrenLeaveTheClilogAlone(t *testing.T) {
	b := newCheckBoard(t, "TP1")
	second := strings.ReplaceAll(mustRead(t, filepath.Join(b.sessions, "s.yaml")), "cli/real", "cli/gone")
	second = strings.Replace(second, "session: s", "session: t", 1)
	if err := os.WriteFile(filepath.Join(b.sessions, "t.yaml"), []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errw := b.check(t, "-project", "engine")
	if code != 0 || !strings.Contains(out, "cli/gone not resolvable here") {
		t.Fatalf("code %d\nout: %s\nerr: %s", code, out, errw)
	}
	calls := b.readCalls(t)
	if strings.Contains(calls, "checklist") {
		t.Fatalf("a checklist child per session:\n%s", calls)
	}
	if !strings.Contains(calls, "projects") {
		t.Fatalf("the project list was not read:\n%s", calls)
	}
	for _, l := range strings.Split(strings.TrimSpace(calls), "\n") {
		if !strings.HasPrefix(l, "log= ") {
			t.Fatalf("a child saw ISSUE_CLI_LOG: %s", l)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
