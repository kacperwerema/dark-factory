package tracker

import (
	"sort"
	"strings"
)

// contentGroup is one sub-heading block of appended text: its heading and
// the lines under it, up to the next heading of its level or shallower.
type contentGroup struct {
	Level int
	Key   string
	Line  string
	Body  []string
}

// splitContentGroups splits text into the lines before its first top-level
// heading (lead) and one group per top-level heading, top level being the
// shallowest heading level text holds. Fenced code is not read as headings.
func splitContentGroups(text string) (lead []string, groups []contentGroup) {
	lines := strings.Split(text, "\n")
	fence := computeFenceFlags(lines)
	top := 0
	for i, line := range lines {
		if level, _, ok := parseHeadingLine(line); ok && !fence[i] && (top == 0 || level < top) {
			top = level
		}
	}
	for i, line := range lines {
		if level, title, ok := parseHeadingLine(line); ok && !fence[i] && level == top {
			groups = append(groups, contentGroup{Level: level, Key: normalizeHeadingKey(title), Line: strings.TrimSpace(line)})
			continue
		}
		if len(groups) == 0 {
			lead = append(lead, line)
		} else {
			g := &groups[len(groups)-1]
			g.Body = append(g.Body, line)
		}
	}
	return lead, groups
}

// trimBlankLines drops lines' leading and trailing blank lines.
func trimBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// mergeIntoSubheadings appends content into the section match when content
// holds a sub-heading the section already has (same level and title): that
// group's lines go at the end of the existing sub-heading's text instead of
// writing the heading a second time. Lines before content's first
// sub-heading go at the end of the section's intro, before its first
// sub-heading; a sub-heading the section lacks is appended at its end. A
// block the section already holds word for word is skipped. ok is false when
// no sub-heading of content matches one in the section, so the caller
// appends the plain way.
func mergeIntoSubheadings(body string, match headingMatch, content string) (next string, changed, ok bool) {
	lead, groups := splitContentGroups(content)
	if len(groups) == 0 || groups[0].Level <= match.Level {
		return body, false, false
	}
	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)

	type sub struct {
		level      int
		key        string
		start, end int // heading line, end of its text (exclusive)
	}
	var subs []sub
	for i := match.StartLine + 1; i < match.EndLine; i++ {
		if level, title, ok := parseHeadingLine(lines[i]); ok && !fence[i] {
			subs = append(subs, sub{level: level, key: normalizeHeadingKey(title), start: i, end: match.EndLine})
		}
	}
	for n := range subs {
		for _, later := range subs[n+1:] {
			if later.level <= subs[n].level {
				subs[n].end = later.start
				break
			}
		}
	}
	find := func(g contentGroup) (sub, bool) {
		for _, s := range subs {
			if s.level == g.Level && s.key == g.Key {
				return s, true
			}
		}
		return sub{}, false
	}
	matched := false
	for _, g := range groups {
		if _, found := find(g); found {
			matched = true
		}
	}
	if !matched {
		return body, false, false
	}

	contains := func(start, end int, block []string) bool {
		return strings.Contains(strings.Join(lines[start:end], "\n"), strings.Join(block, "\n"))
	}
	type insert struct {
		start, end int // the text the lines join: they go after its last non-blank line
		lines      []string
		heading    bool // lines open with a heading
	}
	var ops []insert
	if block := trimBlankLines(lead); len(block) > 0 && !contains(match.StartLine+1, match.EndLine, block) {
		end := match.EndLine
		if len(subs) > 0 {
			end = subs[0].start
		}
		ops = append(ops, insert{start: match.StartLine + 1, end: end, lines: block})
	}
	for _, g := range groups {
		block := trimBlankLines(g.Body)
		if s, found := find(g); found {
			if len(block) > 0 && !contains(s.start+1, s.end, block) {
				ops = append(ops, insert{start: s.start + 1, end: s.end, lines: block})
			}
			continue
		}
		ops = append(ops, insert{start: match.StartLine + 1, end: match.EndLine, lines: append([]string{g.Line}, block...), heading: true})
	}
	if len(ops) == 0 {
		return body, false, true
	}

	// Apply from the bottom up so earlier indexes stay put; at the same
	// spot the later block is applied first, so it ends up below.
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].end < ops[j].end })
	for n := len(ops) - 1; n >= 0; n-- {
		op := ops[n]
		at := op.end
		for at > op.start && strings.TrimSpace(lines[at-1]) == "" {
			at--
		}
		insertLines := op.lines
		// Join a list tightly; set prose or a heading apart with a blank line.
		if at > op.start {
			prev := strings.TrimSpace(lines[at-1])
			if op.heading || !isListLine(prev) || !isListLine(strings.TrimSpace(insertLines[0])) {
				insertLines = append([]string{""}, insertLines...)
			}
		}
		if at < len(lines) && strings.TrimSpace(lines[at]) != "" {
			insertLines = append(insertLines, "")
		}
		out := append([]string(nil), lines[:at]...)
		out = append(out, insertLines...)
		lines = append(out, lines[at:]...)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n", true, true
}

