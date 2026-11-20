package timing

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// waiverMark is the comment that lets one comparison keep its single
// sample: the line it is on or the line above says "one sample: <why>".
const waiverMark = "one sample:"

// A test that times one sample of an operation and holds it to a fixed
// limit fails on a loaded machine whenever a burst lands on that sample:
// it goes through Check.Run instead, which takes the best of several rounds
// against a baseline. A limit is fixed when it is known before anything
// runs, or has a fixed floor (max(300ms, k×base): on a quiet-looking
// baseline it is the floor). Inside a loop, a sample the loop took this
// round and held to such a limit is Check by hand: a table of cases, rounds
// that each fail, or a retry loop (TestSystemsPageFastOnABigProject failed
// on main with its best under the limit, since each of its rounds was a
// hurdle of its own). A poll, whose deadline runs from a start before the
// loop, and a pure ratio against a baseline are not.
func TestEveryTimeLimitGoesThroughTheHelper(t *testing.T) {
	root := "../.."
	dirs := map[string][]string{}
	n := 0
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
		if strings.HasSuffix(path, ".go") {
			dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], path)
			if strings.HasSuffix(path, "_test.go") {
				n++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 50 {
		t.Fatalf("found only %d test files under the module root", n)
	}
	var found []string
	for _, files := range dirs {
		at, err := singleSamples(files)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, at...)
	}
	sort.Strings(found)
	for _, at := range found {
		t.Errorf("%s: holds a timed sample to a fixed limit, alone or in a retry loop by hand; a burst of load on a few samples fails it. "+
			"Use timing.Check{Limit, Ratio, Rounds}.Run(op, base) (internal/timing), "+
			"or say why one sample is right with a %q comment on the line or the one above", at, waiverMark+" <why>")
	}
}

