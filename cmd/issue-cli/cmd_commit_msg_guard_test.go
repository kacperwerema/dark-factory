package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// guardRepo is a git repo on main with scripts/guard-check.sh committed
// (when withScript), and a git func that runs in it with no user config.
func guardRepo(t *testing.T, withScript bool) (string, func(args ...string)) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if withScript {
		writeRepoFile(t, dir, "scripts/guard-check.sh", "#!/bin/sh\n")
		git("add", ".")
	}
	git("commit", "-q", "--allow-empty", "-m", "init")
	return dir, git
}

// A branch's first commit that touches cmd/ or internal/ gets the note to
// run make guard-check; a docs-only commit, a later code commit, main itself
// and a checkout without the step get none.
func TestCommitMsgHintsGuardCheckOnTheFirstCodeCommit(t *testing.T) {
	dir, git := guardRepo(t, true)
	git("checkout", "-q", "-b", "work/x")

	writeRepoFile(t, dir, "docs/a.md", "a\n")
	git("add", ".")
	if n := guardCheckHint(dir); n != "" {
		t.Errorf("a docs-only commit got a hint: %s", n)
	}
	git("commit", "-q", "-m", "docs: a")

	writeRepoFile(t, dir, "cmd/x/main.go", "package main\n")
	git("add", ".")
	n := guardCheckHint(dir)
	for _, want := range []string{"first commit on work/x that touches cmd/ or internal/", "make guard-check", "docs/testing.md"} {
		if !strings.Contains(n, want) {
			t.Errorf("the first code commit's hint lacks %q: %q", want, n)
		}
	}
	// The hook prints it as a note on an accepted message.
	proj, _, _ := makeSimpleProject(t, "idea")
	withCwd(t, dir)
	ctx, _, stderr := newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, "feat: x\n")}); err != nil {
		t.Fatalf("commit-msg refused a plain message: %v\n%s", err, stderr)
	}
	assertContains(t, stderr.String(), "note: the first commit on work/x that touches cmd/ or internal/: run `make guard-check` now")
	git("commit", "-q", "-m", "feat: x")

	writeRepoFile(t, dir, "internal/y/y.go", "package y\n")
	git("add", ".")
	if n := guardCheckHint(dir); n != "" {
		t.Errorf("a second code commit got the hint again: %s", n)
	}
	git("commit", "-q", "-m", "feat: y")

	git("checkout", "-q", "main")
	writeRepoFile(t, dir, "cmd/z/main.go", "package main\n")
	git("add", ".")
	if n := guardCheckHint(dir); n != "" {
		t.Errorf("a commit on main got a hint: %s", n)
	}

	other, git2 := guardRepo(t, false)
	git2("checkout", "-q", "-b", "work/y")
	writeRepoFile(t, other, "cmd/x/main.go", "package main\n")
	git2("add", ".")
	if n := guardCheckHint(other); n != "" {
		t.Errorf("a checkout without scripts/guard-check.sh got a hint: %s", n)
	}
}
