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
)

// Env names a board to read instead of the one beside the git common dir.
const Env = "LIVE_BOARD"

// Dir is the live board's folder, or t skips: no git repository, or no
// board there (a fresh clone).
func Dir(t testing.TB) string {
	t.Helper()
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
