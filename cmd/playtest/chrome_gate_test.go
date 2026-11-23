package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/slowtier"
)

// chromeGateVar is set by make test: 0 when the change since main touches
// none of the Makefile's PLAYTEST_E2E_PATHS (outside the slow tier), 1
// otherwise. Unset (a plain go test) runs the Chrome tests.
const chromeGateVar = "DF_PLAYTEST_E2E"

// chromeGatePaths is the Makefile's PLAYTEST_E2E_PATHS: what the Chrome
// tests drive. A change to any of them runs the tests in make validate.
var chromeGatePaths = []string{"cmd/playtest/", "playtest/", "internal/viewer/templates/", "internal/viewer/static/"}

// chromeGateSkip says why t skips under make test's path gate, or "".
func chromeGateSkip() string {
	if os.Getenv(chromeGateVar) == "0" && !slowtier.On() {
		return "Chrome test left out of make validate: the change touches none of " + strings.Join(chromeGatePaths, " ") +
			" (the slow tier runs it; " + chromeGateVar + "=1 or a plain go test runs it here)"
	}
	return ""
}

// needChrome skips t when make test's path gate leaves the Chrome tests out
// or no Chrome is found. Every test that drives Chrome starts with it.
func needChrome(t testing.TB) {
	t.Helper()
	if why := chromeGateSkip(); why != "" {
		t.Skip(why)
	}
	if _, err := ChromePath(); err != nil {
		t.Skip(err)
	}
}

func TestChromeGateSkipsOnlyAFastTierRunTheGateLeftOut(t *testing.T) {
	for _, c := range []struct {
		gate, slow string
		skip       bool
	}{
		{"", "", false}, {"1", "", false}, {"0", "", true}, {"0", "1", false}, {"1", "1", false},
	} {
		t.Setenv(chromeGateVar, c.gate)
		t.Setenv(slowtier.Var, c.slow)
		if got := chromeGateSkip() != ""; got != c.skip {
			t.Errorf("%s=%q %s=%q: skip %v, want %v", chromeGateVar, c.gate, slowtier.Var, c.slow, got, c.skip)
		}
	}
}

// TestChromeGatePathsMatchTheMakefile: the Makefile gates on the paths the
// skip message names, each one is in the repo, and make test passes the
// gate's answer to the suite (1 in the slow tier without asking git).
func TestChromeGatePathsMatchTheMakefile(t *testing.T) {
	repo := repoDir(t)
	mk, err := os.ReadFile(filepath.Join(repo, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^PLAYTEST_E2E_PATHS := (.+)$`).FindSubmatch(mk)
	if m == nil {
		t.Fatal("the Makefile has no PLAYTEST_E2E_PATHS := line")
	}
	if got, want := strings.Fields(string(m[1])), chromeGatePaths; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("PLAYTEST_E2E_PATHS = %v, want %v", got, want)
	}
	for _, p := range chromeGatePaths {
		if fi, err := os.Stat(filepath.Join(repo, p)); err != nil || !fi.IsDir() {
			t.Errorf("gate path %s is not a folder in the repo (%v)", p, err)
		}
	}
	for _, re := range []string{
		`(?m)^PLAYTEST_E2E = \$\(if \$\(filter 1,\$\(SLOW_TESTS\)\),1,\$\(shell scripts/touches\.sh \$\(PLAYTEST_E2E_PATHS\)\)\)$`,
		`(?m)^test: export ` + chromeGateVar + ` = \$\(PLAYTEST_E2E\)$`,
	} {
		if !regexp.MustCompile(re).Match(mk) {
			t.Errorf("the Makefile has no line matching %s", re)
		}
	}
}

// TestTouchesSaysWhetherTheChangeSinceMainHitsAPath runs scripts/touches.sh
// in a scratch repo: committed, staged, unstaged and untracked changes on a
// branch count, a change elsewhere doesn't, and no main means run (1).
func TestTouchesSaysWhetherTheChangeSinceMainHitsAPath(t *testing.T) {
	script := filepath.Join(repoDir(t), "scripts", "touches.sh")
	dir := t.TempDir()
	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touches := func() string { t.Helper(); return run("sh", script, "web/", "ui/static/") }

	run("git", "init", "-q", "-b", "trunk")
	write("web/a.txt", "a")
	write("other/b.txt", "b")
	run("git", "add", ".")
	run("git", "commit", "-qm", "base")
	if got := touches(); got != "1" {
		t.Errorf("no main branch: %q, want 1 (a gate that can't tell runs the tests)", got)
	}
	run("git", "branch", "main")
	run("git", "checkout", "-qb", "work")
	if got := touches(); got != "0" {
		t.Errorf("no change: %q, want 0", got)
	}
	write("other/b.txt", "b2")
	run("git", "commit", "-qam", "elsewhere")
	if got := touches(); got != "0" {
		t.Errorf("a committed change outside the paths: %q, want 0", got)
	}
	write("ui/static/new.css", "x")
	if got := touches(); got != "1" {
		t.Errorf("an untracked file under a path: %q, want 1", got)
	}
	run("git", "add", "ui")
	if got := touches(); got != "1" {
		t.Errorf("a staged file under a path: %q, want 1", got)
	}
	run("git", "commit", "-qm", "css")
	if got := touches(); got != "1" {
		t.Errorf("a committed file under a path: %q, want 1", got)
	}
	run("git", "reset", "-q", "--hard", "HEAD~1")
	write("web/a.txt", "a2")
	if got := touches(); got != "1" {
		t.Errorf("an unstaged edit under a path: %q, want 1", got)
	}
	run("git", "checkout", "-q", "web/a.txt")
	run("git", "mv", "web/a.txt", "other/a.txt")
	run("git", "commit", "-qm", "move out")
	if got := touches(); got != "1" {
		t.Errorf("a file moved out of a path: %q, want 1", got)
	}
}
