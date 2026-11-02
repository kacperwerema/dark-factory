package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func repoRootT(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestMakeUXTestAllRunsEverySession: `make ux-test ALL=1` passes the runner
// no -issue or -s selector, even in a dispatched worktree (where ISSUE
// defaults to the branch's slug) and even with ISSUE= or S= given.
func TestMakeUXTestAllRunsEverySession(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	makefile, err := os.ReadFile(filepath.Join(repoRootT(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), makefile, 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgSign=false", "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "work/ui/some-issue")
	git("add", "Makefile")
	git("commit", "-q", "-m", "a")
	uxTest := func(vars ...string) string {
		t.Helper()
		cmd := exec.Command("make", append([]string{"-n", "ux-test"}, vars...)...)
		cmd.Dir = root
		for _, kv := range os.Environ() {
			if k, _, _ := strings.Cut(kv, "="); k != "ISSUE" && k != "S" && k != "ALL" && k != "MAKEFLAGS" {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make -n ux-test %v: %v\n%s", vars, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out := uxTest(); !strings.Contains(out, "-issue ui/some-issue") {
		t.Fatalf("without ALL the branch's issue is the selector: %q", out)
	}
	for _, vars := range [][]string{{"ALL=1"}, {"ALL=1", "ISSUE=ui/other"}, {"ALL=1", "S=local-ci"}} {
		out := uxTest(vars...)
		if !strings.Contains(out, "playtest ux-test") || strings.Contains(out, "-issue") || strings.Contains(out, " -s ") {
			t.Errorf("make ux-test %v = %q, want the runner with no -issue/-s selector", vars, out)
		}
	}
}

// TestFailLinesFeedLocalCIFailures: the FAIL lines ux-test prints for failed
// steps are what workflow.yaml's slow-tier failures: regex reads, so a red
// nightly run names <session>/<step>.
func TestFailLinesFeedLocalCIFailures(t *testing.T) {
	res := &Results{Session: "updates", Steps: []*StepResult{
		{ID: "TP7", Title: "fine", Status: StatusPass},
		{ID: "TP8", Title: "the Install button opens the agent", Status: StatusFail},
		{ID: "TP9", Title: "judged", Status: StatusPending},
	}}
	lines := failLines(res)
	if len(lines) != 1 || lines[0] != "FAIL updates/TP8 the Install button opens the agent" {
		t.Fatalf("failLines = %q", lines)
	}
	wf, err := tracker.LoadWorkflow(filepath.Join(repoRootT(t), "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tier := wf.CI.NightlyTier()
	var found bool
	for _, c := range wf.CI.Run {
		if c.TierName() != tier || !strings.Contains(c.Cmd, "ux-test") {
			continue
		}
		found = true
		out := "== ux-test updates (ui/updates) ==\n✗ TP8 the Install button opens the agent\n" + strings.Join(lines, "\n") + "\n✗ ux-test failed\n"
		if got := c.FailureNames(out, 10); strings.Join(got, ",") != "updates/TP8" {
			t.Errorf("failures: %q reads %q, want updates/TP8", c.Failures, got)
		}
	}
	if !found {
		t.Errorf("workflow.yaml's nightly tier %q has no ux-test command", tier)
	}
}
