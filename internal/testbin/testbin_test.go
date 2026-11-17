package testbin_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/testbin"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard), and removes the builds after the run.
func TestMain(m *testing.M) { tmuxguard.Main(m) }

const hello = "./internal/testbin/testdata/hello"

// Many tests asking at once, one of them after moving HOME and TMPDIR (as
// a sandboxed test does), get one build and the same file.
func TestPathBuildsOncePerBinary(t *testing.T) {
	before := testbin.Builds()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	var wg sync.WaitGroup
	paths := make([]string, 8)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths[i] = testbin.Path(t, hello)
		}()
	}
	wg.Wait()
	for _, p := range paths[1:] {
		if p != paths[0] {
			t.Fatalf("two paths for one package: %s and %s", paths[0], p)
		}
	}
	if again := testbin.Path(t, "internal/testbin/testdata/hello/"); again != paths[0] {
		t.Errorf("the same package spelt another way built again: %s", again)
	}
	if got := testbin.Builds() - before; got != 1 {
		t.Errorf("%d builds for one package, want 1", got)
	}
	if strings.HasPrefix(paths[0], os.Getenv("TMPDIR")) {
		t.Errorf("built under the test's own TMPDIR, removed after it: %s", paths[0])
	}
	out, err := exec.Command(paths[0]).CombinedOutput()
	if err != nil || string(out) != "hello from testbin\n" {
		t.Fatalf("built binary: %v %q", err, out)
	}
}

func TestLinkIsTheSameFile(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "hello")
	testbin.Link(t, hello, dst)
	a, err := os.Stat(testbin.Path(t, hello))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Errorf("Link copied where a hard link was possible")
	}
}

// fatalTB records a Fatalf instead of stopping the test.
type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper()                        {}
func (f *fatalTB) Fatalf(format string, a ...any) { f.msg = fmt.Sprintf(format, a...) }

func TestPathFailsWithGoBuildsOutput(t *testing.T) {
	for range 2 { // the second ask gets the first one's error, no new build
		f := &fatalTB{TB: t}
		testbin.Path(f, "./internal/testbin/testdata/missing")
		if !strings.Contains(f.msg, "go build ./internal/testbin/testdata/missing") {
			t.Fatalf("want go build's failure, got %q", f.msg)
		}
	}
}

// goBuilds lists the exec.Command and exec.CommandContext calls in src that
// start `go build`, as file:line.
func goBuilds(fset *token.FileSet, f *ast.File) []string {
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
			return true
		}
		args := call.Args
		switch sel.Sel.Name {
		case "Command":
		case "CommandContext":
			if len(args) == 0 {
				return true
			}
			args = args[1:]
		default:
			return true
		}
		var words []string
		for _, a := range args {
			lit, ok := a.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				break
			}
			w, _ := strconv.Unquote(lit.Value)
			words = append(words, w)
		}
		if len(words) >= 2 && words[0] == "go" && words[1] == "build" {
			found = append(found, fset.Position(call.Pos()).String())
		}
		return true
	})
	return found
}

func TestGoBuildDetectorFindsBothForms(t *testing.T) {
	src := `package x
func f() {
	exec.Command("go", "build", "-o", cli, ".")
	exec.CommandContext(ctx, "go", "build", "./cmd/x")
	exec.Command("go", "test", "./...")
	exec.Command("git", "build")
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := goBuilds(fset, f); len(got) != 2 {
		t.Errorf("want the two go builds, got %v", got)
	}
}

// Every test that needs a binary of this module asks testbin.Path for it,
// so each test binary links each one once, not once per test: a go build
// per test cost tens of seconds each on a loaded machine.
func TestEveryGoBuildInATestGoesThroughTestbin(t *testing.T) {
	root := "../.."
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
	if len(files) < 50 {
		t.Fatalf("found only %d test files under the module root", len(files))
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, at := range goBuilds(fset, f) {
			t.Errorf("%s: runs go build itself; use testbin.Path (internal/testbin), which builds once per test binary", at)
		}
	}
}
