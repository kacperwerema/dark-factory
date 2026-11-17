package ci

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// fakeNpm puts a stand-in npm on PATH that does what npm ci does to
// node_modules: empties it (through a link, the folder it points to),
// then installs, leaving a marker.
func fakeNpm(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "npm"), "rm -rf node_modules/\nmkdir -p node_modules\necho installed > node_modules/.installed\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// sharedModules is another checkout's node_modules with one package in it.
func sharedModules(t *testing.T) string {
	t.Helper()
	target := filepath.Join(realTempDir(t), "main", "node_modules")
	if err := os.MkdirAll(filepath.Join(target, "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target, "left-pad", "index.js"), "module.exports = 1\n")
	return target
}

func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func linkModules(t *testing.T, dir, target string) string {
	t.Helper()
	link := filepath.Join(dir, "node_modules")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func assertUntouched(t *testing.T, target string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(target, "left-pad", "index.js")); err != nil {
		t.Fatalf("the link's target lost its files: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".installed")); err == nil {
		t.Fatal("the command ran through the link")
	}
}

// A worktree whose node_modules links to another checkout's: `npm ci` is
// refused before it runs, naming the link and its target, and the target
// keeps its files. Both ways in: the one command prove --run runs, and a
// whole local CI run (which records an error, not a red).
func TestCIRefusesNpmCiThroughSymlinkedNodeModules(t *testing.T) {
	fakeNpm(t)
	t.Run("RunCommand", func(t *testing.T) {
		wt := realTempDir(t)
		target := sharedModules(t)
		link := linkModules(t, wt, target)
		var out strings.Builder
		exit, _, err := RunCommand(context.Background(), wt, "npm ci", 0, "", &out)
		var refusal *NodeModulesRefusal
		if !errors.As(err, &refusal) {
			t.Fatalf("RunCommand(npm ci) = exit %d, err %v; want a NodeModulesRefusal (output %q)", exit, err, out.String())
		}
		for _, want := range []string{link, target} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %s", err, want)
			}
		}
		assertUntouched(t, target)
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the link is gone or changed: %v", err)
		}
	})
	t.Run("Execute", func(t *testing.T) {
		root := newRepo(t)
		writeFile(t, filepath.Join(root, ".gitignore"), "build.out\n.worktrees/\n.ci/\nnode_modules\n")
		run(t, root, "git", "commit", "-qam", "ignore node_modules")
		if _, _, err := Execute(Options{Root: root, Config: cfg(cmd("true"))}); err != nil {
			t.Fatal(err)
		}
		target := sharedModules(t)
		linkModules(t, WorktreePath(root), target)
		commit(t, root, "next")
		runs, _, err := Execute(Options{Root: root, Config: cfg(cmd("npm ci"))})
		if err != nil {
			t.Fatal(err)
		}
		last := runs[len(runs)-1]
		if last.State != Error || !strings.Contains(last.Error, "rm ") || !strings.Contains(last.Error, target) {
			t.Fatalf("run = %s, error %q; want an error naming the target and the fix", last.State, last.Error)
		}
		assertUntouched(t, target)
	})
}

// A real node_modules folder: npm ci runs as before.
func TestCIRunsNpmCiOnRealNodeModules(t *testing.T) {
	fakeNpm(t)
	wt := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(wt, "node_modules", "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	exit, timedOut, err := RunCommand(context.Background(), wt, "npm ci", 0, "", io.Discard)
	if err != nil || exit != 0 || timedOut {
		t.Fatalf("RunCommand(npm ci) = exit %d, timedOut %v, err %v; want it to run", exit, timedOut, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "node_modules", ".installed")); err != nil {
		t.Fatalf("npm ci did not run: %v", err)
	}
}

// The refusal is one line, and the fix in it works: run the rm it names,
// then the command, and the shared folder keeps its files.
func TestSymlinkRefusalNamesTheFix(t *testing.T) {
	fakeNpm(t)
	wt := filepath.Join(realTempDir(t), "my worktree")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	target := sharedModules(t)
	link := linkModules(t, wt, target)
	err := CheckNodeModules(wt, "npm ci")
	if err == nil {
		t.Fatal("no refusal")
	}
	msg := err.Error()
	if strings.Contains(msg, "\n") {
		t.Fatalf("refusal is more than one line: %q", msg)
	}
	_, fix, ok := strings.Cut(msg, "remove the link first: ")
	if !ok {
		t.Fatalf("refusal %q names no fix", msg)
	}
	fix, _, _ = strings.Cut(fix, ", then run it again")
	if !strings.HasPrefix(fix, "rm ") || !strings.Contains(fix, "'"+link+"'") {
		t.Fatalf("fix %q is not rm of the quoted link %s", fix, link)
	}
	exit, _, err := RunCommand(context.Background(), t.TempDir(), fix, 0, "", io.Discard)
	if err != nil || exit != 0 {
		t.Fatalf("running the fix %q: exit %d, %v", fix, exit, err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the fix left the link: %v", err)
	}
	if exit, _, err := RunCommand(context.Background(), wt, "npm ci", 0, "", io.Discard); err != nil || exit != 0 {
		t.Fatalf("npm ci after the fix: exit %d, %v", exit, err)
	}
	if _, err := os.Stat(filepath.Join(target, "left-pad", "index.js")); err != nil {
		t.Fatalf("the shared folder lost its files after the fix: %v", err)
	}
}

// Which command texts delete through the link, and which don't.
func TestCheckNodeModulesForms(t *testing.T) {
	wt := realTempDir(t)
	target := sharedModules(t)
	linkModules(t, wt, target)
	if err := os.MkdirAll(filepath.Join(wt, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := sharedModules(t)
	linkModules(t, filepath.Join(wt, "web"), other)
	plain := filepath.Join(wt, "plain")
	if err := os.MkdirAll(filepath.Join(plain, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	refused := []string{
		"npm ci",
		"npm clean-install",
		"npm ci --ignore-scripts && npm test",
		"make build; npm ci",
		"CI=1 npm ci",
		"env FOO=1 nice -n 5 npm ci",
		"npm install --force",
		"npm i -f",
		"pnpm prune",
		"pnpm install --force",
		"rm -rf node_modules/",
		"rm -rf ./node_modules/*",
		"rm -r -f node_modules/.",
		"npm --prefix web ci",
		"cd web && npm ci",
		"cd plain && cd .. && npm ci",
		"rm -rf node_modules && ln -s " + other + " node_modules && npm ci",
	}
	for _, c := range refused {
		if err := CheckNodeModules(wt, c); err == nil {
			t.Errorf("CheckNodeModules(%q) = nil, want a refusal", c)
		}
	}
	allowed := []string{
		"npm test",
		"npm install",
		"npm run ci",
		"rm node_modules && npm ci",
		"unlink node_modules; npm ci",
		"rm -rf node_modules && npm ci",
		"rm -rf node_modules/.cache",
		"cd plain && npm ci",
		"npm --prefix plain ci",
		"cd $APP && npm ci",
		"echo npm ci",
		"yarn install --frozen-lockfile",
	}
	for _, c := range allowed {
		if err := CheckNodeModules(wt, c); err != nil {
			t.Errorf("CheckNodeModules(%q) = %v, want it allowed", c, err)
		}
	}
	// A broken link has nothing behind it to lose.
	broken := realTempDir(t)
	linkModules(t, broken, filepath.Join(broken, "gone"))
	if err := CheckNodeModules(broken, "npm ci"); err != nil {
		t.Errorf("broken link: %v, want it allowed", err)
	}
}
