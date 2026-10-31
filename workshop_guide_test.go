package main

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Workshop Board guide (docs/UI/workshop-board.md) lists every wb/*
// component by file; this keeps the list in step with the templates.
func TestWorkshopCatalogMatchesDefines(t *testing.T) {
	define := regexp.MustCompile(`\{\{-?\s*define\s+"(wb/[^"]+)"`)
	files, err := fs.Glob(templateFS, "templates/_workshop*.html")
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]string{}
	for _, f := range files {
		src, _ := fs.ReadFile(templateFS, f)
		for _, m := range define.FindAllStringSubmatch(string(src), -1) {
			defined[m[1]] = f
		}
	}
	if len(defined) == 0 {
		t.Fatal("no wb/* defines found under templates/_workshop*.html")
	}

	doc, err := os.ReadFile("docs/UI/workshop-board.md")
	if err != nil {
		t.Fatal(err)
	}
	listed := workshopCatalog(string(doc))
	for name, file := range defined {
		switch got, ok := listed[name]; {
		case !ok:
			t.Errorf("%s (%s) is missing from the catalog in docs/UI/workshop-board.md", name, file)
		case got != file:
			t.Errorf("%s is listed under %s in docs/UI/workshop-board.md but defined in %s", name, got, file)
		}
	}
	for name, file := range listed {
		if _, ok := defined[name]; !ok {
			t.Errorf("%s is listed under %s in docs/UI/workshop-board.md but no template defines it", name, file)
		}
	}
}

// workshopCatalog reads the guide's catalog: a "### templates/<file>"
// heading, then one "| `wb/<name>` | … |" row per component.
func workshopCatalog(doc string) map[string]string {
	heading := regexp.MustCompile("^### (templates/_workshop[^ ]*\\.html)\\s*$")
	row := regexp.MustCompile("^\\| `(wb/[^`]+)` \\|")
	listed := map[string]string{}
	file := ""
	for _, line := range strings.Split(doc, "\n") {
		if m := heading.FindStringSubmatch(line); m != nil {
			file = m[1]
		} else if strings.HasPrefix(line, "#") {
			file = ""
		} else if m := row.FindStringSubmatch(line); m != nil && file != "" {
			listed[m[1]] = file
		}
	}
	return listed
}

// Every Workshop page loads static/workshop.css, so a wb-* class with two
// rules is styled by whichever comes last, on every page.
func TestWorkshopCSSClassesDefinedOnce(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "static/workshop.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, clash := range wbClassClashes(string(css)) {
		t.Error(clash)
	}
}

func TestWbClassClashes(t *testing.T) {
	cases := []struct {
		name, css string
		want      int
	}{
		{"one rule", "/* ---- a ---- */\n.wb-x { color: red; }\n.wb-x:hover { color: blue; }\n.wb .wb-x { margin: 0; }", 0},
		{"grouped in its block", "/* ---- a ---- */\n.wb-x, .wb-y { gap: 1px; }\n.wb-x { color: red; }", 0},
		{"restated in @media", "/* ---- a ---- */\n.wb-x { padding: 2px; }\n@media (max-width: 640px) { .wb-x { padding: 1px; } }", 0},
		{"two plain rules", "/* ---- a ---- */\n.wb-x { color: red; }\n.wb-x { color: blue; }", 1},
		{"two blocks", "/* ---- a ---- */\n.wb-x { color: red; }\n/* ---- b ---- */\n.wb-x, .wb-y { color: blue; }", 1},
		{"other prefixes ignored", ".upd-x { color: red; }\n.upd-x { color: blue; }", 0},
	}
	for _, c := range cases {
		if got := wbClassClashes(c.css); len(got) != c.want {
			t.Errorf("%s: got %d clashes %q, want %d", c.name, len(got), got, c.want)
		}
	}
}

// wbClassClashes lists the wb-* classes that have more than one plain rule
// (the class alone as the selector) or appear as a whole selector in more
// than one "/* ---- <block> ---- */" block. Rules inside at-rules (@media)
// may restate a class.
func wbClassClashes(css string) []string {
	banner := regexp.MustCompile(`^/\*\s*-{2,}\s*(.*?)\s*-{2,}`)
	class := regexp.MustCompile(`^\.wb-[\w-]+$`)
	type rule struct {
		line  int
		block string
		alone bool
	}
	rules := map[string][]rule{}
	var stack []bool // per open brace: inside an at-rule
	block, start, line := "", 0, 1
	for i := 0; i < len(css); i++ {
		switch {
		case strings.HasPrefix(css[i:], "/*"):
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				end = len(css) - i - 4
			}
			comment := css[i : i+end+4]
			if m := banner.FindStringSubmatch(comment); m != nil && len(stack) == 0 {
				block = m[1]
			}
			line += strings.Count(comment, "\n")
			i += len(comment) - 1
			start = i + 1
		case css[i] == '\n':
			line++
		case css[i] == '{':
			sel := strings.TrimSpace(css[start:i])
			inAt := false
			for _, at := range stack {
				inAt = inAt || at
			}
			stack = append(stack, inAt || strings.HasPrefix(sel, "@"))
			if !inAt && !strings.HasPrefix(sel, "@") {
				parts := strings.Split(sel, ",")
				for _, p := range parts {
					if p = strings.TrimSpace(p); class.MatchString(p) {
						rules[p] = append(rules[p], rule{line, block, len(parts) == 1})
					}
				}
			}
			start = i + 1
		case css[i] == '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			start = i + 1
		case css[i] == ';':
			start = i + 1
		}
	}

	var clashes []string
	for name, rs := range rules {
		alone, blocks := 0, map[string]bool{}
		var where []string
		for _, r := range rs {
			if r.alone {
				alone++
			}
			blocks[r.block] = true
			where = append(where, fmt.Sprintf("line %d (%q)", r.line, r.block))
		}
		if alone > 1 || len(blocks) > 1 {
			clashes = append(clashes, fmt.Sprintf("%s is defined in more than one place in static/workshop.css: %s; keep one rule, or give the page's own class its own name (docs/UI/workshop-board.md#css-class-names)", name, strings.Join(where, ", ")))
		}
	}
	sort.Strings(clashes)
	return clashes
}
