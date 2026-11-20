package ship

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/standin"
)

// workspaceDirs are the folders with a node_modules in an npm workspace
// with two packages.
var workspaceDirs = []string{".", "packages/web", "packages/api"}

// workspaceFixture is the fixture with an npm workspace on main (root
// package.json and lock, packages/web and packages/api), the main
// checkout's node_modules installed in each of the three folders, the
// branch merged up to it, and the worktree linking all three as dispatch's
// ShareDeps does. npm on PATH is a stand-in whose ci does what npm ci does
// in a workspace: empty the root's and every package's node_modules (rm -rf
// <dir>/node_modules/*, which goes through a link), then install.
func workspaceFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	for file, body := range map[string]string{
		".gitignore":                "node_modules\n",
		"package.json":              `{"name":"app","workspaces":["packages/*"]}` + "\n",
		"package-lock.json":         `{"lockfileVersion":3,"v":1}` + "\n",
		"packages/web/package.json": `{"name":"web"}` + "\n",
		"packages/api/package.json": `{"name":"api"}` + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(f.main, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.main, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, f.main, "add", file)
	}
	git(t, f.main, "commit", "-q", "-m", "feat: an npm workspace")
	git(t, f.wt, "merge", "-q", "-m", MergeSubject("cli/x"), "main")
	for _, d := range workspaceDirs {
		pkg := filepath.Join(f.main, d, "node_modules", "dep-"+strings.ReplaceAll(d, "/", "-"))
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "index.js"), []byte("main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.main, d, "node_modules"), filepath.Join(f.wt, d, "node_modules")); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "npm"), `[ "$1" = ci ] || exit 0
for d in . packages/*; do rm -rf "$d"/node_modules/* "$d"/node_modules/.[!.]*; mkdir -p "$d/node_modules"; echo "$PWD" > "$d/node_modules/.installed"; done
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

// mainUntouched fails unless every node_modules of the main checkout holds
// what was installed there and nothing the stand-in wrote.
func (f *fixture) mainUntouched(out string) {
	f.t.Helper()
	for _, d := range workspaceDirs {
		nm := filepath.Join(f.main, d, "node_modules")
		if _, err := os.Stat(filepath.Join(nm, "dep-"+strings.ReplaceAll(d, "/", "-"), "index.js")); err != nil {
			f.t.Errorf("the main checkout's %s lost its packages: %v\n%s", nm, err, out)
		}
		if _, err := os.Stat(filepath.Join(nm, ".installed")); err == nil {
			f.t.Errorf("an install wrote into the main checkout's %s\n%s", nm, out)
		}
	}
}

// TestShipNeverInstallsThroughWorkspaceLinks: in a workspace whose root
// and packages/* node_modules are links to the main checkout's (D-8, the
// 2026-11-19 23:39 incident), ship's npm ci is skipped while the lock file
// is main's, and after a branch changes it the links become the tree's own
// copies before the install. Either way the main checkout's node_modules
// are as they were.
func TestShipNeverInstallsThroughWorkspaceLinks(t *testing.T) {
	t.Run("lock unchanged: skipped", func(t *testing.T) {
		f := workspaceFixture(t)
		var out bytes.Buffer
		o := f.opts(&out)
		o.Check = []string{"npm ci", f.pass("full")}
		o.Prepare = ci.PrepareInstall
		if res := Run(o); !res.Landed {
			t.Fatalf("not landed: %s\n%s", res.Verdict(), out.String())
		}
		if !strings.Contains(out.String(), "skipped: npm ci: node_modules is shared with the main checkout (node_modules, packages/api/node_modules, packages/web/node_modules)") {
			t.Errorf("no skip line:\n%s", out.String())
		}
		f.mainUntouched(out.String())
		for _, d := range workspaceDirs {
			if fi, err := os.Lstat(filepath.Join(f.wt, d, "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("%s is no longer shared: %v", d, err)
			}
		}
	})

	t.Run("lock changed: own copies, then the install", func(t *testing.T) {
		f := workspaceFixture(t)
		f.commit(f.wt, "package-lock.json", `{"lockfileVersion":3,"v":2}`+"\n", "feat: a new dependency")
		var out bytes.Buffer
		o := f.opts(&out)
		o.Check = []string{"npm ci", f.pass("full")}
		o.Prepare = ci.PrepareInstall
		if res := Run(o); !res.Landed {
			t.Fatalf("not landed: %s\n%s", res.Verdict(), out.String())
		}
		if !strings.Contains(out.String(), "before npm ci: node_modules is its own now") {
			t.Errorf("no own-copy line:\n%s", out.String())
		}
		f.mainUntouched(out.String())
		for _, d := range workspaceDirs {
			nm := filepath.Join(f.wt, d, "node_modules")
			if fi, err := os.Lstat(nm); err != nil || !fi.IsDir() {
				t.Errorf("%s is not the tree's own folder: %v", nm, err)
			}
			if _, err := os.Stat(filepath.Join(nm, ".installed")); err != nil {
				t.Errorf("the install did not run in %s: %v", nm, err)
			}
		}
	})

	t.Run("without Prepare the stand-in empties main (the incident)", func(t *testing.T) {
		f := workspaceFixture(t)
		var out bytes.Buffer
		o := f.opts(&out)
		o.Check = []string{"npm ci"}
		Run(o)
		if _, err := os.Stat(filepath.Join(f.main, "packages", "web", "node_modules", "dep-packages-web", "index.js")); err == nil {
			t.Fatal("the stand-in did not go through the links: this test would prove nothing")
		}
	})
}