// isListLine reports whether a trimmed line is a "- " / "* " list item.
func isListLine(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
}

// headingPlaces counts body's headings by place: the heading's level and
// title under the chain of headings that hold it. Two headings with one
// place are a duplicate; a "### Risks" under two different sections is not.
func headingPlaces(body string) (counts map[string]int, lines map[string]string) {
	counts, lines = map[string]int{}, map[string]string{}
	var chain []headingMatch
	for _, h := range findAllHeadings(body) {
		for len(chain) > 0 && chain[len(chain)-1].Level >= h.Level {
			chain = chain[:len(chain)-1]
		}
		var b strings.Builder
		for _, c := range chain {
			b.WriteString(c.Key + "\x00")
		}
		place := b.String() + strings.Repeat("#", h.Level) + " " + h.Key
		counts[place]++
		if _, ok := lines[place]; !ok {
			lines[place] = h.Line
		}
		chain = append(chain, h)
	}
	return counts, lines
}

// NewDuplicateHeadings returns the heading lines after holds more than once
// at one place (same level and title under the same headings) where before
// held that heading fewer times: the duplicates an edit wrote.
func NewDuplicateHeadings(before, after string) []string {
	was, _ := headingPlaces(before)
	now, lines := headingPlaces(after)
	var out []string
	for place, n := range now {
		if n > 1 && n > was[place] {
			out = append(out, lines[place])
		}
	}
	sort.Strings(out)
	return out
}

// MergedSubheadings returns the sub-headings of text that before already
// held under section, at their level, and after holds no more often: the
// ones an append put its lines under instead of writing them again.
func MergedSubheadings(before, after, section, text string) []string {
	var out []string
	secs := findHeadingMatches(before, section)
	if len(secs) == 0 {
		return nil
	}
	sec := secs[0]
	beforeLines := strings.Split(before, "\n")
	inSection := map[string]bool{}
	for _, h := range findAllHeadings(strings.Join(beforeLines[sec.StartLine+1:sec.EndLine], "\n")) {
		inSection[strings.Repeat("#", h.Level)+" "+h.Key] = true
	}
	count := func(body, key string) int {
		n := 0
		for _, h := range findAllHeadings(body) {
			if strings.Repeat("#", h.Level)+" "+h.Key == key {
				n++
			}
		}
		return n
	}
	seen := map[string]bool{}
	for _, h := range findAllHeadings(text) {
		key := strings.Repeat("#", h.Level) + " " + h.Key
		if h.Level <= sec.Level || seen[key] || !inSection[key] {
			continue
		}
		seen[key] = true
		if count(after, key) == count(before, key) {
			out = append(out, h.Line)
		}
	}
	return out
}
