package privacy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ScanStaged reads only added lines, numbers them in the new file, and
// doesn't take an added "++…" line for a file header. A merge in
// progress is seen by Merging.
func TestScanStagedAddedLinesOnly(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("a.txt", "one\nSecret Game was here\nthree\n")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	// The old hit stays (not added now); two new lines are added.
	write("a.txt", "one\nSecret Game was here\nthree\nfour\n++ secret game again\n")
	write("b.txt", "clean\n")
	git("add", "-A")

	leaks, err := ScanStaged(repo, []string{"Secret Game"})
	if err != nil {
		t.Fatal(err)
	}
	if len(leaks) != 1 || leaks[0].Path != "a.txt" || leaks[0].Line != 5 {
		t.Fatalf("want one hit at a.txt:5, got %v", leaks)
	}
	if strings.Contains(strings.ToLower(LeakWarning(leaks)), "secret game") {
		t.Errorf("the warning echoes the name:\n%s", LeakWarning(leaks))
	}
	if Merging(repo) {
		t.Error("no merge in progress, Merging says one is")
	}
}

func TestScanMessageSkipsComments(t *testing.T) {
	msg := "feat: x\n\nabout Secret Game\n# Secret Game in a comment\n"
	leaks := ScanMessage(msg, "commit message", []string{"Secret Game"})
	if len(leaks) != 1 || leaks[0].Line != 3 {
		t.Fatalf("got %v", leaks)
	}
}
