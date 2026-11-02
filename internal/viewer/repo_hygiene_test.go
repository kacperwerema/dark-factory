package viewer

import (
	"os/exec"
	"strings"
	"testing"
)

// TestProfileOutputsNotTracked guards the repo against the files a profile
// run writes: `go test -cpuprofile` leaves <package>.test (a 26 MB binary for
// this package) and the .prof file in the working folder. issue-viewer.test
// was once committed, so every profile run showed a binary change.
func TestProfileOutputsNotTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		t.Skip("not a git checkout")
	}

	// Run from the repo root: ls-files and check-ignore read paths from the
	// working folder, and go test runs in this package's.
	git := func(args ...string) *exec.Cmd {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		return cmd
	}
	out, err := git("ls-files", "--", "*.test", "*.prof").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	if tracked := strings.TrimSpace(string(out)); tracked != "" {
		t.Errorf("profile outputs are tracked (git rm --cached them):\n%s", tracked)
	}

	for _, p := range []string{"internal/viewer/viewer.test", "cmd/issue-cli/issue-cli.test", "cpu.prof", "internal/ci/mem.prof"} {
		if err := git("check-ignore", "-q", "--no-index", p).Run(); err != nil {
			t.Errorf("%s is not gitignored; a profile run would show it in git status", p)
		}
	}
}
