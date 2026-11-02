package viewer

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

// The owner's hard rule (~/CLAUDE.md): never kill-server without env -u TMUX
// and your own socket. A bare kill-server once took down every lead and
// worker on the machine (2026-10-30 07:58). Tests used to rely on TMUX being
// blanked and TMUX_TMPDIR set before a bare tmux call; these checks make every
// test's tmux call name its socket with -S instead.
//
// Go files: a "kill-server" string literal is only allowed as an argument of
// a call that names -S before it, and in a _test.go file every
// exec.Command(Context) running tmux passes -S as its first argument
// (literally, or as append([]string{"-S", …}, …)).
// Shell scripts: a line running kill-server has env -u TMUX and tmux -S.

const tmuxGuardFile = "tmux_socket_guard_test.go"

func TestTmuxCallsInTestsNameTheirSocket(t *testing.T) {
	var problems []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != "." && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if path == tmuxGuardFile {
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".go"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			problems = append(problems, tmuxGoProblems(path, src, strings.HasSuffix(path, "_test.go"))...)
		case strings.HasSuffix(path, ".sh"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			problems = append(problems, tmuxShellProblems(path, string(src))...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// tmuxGoProblems lists the -S and kill-server breaches in one Go file.
func tmuxGoProblems(path string, src []byte, isTest bool) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return []string{path + ": " + err.Error()}
	}
	var problems []string
	allowed := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		seenS := false
		for _, a := range call.Args {
			if strLit(a) == "-S" || startsWithS(a) {
				seenS = true
			}
			if lit, ok := a.(*ast.BasicLit); ok && seenS && strings.Contains(strLit(lit), "kill-server") {
				allowed[lit] = true
			}
		}
		if isTest {
			if rest, ok := tmuxExecArgs(call); ok && !namesSocket(rest) {
				problems = append(problems, fset.Position(call.Pos()).String()+": tmux runs without -S <own socket> as its first argument (use the package's testTmux)")
			}
		}
		if rest, ok := tmuxRunnerArgs(call); ok && !namesSocket(rest) {
			problems = append(problems, fset.Position(call.Pos()).String()+": a runner's tmux without -S <own socket> as its first argument (cmd/playtest: Scenario.Tmux)")
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && strings.Contains(strLit(lit), "kill-server") && !allowed[lit] {
			problems = append(problems, fset.Position(lit.Pos()).String()+": kill-server outside a tmux call that names -S <own socket>")
		}
		return true
	})
	return problems
}

// tmuxExecArgs returns the arguments after the program when call is
// exec.Command or exec.CommandContext running tmux (the literal "tmux" or a
// variable named like tmuxPath).
func tmuxExecArgs(call *ast.CallExpr) ([]ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "exec" {
		return nil, false
	}
	i := 0
	switch sel.Sel.Name {
	case "Command":
	case "CommandContext":
		i = 1
	default:
		return nil, false
	}
	if len(call.Args) <= i {
		return nil, false
	}
	prog := call.Args[i]
	if strLit(prog) == "tmux" {
		return call.Args[i+1:], true
	}
	if id, ok := prog.(*ast.Ident); ok && strings.Contains(strings.ToLower(id.Name), "tmux") {
		return call.Args[i+1:], true
	}
	return nil, false
}

// namesSocket is a tmux argument list that starts with -S.
func namesSocket(args []ast.Expr) bool {
	return len(args) > 0 && (strLit(args[0]) == "-S" || startsWithS(args[0]))
}

// tmuxRunnerArgs returns the arguments after "tmux" when call is a runner
// method x.Run(ctx, "tmux", …), the cmd/playtest Scenario.Run shape: in any
// Go file, test or not, its tmux names the scenario's socket.
func tmuxRunnerArgs(call *ast.CallExpr) ([]ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Run" || len(call.Args) < 2 || strLit(call.Args[1]) != "tmux" {
		return nil, false
	}
	return call.Args[2:], true
}

