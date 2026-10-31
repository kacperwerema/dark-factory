package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// With no -project, the report must name a project this machine has: the
// dispatched worker's ISSUE_VIEWER_PROJECT, else the configured project
// whose issues dir is this repo's, and only then the old issue-viewer name.
// Before, a worker's report ran issue-cli --project issue-viewer, which
// does not exist where the engine board is named dark-factory, and ticked
// nothing.
func TestReportProjectDefault(t *testing.T) {
	projects := []byte(`[
  {"slug": "first", "issue_dir": "/r/first/issues", "default": true},
  {"slug": "dark-factory", "issue_dir": "/r/engine/issues"}
]`)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	list := func() ([]byte, error) { return projects, nil }
	cases := []struct {
		name string
		env  map[string]string
		dirs []string
		want string
	}{
		{"playtest env wins", map[string]string{"PLAYTEST_PROJECT": "p", "ISSUE_VIEWER_PROJECT": "w"}, []string{"/r/engine/issues"}, "p"},
		{"dispatched worker", map[string]string{"ISSUE_VIEWER_PROJECT": "dark-factory"}, nil, "dark-factory"},
		{"repo's issues dir", nil, []string{"/r/engine/.worktrees/x/issues", "/r/engine/issues"}, "dark-factory"},
		{"no match", nil, []string{"/elsewhere/issues"}, "issue-viewer"},
	}
	for _, c := range cases {
		if got := defaultProject(env(c.env), list, c.dirs); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// After APPLY the report reads the checklist back: a box it meant to tick
// that is still open is named, and the run fails, instead of printing the
// plan and nothing else.
func TestApplyPlanReportsUnticked(t *testing.T) {
	p := &Plan{Tick: []string{"TP4", "TP5"}}
	run := func(args ...string) error { return nil } // issue-cli said ok
	var out, errw strings.Builder
	verify := func() (map[string]bool, bool) { return map[string]bool{"TP4": true, "TP5": false}, true }
	code := applyPlan(p, "x/y", nil, run, verify, &out, &errw)
	if code != 1 || !strings.Contains(errw.String(), "still unticked: TP5") || strings.Contains(errw.String(), "TP4") {
		t.Fatalf("code %d\nout: %s\nerr: %s", code, out.String(), errw.String())
	}
	errw.Reset()
	verify = func() (map[string]bool, bool) { return map[string]bool{"TP4": true, "TP5": true}, true }
	if code := applyPlan(p, "x/y", nil, run, verify, &out, &errw); code != 0 || errw.Len() != 0 {
		t.Fatalf("all ticked: code %d, err %s", code, errw.String())
	}
}

// The issues-dir rule against a real git repo and worktree: from the
// worktree it finds the primary checkout's issues/, and a project whose
// issue_dir is written through a symlink (macOS /var → /private/var) still
// matches.
func TestReportProjectFromWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir() // unresolved on macOS: under /var
	main := filepath.Join(root, "engine")
	wt := filepath.Join(main, ".worktrees", "x")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(main, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(root, "init", "-q", main)
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	git(main, "worktree", "add", "-q", wt)
	link := filepath.Join(root, "link")
	if err := os.Symlink(main, link); err != nil {
		t.Fatal(err)
	}
	list := func() ([]byte, error) {
		return []byte(`[{"slug":"other","issue_dir":"/nowhere/issues"},{"slug":"engine","issue_dir":"` + filepath.Join(link, "issues") + `"}]`), nil
	}
	none := func(string) string { return "" }
	if got := defaultProject(none, list, repoIssueDirs(wt)); got != "engine" {
		t.Fatalf("from the worktree: got %q (dirs %v)", got, repoIssueDirs(wt))
	}
}