// The guard catches the shapes it is for, and lets rounds, polls, ratios
// and waived lines through.
func TestSingleSampleGuardShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"since against a literal", `
func TestA(t *testing.T) {
	start := time.Now()
	work()
	if d := time.Since(start); d > 2*time.Second { t.Error(d) }
}`, 1},
		{"inline since against a package const", `
const budget = 3 * time.Second
func TestA(t *testing.T) {
	start := time.Now()
	work()
	if time.Since(start) >= budget { t.Error("slow") }
}`, 1},
		{"a local limit that is a constant", `
func TestA(t *testing.T) {
	limit := 500 * time.Millisecond
	took := timing.Of(work)
	if took > limit { t.Error(took) }
}`, 1},
		{"a timer helper's result", `
func timed(f func()) (string, time.Duration) {
	start := time.Now()
	f()
	return "x", time.Since(start)
}
func TestA(t *testing.T) {
	_, d := timed(work)
	if d > time.Second { t.Error(d) }
}`, 1},
		{"a timer method with a named result", `
func (s *srv) get() (body string, took time.Duration) {
	start := time.Now()
	took = time.Since(start)
	return
}
func TestA(t *testing.T) {
	s := &srv{}
	_, took := s.get()
	if took >= time.Second { t.Error(took) }
}`, 1},
		{"a table loop is still one sample each", `
func TestA(t *testing.T) {
	for _, tc := range cases {
		start := time.Now()
		tc.run()
		if time.Since(start) > time.Second { t.Error(tc) }
	}
}`, 1},
		{"a poll deadline", `
func TestA(t *testing.T) {
	start := time.Now()
	for {
		if ready() { return }
		if time.Since(start) > 10*time.Second { t.Fatal("timed out") }
	}
}`, 0},
		{"a poll loop condition", `
func TestA(t *testing.T) {
	start := time.Now()
	for time.Since(start) < 5*time.Second && !ready() {}
}`, 0},
		{"rounds against a fixed limit, by hand", `
func TestA(t *testing.T) {
	for try := 0; try < 3; try++ {
		d := timing.Of(work)
		if d <= time.Second { return }
	}
	t.Error("slow")
}`, 1},
		{"range over a count", `
func TestA(t *testing.T) {
	for range 3 {
		if timing.Of(work) <= time.Second { return }
	}
}`, 1},
		{"rounds that each fail", `
func TestA(t *testing.T) {
	for range 2 {
		took := timing.Of(work)
		if took >= time.Second { t.Errorf("took %v", took) }
	}
}`, 1},
		{"a poll deadline with an else", `
func TestA(t *testing.T) {
	deadline := time.Now()
	for !ready() {
		if time.Since(deadline) < 5*time.Second { continue } else { t.Fatal("timed out") }
	}
}`, 0},
		{"a timer closure", `
func TestA(t *testing.T) {
	call := func() (time.Duration, string) {
		start := time.Now()
		return time.Since(start), "out"
	}
	d, _ := call()
	if d > time.Second { t.Error(d) }
}`, 1},
		{"a table loop with a t.Run", `
func TestA(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if took := timing.Of(tc.run); took > time.Second { t.Error(took) }
		})
	}
}`, 1},
		{"a floored ratio against one baseline", `
func TestA(t *testing.T) {
	base := timing.Of(cheap)
	d := timing.Of(work)
	if d > max(time.Second, 4*base) { t.Error(d) }
}`, 1},
		{"a limit computed at run time", `
func TestA(t *testing.T) {
	limit := max(time.Second, 4*timing.Of(cheap))
	if timing.Of(work) > limit { t.Error("slow") }
}`, 1},
		{"waived on the line above", `
func TestA(t *testing.T) {
	d := timing.Of(work)
	// one sample: the hold proves it never waited 20 s
	if d > time.Second { t.Error(d) }
}`, 0},
		{"waived on the line", `
func TestA(t *testing.T) {
	d := timing.Of(work)
	if d > time.Second { t.Error(d) } // one sample: why
}`, 0},
		{"a date, not a timing", `
func TestA(t *testing.T) {
	if now.Sub(created) > 24*time.Hour { t.Error("old") }
}`, 0},
		{"a retry loop against a floored limit, each round failing", `
func TestA(t *testing.T) {
	for i := 0; i < 4; i++ {
		var tries []string
		for try := 0; try < 3; try++ {
			base := timing.Of(cheap)
			limit := max(300*time.Millisecond, 6*base)
			d := timing.Of(work)
			if d <= limit { tries = nil; break }
			tries = append(tries, "slow")
		}
		if tries != nil { t.Error(tries) }
	}
}`, 1},
		{"a retry loop against a floored limit", `
func TestA(t *testing.T) {
	for try := 0; try < 3; try++ {
		base := timing.Of(cheap)
		if timing.Of(work) <= max(time.Second, 6*base) { return }
	}
	t.Error("slow")
}`, 1},
		{"a pure ratio in a loop", `
func TestA(t *testing.T) {
	for try := 0; try < 3; try++ {
		base := timing.Of(cheap)
		if timing.Of(work) <= 6*base { return }
	}
	t.Error("slow")
}`, 0},
		{"the Check helper", `
func TestA(t *testing.T) {
	r := timing.Check{Limit: time.Second, Ratio: 4}.Run(timing.Timed(work), timing.Timed(cheap))
	if r.Slow { t.Error(r) }
}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "x_test.go")
			if err := os.WriteFile(path, []byte("package x\n"+tc.src+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := singleSamples([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Errorf("found %d (%v), want %d", len(got), got, tc.want)
			}
		})
	}
}

// singleSamples parses one package's files and returns file:line of each
// comparison in a _test.go file that holds a timed sample to a fixed
// limit outside any rounds or poll loop.
func singleSamples(files []string) ([]string, error) {
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, f)
	}
	consts := map[string]bool{}
	timers := map[string]map[int]bool{}
	for _, f := range parsed {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					for _, name := range spec.(*ast.ValueSpec).Names {
						consts[name.Name] = true
					}
				}
			case *ast.FuncDecl:
				if at := timerResults(d.Type, d.Body); len(at) > 0 {
					timers[d.Name.Name] = at
				}
			}
		}
	}
	var found []string
	for i, f := range parsed {
		if !strings.HasSuffix(files[i], "_test.go") {
			continue
		}
		waived := map[int]bool{}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, waiverMark) {
					line := fset.Position(c.Slash).Line
					waived[line] = true
					waived[line+1] = true
				}
			}
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			newScan(consts, timers, func(at token.Pos) {
				line := fset.Position(at).Line
				if !waived[line] {
					found = append(found, fmt.Sprintf("%s:%d", files[i], line))
				}
			}).walk(fn.Body, 0)
		}
	}
	return found, nil
}

// timerResults is which of a function's results are a time.Duration, when
// its body times something with time.Since or timing.Of.
func timerResults(typ *ast.FuncType, body *ast.BlockStmt) map[int]bool {
	if body == nil || typ.Results == nil {
		return nil
	}
	at := map[int]bool{}
	i := 0
	for _, field := range typ.Results.List {
		n := max(1, len(field.Names))
		if isSel(field.Type, "time", "Duration") {
			for k := range n {
				at[i+k] = true
			}
		}
		i += n
	}
	if len(at) == 0 {
		return nil
	}
	times := false
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && (isSel(c.Fun, "time", "Since") || isSel(c.Fun, "timing", "Of")) {
			times = true
		}
		return !times
	})
	if !times {
		return nil
	}
	return at
}

// scan is one test function's names: the loop depth at which each timed
// sample was taken (and each time.Now start), and which hold a constant
// duration.
type scan struct {
	consts map[string]bool
	timers map[string]map[int]bool
	timed  map[string]int
	starts map[string]int
	fixed  map[string]bool
	floor  map[string]bool
	report func(token.Pos)
}

func newScan(consts map[string]bool, timers map[string]map[int]bool, report func(token.Pos)) *scan {
	local := map[string]map[int]bool{}
	for k, v := range timers {
		local[k] = v
	}
	return &scan{consts: consts, timers: local, timed: map[string]int{}, starts: map[string]int{}, fixed: map[string]bool{}, floor: map[string]bool{}, report: report}
}

// walk visits n in source order at loop depth depth.
func (s *scan) walk(n ast.Node, depth int) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ForStmt:
			s.walk(n.Init, depth)
			s.walk(n.Cond, depth+1)
			s.walk(n.Post, depth+1)
			s.walk(n.Body, depth+1)
			return false
		case *ast.RangeStmt:
			s.walk(n.X, depth)
			s.walk(n.Body, depth+1)
			return false
		case *ast.IfStmt:
			s.walk(n.Init, depth)
			s.walk(n.Cond, depth)
			s.walk(n.Body, depth)
			s.walk(n.Else, depth)
			return false
		case *ast.AssignStmt:
			for _, e := range n.Rhs {
				s.walk(e, depth)
			}
			s.assign(n.Lhs, n.Rhs, depth)
			return false
		case *ast.ValueSpec:
			for _, e := range n.Values {
				s.walk(e, depth)
			}
			lhs := make([]ast.Expr, len(n.Names))
			for i, name := range n.Names {
				lhs[i] = name
			}
			s.assign(lhs, n.Values, depth)
			return false
		case *ast.BinaryExpr:
			switch n.Op {
			case token.LSS, token.LEQ, token.GTR, token.GEQ:
				if s.oneSample(n.X, n.Y, depth) || s.oneSample(n.Y, n.X, depth) {
					s.report(n.OpPos)
				}
			}
		}
		return true
	})
}

// oneSample says timed against limit holds a sample to a fixed limit by
// hand: outside any loop, or in a loop that took the sample this round
// (a poll's deadline runs from a start before the loop).
func (s *scan) oneSample(timed, limit ast.Expr, depth int) bool {
	at, ok := s.sampled(timed, depth)
	if !ok || !s.isFixed(limit) && !s.hasFloor(limit) {
		return false
	}
	return depth == 0 || at >= depth
}

// hasFloor says e is a limit with a fixed floor: max(<fixed>, ...), or a
// name holding one.
func (s *scan) hasFloor(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return s.hasFloor(e.X)
	case *ast.Ident:
		return s.floor[e.Name]
	case *ast.CallExpr:
		if fn, ok := e.Fun.(*ast.Ident); ok && fn.Name == "max" {
			for _, a := range e.Args {
				if s.isFixed(a) || s.hasFloor(a) {
					return true
				}
			}
		}
	}
	return false
}

func (s *scan) assign(lhs, rhs []ast.Expr, depth int) {
	set := func(e ast.Expr, at int, timed, fixed bool) {
		id, ok := e.(*ast.Ident)
		if !ok || id.Name == "_" {
			return
		}
		delete(s.timed, id.Name)
		delete(s.starts, id.Name)
		delete(s.timers, id.Name)
		if timed {
			s.timed[id.Name] = at
		}
		s.fixed[id.Name] = fixed
		delete(s.floor, id.Name)
	}
	if len(rhs) == 1 && len(lhs) > 1 {
		res := s.timerCall(rhs[0])
		for i, e := range lhs {
			set(e, depth, res[i], false)
		}
		return
	}
	for i, e := range lhs {
		if i >= len(rhs) {
			continue
		}
		at, timed := s.sampled(rhs[i], depth)
		set(e, at, timed, s.isFixed(rhs[i]))
		id, ok := e.(*ast.Ident)
		if !ok {
			continue
		}
		if s.hasFloor(rhs[i]) {
			s.floor[id.Name] = true
		}
		if c, ok := rhs[i].(*ast.CallExpr); ok && isSel(c.Fun, "time", "Now") {
			s.starts[id.Name] = depth
		}
		if fl, ok := rhs[i].(*ast.FuncLit); ok {
			if res := timerResults(fl.Type, fl.Body); res != nil {
				s.timers[id.Name] = res
			}
		}
	}
}

// timerCall is which results of call e are a timed sample.
func (s *scan) timerCall(e ast.Expr) map[int]bool {
	c, ok := e.(*ast.CallExpr)
	if !ok {
		return nil
	}
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		return s.timers[fn.Name]
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok && (x.Name == "time" || x.Name == "timing") {
			return nil
		}
		return s.timers[fn.Sel.Name]
	}
	return nil
}

// sampled says e is a timed sample and the loop depth it was taken at:
// time.Since(start) dates from start's depth, so a poll's deadline that
// runs from before the loop is not a sample of this round.
func (s *scan) sampled(e ast.Expr, depth int) (int, bool) {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return s.sampled(e.X, depth)
	case *ast.Ident:
		at, ok := s.timed[e.Name]
		return at, ok
	case *ast.CallExpr:
		if isSel(e.Fun, "time", "Since") {
			if len(e.Args) == 1 {
				if id, ok := e.Args[0].(*ast.Ident); ok {
					if at, ok := s.starts[id.Name]; ok {
						return at, true
					}
				}
			}
			return depth, true
		}
		if isSel(e.Fun, "timing", "Of") {
			return depth, true
		}
		if res := s.timerCall(e); len(res) == 1 && res[0] {
			return depth, true
		}
	}
	return 0, false
}

// isFixed says e is a duration known before anything runs: literals, time
// units, constants and names holding only those.
func (s *scan) isFixed(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return s.isFixed(e.X)
	case *ast.Ident:
		return s.consts[e.Name] || s.fixed[e.Name]
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		return ok && x.Name == "time" && e.Sel.Name != "Now"
	case *ast.BinaryExpr:
		return s.isFixed(e.X) && s.isFixed(e.Y)
	case *ast.UnaryExpr:
		return s.isFixed(e.X)
	case *ast.CallExpr:
		return isSel(e.Fun, "time", "Duration") && len(e.Args) == 1 && s.isFixed(e.Args[0])
	}
	return false
}

func isSel(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == pkg
}
