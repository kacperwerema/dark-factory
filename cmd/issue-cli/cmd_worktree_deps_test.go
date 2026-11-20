package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// TestWorktreeDeps: worktree add links the main checkout's node_modules and
// says how to install your own; deps shows the link; deps --own removes it
// (the main checkout's packages stay) and installs; deps --shared links
// again once the own folder is gone; deps in the main checkout is refused.
func TestWorktreeDeps(t *testing.T) {
	proj, _ := ownTreeProject(t)
	dir := proj.WorkDir
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("checkout", "-q", "main")
	for name, body := range map[string]string{".gitignore": "issues/\nworkflow.yaml\nnode_modules\n", "package.json": `{"name":"app"}`, "package-lock.json": `{"lockfileVersion":3}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".gitignore", "package.json", "package-lock.json")
	git("commit", "-q", "-m", "js")
	pkg := filepath.Join(dir, "node_modules", "left-pad", "index.js")
	if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "npm"), "mkdir -p node_modules/own && echo \"$*\" > node_modules/own/args\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorktrees(ctx, []string{"add", "cli/sample"}); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, stdout)
	}
	assertContains(t, stdout.String(), "Shared node_modules: linked node_modules")
	assertContains(t, stdout.String(), "issue-cli worktree deps --own")
	tree := filepath.Join(dir, ".worktrees", "cli", "sample")
	link := filepath.Join(tree, "node_modules")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("no link at %s: %v", link, err)
	}

	stdout.Reset()
	if err := runWorktrees(ctx, []string{"deps", "--dir", tree}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "node_modules: shared: a link to")

	stdout.Reset()
	if err := runWorktrees(ctx, []string{"deps", "--own", "--dir", tree}); err != nil {
		t.Fatalf("deps --own: %v\n%s", err, stdout)
	}
	assertContains(t, stdout.String(), "is this worktree's own copy")
	assertContains(t, stdout.String(), "$ npm ci")
	if _, err := os.Stat(filepath.Join(tree, "node_modules", "own", "args")); err != nil {
		t.Fatalf("the install did not run in the worktree: %v\n%s", err, stdout)
	}
	if _, err := os.Stat(pkg); err != nil {
		t.Fatalf("--own went through the link: %v", err)
	}

	stdout.Reset()
	if err := runWorktrees(ctx, []string{"deps", "--dir", tree}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "its own folder")

	if err := os.RemoveAll(filepath.Join(tree, "node_modules")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runWorktrees(ctx, []string{"deps", "--shared", "--dir", tree}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "linked node_modules")

	if err := runWorktrees(ctx, []string{"deps", "--dir", dir}); err == nil {
		t.Error("deps in the main checkout should be refused")
	}
	if err := runWorktrees(ctx, []string{"deps", "--own", "--shared"}); err == nil {
		t.Error("--own with --shared should be a usage error")
	}
}
