package agentrun

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/restrace"
)

// TestWorktreeStartAndStopAreTraced: with DF_TRACE=resources a dispatch's
// worktree writes a start line when git adds it and a stop line when it is
// removed, so doctor leaks sees nothing open; with tracing off neither does.
func TestWorktreeStartAndStopAreTraced(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(restrace.EnvVar, "resources")
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := RunGitWorktreeAdd(repo, "work/x", wt, ""); err != nil {
		t.Fatalf("worktree add: %v %s", err, out)
	}
	if err := RemoveTree(repo, wt); err != nil {
		t.Fatal(err)
	}
	ev := restrace.Read(restrace.Path())
	if len(ev) != 2 || ev[0].Event != restrace.EventStart || ev[0].Kind != restrace.KindWorktree || ev[0].ID != wt || ev[1].Event != restrace.EventStop {
		t.Fatalf("events = %+v, want a worktree start and stop for %s", ev, wt)
	}
	if open := restrace.Open(ev); len(open) != 0 {
		t.Fatalf("open after remove: %+v", open)
	}
}
