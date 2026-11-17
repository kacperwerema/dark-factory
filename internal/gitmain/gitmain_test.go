package gitmain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestRootFromFilesMatchesGit: Root reads the main checkout from the files
// for a checkout's top folder and agrees with `git worktree list` there; a
// subdirectory, a bare repository and core.worktree still go to git.
func TestRootFromFilesMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	wt := filepath.Join(base, "wt")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "worktree", "add", "-q", "-b", "side", wt)
	// extensions.worktreeConfig names "worktree" without moving the checkout.
	git(t, repo, "config", "extensions.worktreeConfig", "true")

	for _, dir := range []string{repo, wt} {
		if got := rootFromFiles(dir); got != repo {
			t.Errorf("rootFromFiles(%s) = %q, want %q", dir, got, repo)
		}
		if got := Root(dir); got != repo {
			t.Errorf("Root(%s) = %q, want %q", dir, got, repo)
		}
	}
	if got := rootFromFiles(filepath.Join(repo, "sub")); got != "" {
		t.Errorf("a subdirectory is git's to answer, got %q", got)
	}
	if got := Root(filepath.Join(repo, "sub")); got != repo {
		t.Errorf("Root(subdirectory) = %q, want %q", got, repo)
	}

	for _, c := range []struct{ name, cfg string }{
		{"core.worktree", "[core]\n\tworktree = /elsewhere\n"},
		{"core.bare", "[core]\n\tbare = true\n"},
	} {
		if !coreMovesCheckout(c.cfg) {
			t.Errorf("%s: coreMovesCheckout = false, want true", c.name)
		}
	}
	for _, cfg := range []string{"[core]\n\tbare = false\n", "[extensions]\n\tworktreeConfig = true\n", "[branch \"worktree\"]\n\tremote = origin\n"} {
		if coreMovesCheckout(cfg) {
			t.Errorf("coreMovesCheckout(%q) = true, want false", cfg)
		}
	}
	bare := filepath.Join(base, "bare.git")
	git(t, base, "init", "-q", "--bare", bare)
	if got := rootFromFiles(bare); got != "" {
		t.Errorf("a bare repository is git's to answer, got %q", got)
	}
}

func TestHasRemote(t *testing.T) {
	dir := t.TempDir()
	if _, err := HasRemote(dir); err == nil {
		t.Error("a folder that is no repository answered; want an error (can't tell)")
	}
	git(t, dir, "init", "-q")
	if has, err := HasRemote(dir); err != nil || has {
		t.Errorf("fresh repo: HasRemote = %v, %v; want false, nil", has, err)
	}
	git(t, dir, "remote", "add", "origin", "https://example.invalid/x.git")
	if has, err := HasRemote(dir); err != nil || !has {
		t.Errorf("repo with origin: HasRemote = %v, %v; want true, nil", has, err)
	}
}
