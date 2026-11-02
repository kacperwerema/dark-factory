package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/checks"
)

// is_worker in the check library asks this issue-cli's lead whoami, the
// worker guard's own test: 0 for a worker, 1 for a clean shell.
func TestCheckLibIsWorkerAsksTheGuard(t *testing.T) {
	bash, err := checks.FindBash()
	if err != nil {
		t.Skip("no bash 4 or newer")
	}
	if testing.Short() {
		t.Skip("builds issue-cli")
	}
	// A real build: this test binary's init() drops a worker's identity
	// (lead_test.go), so it can't stand in for issue-cli here.
	dir := t.TempDir()
	cli := filepath.Join(dir, "issue-cli")
	if out, err := exec.Command("go", "build", "-o", cli, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, checks.Lib, 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "probe.sh")
	os.WriteFile(script, []byte("source \"$CHECKS_LIB\"\nis_worker\necho \"RC=$?\"\n"), 0o755)
	root := t.TempDir() // not under .worktrees/: the folder alone isn't a worker signal

	run := func(extra ...string) string {
		t.Helper()
		var env []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if k == "TMUX" || k == "AGENT_NAME" || strings.HasPrefix(k, "ISSUE_VIEWER_") || strings.HasPrefix(k, "CHECKS_") {
				continue
			}
			env = append(env, kv)
		}
		env = append(env, "CHECKS_LIB="+lib, "CHECKS_ROOT="+root, "CHECKS_YES=1", "NO_COLOR=1",
			"ISSUE_CLI="+cli, "TMPDIR="+t.TempDir())
		cmd := exec.Command(bash, script)
		cmd.Dir = root
		cmd.Env = append(env, extra...)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if out := run("ISSUE_VIEWER_ISSUE_SLUG=cli/some-issue"); !strings.Contains(out, "RC=0\n") {
		t.Fatalf("worker env: want RC=0\n%s", out)
	}
	if out := run(); !strings.Contains(out, "RC=1\n") {
		t.Fatalf("clean env: want RC=1\n%s", out)
	}
}
