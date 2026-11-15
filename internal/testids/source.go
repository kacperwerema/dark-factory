package testids

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"sync"
)

// listNames are the package's lists a source file can fill: IDs (appended
// to in each page's init), FixtureIDs and StubIDs.
var listNames = map[string]bool{"IDs": true, "FixtureIDs": true, "StubIDs": true}

// ParseSource returns the ids one Go file of this package lists: the string
// literals appended to IDs, FixtureIDs or StubIDs, or given as their value
// in a var declaration. Lint reads a project's internal/testids this way,
// so a binary built before an id was added still sees it.
func ParseSource(name string, src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	lits := func(exprs []ast.Expr) {
		for _, e := range exprs {
			if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
				if s, err := strconv.Unquote(b.Value); err == nil {
					out = append(out, s)
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if fn, ok := n.Fun.(*ast.Ident); ok && fn.Name == "append" && len(n.Args) > 0 {
				if list, ok := n.Args[0].(*ast.Ident); ok && listNames[list.Name] {
					lits(n.Args[1:])
				}
			}
		case *ast.ValueSpec:
			for i, name := range n.Names {
				if !listNames[name.Name] || i >= len(n.Values) {
					continue
				}
				if c, ok := n.Values[i].(*ast.CompositeLit); ok {
					lits(c.Elts)
				}
			}
		}
		return true
	})
	return out, nil
}

// Set is one list of ids and patterns with its index: the list built into
// this binary (Builtin) or one read from a project's sources.
type Set struct {
	ids []string
	idx *idIndex
}

// NewSet indexes ids.
func NewSet(ids []string) *Set {
	return &Set{ids: ids, idx: newIndex(ids)}
}

// Builtin is the list this binary was built with.
var Builtin = sync.OnceValue(func() *Set { return NewSet(allIDs()) })

// Len is the number of entries.
func (s *Set) Len() int { return len(s.ids) }
