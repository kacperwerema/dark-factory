package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/testbin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// staleCheckout is a checkout with a branch build and one Go file under
// cmd/issue-cli dated at, the build dated an hour before now.
func staleCheckout(t *testing.T, at time.Time) (root, build, code string) {
	t.Helper()
	root = t.TempDir()
	build = filepath.Join(root, tracker.BranchBuild)
	// not a stand-in: a branch build whose mtime the test sets
	if err := os.WriteFile(build, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	built := time.Now().Add(-time.Hour)
	if err := os.Chtimes(build, built, built); err != nil {
		t.Fatal(err)
	}
	code = filepath.Join(root, "cmd", "issue-cli", "main.go")
	if err := os.MkdirAll(filepath.Dir(code), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(code, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(code, at, at); err != nil {
		t.Fatal(err)
	}
	return root, build, code
}

// A branch build older than its checkout's code gets one line naming the
// change and the rebuild; a build newer than the code, a file under another
// name, and a ./issue-cli outside an issue-cli checkout get none.
func TestStaleBuildWarning(t *testing.T) {
	root, build, _ := staleCheckout(t, time.Now())
	line := staleBuildWarning(build)
	for _, want := range []string{
		"issue-cli: warning: this ./issue-cli is older than its code",
		filepath.Join("cmd", "issue-cli", "main.go") + " changed",
		"this call ran the old code",
		"go build -o ./issue-cli ./cmd/issue-cli",
		root,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("warning lacks %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "\n") {
		t.Errorf("warning is more than one line:\n%s", line)
	}

	// Reached through a link (a PATH entry pointing at the worktree's build).
	link := filepath.Join(t.TempDir(), "issue-cli")
	if err := os.Symlink(build, link); err != nil {
		t.Fatal(err)
	}
	if staleBuildWarning(link) == "" {
		t.Error("a link to a stale branch build printed nothing")
	}

	_, fresh, _ := staleCheckout(t, time.Now().Add(-2*time.Hour))
	if got := staleBuildWarning(fresh); got != "" {
		t.Errorf("a build newer than its code warned:\n%s", got)
	}

	other := filepath.Join(root, "issue-cli-old")
	if err := os.Link(build, other); err != nil {
		t.Fatal(err)
	}
	if got := staleBuildWarning(other); got != "" {
		t.Errorf("a file not named %s warned:\n%s", tracker.BranchBuild, got)
	}

	// An installed build: a folder with no cmd/issue-cli beside it.
	bin := t.TempDir()
	installed := filepath.Join(bin, tracker.BranchBuild)
	// not a stand-in: a build the check only stats, never runs
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := staleBuildWarning(installed); got != "" {
		t.Errorf("a build outside a checkout warned:\n%s", got)
	}
}

// End to end: a real build linked in as a checkout's ./issue-cli prints the
// line on stderr after any command, stdout untouched, and nothing once its
// code is older than it.
func TestAStaleBranchBuildWarnsAfterAnyCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("builds issue-cli")
	}
	root := t.TempDir()
	cli := filepath.Join(root, tracker.BranchBuild)
	testbin.Link(t, "./cmd/issue-cli", cli)
	code := filepath.Join(root, "cmd", "issue-cli", "main.go")
	if err := os.MkdirAll(filepath.Dir(code), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(code, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() (string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(cli, "version", "--json")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "ISSUE_CLI_LOG=", "ISSUE_VIEWER_CONFIG="+filepath.Join(root, "none.yaml"))
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("version: %v\n%s%s", err, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}

	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(code, future, future); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := run()
	if strings.Count(stderr, "is older than its code") != 1 {
		t.Fatalf("want one warning on stderr, got:\n%s", stderr)
	}
	if strings.Contains(stdout, "older than") {
		t.Fatalf("the warning went to stdout:\n%s", stdout)
	}

	past := time.Unix(0, 0)
	if err := os.Chtimes(code, past, past); err != nil {
		t.Fatal(err)
	}
	if _, stderr := run(); strings.Contains(stderr, "older than") {
		t.Fatalf("a build newer than its code warned:\n%s", stderr)
	}
}
