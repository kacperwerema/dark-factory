// Package liveboard finds the live board for tests that read it, and for
// playtest check: the issues/ folder of this checkout's primary repository. A worktree has no
// issues/ of its own (the board lives on the primary checkout, beside the
// git common dir), and a fresh clone has none at all, so such a test skips
// there instead of failing.
//
//	issues := tracker.LoadIssues(liveboard.Dir(t))
//
// LIVE_BOARD names another board, such as a copy to try a change on.
package liveboard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/testshard"
)

// Env names a board to read instead of the one beside the git common dir.
const Env = "LIVE_BOARD"

// DeferEnv is set by make test's cached run (tools/test-shards) to the
// cache list's path (scripts/test-cache.txt). The live board changes every
// few seconds, so a package whose test reads it would never come from go
// test's cache: there Dir skips a test the list names on a fresh-test line
// (it runs in the fresh run beside) and fails one it doesn't name.
const DeferEnv = "LIVE_BOARD_DEFER"

// Dir is the live board's folder, or t skips: no git repository, or no
// board there (a fresh clone), or a cached run that leaves this test to the
// fresh one (DeferEnv).
func Dir(t testing.TB) string {
	t.Helper()
	if list := os.Getenv(DeferEnv); list != "" {
		deferToFreshRun(t, list)
	}
	dir, err := Find(".", os.Getenv(Env))
	if err != nil {
		t.Skipf("no live board: %v", err)
	}
	return dir
}

// Find is the board for a test running in from: override when set, else
// <primary checkout>/issues (the git common dir's parent). An error says
// why there is none: not a git repository, or no issue file under that
// folder.
func Find(from, override string) (string, error) {
	dir := override
	if dir == "" {
		cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		cmd.Dir = from
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("%s is not in a git repository: %v", from, err)
		}
		dir = filepath.Join(filepath.Dir(strings.TrimSpace(string(out))), "issues")
	}
	if !hasIssue(dir) {
		return "", fmt.Errorf("no issue files under %s (a fresh clone)", dir)
	}
	return dir, nil
}

// hasIssue reports whether dir holds a markdown issue at its top or one
// folder down (issues/<System>/), skipping dot folders such as .outputs.
func hasIssue(dir string) bool {
	for _, pat := range []string{"*.md", "[^.]*/*.md"} {
		if m, _ := filepath.Glob(filepath.Join(dir, pat)); len(m) > 0 {
			return true
		}
	}
	return false
}

// deferToFreshRun skips t when the cache list names it on a fresh-test line
// for this package, and fails it otherwise: read here, its board would be
// cached with the package.
func deferToFreshRun(t testing.TB, listPath string) {
	t.Helper()
	data, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatalf("%s: %v", DeferEnv, err)
	}
	list, err := testshard.ParseCacheList(data)
	if err != nil {
		t.Fatalf("%s: %v", listPath, err)
	}
	pkg, err := packageDir()
	if err != nil {
		t.Fatalf("%s: %v", DeferEnv, err)
	}
	test, _, _ := strings.Cut(t.Name(), "/")
	if _, ok := list.FreshTests[pkg][test]; ok {
		t.Skipf("reads the live board: runs in make test's fresh run (%s)", listPath)
	}
	t.Fatalf("%s reads the live board, which go test's cache can't see: add `fresh-test %s %s reads the live board` to %s", test, pkg, test, listPath)
}

// packageDir is the running test's package folder from the module root,
// as the cache list spells it ("./internal/x").
func packageDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for root := wd; ; {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			rel, err := filepath.Rel(root, wd)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return ".", nil
			}
			return "./" + filepath.ToSlash(rel), nil
		}
		up := filepath.Dir(root)
		if up == root {
			return "", fmt.Errorf("no go.mod above %s", wd)
		}
		root = up
	}
}
