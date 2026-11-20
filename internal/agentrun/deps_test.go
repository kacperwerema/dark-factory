package agentrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeAt(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsRepo is a committed npm workspace (root and packages/web), its
// .gitignore saying node_modules/ (which ignores folders, not links), with
// installed node_modules in the main checkout.
func jsRepo(t *testing.T) string {
	t.Helper()
	repo := gitRepo(t)
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	writeAt(t, repo, ".gitignore", "node_modules/\n.worktrees/\n")
	writeAt(t, repo, "package.json", `{"workspaces":["packages/*"]}`)
	writeAt(t, repo, "package-lock.json", `{"lockfileVersion":3}`)
	writeAt(t, repo, "packages/web/package.json", `{"name":"web"}`)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "js")
	writeAt(t, repo, "node_modules/left-pad/index.js", "module.exports = 1\n")
	writeAt(t, repo, "packages/web/node_modules/.bin/vite", "#!/bin/sh\n")
	return repo
}

func depSteps(steps []Step) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.Status + " " + s.Name + "\n")
	}
	return b.String()
}

// TestShareDeps: a worktree cut from main links the main checkout's
// node_modules (root and workspace), stays clean for git (so it can go
// after its branch lands), and removing the tree leaves the main
// checkout's packages in place. A reuse adds a missing link (dispatch
// --fresh); a branch with its own lock file gets no link; worktree_deps:
// own links nothing; a real node_modules folder is never replaced.
func TestShareDeps(t *testing.T) {
	repo := jsRepo(t)
	wt := filepath.Join(repo, ".worktrees", "x")
	dir, steps, ok := EnsureWorktree(repo, tracker.WorktreeSpec{Path: wt, Branch: "work/x", StartPoint: "main"}, &tracker.WorkflowConfig{})
	if !ok || dir != wt {
		t.Fatalf("EnsureWorktree = %q ok=%v\n%s", dir, ok, depSteps(steps))
	}
	if !strings.Contains(depSteps(steps), "issue-cli worktree deps --own") {
		t.Errorf("the step names the way to install your own:\n%s", depSteps(steps))
	}
	for _, d := range []string{".", "packages/web"} {
		link := filepath.Join(wt, d, "node_modules")
		fi, err := os.Lstat(link)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is no link: %v", link, err)
		}
		if !pointsTo(link, filepath.Join(repo, d, "node_modules")) {
			t.Errorf("%s points elsewhere", link)
		}
	}
	if st := gitIn(t, wt, "status", "--porcelain"); st != "" {
		t.Fatalf("the links make the tree dirty:\n%s", st)
	}

	// dispatch --fresh into the same tree: a removed link comes back.
	if err := os.Remove(filepath.Join(wt, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if _, steps, ok = EnsureWorktree(repo, tracker.WorktreeSpec{Path: wt, Branch: "work/x"}, nil); !ok {
		t.Fatalf("reuse: %s", depSteps(steps))
	}
	if !pointsTo(filepath.Join(wt, "node_modules"), filepath.Join(repo, "node_modules")) {
		t.Errorf("reuse did not link again:\n%s", depSteps(steps))
	}

	// The tree goes once its branch is on main: the packages stay.
	if err := RemoveTree(repo, wt); err != nil {
		t.Fatalf("RemoveTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "node_modules", "left-pad", "index.js")); err != nil {
		t.Fatalf("removing the tree emptied the main checkout's node_modules: %v", err)
	}

	// A branch that changed the lock file installs its own.
	gitIn(t, repo, "branch", "work/y")
	other := filepath.Join(t.TempDir(), "y")
	gitIn(t, repo, "worktree", "add", "-q", other, "work/y")
	writeAt(t, other, "package-lock.json", `{"lockfileVersion":3,"packages":{"x":{}}}`)
	gitIn(t, other, "commit", "-q", "-am", "dep")
	gitIn(t, repo, "worktree", "remove", other)
	ywt := filepath.Join(repo, ".worktrees", "y")
	_, steps, _ = EnsureWorktree(repo, tracker.WorktreeSpec{Path: ywt, Branch: "work/y"}, &tracker.WorkflowConfig{})
	if _, err := os.Lstat(filepath.Join(ywt, "node_modules")); err == nil {
		t.Errorf("a changed lock file got a link:\n%s", depSteps(steps))
	}
	if !strings.Contains(depSteps(steps), "package-lock.json differs") {
		t.Errorf("the step says why nothing was shared:\n%s", depSteps(steps))
	}

	// worktree_deps: own links nothing.
	zwt := filepath.Join(repo, ".worktrees", "z")
	EnsureWorktree(repo, tracker.WorktreeSpec{Path: zwt, Branch: "work/z", StartPoint: "main"}, &tracker.WorkflowConfig{WorktreeDeps: "own"})
	if _, err := os.Lstat(filepath.Join(zwt, "node_modules")); err == nil {
		t.Error("worktree_deps: own still linked")
	}

	// A real folder stays as it is.
	writeAt(t, zwt, "node_modules/own/index.js", "x")
	for _, l := range ShareDeps(repo, zwt) {
		if l.Dir == "." && l.State != DepOwn {
			t.Errorf("root state %q, want %q", l.State, DepOwn)
		}
	}
	if _, err := os.Stat(filepath.Join(zwt, "node_modules", "own", "index.js")); err != nil {
		t.Errorf("ShareDeps touched a real node_modules: %v", err)
	}
}

// TestOwnDeps turns the links into the worktree's own copies (never
// through them: the main checkout's stay as they were) and names the
// install for the lock file.
func TestOwnDeps(t *testing.T) {
	repo := jsRepo(t)
	wt := filepath.Join(repo, ".worktrees", "x")
	if _, steps, ok := EnsureWorktree(repo, tracker.WorktreeSpec{Path: wt, Branch: "work/x", StartPoint: "main"}, nil); !ok {
		t.Fatal(depSteps(steps))
	}
	owned, err := OwnDeps(repo, wt)
	if err != nil || len(owned) != 2 {
		t.Fatalf("OwnDeps = %v, %v; want 2 links owned", owned, err)
	}
	if fi, err := os.Lstat(filepath.Join(wt, "node_modules")); err != nil || !fi.IsDir() {
		t.Fatalf("node_modules is no folder of its own: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "node_modules", "left-pad", "index.js")); err != nil {
		t.Errorf("the own copy lacks the main checkout's packages: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(wt, "node_modules", "left-pad")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "node_modules", "left-pad", "index.js")); err != nil {
		t.Fatalf("the own copy is still the main checkout's: %v", err)
	}
	if got := InstallCommand(wt); got != "npm ci" {
		t.Errorf("InstallCommand = %q, want npm ci", got)
	}
}

// TestWorktreeSetupNeverInstallsThroughWorkspaceLinks: worktree_setup:
// "npm ci" in a new worktree whose root and packages/web node_modules link
// to the main checkout's is skipped while the lock file is main's, and the
// main checkout's packages stay (D-8). A branch whose lock file differs
// gets no links (TestShareDeps), so its setup installs into its own.
func TestWorktreeSetupNeverInstallsThroughWorkspaceLinks(t *testing.T) {
	repo := jsRepo(t)
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "npm"), `[ "$1" = ci ] || exit 0
for d in . packages/*; do rm -rf "$d"/node_modules/*; mkdir -p "$d/node_modules"; echo x > "$d/node_modules/.installed"; done
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mainKept := func(steps []Step) {
		t.Helper()
		for _, p := range []string{"node_modules/left-pad/index.js", "packages/web/node_modules/.bin/vite"} {
			if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
				t.Errorf("the main checkout lost %s: %v\n%s", p, err, depSteps(steps))
			}
		}
	}
	wf := &tracker.WorkflowConfig{WorktreeSetup: "npm ci"}
	wt := filepath.Join(repo, ".worktrees", "x")
	spec := tracker.WorktreeSpec{Path: wt, Branch: "work/x", StartPoint: "main"}
	_, steps, ok := EnsureWorktree(repo, spec, wf)
	if !ok || !strings.Contains(depSteps(steps), "skipped: npm ci: node_modules is shared") {
		t.Fatalf("ok %v, want the setup skipped:\n%s", ok, depSteps(steps))
	}
	mainKept(steps)

}
