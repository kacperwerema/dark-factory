package jsonfile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fixedTempFindings lists each temp name in src built from the target's
// path and a fixed ".tmp" suffix (path + ".tmp", fmt.Sprintf("%s.tmp…")):
// two writers of one file share that name and clobber each other's temp
// file. A whole-file write goes through gitfile.WriteAtomic, whose temp
// name is unique (os.CreateTemp).
func fixedTempFindings(t *testing.T, path string, src any) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	lit := func(e ast.Expr) (string, bool) {
		bl, ok := e.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(bl.Value)
		return s, err == nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			// A "*" is an os.CreateTemp pattern: that name is unique.
			if s, ok := lit(n.Y); ok && strings.HasPrefix(s, ".tmp") && !strings.Contains(s, "*") {
				out = append(out, fset.Position(n.Pos()).String()+": "+strconv.Quote(s)+" appended to a path")
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Sprintf" || len(n.Args) == 0 {
				return true
			}
			if s, ok := lit(n.Args[0]); ok && strings.HasPrefix(s, "%s.tmp") {
				out = append(out, fset.Position(n.Pos()).String()+": Sprintf("+strconv.Quote(s)+")")
			}
		}
		return true
	})
	return out
}

// TestOneTempName: no non-test code names a temp file path + ".tmp"; whole
// file writes go through gitfile.WriteAtomic.
func TestOneTempName(t *testing.T) {
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
			// gitfile builds the CreateTemp pattern from ".tmp-*": the one unique name.
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(filepath.Dir(path)) == "gitfile" {
				return nil
			}
			scanned++
			for _, bad := range fixedTempFindings(t, path, nil) {
				t.Errorf("%s: write through gitfile.WriteAtomic", bad)
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

func TestOneTempNameGuardCatches(t *testing.T) {
	src := `package x
import ("fmt"; "os")
func a(path string, b []byte) { tmp := path + ".tmp"; os.WriteFile(tmp, b, 0o644) }
func b(path string, b []byte) { os.WriteFile(path+".tmp-1", b, 0o644) }
func c(path string) string { return fmt.Sprintf("%s.tmp-%d", path, 1) }
func d(dir string) { os.CreateTemp(dir, "lib-*.tmp") }
func e(name string) bool { return name == "x.tmp" }
func f(dir, path string) { os.CreateTemp(dir, path+".tmp-*") }
`
	if got := fixedTempFindings(t, "x.go", src); len(got) != 3 {
		t.Errorf("findings = %q, want a, b and c", got)
	}
}
