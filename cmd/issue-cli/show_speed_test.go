package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/testbin"
)

// showLimit is what show may take on a quiet machine with its git cache
// cold. showVsChecklist is the slow machine's allowance: show may take that
// many times a checklist call timed in the same try (the same process start
// and board read, without the branch scan). On this fixture the unchanged
// code's cold show took 2.3 to 3.2 s against a 11 to 18 ms checklist (over
// 150×); with the scan's git calls side by side, 0.67 to 0.77 s against
// 13 to 24 ms, on a Mac at load 30 to 70.
const (
	showLimit       = time.Second
	showVsChecklist = 50
	showBranches    = 12
)

// TestShowFastWithAColdBranchScan keeps show under a second when its Shared
// files section has to rescan every open branch: main moved or a branch
// committed, so the git cache has none of the merge-bases, counts and diffs
// it needs. One show on the live board ran 70 git calls one after another
// there and took 8 to 16 s on a loaded machine
// (cli/issue-cli-show-takes-under-a-second-on-this-board).
func TestShowFastWithAColdBranchScan(t *testing.T) {
	if testing.Short() {
		t.Skip("builds issue-cli and a repo with open branches")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base := realTempDir(t)
	// A real process per call: each starts with no git answers in memory,
	// as an agent's show does.
	cli := testbin.Path(t, "./cmd/issue-cli")
	root := filepath.Join(base, "repo")
	branchScanRepo(t, root, showBranches)
	cfg := filepath.Join(base, "projects.yaml")
	mustWrite(t, cfg, fmt.Sprintf("projects:\n  - name: fx\n    slug: fx\n    workdir: %q\n    issues: %q\n    workflow: %q\n",
		root, filepath.Join(root, "issues"), filepath.Join(root, "workflow.yaml")))
	t.Setenv("ISSUE_VIEWER_CONFIG", cfg)
	t.Setenv("ISSUE_CLI_LOG", "")
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("TMUX", "")
	t.Setenv(telemetry.DisableEnv, "on")
	t.Setenv(telemetry.SkipEnv, "")

	cache := filepath.Join(root, ".git", "issue-cli-gitcache.jsonl")
	call := func(args ...string) (time.Duration, string) {
		t.Helper()
		if args[0] != "version" {
			args = append([]string{"--project", "fx"}, args...)
		}
		cmd := exec.Command(cli, args...)
		cmd.Dir = root
		start := time.Now()
		out, err := cmd.CombinedOutput()
		d := time.Since(start)
		if err != nil {
			t.Fatalf("issue-cli %v: %v\n%s", args, err, out)
		}
		return d, string(out)
	}
	// macOS checks a fresh executable on its first exec; that is no part of
	// either call's time.
	call("version")
	var tries []string
	for try := 0; try < 3; try++ {
		if err := os.Remove(cache); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		ref, _ := call("checklist", "cli/b0")
		d, out := call("show", "cli/b0")
		if !strings.Contains(out, "== Shared files (") {
			t.Fatalf("show printed no Shared files section, so it ran no branch scan:\n%s", out)
		}
		limit := max(showLimit, showVsChecklist*ref)
		if d <= limit {
			t.Logf("cold show %v, checklist %v (limit %v)", d, ref, limit)
			return
		}
		tries = append(tries, fmt.Sprintf("%v (limit %v: checklist took %v)", d, limit, ref))
	}
	t.Errorf("show with a cold branch scan of %d branches: %s; want under %v, or %d× checklist on a slow machine",
		showBranches, strings.Join(tries, ", "), showLimit, showVsChecklist)
}

// branchScanRepo is a repo on main with n open issues, each on its own
// work/cli/b<i> branch with two commits that touch shared.txt. Every third
// branch is forked from the one before it, so the scan restacks it and
// counts the commits the two share.
func branchScanRepo(t *testing.T, root string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "issues", "CLI"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "workflow.yaml"), "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\ntransitions:\n  - from: \"backlog\"\n    to: \"in progress\"\n")
	for i := 0; i < n; i++ {
		mustWrite(t, filepath.Join(root, "issues", "CLI", fmt.Sprintf("b%d.md", i)),
			fmt.Sprintf("---\ntitle: \"b%d\"\nstatus: \"in progress\"\nsystem: \"CLI\"\n---\n\n- [ ] one\n", i))
	}
	mustWrite(t, filepath.Join(root, "shared.txt"), "base\n")
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	commit := func(msg string) {
		git(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", msg)
	}
	commit("init")
	for i := 0; i < n; i++ {
		from := "main"
		if i%3 == 2 {
			from = fmt.Sprintf("work/cli/b%d", i-1)
		}
		git(t, root, "checkout", "-q", "-b", fmt.Sprintf("work/cli/b%d", i), from)
		for c := 0; c < 2; c++ {
			mustWrite(t, filepath.Join(root, "shared.txt"), fmt.Sprintf("b%d %d\n", i, c))
			commit(fmt.Sprintf("b%d %d", i, c))
		}
	}
	git(t, root, "checkout", "-q", "main")
}
