package gitdir

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// outsideTemp is a fresh folder with no repository above it, or a skip when
// the machine's temp folder sits inside one.
func outsideTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if !Outside(dir) {
		t.Skipf("temp folder %s is inside a repository", dir)
	}
	return dir
}

// noGitDir unsets GIT_DIR for the test: an empty GIT_DIR is no unset one
// to git.
func noGitDir(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_DIR", "x") // restores the old value at cleanup
	os.Unsetenv("GIT_DIR")
}

func gitSeesRepo(t *testing.T, dir string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func TestOutsideAgreesWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	noGitDir(t)
	dir := outsideTemp(t)
	deep := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if gitSeesRepo(t, deep) {
		t.Fatal("git finds a repository where Outside says none")
	}
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if Outside(deep) {
		t.Error("Outside still true after git init in a parent")
	}
	if !gitSeesRepo(t, deep) {
		t.Error("git finds no repository after git init")
	}
}

// A .git made later in a parent folder is seen on the next call: nothing
// is kept between calls.
func TestOutsideSeesALaterDotGit(t *testing.T) {
	noGitDir(t)
	dir := outsideTemp(t)
	sub := filepath.Join(dir, "board", "issues")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if !Outside(sub) {
		t.Fatal("Outside false with no .git anywhere")
	}
	// A worktree's .git file counts as much as a folder.
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Outside(sub) {
		t.Error("Outside true with a .git file in a parent")
	}
	if err := os.Remove(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if !Outside(sub) {
		t.Error("Outside false after the .git file went")
	}
	if err := os.Mkdir(filepath.Join(dir, "board", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if Outside(sub) {
		t.Error("Outside true with a .git folder in a parent")
	}
}

func TestOutsideKnowsABareRepository(t *testing.T) {
	noGitDir(t)
	dir := outsideTemp(t)
	for _, d := range []string{"objects", "refs"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if !Outside(dir) {
		t.Fatal("objects/ and refs/ without HEAD are no repository")
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Outside(dir) || Outside(filepath.Join(dir, "refs")) {
		t.Error("a bare repository's folder is not outside")
	}
}

func TestOutsideIsNeverSureWithoutReason(t *testing.T) {
	dir := outsideTemp(t)
	t.Setenv("GIT_DIR", "/some/repo.git")
	if Outside(dir) {
		t.Error("GIT_DIR set: git may find a repository anywhere")
	}
	noGitDir(t)
	if Outside(filepath.Join(dir, "missing")) {
		t.Error("a missing folder is not a sure no")
	}
}

func TestNoRepoLeavesCallsThatNeedNoRepository(t *testing.T) {
	noGitDir(t)
	dir := outsideTemp(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"clone", "x", "y"},
		{"ls-remote", "origin"},
		{"--version"},
		{"config", "--global", "user.name"},
		{"--git-dir", "/x/.git", "status"},
		{"--git-dir=/x/.git", "status"},
		{"--work-tree=/x", "status"},
		{"diff", "--no-index", "a", "b"},
		{"-c", "core.x=1", "init"},
	} {
		if NoRepo(dir, args...) {
			t.Errorf("NoRepo(%v) = true, want git asked", args)
		}
	}
	for _, args := range [][]string{
		{"worktree", "list", "--porcelain"},
		{"rev-parse", "--git-common-dir"},
		{"-c", "core.x=1", "for-each-ref"},
		{"ls-files", "-z", "--", "issues"},
	} {
		if !NoRepo(dir, args...) {
			t.Errorf("NoRepo(%v) = false, want no call", args)
		}
	}
	// -C moves the folder git starts in.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if NoRepo(dir, "-C", repo, "status") {
		t.Error("-C into a repository is not outside")
	}
}
