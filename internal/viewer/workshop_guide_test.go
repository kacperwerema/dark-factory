package viewer

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

	doc, err := os.ReadFile(repoPath("docs", "UI", "workshop-board.md"))
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

// A lost "}" puts every rule after it inside the block it left open: the
// memory page's phone @media once swallowed the HQ report's and the reader's
// rules, so both pages rendered unstyled on a desktop while every phone
// check passed. Each stylesheet closes every block before the next banner
// and before the end of the file.
func TestStylesheetsCloseEveryBlock(t *testing.T) {
	for _, name := range []string{"static/workshop.css", "static/style.css"} {
		css, err := fs.ReadFile(staticFS, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range unclosedBlocks(string(css)) {
			t.Errorf("%s: %s", name, p)
		}
	}
}

func TestUnclosedBlocks(t *testing.T) {
	cases := []struct {
		name, css string
		want      int
	}{
		{"closed", "/* ---- a ---- */\n.x { a: b; }\n@media (max-width: 1px) { .x { a: c; } }\n/* ---- b ---- */\n.y { a: b; }", 0},
		{"open at a banner", "@media (max-width: 1px) {\n .x { a: c; }\n/* ---- b ---- */\n.y { a: b; }\n}", 1},
		{"open at the end", ".x { a: b; }\n@media (max-width: 1px) {\n .x { a: c; }", 1},
		{"braces in a comment", "/* { */\n.x { a: b; }", 0},
	}
	for _, c := range cases {
		if got := unclosedBlocks(c.css); len(got) != c.want {
			t.Errorf("%s: got %q, want %d problems", c.name, got, c.want)
		}
	}
}

// unclosedBlocks names each block still open at a "/* ---- <block> ---- */"
// banner or at the end of the file, with the line it opened on.
func unclosedBlocks(css string) []string {
	banner := regexp.MustCompile(`^/\*\s*-{2,}\s*(.*?)\s*-{2,}`)
	type open struct {
		line int
		head string
	}
	var stack []open
	var problems []string
	start, line := 0, 1
	report := func(where string) {
		o := stack[len(stack)-1]
		problems = append(problems, fmt.Sprintf("%q opened on line %d is still open %s", o.head, o.line, where))
		stack = nil
	}
	for i := 0; i < len(css); i++ {
		switch {
		case strings.HasPrefix(css[i:], "/*"):
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				end = len(css) - i - 4
			}
			comment := css[i : i+end+4]
			if m := banner.FindStringSubmatch(comment); m != nil && len(stack) > 0 {
				report(fmt.Sprintf("at the banner %q on line %d", m[1], line))
			}
			line += strings.Count(comment, "\n")
			i += len(comment) - 1
			start = i + 1
		case css[i] == '\n':
			line++
		case css[i] == '{':
			stack = append(stack, open{line, strings.TrimSpace(css[start:i])})
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
	if len(stack) > 0 {
		report("at the end of the file")
	}
	return problems
}

// A Workshop template that uses a wb-* class with no rule in
// static/workshop.css draws that element unstyled, and a playtest's
// styles_applied check fails on it later (wb-ny-sec--after did). Every wb-*
// class a Workshop template names has a rule somewhere in workshop.css.
func TestWorkshopTemplateClassesHaveRules(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "static/workshop.css")
	if err != nil {
		t.Fatal(err)
	}
	ruled := wbCSSClasses(string(css))
	files, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		src, _ := fs.ReadFile(templateFS, f)
		if !isWorkshopTemplate(f, string(src)) {
			continue
		}
		checked++
		for _, u := range wbTemplateClasses(string(src)) {
			if !ruled[u.class] && wbHookClasses[u.class] == "" {
				t.Errorf("%s:%d uses class %s, which has no rule in static/workshop.css: add one in the page's block, or drop the class (docs/UI/workshop-board.md#css-class-names)", f, u.line, u.class)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Workshop templates found under templates/")
	}
}

// wbHookClasses carry no rule on purpose; each says who reads it. Add one only
// when a script or a playtest selects the element by that class.
var wbHookClasses = map[string]string{
	"wb-copy-label": "templates/report.html's copy script finds the label inside the Copy button",
	"wb-stampcard":  "playtest sessions select the station's stamp card by it",
	"wb-statbig":    "wrapper: its wb-statbig-num and wb-statbig-label are styled",
	"wb-figure":     "wrapper: its wb-figure-num and wb-figure-label are styled",
	"wb-fl-num":     "wrapper: its wb-fl-num-v and wb-fl-num-l are styled",
}

// isWorkshopTemplate: the component files (templates/_workshop*.html) and the
// pages that load workshop.css through wb/head. Classic pages use style.css.
func isWorkshopTemplate(name, src string) bool {
	return strings.HasPrefix(name, "templates/_workshop") ||
		regexp.MustCompile(`\{\{-?\s*template\s+"wb/head"`).MatchString(src)
}

// wbCSSClasses is every .wb-* class that appears in a selector of the
// stylesheet, alone or as part of a compound or descendant selector.
func wbCSSClasses(css string) map[string]bool {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	set := map[string]bool{}
	for _, m := range regexp.MustCompile(`\.(wb-[\w-]+)`).FindAllStringSubmatch(css, -1) {
		set[m[1]] = true
	}
	return set
}

type wbClassUse struct {
	class string
	line  int
}

// wbTemplateClasses lists the wb-* classes named literally in class="…"
// attributes. Template actions are blanked first, so a class built from data
// (wb-chip--{{.Tone}}) is left out and one inside {{if}} is kept.
func wbTemplateClasses(src string) []wbClassUse {
	action := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	src = action.ReplaceAllStringFunc(src, func(a string) string {
		return strings.Repeat("\n", strings.Count(a, "\n")) + " "
	})
	attr := regexp.MustCompile(`\bclass="([^"]*)"`)
	token := regexp.MustCompile(`^wb-[\w-]*[\w]$`)
	var uses []wbClassUse
	for _, m := range attr.FindAllStringSubmatchIndex(src, -1) {
		line := 1 + strings.Count(src[:m[0]], "\n")
		for _, c := range strings.Fields(src[m[2]:m[3]]) {
			if token.MatchString(c) {
				uses = append(uses, wbClassUse{c, line})
			}
		}
	}
	return uses
}

func TestWbTemplateClasses(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"plain", `<div class="wb-a wb-b--x other">`, []string{"wb-a", "wb-b--x"}},
		{"built from data", `<span class="wb-chip wb-chip--{{.Tone}}">`, []string{"wb-chip"}},
		{"inside if", `<p class="wb-a{{if .On}} wb-on{{end}}">`, []string{"wb-a", "wb-on"}},
		{"quotes inside an action", `<p class="{{if eq .K "x"}}wb-k{{end}}">`, []string{"wb-k"}},
		{"not a class attribute", `<p data-x="wb-a">wb-b</p>`, nil},
	}
	for _, c := range cases {
		var got []string
		for _, u := range wbTemplateClasses(c.src) {
			got = append(got, u.class)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	css := "/* .wb-c */ .wb-a { x: y; }\n.wb-p .wb-b:hover, .wb-q.wb-r { x: y; }"
	got := wbCSSClasses(css)
	for _, c := range []string{"wb-a", "wb-b", "wb-p", "wb-q", "wb-r"} {
		if !got[c] {
			t.Errorf("wbCSSClasses misses %s", c)
		}
	}
	if got["wb-c"] {
		t.Error("wbCSSClasses counts a class named only in a comment")
	}
}
