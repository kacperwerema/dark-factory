package viewer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The route inventory in docs/API/security.md names every route and its
// class (R/W/O/X). routeInventoryGaps reads the router from routes.go's
// source (the mux patterns in Routes and every case of handleProjectRoutes'
// switch) and reports each route with no row and each row with no route.

// routeAtom is one condition a switch case puts on the request: rest equal
// to, starting with, ending with or holding a literal, or the method.
type routeAtom struct{ kind, lit string }

// routeCase is one way through a case clause: every atom must hold.
type routeCase struct {
	atoms []routeAtom
	pos   string
}

// invRow is one route of the inventory, as a sample path with each
// placeholder filled in, with its methods and class.
type invRow struct {
	path    string // "issue/x/comments" (under /p/<project>/) or "/push/key"
	shown   string // as the doc writes it
	methods map[string]bool
	class   string
}

func routerFromSource(t *testing.T, src string) (mux []string, cases []routeCase) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "routes.go", src, 0)
	if err != nil {
		t.Fatalf("parse routes.go: %v", err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		switch fn.Name.Name {
		case "Routes":
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
					return true
				}
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					p, _ := strconv.Unquote(lit.Value)
					mux = append(mux, p)
				}
				return true
			})
		case "handleProjectRoutes":
			for _, st := range fn.Body.List {
				sw, ok := st.(*ast.SwitchStmt)
				if !ok || sw.Tag != nil {
					continue
				}
				for _, c := range sw.Body.List {
					cc := c.(*ast.CaseClause)
					for _, e := range cc.List {
						for _, conj := range routeDNF(e) {
							cases = append(cases, routeCase{atoms: conj, pos: fset.Position(e.Pos()).String()})
						}
					}
				}
			}
		}
	}
	if len(mux) == 0 || len(cases) == 0 {
		t.Fatalf("found %d mux patterns and %d switch cases in routes.go: has the router moved?", len(mux), len(cases))
	}
	return mux, cases
}

// routeDNF turns a case expression into its ways through (an OR of ANDs).
// Conditions on anything but rest and r.Method (?classic=1 and the like)
// hold either way, so they add no atom.
func routeDNF(e ast.Expr) [][]routeAtom {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return routeDNF(x.X)
	case *ast.BinaryExpr:
		switch x.Op {
		case token.LOR:
			return append(routeDNF(x.X), routeDNF(x.Y)...)
		case token.LAND:
			var out [][]routeAtom
			for _, l := range routeDNF(x.X) {
				for _, r := range routeDNF(x.Y) {
					out = append(out, append(append([]routeAtom(nil), l...), r...))
				}
			}
			return out
		case token.EQL:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == "rest" {
				if lit, ok := x.Y.(*ast.BasicLit); ok {
					v, _ := strconv.Unquote(lit.Value)
					return [][]routeAtom{{{"eq", v}}}
				}
			}
			if sel, ok := x.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Method" {
				if m, ok := x.Y.(*ast.SelectorExpr); ok {
					method := strings.ToUpper(strings.TrimPrefix(m.Sel.Name, "Method"))
					if method == "HEAD" {
						method = "GET" // a GET row covers its HEAD
					}
					return [][]routeAtom{{{"method", method}}}
				}
			}
		}
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if ok && len(x.Args) == 2 {
			if id, ok := x.Args[0].(*ast.Ident); ok && id.Name == "rest" {
				if lit, ok := x.Args[1].(*ast.BasicLit); ok {
					v, _ := strconv.Unquote(lit.Value)
					kind := map[string]string{"HasPrefix": "prefix", "HasSuffix": "suffix", "Contains": "contains"}[sel.Sel.Name]
					if kind != "" {
						return [][]routeAtom{{{kind, v}}}
					}
				}
			}
		}
	}
	return [][]routeAtom{{}}
}

var (
	invPlaceholder = regexp.MustCompile(`<[^>]+>`)
	invTick        = regexp.MustCompile("`([^`]+)`")
	invMethod      = regexp.MustCompile(`[A-Z]+`)
)

// inventoryRows reads the route table: each backticked route in the first
// cell, expanded (a|b alternatives, [/…] optional tails, …/ for issue/<slug>/,
// placeholders filled with x, the query dropped).
func inventoryRows(doc string) []invRow {
	var rows []invRow
	for _, line := range strings.Split(doc, "\n") {
		cells := strings.Split(strings.ReplaceAll(line, `\|`, "\x00"), "|")
		if len(cells) < 5 {
			continue
		}
		class := strings.TrimSpace(cells[3])
		if class != "R" && class != "W" && class != "O" && class != "X" {
			continue
		}
		methods := map[string]bool{}
		for _, m := range invMethod.FindAllString(cells[2], -1) {
			methods[m] = true
		}
		if len(methods) == 0 {
			continue // the class legend
		}
		for _, m := range invTick.FindAllStringSubmatch(cells[1], -1) {
			for _, p := range expandInventoryRoute(strings.ReplaceAll(m[1], "\x00", "|")) {
				rows = append(rows, invRow{path: p, shown: m[1], methods: methods, class: class})
			}
		}
	}
	return rows
}

func expandInventoryRoute(tok string) []string {
	tok, _, _ = strings.Cut(tok, "?")
	var forms []string
	if base, ok := strings.CutSuffix(tok, "[/…]"); ok {
		forms = []string{base, base + "/x"}
	} else {
		forms = []string{tok}
	}
	var out []string
	for _, f := range forms {
		alts := strings.Split(f, "|")
		base := alts[0][:strings.LastIndex(alts[0], "/")+1]
		for i, a := range alts {
			if i > 0 {
				a = base + a
			}
			if rest, ok := strings.CutPrefix(a, "…/"); ok {
				a = "issue/x/" + rest
			}
			a = invPlaceholder.ReplaceAllString(a, "x")
			a = strings.ReplaceAll(a, "…", "x")
			if rest, ok := strings.CutPrefix(a, "/p/x/"); ok {
				a = rest // /p/<project>/ is the project's home: rest ""
			}
			out = append(out, a)
		}
	}
	return out
}

