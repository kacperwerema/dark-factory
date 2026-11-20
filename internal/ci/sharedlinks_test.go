package ci

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

func TestInstalls(t *testing.T) {
	for cmd, want := range map[string]bool{
		"npm ci":                        true,
		"npm install":                   true,
		"npm i left-pad":                true,
		"npm ci && npm test":            true,
		"cd web && pnpm install":        true,
		"pnpm add zod":                  true,
		"yarn":                          true,
		"bun install":                   true,
		"npm test":                      false,
		"npm run build":                 false,
		"pnpm run lint":                 false,
		"yarn test":                     false,
		"make validate":                 false,
		"go test ./...":                 false,
		"NODE_ENV=test npm ci --silent": true,
	} {
		if got := Installs(cmd); got != want {
			t.Errorf("Installs(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// linkedWorkspace is a main checkout with an npm workspace (root,
// packages/web, packages/api) installed in each folder, and a tree (a
// .git file, as a worktree has) with the same package files whose three
// node_modules link to the main checkout's. npm is a stand-in whose ci
// empties every node_modules of the workspace (through a link too) and
// installs.
func linkedWorkspace(t *testing.T) (main, tree string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main, tree = filepath.Join(root, "main"), filepath.Join(root, "tree")
	files := map[string]string{
		"package.json":              `{"workspaces":["packages/*"]}`,
		"package-lock.json":         `{"lockfileVersion":3}`,
		"packages/web/package.json": `{"name":"web"}`,
		"packages/api/package.json": `{"name":"api"}`,
	}
	for _, base := range []string{main, tree} {
		for rel, body := range files {
			writeAtPath(t, filepath.Join(base, rel), body)
		}
	}
	writeAtPath(t, filepath.Join(tree, ".git"), "gitdir: elsewhere\n")
	for _, d := range []string{".", "packages/web", "packages/api"} {
		writeAtPath(t, filepath.Join(main, d, "node_modules", "dep", "index.js"), "main\n")
		if err := os.Symlink(filepath.Join(main, d, "node_modules"), filepath.Join(tree, d, "node_modules")); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "npm"), `[ "$1" = ci ] || exit 0
for d in . packages/*; do rm -rf "$d"/node_modules/*; mkdir -p "$d/node_modules"; echo x > "$d/node_modules/.installed"; done
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return main, tree
}

func writeAtPath(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertMainKept(t *testing.T, main, out string) {
	t.Helper()
	for _, d := range []string{".", "packages/web", "packages/api"} {
		if _, err := os.Stat(filepath.Join(main, d, "node_modules", "dep", "index.js")); err != nil {
			t.Errorf("the main checkout's %s/node_modules lost its packages: %v\n%s", d, err, out)
		}
		if _, err := os.Stat(filepath.Join(main, d, "node_modules", ".installed")); err == nil {
			t.Errorf("an install wrote into the main checkout's %s/node_modules\n%s", d, out)
		}
	}
}

// TestRunCommandNeverInstallsThroughWorkspaceLinks is what local CI's
// ci.run and prove --run go through (RunCommand), run from the root and
// from a workspace package: a plain install is skipped while the lock file
// and package.json are main's; a chained one, or one after the lock
// changed, gets the tree's own copies first. The main checkout's
// node_modules stay as they were (D-8).
func TestRunCommandNeverInstallsThroughWorkspaceLinks(t *testing.T) {
	run := func(t *testing.T, dir, cmd string) string {
		t.Helper()
		var out bytes.Buffer
		exit, _, err := RunCommand(context.Background(), dir, cmd, 0, "", &out)
		if err != nil || exit != 0 {
			t.Fatalf("%s: exit %d, %v\n%s", cmd, exit, err, out.String())
		}
		return out.String()
	}
	t.Run("unchanged: skipped", func(t *testing.T) {
		main, tree := linkedWorkspace(t)
		for _, dir := range []string{tree, filepath.Join(tree, "packages", "web")} {
			out := run(t, dir, "npm ci")
			if !strings.Contains(out, "skipped: npm ci: node_modules is shared") {
				t.Errorf("no skip in %s:\n%s", dir, out)
			}
			assertMainKept(t, main, out)
		}
		if links := SharedLinks(tree); len(links) != 3 {
			t.Errorf("links = %v, want the 3 still shared", links)
		}
	})
	t.Run("chained: own copies first", func(t *testing.T) {
		main, tree := linkedWorkspace(t)
		out := run(t, tree, "npm ci && npm test")
		assertMainKept(t, main, out)
		if links := SharedLinks(tree); len(links) != 0 {
			t.Errorf("links left = %v", links)
		}
		if _, err := os.Stat(filepath.Join(tree, "packages", "api", "node_modules", ".installed")); err != nil {
			t.Errorf("the install did not run in the tree: %v\n%s", err, out)
		}
	})
	t.Run("lock changed in a package's package.json: own copies first", func(t *testing.T) {
		main, tree := linkedWorkspace(t)
		writeAtPath(t, filepath.Join(tree, "packages", "web", "package.json"), `{"name":"web","dependencies":{"zod":"3"}}`)
		out := run(t, tree, "npm ci")
		if !strings.Contains(out, "before npm ci: node_modules is its own now") {
			t.Errorf("no own-copy line:\n%s", out)
		}
		assertMainKept(t, main, out)
		if _, err := os.Stat(filepath.Join(tree, "node_modules", ".installed")); err != nil {
			t.Errorf("the install did not run in the tree: %v\n%s", err, out)
		}
	})
	t.Run("no install: the links stay", func(t *testing.T) {
		_, tree := linkedWorkspace(t)
		run(t, tree, "npm test")
		if links := SharedLinks(tree); len(links) != 3 {
			t.Errorf("links = %v, want 3", links)
		}
	})
}

// TestOwnCopyFallsBackAndNeverTouchesTheTarget: a clone that fails leaves
// the link and the target as they were.
func TestOwnCopyFallsBackAndNeverTouchesTheTarget(t *testing.T) {
	main, tree := linkedWorkspace(t)
	old := cloneCommand
	t.Cleanup(func() { cloneCommand = old })
	cloneCommand = func(src, dst string) (string, error) { return "", os.ErrPermission }
	if _, err := OwnCopy(filepath.Join(tree, "node_modules")); err == nil {
		t.Fatal("OwnCopy passed with a failing copy")
	}
	if fi, err := os.Lstat(filepath.Join(tree, "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link went: %v", err)
	}
	assertMainKept(t, main, "")
	if _, _, err := PrepareInstall(tree, "npm ci && npm test"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("PrepareInstall = %v, want a refusal", err)
	}
}
