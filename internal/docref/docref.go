// Package docref gives the compact form of a docs page: its outline (each
// heading with its first sentence) or one section, so an agent reads the
// part it needs instead of a 100-200 KB page. It is computed from the docs
// on each call, so there is no copy to keep in step (issue-cli ref).
package docref

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Top are the pages dispatched workers read most, measured from their
// transcripts over the 7 days to 2026-11-15 (by sessions that read them):
// issue-cli ref lists them first. ref --measure measures again.
var Top = []string{
	"CLI/overview.md",
	"departments.md",
	"agent-dispatch.md",
	"workflow.md",
	"Workflow/proof.md",
	"workbench-files.md",
	"UI/workshop-board.md",
	"code-structure.md",
	"patterns/lead-session.md",
	"UI/overview.md",
}

// Page is one docs page.
type Page struct {
	Rel   string // path under the docs dir, slash-separated
	Path  string
	Title string
	Lines []string
}

// Key is the short name ref takes for a page: its file name without .md,
// lowercased; an overview or README page takes its folder's name.
func Key(rel string) string {
	rel = filepath.ToSlash(rel)
	base := strings.TrimSuffix(filepath.Base(rel), ".md")
	if l := strings.ToLower(base); (l == "overview" || l == "readme") && filepath.Dir(rel) != "." {
		base = filepath.Base(filepath.Dir(rel))
	}
	return strings.ToLower(base)
}

// Pages are the markdown pages under dir.
func Pages(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			if rel, err := filepath.Rel(dir, p); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Resolve finds the page topic names: a key, a path under dir (with or
// without docs/ and .md), or words of its title. On no match the error
// lists the near ones.
func Resolve(dir, topic string) (*Page, error) {
	t := strings.TrimSpace(topic)
	t = strings.TrimPrefix(filepath.ToSlash(t), "docs/")
	pages := Pages(dir)
	try := func(rel string) (*Page, error) { return Load(dir, rel) }
	for _, rel := range pages {
		if strings.EqualFold(rel, t) || strings.EqualFold(strings.TrimSuffix(rel, ".md"), t) {
			return try(rel)
		}
	}
	low := strings.ToLower(t)
	var keyed []string
	for _, rel := range pages {
		if Key(rel) == low {
			keyed = append(keyed, rel)
		}
	}
	if len(keyed) > 0 {
		// The top page wins a shared key (cli: CLI/overview.md).
		sort.SliceStable(keyed, func(i, j int) bool { return rank(keyed[i]) < rank(keyed[j]) })
		return try(keyed[0])
	}
	var near []string
	for _, rel := range pages {
		p, err := Load(dir, rel)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(p.Title), low) || strings.Contains(strings.ToLower(rel), low) {
			near = append(near, rel)
		}
	}
	if len(near) == 1 {
		return try(near[0])
	}
	if len(near) > 1 {
		return nil, fmt.Errorf("%q names %d pages: %s", topic, len(near), strings.Join(near, ", "))
	}
	return nil, fmt.Errorf("no docs page %q in %s (issue-cli ref lists them)", topic, dir)
}

func rank(rel string) int {
	for i, t := range Top {
		if t == rel {
			return i
		}
	}
	return len(Top)
}

// Load reads the page at rel under dir.
func Load(dir, rel string) (*Page, error) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	page := &Page{Rel: rel, Path: p, Lines: strings.Split(strings.TrimRight(string(data), "\n"), "\n")}
	page.Title = frontTitle(page.Lines)
	if page.Title == "" {
		page.Title = strings.TrimSuffix(filepath.Base(rel), ".md")
	}
	return page, nil
}