// startsWithS is append([]string{"-S", …}, …).
func startsWithS(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "append" {
		return false
	}
	lit, ok := call.Args[0].(*ast.CompositeLit)
	return ok && len(lit.Elts) > 0 && strLit(lit.Elts[0]) == "-S"
}

// strLit is the value of a string literal, or "".
func strLit(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// tmuxShellProblems lists the lines of a shell script that run kill-server
// without env -u TMUX and tmux -S.
func tmuxShellProblems(path, src string) []string {
	var problems []string
	for i, line := range strings.Split(src, "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") || !strings.Contains(code, "kill-server") {
			continue
		}
		if !strings.Contains(code, "env -u TMUX ") || !strings.Contains(code, "tmux -S ") {
			problems = append(problems, path+":"+strconv.Itoa(i+1)+": kill-server without env -u TMUX and tmux -S <own socket>")
		}
	}
	return problems
}

// The checks themselves: what they refuse and what they let through.
func TestTmuxGuardCatchesBareCalls(t *testing.T) {
	goCases := []struct {
		name   string
		src    string
		isTest bool
		bad    int
	}{
		{"bare kill-server", `exec.Command("tmux", "kill-server").Run()`, true, 2},
		{"kill-server with -S", `exec.Command("tmux", "-S", sock, "kill-server").Run()`, true, 0},
		{"tmux without -S", `exec.Command("tmux", "list-sessions").Run()`, true, 1},
		{"tmux via append -S", `exec.Command("tmux", append([]string{"-S", sock}, args...)...)`, true, 0},
		{"tmux args spread", `exec.Command("tmux", args...)`, true, 1},
		{"CommandContext without -S", `exec.CommandContext(ctx, "tmux", "ls")`, true, 1},
		{"tmux path variable", `exec.Command(tmuxPath, "has-session")`, true, 1},
		{"wait-for -S is not the socket", `exec.Command("tmux", "wait-for", "-S", "x")`, true, 1},
		{"kill-server through a wrapper", `run("kill-server")`, true, 1},
		{"kill-server in a key list", `_ = []string{"q; tmux kill-server"}`, true, 1},
		{"runner kill-server with -S", `s.Run(ctx, "tmux", "-S", s.TmuxSocket(), "kill-server")`, false, 0},
		{"runner bare kill-server", `s.Run(ctx, "tmux", "kill-server")`, false, 2},
		{"runner tmux without -S", `s.Run(ctx, "tmux", "list-sessions")`, false, 1},
		{"runner tmux via append -S", `s.Run(ctx, "tmux", append([]string{"-S", s.TmuxSocket()}, args...)...)`, false, 0},
		{"non-test tmux call is the code under test", `exec.Command("tmux", "list-sessions")`, false, 0},
		{"LookPath is not a call to tmux", `exec.LookPath("tmux")`, true, 0},
	}
	for _, c := range goCases {
		src := "package x\n\nfunc f() {\n\t" + c.src + "\n}\n"
		got := tmuxGoProblems("x.go", []byte(src), c.isTest)
		if len(got) != c.bad {
			t.Errorf("%s: %d problems %q, want %d", c.name, len(got), got, c.bad)
		}
	}
	if got := tmuxGoProblems("x.go", []byte("package x\n\n// tmux kill-server in a comment\n"), true); len(got) != 0 {
		t.Errorf("comment flagged: %q", got)
	}

	shCases := []struct {
		line string
		bad  int
	}{
		{`env -u TMUX TMUX_TMPDIR="$1" tmux kill-server 2> /dev/null`, 1},
		{`tmux kill-server`, 1},
		{`tmuxs kill-server 2>/dev/null || true`, 1},
		{`tmux -S "$sock" kill-server`, 1},
		{`env -u TMUX tmux -S "$1/tmux-$(id -u)/default" kill-server 2> /dev/null`, 0},
		{`# on exit kill-server runs on it only`, 0},
	}
	for _, c := range shCases {
		if got := tmuxShellProblems("x.sh", "#!/bin/sh\n"+c.line+"\n"); len(got) != c.bad {
			t.Errorf("%q: %q, want %d problems", c.line, got, c.bad)
		}
	}
}

