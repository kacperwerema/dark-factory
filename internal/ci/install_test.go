package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

func TestIsInstall(t *testing.T) {
	for cmd, want := range map[string]bool{
		"npm ci":                         true,
		"npm ci --no-audit --no-fund":    true,
		"npm install":                    true,
		"npm i --prefer-offline":         true,
		"pnpm install --frozen-lockfile": true,
		"yarn":                           true,
		"yarn install --immutable":       true,
		"bun install":                    true,
		"npm install left-pad":           false, // adds a package
		"pnpm add zod":                   false,
		"npm test":                       false,
		"npm ci && npm test":             false, // a chain is not one install
		"cd web; npm ci":                 false,
		"npm ci > log":                   false,
		"NODE_ENV=x npm ci":              false,
		"make install":                   false,
		"go mod download":                false, // no checkout churn: nothing to skip
		"":                               false,
	} {
		if got := IsInstall(cmd); got != want {
			t.Errorf("IsInstall(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// installNpm is a stand-in npm: `npm ci` empties and refills node_modules and
// counts its runs in $NPM_LOG; anything else just passes.
const installNpm = `if [ "$1" = ci ]; then
  rm -rf node_modules && mkdir -p node_modules/pkg && echo x > node_modules/pkg/index.js
  echo ci >> "$NPM_LOG"
fi
`

func installCount(t *testing.T, log string) int {
	t.Helper()
	b, _ := os.ReadFile(log)
	return strings.Count(string(b), "ci\n")
}

// TestInstallSkip: the second run on an unchanged lock file skips npm ci
// and keeps node_modules; a changed lock file, a changed node version, a
// wiped node_modules and --reinstall (ForgetInstalls) install again.
func TestInstallSkip(t *testing.T) {
	root := newRepo(t)
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "npm"), installNpm)
	log := filepath.Join(t.TempDir(), "npm.log")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NPM_LOG", log)
	node := "v22.1.0"
	orig := nodeVersion
	nodeVersion = func(string) string { return node }
	t.Cleanup(func() { nodeVersion = orig })

	writeFile(t, filepath.Join(root, ".gitignore"), "build.out\n.worktrees/\n.ci/\nnode_modules\n")
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"x"}`)
	writeFile(t, filepath.Join(root, "package-lock.json"), `{"lockfileVersion":3}`)
	run(t, root, "git", "add", ".")
	run(t, root, "git", "commit", "-q", "-m", "lock")

	conf := cfg(cmd("npm ci --no-audit"), cmd("test -f node_modules/pkg/index.js"))
	runOnceNow := func(want int, wantSkip bool) {
		t.Helper()
		var out strings.Builder
		runs, _, err := Execute(Options{Root: root, Config: conf, Out: &out})
		if err != nil || len(runs) != 1 {
			t.Fatalf("runs=%d err=%v", len(runs), err)
		}
		r := runs[0]
		if r.State != Green {
			t.Fatalf("state %s: %s", r.State, out.String())
		}
		if got := installCount(t, log); got != want {
			t.Fatalf("npm ci ran %d times, want %d\n%s", got, want, out.String())
		}
		if skipped := r.Commands[0].Skipped != ""; skipped != wantSkip {
			t.Fatalf("skipped=%v, want %v\n%s", skipped, wantSkip, out.String())
		}
		if wantSkip && !strings.Contains(out.String(), "ci run --reinstall") {
			t.Errorf("a skip names the way to install again:\n%s", out.String())
		}
	}
	newHead := func(msg string) {
		t.Helper()
		commit(t, root, msg)
	}

	runOnceNow(1, false)
	newHead("no lock change")
	runOnceNow(1, true)

	writeFile(t, filepath.Join(root, "package-lock.json"), `{"lockfileVersion":3,"packages":{}}`)
	run(t, root, "git", "commit", "-q", "-am", "lock change")
	runOnceNow(2, false)

	node = "v24.0.0"
	newHead("node change")
	runOnceNow(3, false)

	if err := os.RemoveAll(filepath.Join(WorktreePath(root), "node_modules")); err != nil {
		t.Fatal(err)
	}
	newHead("wiped node_modules")
	runOnceNow(4, false)

	if n, err := ForgetInstalls(root); err != nil || n != 1 {
		t.Fatalf("ForgetInstalls = %d, %v; want 1", n, err)
	}
	newHead("reinstall")
	runOnceNow(5, false)
}

// TestInstallStampNotWrittenThroughALink: a node_modules that is a link
// (to the main checkout's) gets no stamp, and its install is never
// skipped on the strength of someone else's folder.
func TestInstallStampNotWrittenThroughALink(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "package-lock.json"), "{}")
	if err := os.Symlink(target, filepath.Join(dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	_, _, key := InstallSkip(dir, "npm ci")
	if key == "" {
		t.Fatal("no key for an install in a dir with a lock file")
	}
	if err := WriteInstallStamp(dir, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, StampName)); err == nil {
		t.Fatal("stamp written through the link")
	}
	if skip, _, _ := InstallSkip(dir, "npm ci"); skip {
		t.Fatal("skipped an install whose node_modules is a link")
	}
}