func frontTitle(lines []string) string {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		for _, l := range lines {
			if h, ok := strings.CutPrefix(l, "# "); ok {
				return strings.TrimSpace(h)
			}
		}
		return ""
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, "title:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// Heading is one heading of a page with where its section runs.
type Heading struct {
	Level      int
	Text       string
	Line, End  int // 0-based first line, and the line after the section
	FirstWords string
}

var headingRe = regexp.MustCompile(`^(#{2,4})\s+(.+?)\s*#*\s*$`)

// Headings are the page's ## to #### headings, outside code fences, each
// with its section's end and its first sentence.
func (p *Page) Headings() []Heading {
	var hs []Heading
	fence := false
	start := bodyStart(p.Lines)
	for i := start; i < len(p.Lines); i++ {
		l := p.Lines[i]
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if m := headingRe.FindStringSubmatch(l); m != nil {
			hs = append(hs, Heading{Level: len(m[1]), Text: m[2], Line: i})
		}
	}
	for i := range hs {
		hs[i].End = len(p.Lines)
		for j := i + 1; j < len(hs); j++ {
			if hs[j].Level <= hs[i].Level {
				hs[i].End = hs[j].Line
				break
			}
		}
		next := len(p.Lines)
		if i+1 < len(hs) {
			next = hs[i+1].Line
		}
		hs[i].FirstWords = firstSentence(p.Lines[hs[i].Line+1 : next])
	}
	return hs
}

func bodyStart(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i + 1
		}
	}
	return 0
}

// sentenceMax caps an outline line's sentence.
const sentenceMax = 160

// firstSentence is the first sentence of the first prose paragraph in
// lines (not a table, code, image or list), cut at sentenceMax.
func firstSentence(lines []string) string {
	fence := false
	var para []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if t == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "|") || strings.HasPrefix(t, "![") || strings.HasPrefix(t, "<") {
			if len(para) > 0 {
				break
			}
			continue
		}
		t = strings.TrimLeft(t, "-*0123456789. ")
		para = append(para, t)
	}
	s := strings.Join(para, " ")
	if i := sentenceEnd(s); i > 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > sentenceMax {
		s = string(r[:sentenceMax-1]) + "…"
	}
	return s
}

// sentenceEnd is the index just after the first ". " (or the end).
func sentenceEnd(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == '.' || s[i] == ':') && s[i+1] == ' ' && i > 20 {
			return i + 1
		}
	}
	return len(s)
}

// Outline is the page's compact form: its title and size, then each
// heading (indented by level) with its first sentence and line.
func (p *Page) Outline(sectionCmd string) string {
	var b strings.Builder
	size := 0
	for _, l := range p.Lines {
		size += len(l) + 1
	}
	fmt.Fprintf(&b, "%s (docs/%s, %d lines, %d KB)\n", p.Title, p.Rel, len(p.Lines), (size+1023)/1024)
	if s := firstSentence(p.Lines[bodyStart(p.Lines):min(len(p.Lines), firstHeadingLine(p))]); s != "" {
		fmt.Fprintf(&b, "%s\n", s)
	}
	b.WriteString("\n")
	for _, h := range p.Headings() {
		indent := strings.Repeat("  ", h.Level-2)
		line := fmt.Sprintf("%s%s %s  [L%d]", indent, strings.Repeat("#", h.Level), h.Text, h.Line+1)
		if h.FirstWords != "" {
			line += " — " + h.FirstWords
		}
		b.WriteString(line + "\n")
	}
	if sectionCmd != "" {
		fmt.Fprintf(&b, "\nOne section in full: %s\n", sectionCmd)
	}
	return b.String()
}

func firstHeadingLine(p *Page) int {
	if hs := p.Headings(); len(hs) > 0 {
		return hs[0].Line
	}
	return len(p.Lines)
}

// Section is the section whose heading holds every word (case-insensitive),
// in full; the first such heading wins, an exact match first.
func (p *Page) Section(words []string) (string, error) {
	want := strings.ToLower(strings.Join(words, " "))
	hs := p.Headings()
	pick := -1
	for i, h := range hs {
		if strings.EqualFold(h.Text, want) {
			pick = i
			break
		}
	}
	if pick < 0 {
		for i, h := range hs {
			text := strings.ToLower(h.Text)
			all := true
			for _, w := range words {
				if !strings.Contains(text, strings.ToLower(w)) {
					all = false
					break
				}
			}
			if all {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		var names []string
		for _, h := range hs {
			if h.Level == 2 {
				names = append(names, h.Text)
			}
		}
		return "", fmt.Errorf("no heading in docs/%s holds %q; its sections: %s", p.Rel, strings.Join(words, " "), strings.Join(names, " · "))
	}
	h := hs[pick]
	var b strings.Builder
	fmt.Fprintf(&b, "docs/%s lines %d-%d:\n\n", p.Rel, h.Line+1, h.End)
	b.WriteString(strings.Join(p.Lines[h.Line:h.End], "\n"))
	b.WriteString("\n")
	return b.String(), nil
}
