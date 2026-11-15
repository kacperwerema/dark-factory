package standin

import (
	"go/ast"
	"go/parser"
	"go/token"
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
// a different body gets its own; each runs its body, and the warm run ran
// nothing of it.
func TestOneCachedFilePerDistinctBody(t *testing.T) {
	cache := privateCache(t)
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
	entries, _ := os.ReadDir(cache)
	if len(entries) != 2 {
		t.Errorf("cache holds %d entries, want 2", len(entries))
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
	if err := os.WriteFile(a, []byte("#!/bin/sh\n"), 0o755); err == nil {
		t.Error("wrote through a cached stand-in")
	}
}

// A cached file that no longer holds its body (changed by hand) is made
// again, and the stand-in still runs the body asked for.
func TestACachedFileThatDiffersIsMadeAgain(t *testing.T) {
	cache := privateCache(t)
	dir := t.TempDir()
	Write(t, filepath.Join(dir, "a"), "echo one\n")
	entries, _ := os.ReadDir(cache)
	if len(entries) != 1 {
		t.Fatalf("cache: %v", entries)
	}
	p := filepath.Join(cache, entries[0].Name())
	os.Chmod(p, 0o755)
	os.WriteFile(p, []byte(Header+"echo two\n"), 0o755)
	Write(t, filepath.Join(dir, "b"), "echo one\n")
	if out, _ := exec.Command(filepath.Join(dir, "b")).Output(); string(out) != "one\n" {
		t.Errorf("b printed %q", out)
	}
}

// A body with its own "#!" line keeps that interpreter, the warm no-op
// right below it, and runs as written.
func TestABodysOwnShebangIsKept(t *testing.T) {
	privateCache(t)
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

// adopted are the test files that write their stand-ins through Write:
// a package's folder (every _test.go in it) or one file.
var adopted = []string{
	"../agentrun",
	"../memguard",
	"../viewer",
	"../checks",
	"../bench",
	"../tracker",
	"../proof",
	"../../cmd/issue-cli",
	"../../cmd/playtest",
}

// Every executable an adopted test writes goes through Write, so it pays
// macOS's first-exec check once per distinct script, never inside a timed
// wait. A new package moves onto the helper by joining adopted. A mode that
// is no stand-in (a folder made read-only) says so in a comment on its line
// or the line above: "not a stand-in: <why>".
func TestAdoptedTestsWriteStandInsThroughTheHelper(t *testing.T) {
	var files []string
	for _, a := range adopted {
		if strings.HasSuffix(a, ".go") {
			files = append(files, a)
			continue
		}
		m, err := filepath.Glob(filepath.Join(a, "*_test.go"))
		if err != nil || len(m) == 0 {
			t.Fatalf("%s: no test files (%v)", a, err)
		}
		files = append(files, m...)
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