func (c routeCase) matches(r invRow) bool {
	if strings.HasPrefix(r.path, "/") {
		return false
	}
	for _, a := range c.atoms {
		switch a.kind {
		case "eq":
			// rest "docs/" (or "/") is the page without its slash
			if r.path != a.lit && r.path != strings.TrimSuffix(a.lit, "/") {
				return false
			}
		case "prefix":
			if !strings.HasPrefix(r.path, a.lit) {
				return false
			}
		case "suffix":
			if !strings.HasSuffix(r.path, a.lit) {
				return false
			}
		case "contains":
			if !strings.Contains(r.path, a.lit) {
				return false
			}
		case "method":
			if !r.methods[a.lit] {
				return false
			}
		}
	}
	return true
}

func (c routeCase) String() string {
	var parts []string
	for _, a := range c.atoms {
		parts = append(parts, a.kind+" "+strconv.Quote(a.lit))
	}
	return strings.Join(parts, " && ")
}

func muxCovers(pattern, path string) bool {
	if pattern == "/" || !strings.HasSuffix(pattern, "/") {
		return path == pattern
	}
	return strings.HasPrefix(path, pattern)
}

func routeInventoryGaps(t *testing.T, routesSrc, doc string) []string {
	t.Helper()
	mux, cases := routerFromSource(t, routesSrc)
	rows := inventoryRows(doc)
	if len(rows) < 50 {
		t.Fatalf("found %d routes in the inventory, want the whole table", len(rows))
	}
	var gaps []string
	for _, p := range mux {
		if p == "/p/" {
			continue // handleProjectRoutes: its cases are checked one by one
		}
		found := false
		for _, r := range rows {
			if muxCovers(p, r.path) {
				found = true
				break
			}
		}
		if !found {
			gaps = append(gaps, "mux pattern "+strconv.Quote(p)+" has no row")
		}
	}
	for _, c := range cases {
		found := false
		for _, r := range rows {
			if c.matches(r) {
				found = true
				break
			}
		}
		if !found {
			gaps = append(gaps, "handleProjectRoutes case at "+c.pos+" ("+c.String()+") has no row")
		}
	}
	for _, r := range rows {
		found := false
		if strings.HasPrefix(r.path, "/") {
			for _, p := range mux {
				if p != "/p/" && muxCovers(p, r.path) {
					found = true
					break
				}
			}
		} else {
			for _, c := range cases {
				if c.matches(r) {
					found = true
					break
				}
			}
		}
		if !found {
			gaps = append(gaps, "row `"+r.shown+"` ("+r.path+") matches no route")
		}
	}
	sort.Strings(gaps)
	return gaps
}

const routeInventoryHelp = `Each route the router serves needs a row in docs/API/security.md's "Route inventory", classified R (reads), W (writes issue or board files), O (an owner decision: also add it to ownerOnlyRoute and ownerRoutes) or X (starts a process); run make security-check after touching routes.go, origin.go, identity.go, headers.go or the upload code. A row whose route is gone goes too.`

func TestEveryRouteIsInTheSecurityInventory(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(repoPath("docs", "API", "security.md"))
	if err != nil {
		t.Fatal(err)
	}
	if gaps := routeInventoryGaps(t, string(src), string(doc)); len(gaps) > 0 {
		t.Errorf("%s\n\n%s", strings.Join(gaps, "\n"), routeInventoryHelp)
	}
}

// A route added to the router with no row fails the check, and so does a
// row left behind.
func TestRouteInventoryCatchesANewRouteAndAStaleRow(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(repoPath("docs", "API", "security.md"))
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "\tdefault:\n\t\thttp.NotFound(w, r)"
	if !strings.Contains(string(src), anchor) {
		t.Fatal("routes.go's switch has no default case to add a route before")
	}
	withRoute := strings.Replace(string(src), anchor,
		"\tcase rest == \"temporary-test-route\" && r.Method == http.MethodPost:\n\t\thttp.NotFound(w, r)\n"+anchor, 1)
	withRoute = strings.Replace(withRoute, `mux.HandleFunc("/owner", s.handleOwner)`,
		`mux.HandleFunc("/owner", s.handleOwner)`+"\n\tmux.HandleFunc(\"/temporary-top\", s.handleOwner)", 1)
	gaps := strings.Join(routeInventoryGaps(t, withRoute, string(doc)), "\n")
	for _, want := range []string{`eq "temporary-test-route" && method "POST") has no row`, `mux pattern "/temporary-top" has no row`} {
		if !strings.Contains(gaps, want) {
			t.Errorf("a route with no row went unnoticed: want %q in\n%s", want, gaps)
		}
	}

	// The same path documented as GET is still a gap for the POST route.
	wrongMethod := string(doc) + "\n| `temporary-test-route` | GET | R | x |\n"
	if g := strings.Join(routeInventoryGaps(t, withRoute, wrongMethod), "\n"); !strings.Contains(g, `"temporary-test-route" && method "POST") has no row`) {
		t.Errorf("a GET row covered a POST route:\n%s", g)
	}

	stale := string(doc) + "\n| `no-such-route` | GET | R | x |\n"
	if g := strings.Join(routeInventoryGaps(t, string(src), stale), "\n"); !strings.Contains(g, "row `no-such-route` (no-such-route) matches no route") {
		t.Errorf("a row with no route went unnoticed:\n%s", g)
	}
}
