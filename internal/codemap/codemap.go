// Package codemap is the code map: a repository's Go packages grouped into
// the contexts of workbench.contexts, the contexts stacked in the layers of
// workbench.layers, and the import graph between them read from the source
// with go/parser (no go list, no toolchain). It answers which imports point
// up a layer and what a change reaches through reverse imports
// (docs/workbench-files.md#code-map).
package codemap

import (
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Package is one Go package of the repository.
type Package struct {
	Dir     string   `json:"dir"`            // repo-relative slash path, "internal/tracker"
	Name    string   `json:"name"`           // package clause; "main" is a binary
	Lines   int      `json:"lines"`          // lines of its non-test files
	Files   []string `json:"files"`          // its non-test .go files, repo-relative
	Imports []string `json:"imports"`        // the repository's packages it imports (dirs)
	Context string   `json:"context"`        // the owning context's key, "" when unmapped
	Up      []string `json:"up,omitempty"`   // imported packages a layer above its own
	Main    bool     `json:"main,omitempty"` // a binary
}

// File is one non-test .go file and the repository packages it imports.
type File struct {
	Path    string   `json:"path"`
	Package string   `json:"package"`
	Lines   int      `json:"lines"`
	Imports []Import `json:"imports"`
}

// Import is one import of a repository package, with its line.
type Import struct {
	Package string `json:"package"`
	Line    int    `json:"line"`
}

// Context is one workbench.contexts entry with what the map found for it.
type Context struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Layer    string   `json:"layer"`
	Level    int      `json:"level"` // the layer's index, 0 at the bottom; -1 when layer: names none
	Shared   bool     `json:"shared,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Packages []string `json:"packages"` // the packages it owns, biggest first
	Lines    int      `json:"lines"`
	Paths    []string `json:"paths,omitempty"`
	Leaks    []Leak   `json:"leaks,omitempty"`
	Patterns []string `json:"-"` // packages: as written
}

// Leak is a known upward import, "from -> to".
type Leak struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Edge is an upward import: From's package imports To's, To's layer above.
type Edge struct {
	From, To         string // package dirs
	FromCtx, ToCtx   string // context keys
	File             string // the first file that imports it
	Line             int
	Known            bool // listed in the From context's leaks:
	FromName, ToName string
}

// Map is the built code map.
type Map struct {
	Module   string              `json:"module,omitempty"`
	Layers   []Layer             `json:"layers"`
	Contexts []*Context          `json:"contexts"`
	Packages map[string]*Package `json:"packages"`
	Files    map[string]*File    `json:"-"`
	// HasGo is false for a project with no go.mod: the map is paths only.
	HasGo bool `json:"has_go"`

	byKey map[string]*Context
	rev   map[string][]string
}

// Layer is a layer and its index.
type Layer struct {
	Name   string `json:"name"`
	Level  int    `json:"level"`
	Shared bool   `json:"shared,omitempty"`
}

// Configured reports whether the workbench block has a code map.
func Configured(wb tracker.WorkbenchConfig) bool {
	return len(wb.Contexts) > 0
}

// New builds a map from packages and files already read (Build reads them
// from a tree; tests give them directly). Each package goes to the context
// whose packages: entry matches it most closely: an exact dir beats a
// dir/... one, a longer dir/... beats a shorter, and file order breaks a
// tie.
func New(wb tracker.WorkbenchConfig, pkgs []*Package, files []*File) *Map {
	m := &Map{Packages: map[string]*Package{}, Files: map[string]*File{}, byKey: map[string]*Context{}, rev: map[string][]string{}}
	level := map[string]Layer{}
	for i, l := range wb.Layers {
		ly := Layer{Name: l.Name, Level: i, Shared: l.Shared}
		m.Layers = append(m.Layers, ly)
		if _, dup := level[l.Name]; !dup {
			level[l.Name] = ly
		}
	}
	for _, c := range wb.Contexts {
		ctx := &Context{Key: c.ContextKey(), Name: c.Name, Layer: c.Layer, Level: -1, Summary: c.Summary, Paths: c.Paths, Patterns: c.Packages}
		if ly, ok := level[c.Layer]; ok {
			ctx.Level, ctx.Shared = ly.Level, ly.Shared
		}
		for _, l := range c.Leaks {
			if from, to, ok := ParseLeak(l); ok {
				ctx.Leaks = append(ctx.Leaks, Leak{From: from, To: to})
			}
		}
		m.Contexts = append(m.Contexts, ctx)
		if _, dup := m.byKey[ctx.Key]; !dup {
			m.byKey[ctx.Key] = ctx
		}
	}
	for _, p := range pkgs {
		m.Packages[p.Dir] = p
	}
	for _, f := range files {
		m.Files[f.Path] = f
	}
	for _, p := range pkgs {
		var keep []string
		for _, d := range p.Imports {
			if _, ok := m.Packages[d]; ok && d != p.Dir {
				keep = append(keep, d)
				m.rev[d] = append(m.rev[d], p.Dir)
			}
		}
		sort.Strings(keep)
		p.Imports = keep
		if c := m.owner(p.Dir); c != nil {
			p.Context = c.Key
			c.Packages = append(c.Packages, p.Dir)
			c.Lines += p.Lines
		}
	}
	for _, c := range m.Contexts {
		sort.Slice(c.Packages, func(i, j int) bool {
			a, b := m.Packages[c.Packages[i]], m.Packages[c.Packages[j]]
			if a.Lines != b.Lines {
				return a.Lines > b.Lines
			}
			return a.Dir < b.Dir
		})
	}
	for _, p := range pkgs {
		p.Up = nil
		for _, d := range p.Imports {
			if m.upward(p.Dir, d) {
				p.Up = append(p.Up, d)
			}
		}
	}
	return m
}

// ParseLeak splits "from -> to".
func ParseLeak(s string) (from, to string, ok bool) {
	a, b, found := strings.Cut(s, "->")
	from, to = cleanDir(a), cleanDir(b)
	return from, to, found && from != "" && to != ""
}

func cleanDir(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "./")
	return strings.TrimSuffix(s, "/")
}

// Match reports how closely the packages: entry pattern matches dir: 0 for
// no match, 1+len for a dir/... match, and a higher score for an exact one.
func Match(pattern, dir string) int {
	p := cleanDir(pattern)
	if p == "" {
		return 0
	}
	if base, ok := strings.CutSuffix(p, "/..."); ok {
		if base == "." || dir == base || strings.HasPrefix(dir, base+"/") {
			return 1 + len(base)
		}
		return 0
	}
	if p == dir {
		return 1 << 20
	}
	return 0
}

// owner is the context whose packages: match dir best.
func (m *Map) owner(dir string) *Context {
	var best *Context
	score := 0
	for _, c := range m.Contexts {
		for _, pat := range c.Patterns {
			if s := Match(pat, dir); s > score {
				best, score = c, s
			}
		}
	}
	return best
}

// Claims lists every context whose packages: match dir at the best score:
// more than one is a package in two contexts.
func (m *Map) Claims(dir string) []string {
	score := 0
	var out []string
	for _, c := range m.Contexts {
		s := 0
		for _, pat := range c.Patterns {
			if v := Match(pat, dir); v > s {
				s = v
			}
		}
		switch {
		case s == 0:
		case s > score:
			score, out = s, []string{c.Key}
		case s == score:
			out = append(out, c.Key)
		}
	}
	return out
}

// Context is the context with key, nil when none.
func (m *Map) Context(key string) *Context { return m.byKey[key] }

// ContextOf is the package's context, nil when unmapped.
func (m *Map) ContextOf(dir string) *Context {
	if p := m.Packages[dir]; p != nil {
		return m.byKey[p.Context]
	}
	return nil
}

func (m *Map) upward(from, to string) bool {
	a, b := m.ContextOf(from), m.ContextOf(to)
	return a != nil && b != nil && a.Level >= 0 && b.Level > a.Level
}

// Top is the highest layer's index, -1 with no layers.
func (m *Map) Top() int { return len(m.Layers) - 1 }

// PackageDirs lists every package dir, sorted.
func (m *Map) PackageDirs() []string {
	out := make([]string, 0, len(m.Packages))
	for d := range m.Packages {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Unmapped lists the packages no context lists.
func (m *Map) Unmapped() []string {
	var out []string
	for _, d := range m.PackageDirs() {
		if m.Packages[d].Context == "" {
			out = append(out, d)
		}
	}
	return out
}

// Upward lists every import that points up a layer, unknown ones first,
// each with the first file and line that imports it.
func (m *Map) Upward() []Edge {
	var out []Edge
	for _, d := range m.PackageDirs() {
		p := m.Packages[d]
		for _, to := range p.Up {
			a, b := m.ContextOf(d), m.ContextOf(to)
			e := Edge{From: d, To: to, FromCtx: a.Key, ToCtx: b.Key, FromName: a.Name, ToName: b.Name}
			for _, l := range a.Leaks {
				if l.From == d && l.To == to {
					e.Known = true
				}
			}
			e.File, e.Line = m.firstImport(d, to)
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return !out[i].Known && out[j].Known })
	return out
}

func (m *Map) firstImport(pkg, to string) (string, int) {
	p := m.Packages[pkg]
	for _, f := range p.Files {
		if fl := m.Files[f]; fl != nil {
			for _, im := range fl.Imports {
				if im.Package == to {
					return f, im.Line
				}
			}
		}
	}
	return "", 0
}

// StaleLeaks lists leaks: entries whose import no longer happens.
func (m *Map) StaleLeaks() []Leak {
	var out []Leak
	for _, c := range m.Contexts {
		for _, l := range c.Leaks {
			p := m.Packages[l.From]
			found := false
			if p != nil {
				for _, d := range p.Imports {
					found = found || d == l.To
				}
			}
			if !found {
				out = append(out, l)
			}
		}
	}
	return out
}

// Count is the number of package imports from one context to another.
type Count struct {
	From, To string // context keys
	N        int
	Up       bool
}

// Between counts package imports between contexts (a package importing
// three of another context's packages counts three), in context order.
func (m *Map) Between() []Count {
	idx := map[string]int{}
	for i, c := range m.Contexts {
		if _, ok := idx[c.Key]; !ok {
			idx[c.Key] = i
		}
	}
	n := map[[2]string]int{}
	for _, p := range m.Packages {
		for _, d := range p.Imports {
			a, b := p.Context, m.Packages[d].Context
			if a != "" && b != "" && a != b {
				n[[2]string{a, b}]++
			}
		}
	}
	var out []Count
	for k, v := range n {
		out = append(out, Count{From: k[0], To: k[1], N: v, Up: m.byKey[k[1]].Level > m.byKey[k[0]].Level && m.byKey[k[0]].Level >= 0})
	}
	sort.Slice(out, func(i, j int) bool {
		if idx[out[i].From] != idx[out[j].From] {
			return idx[out[i].From] < idx[out[j].From]
		}
		return idx[out[i].To] < idx[out[j].To]
	})
	return out
}

// Reaches is every package that imports dir, directly or not.
func (m *Map) Reaches(dirs ...string) []string {
	return m.closure(dirs, func(d string) []string { return m.rev[d] })
}

// DependsOn is every package dir imports, directly or not.
func (m *Map) DependsOn(dirs ...string) []string {
	return m.closure(dirs, func(d string) []string {
		if p := m.Packages[d]; p != nil {
			return p.Imports
		}
		return nil
	})
}

func (m *Map) closure(start []string, next func(string) []string) []string {
	seen := map[string]bool{}
	stack := append([]string(nil), start...)
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, y := range next(x) {
			if !seen[y] {
				seen[y] = true
				stack = append(stack, y)
			}
		}
	}
	for _, s := range start {
		delete(seen, s)
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Importers is the number of packages that import dir directly.
func (m *Map) Importers(dir string) int { return len(m.rev[dir]) }

// IsSurface reports whether the package sits in the top layer: its files
// each get the contexts they import (FileContexts).
func (m *Map) IsSurface(dir string) bool {
	c := m.ContextOf(dir)
	return c != nil && c.Level >= 0 && c.Level == m.Top()
}

// FileContexts are the contexts a surface file works in: the contexts of
// the packages it imports, without shared layers' and its own, in context
// order. Nil for a file the map doesn't know.
func (m *Map) FileContexts(path string) []string {
	f := m.Files[path]
	if f == nil {
		return nil
	}
	own := m.Packages[f.Package].Context
	got := map[string]bool{}
	for _, im := range f.Imports {
		c := m.ContextOf(im.Package)
		if c != nil && !c.Shared && c.Key != own {
			got[c.Key] = true
		}
	}
	return m.ordered(got)
}

func (m *Map) ordered(set map[string]bool) []string {
	var out []string
	done := map[string]bool{}
	for _, c := range m.Contexts {
		if set[c.Key] && !done[c.Key] {
			done[c.Key] = true
			out = append(out, c.Key)
		}
	}
	return out
}

// PathContexts are the contexts whose paths: match the repo-relative file.
func (m *Map) PathContexts(file string) []string {
	got := map[string]bool{}
	for _, c := range m.Contexts {
		for _, g := range c.Paths {
			if tracker.MatchGlob(g, file) {
				got[c.Key] = true
			}
		}
	}
	return m.ordered(got)
}

// ContextName is the context's name, the key itself when unknown.
func (m *Map) ContextName(key string) string {
	if c := m.byKey[key]; c != nil {
		return c.Name
	}
	return key
}

// SurfaceFiles lists, for a context, the surface files that work in it
// with their line counts, biggest first.
func (m *Map) SurfaceFiles(key string) []*File {
	var out []*File
	for _, f := range m.Files {
		if !m.IsSurface(f.Package) {
			continue
		}
		for _, k := range m.FileContexts(f.Path) {
			if k == key {
				out = append(out, f)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Path < out[j].Path
	})
	return out
}
