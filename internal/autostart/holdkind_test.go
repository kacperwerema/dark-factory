package autostart

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// The memory line (`!Found || !CanStart || Used >= below`) lives in
// MemoryHolds alone: no other function in the module reads a reading's
// CanStart beside a Used comparison. A function that needs the rule calls
// MemoryHolds, or MachineHoldOf for its kind and text.
func TestOneMemoryLineRule(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (strings.HasPrefix(n, ".") || n == "testdata" || n == "vendor" || n == "issues") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || (fn.Name.Name == "MemoryHolds" && f.Name.Name == "autostart") {
				continue
			}
			canStart, usedCmp := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if n.Sel.Name == "CanStart" {
						canStart = true
					}
				case *ast.BinaryExpr:
					switch n.Op {
					case token.GEQ, token.GTR, token.LSS, token.LEQ:
						for _, side := range []ast.Expr{n.X, n.Y} {
							if s, ok := side.(*ast.SelectorExpr); ok && s.Sel.Name == "Used" {
								usedCmp = true
							}
						}
					}
				}
				return true
			})
			if canStart && usedCmp {
				bad = append(bad, fset.Position(fn.Pos()).String()+" "+fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("these functions rewrite the memory line instead of calling autostart.MemoryHolds (or MachineHoldOf for its kind):\n  %s", strings.Join(bad, "\n  "))
	}
}

// Each machine hold says its kind; only the memory ones are Memory().
func TestMachineHoldKinds(t *testing.T) {
	busy := CPU{Found: true, Avg1: 20, Avg5: 20, Cores: 14}
	for _, c := range []struct {
		mem  Memory
		cpu  CPU
		kind HoldKind
	}{
		{Memory{}, CPU{}, HoldMemGuardMissing},
		{Memory{Found: true, Used: 10}, CPU{}, HoldMemGuard},
		{Memory{Found: true, Used: 60, CanStart: true}, CPU{}, HoldMemory},
		{Memory{Found: true, Used: 10, CanStart: true}, busy, HoldLoad},
		{Memory{Found: true, Used: 10, CanStart: true}, CPU{}, HoldNone},
	} {
		h := MachineHoldOf(c.mem, c.cpu, 50, 0)
		if h.Kind != c.kind || (h.Text == "") != (c.kind == HoldNone) {
			t.Errorf("%+v %+v: %+v, want kind %q", c.mem, c.cpu, h, c.kind)
		}
		if MachineHold(c.mem, c.cpu, 50, 0) != h.Text {
			t.Errorf("MachineHold and MachineHoldOf disagree on %+v", c.mem)
		}
		if MemoryHolds(c.mem, 50) != (c.kind == HoldMemGuardMissing || c.kind.Memory()) {
			t.Errorf("MemoryHolds(%+v) disagrees with kind %q", c.mem, c.kind)
		}
	}
}