// Every test binary drops $TMUX before its first test: each package with
// tests has a TestMain that calls tmuxguard.Main or tmuxguard.Isolate. That
// covers what the -S checks can't see: the code under test, scripts and
// built binaries a test runs, and a child started with a nil Env (the
// cmd/playtest Scenario.Stop shape that killed the shared server on
// 2026-10-31 and 2026-11-01).
func TestEveryTestBinaryDropsTMUX(t *testing.T) {
	tests := map[string]bool{}   // dirs with a _test.go
	guarded := map[string]bool{} // dirs whose TestMain calls tmuxguard
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		tests[dir] = true
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if testMainCallsTmuxguard(src) {
			guarded[dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for dir := range tests {
		if !guarded[dir] {
			t.Errorf("%s: no TestMain calling tmuxguard.Main(m) (or tmuxguard.Isolate around m.Run): its tests inherit $TMUX", dir)
		}
	}
}

// testMainCallsTmuxguard is a Go file whose TestMain calls tmuxguard.Main
// or tmuxguard.Isolate.
func testMainCallsTmuxguard(src []byte) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		return false
	}
	found := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestMain" || fn.Recv != nil || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "tmuxguard" && (sel.Sel.Name == "Main" || sel.Sel.Name == "Isolate") {
					found = true
				}
			}
			return true
		})
	}
	return found
}

func TestTestMainGuardCheck(t *testing.T) {
	for _, c := range []struct {
		src  string
		want bool
	}{
		{"func TestMain(m *testing.M) { tmuxguard.Main(m) }", true},
		{"func TestMain(m *testing.M) { c := tmuxguard.Isolate(); code := m.Run(); c(); os.Exit(code) }", true},
		{"func TestMain(m *testing.M) { os.Exit(m.Run()) }", false},
		{"func helper() { tmuxguard.Main(nil) }", false},
	} {
		if got := testMainCallsTmuxguard([]byte("package x\n\n" + c.src + "\n")); got != c.want {
			t.Errorf("%s: %v, want %v", c.src, got, c.want)
		}
	}
}

// The Scenario.Stop shape under tmuxguard: $TMUX names a live server (here a
// decoy this test starts on its own socket) and a child with a nil Env runs
// tmux without -S. After Isolate it reaches the private server, never the
// one $TMUX names: the decoy's session is still there.
func TestIsolatedTestsNeverReachTMUX(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "tmd-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	decoy := filepath.Join(dir, "decoy")
	decoyTmux := func(args ...string) *exec.Cmd {
		c := exec.Command("tmux", append([]string{"-S", decoy}, args...)...)
		c.Env = tmuxguard.EnvWithoutTMUX()
		return c
	}
	if out, err := decoyTmux("new-session", "-d", "-s", "decoy", "sleep 60").CombinedOutput(); err != nil {
		t.Skipf("tmux can't start here: %v %s", err, out)
	}
	defer decoyTmux("kill-server").Run()

	t.Setenv("TMUX", decoy+",1,0")
	t.Setenv("TMUX_PANE", "%0")
	t.Setenv("TMUX_TMPDIR", os.Getenv("TMUX_TMPDIR")) // restored after Isolate
	cleanup := tmuxguard.Isolate()
	if os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != "" {
		t.Fatalf("Isolate left TMUX=%q TMUX_PANE=%q", os.Getenv("TMUX"), os.Getenv("TMUX_PANE"))
	}
	// Nil Env: the child inherits this process's environment, as
	// Scenario.Run did for a Scenario built with only Root.
	_ = exec.Command("tmux", "kill-session", "-t", "decoy").Run()
	cleanup()
	if err := decoyTmux("has-session", "-t", "decoy").Run(); err != nil {
		t.Fatalf("a tmux with a nil Env reached the server $TMUX named: %v", err)
	}
}
