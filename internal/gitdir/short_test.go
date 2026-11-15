package gitdir

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// shaName is a variable that holds a commit hash.
var shaName = regexp.MustCompile(`(?i)sha$|commit$|^head$|^full$`)

// precisionVerb is a format verb that cuts a string to N bytes (%.7s), the
// printf way of cutting a hash by hand.
var precisionVerb = regexp.MustCompile(`%[-+# 0]*[0-9]*\.[0-9]+s`)

func litInt(e ast.Expr) (int, bool) {
	l, ok := e.(*ast.BasicLit)
	if !ok || l.Kind != token.INT {
		return 0, false
	}
	n, err := strconv.Atoi(l.Value)
	return n, err == nil
}

func exprName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

// shortFindings lists what in src cuts a hash by itself: a slice of a name
// that holds a hash, a format string with a %.Ns verb, or a func(string) string that only cuts its argument
// (if len(s) > 9 { return s[:9] }; return s), whatever it is called.
func shortFindings(t *testing.T, path string, src any) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	cutter := func(typ *ast.FuncType, body *ast.BlockStmt) bool {
		if body == nil || len(body.List) != 2 || typ.Params == nil || len(typ.Params.List) != 1 || len(typ.Params.List[0].Names) != 1 {
			return false
		}
		if exprName(typ.Params.List[0].Type) != "string" || typ.Results == nil || len(typ.Results.List) != 1 {
			return false
		}
		param := typ.Params.List[0].Names[0].Name
		cond, ok := body.List[0].(*ast.IfStmt)
		if !ok || len(cond.Body.List) != 1 {
			return false
		}
		ret, ok := cond.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return false
		}
		cut, ok := ret.Results[0].(*ast.SliceExpr)
		if !ok || cut.Low != nil || exprName(cut.X) != param {
			return false
		}
		_, lit := litInt(cut.High)
		return lit
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Recv == nil && cutter(n.Type, n.Body) {
				out = append(out, fset.Position(n.Pos()).String()+": func "+n.Name.Name)
			}
		case *ast.FuncLit:
			if cutter(n.Type, n.Body) {
				out = append(out, fset.Position(n.Pos()).String()+": a func that cuts its argument")
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				if v, err := strconv.Unquote(n.Value); err == nil && precisionVerb.MatchString(strings.ReplaceAll(v, "%%", "")) {
					out = append(out, fset.Position(n.Pos()).String()+": "+precisionVerb.FindString(strings.ReplaceAll(v, "%%", ""))+" cuts by hand")
				}
			}
		case *ast.SliceExpr:
			if n.High != nil && n.Low == nil && shaName.MatchString(exprName(n.X)) {
				if _, lit := litInt(n.High); lit {
					out = append(out, fset.Position(n.Pos()).String()+": "+exprName(n.X)+" cut by hand")
				}
			}
		}
		return true
	})
	return out
}

// TestOneShortSHA: every hash is shown at one length, cut in one place. A
// cut of something else with the same shape goes in notAHash, with why.
func TestOneShortSHA(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"abc":                   "abc",
		"abcdef1":               "abcdef1",
		"abcdef12":              "abcdef1",
		strings.Repeat("a", 40): "aaaaaaa",
	} {
		if got := ShortSHA(in); got != want {
			t.Errorf("ShortSHA(%q) = %q, want %q", in, got, want)
		}
	}
	notAHash := map[string]string{}
	root := filepath.Join("..", "..")
	var scanned int
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if err == nil && d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return err
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "short.go" {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			scanned++
			if _, ok := notAHash[filepath.ToSlash(rel)]; ok {
				return nil
			}
			for _, bad := range shortFindings(t, path, nil) {
				t.Errorf("%s: use gitdir.ShortSHA", bad)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files: the walk is broken", scanned)
	}
}

func TestOneShortSHAGuardCatches(t *testing.T) {
	src := `package x
func anyName(s string) string {
	if len(s) > 9 {
		return s[:9]
	}
	return s
}
var f = func(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
func g(sha string, r struct{ Commit string }) string { return sha[:8] + r.Commit[:12] }
func keep(name string, b []byte) string { return name[:3] + string(b[:2]) }
var msg = fmt.Sprintf("removed in %.7s", from) + fmt.Sprintf("%-10.8s|%5.2f", a, b) + fmt.Sprintf("%s|%%.7s|%.2f", c, d)
`
	if got := shortFindings(t, "x.go", src); len(got) != 6 {
		t.Errorf("findings = %q, want 6", got)
	}
}
