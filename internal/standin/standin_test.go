package standin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard).
func TestMain(m *testing.M) { tmuxguard.Main(m) }

func privateCache(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cache")
	old := Dir
	Dir = func() string { return dir }
	t.Cleanup(func() { Dir = old })
	return dir
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return statIno(fi)
}

// Two tests writing the same body get the same file (one first-exec check);
// a different body gets its own; each runs its body. It uses the machine's
// cache, as every other test does, so its bodies are checked once per
// machine, not once per run: it passes with them cached or not. The cold
// path (made, warmed, renamed in) is TestACachedFileThatDiffersIsMadeAgain's.
func TestOneCachedFilePerDistinctBody(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "ran")
	t.Setenv("RAN_LOG", log)
	body := "echo \"$1\" >> \"$RAN_LOG\"\n"
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	Write(t, a, body)
	Write(t, b, body)
	Write(t, c, body+"exit 3\n")
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the warm run ran the body")
	}
	if inode(t, a) != inode(t, b) {
		t.Error("the same body twice: two files")
	}
	if inode(t, a) == inode(t, c) {
		t.Error("two bodies share a file")
	}
	for path, body := range map[string]string{a: body, c: body + "exit 3\n"} {
		if inode(t, path) != inode(t, cachePath(Script(body))) {
			t.Errorf("%s is no link to its body's cached file", filepath.Base(path))
		}
	}
	if out, err := exec.Command(a, "one").CombinedOutput(); err != nil {
		t.Fatalf("run a: %v %s", err, out)
	}
	err := exec.Command(c, "two").Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
		t.Fatalf("run c: %v", err)
	}
	if got, _ := os.ReadFile(log); string(got) != "one\ntwo\n" {
		t.Errorf("ran %q", got)
	}
	// A link is read-only: writing through it would change every copy.
	// not a stand-in: checks that writing through a link fails
	if err := os.WriteFile(a, []byte("#!/bin/sh\n"), 0o755); err == nil {
		t.Error("wrote through a cached stand-in")
	}
}

// The cold path, in a cache of its own: a cached file that no longer holds
// its body (changed by hand) is made again, warmed without running the
// body, renamed into place and linked in; the stand-in runs the body asked
// for. The spoiled file is written straight into the cache and never run,
// so the test pays macOS's first-exec check once, for the remade file.
func TestACachedFileThatDiffersIsMadeAgain(t *testing.T) {
	cache := privateCache(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "ran")
	t.Setenv("RAN_LOG", log)
	body := "echo one >> \"$RAN_LOG\"\n"
	spoiled := cachePath(Script(body))
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spoiled, []byte(Header+"echo two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "b")
	Write(t, b, body)
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the warm run ran the body")
	}
	if got, _ := os.ReadFile(spoiled); string(got) != Script(body) {
		t.Errorf("the cache still holds %q", got)
	}
	if inode(t, b) != inode(t, spoiled) {
		t.Error("b is no link to the remade cached file")
	}
	entries, _ := os.ReadDir(cache)
	if len(entries) != 1 {
		t.Errorf("cache holds %v, want the one file (no temp file left)", entries)
	}
	if out, err := exec.Command(b).CombinedOutput(); err != nil {
		t.Fatalf("run b: %v %s", err, out)
	}
	if got, _ := os.ReadFile(log); string(got) != "one\n" {
		t.Errorf("ran %q", got)
	}
}

// A body with its own "#!" line keeps that interpreter, the warm no-op
// right below it, and runs as written. The machine's cache, as above.
func TestABodysOwnShebangIsKept(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "ran")
	t.Setenv("RAN_LOG", log)
	path := filepath.Join(dir, "b")
	Write(t, path, "#!/bin/bash\necho \"${BASH_VERSINFO[0]}\" >> \"$RAN_LOG\"\n")
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the warm run ran the body")
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), "#!/bin/bash\n[ -n ") || strings.Count(string(got), "#!") != 1 {
		t.Errorf("script:\n%s", got)
	}
	if out, err := exec.Command(path).CombinedOutput(); err != nil {
		t.Fatalf("run: %v %s", err, out)
	}
	if b, _ := os.ReadFile(log); len(strings.TrimSpace(string(b))) == 0 {
		t.Error("not run by bash")
	}
}

// With no cache folder to make, Write still writes a warmed stand-in.
func TestNoCacheStillWrites(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, nil, 0o644)
	old := Dir
	Dir = func() string { return filepath.Join(blocker, "cache") }
	t.Cleanup(func() { Dir = old })
	path := filepath.Join(t.TempDir(), "x")
	Write(t, path, "echo fresh\n")
	if out, _ := exec.Command(path).Output(); string(out) != "fresh\n" {
		t.Errorf("printed %q", out)
	}
}

// testFiles are every _test.go in the module: the guard below holds them
// all, so a new package's test is on it from its first commit. Dot folders
// (.git, .worktrees) and testdata are skipped; testdata is never compiled.
func testFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Every executable a test in any package writes goes through Write, so it
// pays macOS's first-exec check once per distinct script, never inside a
// timed wait. A mode that is no stand-in (a folder made read-only) says so
// in a comment on its line or the line above: "not a stand-in: <why>".
func TestAdoptedTestsWriteStandInsThroughTheHelper(t *testing.T) {
	files := testFiles(t, "../..")
	if len(files) < 50 {
		t.Fatalf("found only %d test files under the module root", len(files))
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		exempt := map[int]bool{}
		for _, g := range f.Comments {
			for _, c := range g.List {
				if strings.Contains(c.Text, "not a stand-in:") {
					line := fset.Position(c.Pos()).Line
					exempt[line], exempt[line+1] = true, true
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			var mode ast.Expr
			switch {
			case sel.Sel.Name == "WriteFile" && len(call.Args) == 3:
				mode = call.Args[2]
			case sel.Sel.Name == "Chmod" && len(call.Args) == 2:
				mode = call.Args[1]
			default:
				return true
			}
			lit, ok := mode.(*ast.BasicLit)
			if !ok {
				return true
			}
			m, err := strconv.ParseUint(strings.Replace(lit.Value, "0o", "0", 1), 0, 32)
			if err == nil && m&0o111 != 0 && !exempt[fset.Position(call.Pos()).Line] {
				t.Errorf("%s: writes an executable with os.%s; use standin.Write", fset.Position(call.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}
