package tracker

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
)

// BoxSections lists body's "## " section names in order, once each, outside
// fenced blocks: the sections ListCheckboxes numbers boxes in.
func BoxSections(body string) []string {
	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)
	var out []string
	seen := map[string]bool{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence[i] || !strings.HasPrefix(trimmed, "## ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
		if key := strings.ToLower(name); name != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}

// AddCheckbox adds one open box with text to the "## " section named
// section (any case): right after the section's last box or removed-box
// slot, with that box's indentation, so it takes the next index; in a
// section with no boxes, after its last non-blank line. A leading copy of
// the new id in text is dropped. It refuses an unknown section (listing the
// sections), text over one line, and an add that would change any other
// box's id (a first box can move the short prefixes of the sections after
// it). Returns the new body and the new box.
func AddCheckbox(body, section, text string) (string, CheckboxItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return body, CheckboxItem{}, fmt.Errorf("a box needs text")
	}
	if strings.ContainsAny(text, "\r\n") {
		return body, CheckboxItem{}, fmt.Errorf("a checkbox's text must be one line")
	}
	name := ""
	for _, s := range BoxSections(body) {
		if strings.EqualFold(s, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(section), "##"))) {
			name = s
		}
	}
	if name == "" {
		return body, CheckboxItem{}, fmt.Errorf("no section %q; sections in this issue: %s", section, quoteList(BoxSections(body)))
	}

	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)
	// The last box or removed slot in the section, else the last
	// non-blank line of its last heading's span.
	at, indent := -1, ""
	cur := ""
	lastContent := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !fence[i] && strings.HasPrefix(trimmed, "## ") {
			cur = strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
			if strings.EqualFold(cur, name) {
				lastContent = i
			}
			continue
		}
		if !strings.EqualFold(cur, name) {
			continue
		}
		if trimmed != "" {
			lastContent = i
		}
		if fence[i] {
			continue
		}
		if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "- [x]") ||
			strings.HasPrefix(trimmed, "- [X]") || strings.HasPrefix(trimmed, removedBoxMarker) {
			at = i
			indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		}
	}
	if at < 0 {
		at = lastContent
	}

	before := ListCheckboxes(body)
	insert := func(t string) string {
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:at+1]...)
		out = append(out, indent+"- [ ] "+t)
		out = append(out, lines[at+1:]...)
		return strings.Join(out, "\n")
	}
	next := insert(text)
	item, ok := boxAtLine(next, at+1)
	if !ok {
		return body, CheckboxItem{}, fmt.Errorf("the box could not be placed in ## %s", name)
	}
	if stripped := StripCheckboxID(item.ID, text); stripped != text {
		next = insert(stripped)
		item, _ = boxAtLine(next, at+1)
	}
	after := ListCheckboxes(next)
	for _, b := range before {
		for _, a := range after {
			if a.Line == shiftLine(b.Line, at) && a.ID != b.ID {
				return body, CheckboxItem{}, fmt.Errorf("a first box in ## %s would change other boxes' ids (%s would become %s), so nothing was added; to add it anyway, moving those ids: issue-cli append --section %s --body \"- [ ] ...\"", name, b.ID, a.ID, shell.Quote(name))
			}
		}
	}
	return next, item, nil
}

func boxAtLine(body string, line int) (CheckboxItem, bool) {
	for _, it := range ListCheckboxes(body) {
		if it.Line == line {
			return it, true
		}
	}
	return CheckboxItem{}, false
}

// shiftLine is where line sits after a line was inserted below at.
func shiftLine(line, at int) int {
	if line > at {
		return line + 1
	}
	return line
}

func quoteList(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
}
