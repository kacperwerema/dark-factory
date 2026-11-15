package codemap

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Build reads the tree at root: the module path from go.mod, then every
// non-test .go file outside testdata/, vendor/ and dot or underscore
// folders (a nested module is skipped), parsed for its imports only. A
// file with a //go:build ignore line is left out. With no go.mod the map
// has no packages and maps files by paths: alone.
func Build(root string, wb tracker.WorkbenchConfig) (*Map, error) {
	module := modulePath(filepath.Join(root, "go.mod"))
	if module == "" {
		m := New(wb, nil, nil)
		return m, nil
	}
	parses.Add(1)
	fset := token.NewFileSet()
	pkgs := map[string]*Package{}
	var files []*File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p == root {
				return nil
			}
			if name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			if rel, err := filepath.Rel(root, p); err == nil && excluded(wb.CodeMapExclude, filepath.ToSlash(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		f, err := parser.ParseFile(fset, p, src, parser.ImportsOnly|parser.ParseComments)
		if err != nil || f.Name == nil {
			return nil
		}
		if ignored(f.Comments, f.Package) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		dir := path.Dir(rel)
		pk := pkgs[dir]
		if pk == nil {
			pk = &Package{Dir: dir, Name: f.Name.Name}
			pkgs[dir] = pk
		}
		lines := bytes.Count(src, []byte("\n"))
		if len(src) > 0 && src[len(src)-1] != '\n' {
			lines++
		}
		pk.Lines += lines
		pk.Files = append(pk.Files, rel)
		pk.Main = pk.Main || f.Name.Name == "main"
		file := &File{Path: rel, Package: dir, Lines: lines}
		for _, im := range f.Imports {
			ip, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				continue
			}
			var to string
			switch {
			case ip == module:
				to = "."
			case strings.HasPrefix(ip, module+"/"):
				to = strings.TrimPrefix(ip, module+"/")
			default:
				continue
			}
			file.Imports = append(file.Imports, Import{Package: to, Line: fset.Position(im.Pos()).Line})
			if !contains(pk.Imports, to) {
				pk.Imports = append(pk.Imports, to)
			}
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	list := make([]*Package, 0, len(pkgs))
	for _, p := range pkgs {
		sort.Strings(p.Files)
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Dir < list[j].Dir })
	m := New(wb, list, files)
	m.Module, m.HasGo = module, true
	return m, nil
}

// excluded reports whether the folder dir matches a codemap_exclude glob
// (as a folder, or as a file inside it).
func excluded(globs []string, dir string) bool {
	for _, g := range globs {
		if tracker.MatchGlob(g, dir) || tracker.MatchGlob(g, dir+"/") || tracker.MatchGlob(strings.TrimSuffix(strings.TrimSuffix(g, "**"), "/"), dir) {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// ignored reports a //go:build ignore line before the package clause.
func ignored(groups []*ast.CommentGroup, pkg token.Pos) bool {
	for _, g := range groups {
		if g.Pos() > pkg {
			break
		}
		for _, c := range g.List {
			t := strings.TrimSpace(c.Text)
			if strings.HasPrefix(t, "//go:build") && strings.Contains(" "+strings.TrimPrefix(t, "//go:build")+" ", " ignore ") {
				return true
			}
		}
	}
	return false
}

func modulePath(gomod string) string {
	f, err := os.Open(gomod)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// parses counts Build's tree walks (TestCodemapParsesOncePerHead).
var parses counter

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) Add(n int) { c.mu.Lock(); c.n += n; c.mu.Unlock() }
func (c *counter) Get() int  { c.mu.Lock(); defer c.mu.Unlock(); return c.n }

// Parses is how many times Build walked a tree in this process.
func Parses() int { return parses.Get() }

var cache = struct {
	sync.Mutex
	byRoot map[string]cached
}{byRoot: map[string]cached{}}

type cached struct {
	key string
	m   *Map
}

// Load is Build kept per root and key: a caller passes what changes the
// map (main's head and workflow.yaml's stat), and a second Load with the
// same key returns the same map without reading the tree.
func Load(root, key string, wb tracker.WorkbenchConfig) (*Map, error) {
	cache.Lock()
	c, ok := cache.byRoot[root]
	cache.Unlock()
	if ok && c.key == key {
		return c.m, nil
	}
	m, err := Build(root, wb)
	if err != nil {
		return nil, err
	}
	cache.Lock()
	cache.byRoot[root] = cached{key: key, m: m}
	cache.Unlock()
	return m, nil
}

// ForProject is the project's map from its checkout, cached by main's head
// and the workflow file's size and mtime; nil when the workflow has no
// contexts.
func ForProject(p *tracker.Project, wf *tracker.WorkflowConfig) (*Map, string, error) {
	if wf == nil || !Configured(wf.Workbench) {
		return nil, "", nil
	}
	root := p.Root()
	_, sha, _ := tracker.MainHead(root)
	key := sha
	if st, err := os.Stat(p.WorkflowPath()); err == nil {
		key += "|" + strconv.FormatInt(st.Size(), 10) + "|" + strconv.FormatInt(st.ModTime().UnixNano(), 10)
	}
	m, err := Load(root, key, wf.Workbench)
	return m, sha, err
}
