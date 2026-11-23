package ci

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestAheadTextWording(t *testing.T) {
	for n, want := range map[int]string{
		0: "",
		1: "; main is 1 commit ahead of your branch's base: merge main before you debug a red test",
		3: "; main is 3 commits ahead of your branch's base: merge main before you debug a red test",
	} {
		if got := AheadText("main", n); got != want {
			t.Errorf("AheadText(main, %d) = %q, want %q", n, got, want)
		}
	}
}

// A worker's briefing says main moved past its branch's base: "main green
// at <sha>" is main's head, not the base the worker's tests ran on.
func TestAgentLineSaysHowFarMainIsAheadOfTheBranchBase(t *testing.T) {
	root := newRepo(t)
	run(t, root, "git", "config", "maintenance.auto", "false")
	run(t, root, "git", "config", "gc.auto", "0")
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, root, "git", "worktree", "add", "-q", "-b", "work/cli/x", wt, "main")
	wf := &tracker.WorkflowConfig{CI: &tracker.CIConfig{Run: []tracker.CICommand{{Cmd: "true"}}}}
	if err := WriteState(root, greenState(time.Now())); err != nil {
		t.Fatal(err)
	}

	if line := AgentLine(wt, wf); strings.Contains(line, "ahead") {
		t.Errorf("main at the branch's base: no ahead note, got:\n%s", line)
	}

	commit(t, root, "fix(x): one")
	commit(t, root, "fix(x): two")
	line := AgentLine(wt, wf)
	want := "Local CI: main green at 64be5a9 (1m ago); main is 2 commits ahead of your branch's base: merge main before you debug a red test"
	if line != want {
		t.Errorf("line:\n%s\nwant:\n%s", line, want)
	}
	if line := AgentLine(root, wf); strings.Contains(line, "ahead") {
		t.Errorf("on main itself: no ahead note, got:\n%s", line)
	}
}
