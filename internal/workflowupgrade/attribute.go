package workflowupgrade

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// What makes a change to a shared source file an entry's own, finer than
// the file (docs/Workflow/overview.md, "Keeping the catalog complete"):
//
//   - `file.go#Symbol`: only that declaration counts. When the entry's
//     detect: has rule_in: names and the declaration switches on them (the
//     rule switch in workflow_validate.go's checkRule), only the switch's
//     cases for those names, and everything outside the switch, count: a
//     case for another entry's rule is that entry's change.
//   - a bundled template (cmd/issue-cli/templates/workflow/*.yaml): only
//     the parts the entry's detect: probes see count, each match widened to
//     its list item (the whole status, the whole transition). The template
//     still needs its stamp bump for any change.
//   - a TextSources file: a change to its string literals and comments only
//     (engine-built briefing text) is no entry's change.
//
// Anything the finer match can't read (a file that doesn't parse, an entry
// without detect:) falls back to the whole file, so the check stays strict.

// ruleNames are the rule names an entry's detect: probes match by rule_in.
func ruleNames(e Entry) map[string]bool {
	out := map[string]bool{}
	for _, p := range e.Detect {
		for _, r := range p.RuleIn {
			out[strings.TrimSpace(r)] = true
		}
	}
	return out
}

// stringConsts maps the top-level string constants of Go source to their
// values.
func stringConsts(src []byte, into map[string]string) {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		return
	}
	for _, d := range file.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						into[n.Name] = s
					}
				}
			}
		}
	}
}

// caseNames are the string values a case clause matches; ok is false for
// the default clause or when one value isn't a string literal or a known
// string constant.
func caseNames(cc *ast.CaseClause, consts map[string]string) (names []string, ok bool) {
	if len(cc.List) == 0 {
		return nil, false
	}
	for _, x := range cc.List {
		switch x := x.(type) {
		case *ast.BasicLit:
			s, err := strconv.Unquote(x.Value)
			if x.Kind != token.STRING || err != nil {
				return nil, false
			}
			names = append(names, s)
		case *ast.Ident:
			s, found := consts[x.Name]
			if !found {
				return nil, false
			}
			names = append(names, s)
		default:
			return nil, false
		}
	}
	return names, true
}

// ruleSymbolSource is symbolSource narrowed to rules: in each switch of the
// declaration with a case for one of the rules, the cases that match only
// other names are left out. consts resolves case constants.
func ruleSymbolSource(src []byte, sym string, rules map[string]bool, consts map[string]string) string {
	if len(rules) == 0 || len(src) == 0 {
		return symbolSource(src, sym)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return symbolSource(src, sym)
	}
	local := map[string]string{}
	for k, v := range consts {
		local[k] = v
	}
	stringConsts(src, local)
	off := func(p token.Pos) int { return fset.Position(p).Offset }
	var out []string
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != sym {
			continue
		}
		type cut struct{ from, to int }
		var cuts []cut
		ast.Inspect(fd, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			var others []cut
			mine := false
			for _, st := range sw.Body.List {
				cc := st.(*ast.CaseClause)
				names, ok := caseNames(cc, local)
				if !ok {
					continue
				}
				hit := false
				for _, nm := range names {
					hit = hit || rules[nm]
				}
				if hit {
					mine = true
				} else {
					// The cut takes the case's indent and line break too.
					from := off(cc.Pos())
					for from > 0 && strings.ContainsRune(" \t\n", rune(src[from-1])) {
						from--
					}
					others = append(others, cut{from, off(cc.End())})
				}
			}
			if mine {
				cuts = append(cuts, others...)
				return false // nested switches sit in kept or cut cases
			}
			return true
		})
		start, end := off(fd.Pos()), off(fd.End())
		var b strings.Builder
		at := start
		sort.Slice(cuts, func(i, j int) bool { return cuts[i].from < cuts[j].from })
		for _, c := range cuts {
			b.Write(src[at:c.from])
			at = c.to
		}
		b.Write(src[at:end])
		out = append(out, strings.TrimSpace(b.String()))
	}
	if len(out) == 0 {
		return symbolSource(src, sym)
	}
	// The symbol may also name a type, const or var (not narrowed).
	for _, s := range SymbolDecls(src, sym) {
		if !strings.HasPrefix(s, "func") {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// codeShape is Go source as tokens with every string literal blanked and
// comments dropped: two files with the same shape differ only in text.
// ok is false when the source doesn't scan.
func codeShape(src []byte) (string, bool) {
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	bad := false
	s.Init(f, src, func(token.Position, string) { bad = true }, 0)
	var b strings.Builder
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		switch tok {
		case token.STRING:
			lit = `""`
		case token.SEMICOLON:
			lit = ";"
		}
		if lit == "" {
			lit = tok.String()
		}
		b.WriteString(lit)
		b.WriteByte(' ')
	}
	return b.String(), !bad
}

// textOnlyChange reports whether before and after differ only in string
// literals and comments.
func textOnlyChange(before, after []byte) bool {
	a, okA := codeShape(before)
	b, okB := codeShape(after)
	return okA && okB && len(before) > 0 && len(after) > 0 && a == b
}

var listIndex = regexp.MustCompile(`^(.*?\[\d+\])`)

// detectRegion is what an entry's detect: probes see in a workflow file:
// every matching value widened to its outermost list item (a whole status
// or transition), each rendered as YAML, sorted. ok is false when the file
// doesn't parse or the entry has no probes.
func detectRegion(raw []byte, probes []Probe) (string, bool) {
	if len(probes) == 0 {
		return "", false
	}
	var doc map[string]any
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return "", false
		}
	}
	seen := map[string]bool{}
	var parts []string
	for _, p := range probes {
		for _, root := range expandProbePath(p.Path) {
			walkProbePath(doc, root, func(at string, v any) {
				if !p.matches(v) {
					return
				}
				loc := at
				if m := listIndex.FindStringSubmatch(at); m != nil {
					loc = m[1]
				}
				if seen[loc] {
					return
				}
				seen[loc] = true
				item := lookupAt(doc, loc)
				data, err := yaml.Marshal(item)
				if err != nil {
					data = []byte("?")
				}
				parts = append(parts, string(data))
			})
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n---\n"), true
}

var locSegment = regexp.MustCompile(`^([^\[]*)(?:\[(\d+)\])?$`)

// lookupAt returns the value at a concrete location from walkProbePath,
// such as types.feature.statuses[2].
func lookupAt(node any, loc string) any {
	for _, seg := range strings.Split(loc, ".") {
		m := locSegment.FindStringSubmatch(seg)
		mp, ok := node.(map[string]any)
		if m == nil || !ok {
			return nil
		}
		node = mp[m[1]]
		if m[2] != "" {
			i, _ := strconv.Atoi(m[2])
			items, _ := node.([]any)
			if i >= len(items) {
				return nil
			}
			node = items[i]
		}
	}
	return node
}

// isTemplate reports whether p is a bundled workflow template.
func isTemplate(p string) bool {
	return path.Dir(p) == TemplatesPath && path.Ext(p) == ".yaml"
}

// packageConsts resolves the string constants of the Go package in dir at
// the head, cached per folder.
func (c *checker) packageConsts(dir string) map[string]string {
	if m, ok := c.consts[dir]; ok {
		return m
	}
	m := map[string]string{}
	if c.b.HeadDir != nil {
		for _, name := range c.b.HeadDir(dir) {
			if path.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if data, ok := c.b.Head(dir + "/" + name); ok && bytes.Contains(data, []byte("const")) {
				stringConsts(data, m)
			}
		}
	}
	c.consts[dir] = m
	return m
}
