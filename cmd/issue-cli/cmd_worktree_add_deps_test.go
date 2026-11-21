package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// workspaceProject is ownTreeProject made an npm workspace on main: a root
// package.json and lock file, packages/core/package.json, and in the main
// checkout root node_modules/.bin/tsx and packages/core/node_modules. The
// workflow is wf (no worktree_setup).
func workspaceProject(t *testing.T, wf string) (proj *tracker.Project, dir string) {
	t.Helper()
	proj, _ = ownTreeProject(t)
	dir = proj.WorkDir
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("checkout", "-q", "main")
	files := map[string]string{
		".gitignore":                         "issues/\nworkflow.yaml\nnode_modules/\n",
		"package.json":                       `{"name":"app","workspaces":["packages/*"]}`,
		"package-lock.json":                  `{"lockfileVersion":3}`,
		"packages/core/package.json":         `{"name":"@app/core"}`,
		"workflow.yaml":                      wf,
		"node_modules/.bin/tsx":              "#!/bin/sh\necho tsx\n",
		"packages/core/node_modules/x/a.js":  "1\n",
		"node_modules/left-pad/package.json": `{"name":"left-pad"}`,
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".gitignore", "package.json", "package-lock.json", "packages/core/package.json")
	git("commit", "-q", "-m", "workspace")
	return proj, dir
}

const wsWorkflow = "statuses:\n  - name: \"in progress\"\n  - name: \"done\"\ntransitions:\n  - from: \"in progress\"\n    to: \"done\"\n"

// TestWorktreeAddWorkspaceDeps: worktree add gives the new tree every
// node_modules the main checkout has, so node_modules/.bin/tsx is found from
// the tree's root: a link under worktree_deps: shared, the tree's own copy
// under own (never a link into the main checkout). A folder it can't give
// (the lock file differs) makes it exit non-zero, naming the folder and the
// install command, instead of printing success.
func TestWorktreeAddWorkspaceDeps(t *testing.T) {
	for _, tc := range []struct {
		name, deps string
		link       bool
	}{
		{"shared", "", true},
		{"own", "worktree_deps: own\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj, dir := workspaceProject(t, tc.deps+wsWorkflow)
			ctx, stdout, _ := newTestContext(proj, false)
			if err := runWorktrees(ctx, []string{"add", "cli/sample"}); err != nil {
				t.Fatalf("worktree add: %v\n%s", err, stdout)
			}
			tree := filepath.Join(dir, ".worktrees", "cli", "sample")
			if _, err := os.Stat(filepath.Join(tree, "node_modules", ".bin", "tsx")); err != nil {
				t.Fatalf("node_modules/.bin/tsx not found from the worktree root: %v\n%s", err, stdout)
			}
			if _, err := os.Stat(filepath.Join(tree, "packages", "core", "node_modules", "x", "a.js")); err != nil {
				t.Fatalf("packages/core/node_modules not in the worktree: %v\n%s", err, stdout)
			}
			for _, d := range []string{"node_modules", filepath.Join("packages", "core", "node_modules")} {
				fi, err := os.Lstat(filepath.Join(tree, d))
				if err != nil {
					t.Fatal(err)
				}
				if got := fi.Mode()&os.ModeSymlink != 0; got != tc.link {
					t.Errorf("%s: symlink=%v, want %v\n%s", d, got, tc.link, stdout)
				}
			}
			if !tc.link {
				assertContains(t, stdout.String(), "own copy")
				// The tree's own: writing into it leaves the main checkout's.
				if err := os.RemoveAll(filepath.Join(tree, "node_modules", "left-pad")); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(dir, "node_modules", "left-pad", "package.json")); err != nil {
					t.Fatalf("the main checkout's packages went with the copy's: %v", err)
				}
			}
			if out, err := exec.Command("git", "-C", tree, "status", "--porcelain").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "" {
				t.Errorf("the tree is not clean after worktree add: %v %q", err, out)
			}
		})
	}

	t.Run("lock-diff", func(t *testing.T) {
		proj, dir := workspaceProject(t, wsWorkflow)
		// The main checkout's packages were installed from another lock file.
		if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{"lockfileVersion":3,"x":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx, stdout, _ := newTestContext(proj, false)
		err := runWorktrees(ctx, []string{"add", "cli/sample"})
		if err == nil {
			t.Fatalf("worktree add printed success with no node_modules:\n%s", stdout)
		}
		msg := err.Error() + stdout.String()
		assertContains(t, msg, "node_modules")
		assertContains(t, msg, "npm ci")
	})
}
