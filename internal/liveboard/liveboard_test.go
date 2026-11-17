package liveboard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo is a primary checkout with one commit and a worktree of it, the
// shape a worker's tests run in. Paths are resolved: on a Mac TempDir is
// under /var, a link to /private/var, and git prints the resolved path.
func repo(t *testing.T) (primary, worktree string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	primary = filepath.Join(root, "repo")
	write(t, filepath.Join(primary, "README.md"), "x\n")
	git(t, primary, "init", "-q", "-b", "main")
	git(t, primary, "config", "maintenance.auto", "false")
	git(t, primary, "config", "gc.auto", "0")
	git(t, primary, "add", ".")
	git(t, primary, "commit", "-q", "-m", "init")
	worktree = filepath.Join(root, "wt")
	git(t, primary, "worktree", "add", "-q", "-b", "work", worktree)
	return primary, worktree
}

func TestFindFromAWorktreeReadsThePrimaryBoard(t *testing.T) {
	primary, worktree := repo(t)
	write(t, filepath.Join(primary, "issues", "Factory", "a.md"), "---\ntitle: A\n---\n")
	sub := filepath.Join(worktree, "internal", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{primary, worktree, sub} {
		got, err := Find(from, "")
		if err != nil {
			t.Fatalf("Find(%s): %v", from, err)
		}
		if want := filepath.Join(primary, "issues"); got != want {
			t.Errorf("Find(%s) = %s, want %s", from, got, want)
		}
	}
}

func TestFindTopLevelIssue(t *testing.T) {
	primary, _ := repo(t)
	write(t, filepath.Join(primary, "issues", "a.md"), "---\ntitle: A\n---\n")
	if _, err := Find(primary, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFindFreshCloneHasNoBoard(t *testing.T) {
	primary, worktree := repo(t)
	// Only work files in dot folders: still no board.
	write(t, filepath.Join(primary, "issues", ".outputs", "x.md"), "x\n")
	for _, from := range []string{primary, worktree} {
		if _, err := Find(from, ""); err == nil || !strings.Contains(err.Error(), "fresh clone") {
			t.Errorf("Find(%s) err = %v, want a fresh-clone error", from, err)
		}
	}
}

func TestFindOutsideGit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if _, err := Find(dir, ""); err == nil || !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("err = %v, want not in a git repository", err)
	}
}

func TestFindOverride(t *testing.T) {
	board := t.TempDir()
	write(t, filepath.Join(board, "S", "a.md"), "---\ntitle: A\n---\n")
	got, err := Find(t.TempDir(), board)
	if err != nil || got != board {
		t.Errorf("Find override = %q, %v; want %q", got, err, board)
	}
	if _, err := Find(".", t.TempDir()); err == nil {
		t.Error("an empty override board should be no board")
	}
}

// TestDirSkipsWithoutABoard: Dir skips (never fails) where there is no
// board, so the suite stays green on a fresh clone.
func TestDirSkipsWithoutABoard(t *testing.T) {
	t.Setenv(DeferEnv, "") // not make test's cached run
	t.Setenv(Env, t.TempDir())
	skipped := false
	t.Run("inner", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		Dir(t)
	})
	if !skipped {
		t.Error("Dir did not skip on an empty board")
	}
}

// stopTB records the Skipf or Fatalf that ends a call to Dir.
type stopTB struct {
	testing.TB
	name, skipped, failed string
}

type stopped struct{}

func (s *stopTB) Helper()      {}
func (s *stopTB) Name() string { return s.name }
func (s *stopTB) Skipf(format string, args ...any) {
	s.skipped = fmt.Sprintf(format, args...)
	panic(stopped{})
}
func (s *stopTB) Fatalf(format string, args ...any) {
	s.failed = fmt.Sprintf(format, args...)
	panic(stopped{})
}

func dirStops(tb *stopTB) {
	defer func() {
		if r := recover(); r != nil && r != (stopped{}) {
			panic(r)
		}
	}()
	Dir(tb)
}

// In make test's cached run (DeferEnv names the cache list) Dir skips a
// test a fresh-test line names, a subtest by its top-level test, and fails
// one no line names: its board would be cached with the package.
func TestDirInTheCachedRunLeavesTheBoardToTheFreshRun(t *testing.T) {
	list := filepath.Join(t.TempDir(), "test-cache.txt")
	write(t, list, "cached ./internal/liveboard\nfresh-test ./internal/liveboard TestReadsTheBoard reads the live board\n")
	t.Setenv(DeferEnv, list)
	t.Setenv(Env, t.TempDir())

	listed := &stopTB{TB: t, name: "TestReadsTheBoard/sub"}
	dirStops(listed)
	if listed.failed != "" || !strings.Contains(listed.skipped, "runs in make test's fresh run") {
		t.Errorf("a listed test: skipped %q, failed %q", listed.skipped, listed.failed)
	}
	other := &stopTB{TB: t, name: "TestAlsoReadsIt"}
	dirStops(other)
	if want := "add `fresh-test ./internal/liveboard TestAlsoReadsIt reads the live board` to " + list; !strings.Contains(other.failed, want) {
		t.Errorf("an unlisted test: failed %q, want it to say %q", other.failed, want)
	}

	// Outside the cached run the board is read as before.
	t.Setenv(DeferEnv, "")
	plain := &stopTB{TB: t, name: "TestAlsoReadsIt"}
	dirStops(plain)
	if plain.failed != "" || !strings.HasPrefix(plain.skipped, "no live board") {
		t.Errorf("without %s: skipped %q, failed %q", DeferEnv, plain.skipped, plain.failed)
	}
}
