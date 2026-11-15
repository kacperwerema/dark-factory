package tracker

import (
	"fmt"

	"strings"

	"unicode/utf8"
)

// CountCheckboxes returns total and checked checkbox counts in markdown.
// Boxes inside fenced code blocks are not counted, matching ListCheckboxes.
func CountCheckboxes(body string) (total, checked int) {
	for _, it := range ListCheckboxes(body) {
		total++
		if it.Checked {
			checked++
		}
	}
	return
}

// CountCheckboxesInSection counts checkboxes only under a specific ## heading.
// The section ends at the next ## heading or end of body. Boxes inside fenced
// code blocks are not counted, matching ListCheckboxes.
func CountCheckboxesInSection(body, section string) (total, checked int) {
	for _, it := range ListCheckboxes(body) {
		if !strings.EqualFold(it.Section, section) {
			continue
		}
		total++
		if it.Checked {
			checked++
		}
	}
	return
}

// CheckCheckbox finds an unchecked checkbox whose text contains the query and checks it off.
// Returns the updated body and whether a match was found. Checkboxes inside
// fenced code blocks are skipped — they are illustrative quotes, not workflow
// state.
func CheckCheckbox(body, query string) (string, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	lines := strings.Split(body, "\n")
	fenceFlags := computeFenceFlags(lines)
	for i, line := range lines {
		if fenceFlags[i] {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") {
			text := strings.ToLower(strings.TrimSpace(trimmed[5:]))
			if strings.Contains(text, query) {
				lines[i] = strings.Replace(line, "- [ ]", "- [x]", 1)
				return strings.Join(lines, "\n"), true
			}
		}
	}
	return body, false
}

// CheckboxItem describes one markdown checkbox and where it sits.
type CheckboxItem struct {
	// Section is the nearest "## " heading above the checkbox ("" when the
	// checkbox appears before any such heading).
	Section string
	// Index is the checkbox's 1-based position within its section. It is
	// stable: it counts both checked and unchecked boxes in document order,
	// so the index of a given box never shifts as other boxes get ticked.
	Index int
	// Text is the checkbox label with the "- [ ]"/"- [x]" prefix stripped.
	Text string
	// Checked reports whether the box is ticked.
	Checked bool
	// Line is the 0-based line number of the checkbox in the body.
	Line int
	// ID is the short reference for the box: its section's abbreviation
	// followed by Index ("D3", "AC2"). Empty for boxes before any "## "
	// heading, and for a section whose abbreviation could not be made unique.
	ID string
}

// Label is the box's text for display next to its id: a leading copy of
// the id that the author wrote into the text ("MV1: …") is dropped so the
// box doesn't read "MV1 [ ] MV1: …". The body is never changed.
func (it CheckboxItem) Label() string {
	return StripCheckboxID(it.ID, it.Text)
}

// StripCheckboxID returns text without a leading copy of id (any case)
// followed by a separator (space, ":", ".", ")", "-", "–", "—"). Text that
// is only the id, or that starts with another box's id, is returned as is.
func StripCheckboxID(id, text string) string {
	if id == "" || len(text) <= len(id) || !strings.EqualFold(text[:len(id)], id) {
		return text
	}
	const seps = " \t:.)-–—"
	rest := text[len(id):]
	if r, _ := utf8.DecodeRuneInString(rest); !strings.ContainsRune(seps, r) {
		return text
	}
	if r := strings.TrimLeft(rest, seps); r != "" {
		return r
	}
	return text
}

// ListCheckboxes enumerates every checkbox in body, skipping those inside
// fenced code blocks (illustrative quotes, not workflow state). Index is
// 1-based and resets at each "## " section heading, matching the section
// boundaries used by CountCheckboxesInSection.
func ListCheckboxes(body string) []CheckboxItem {
	lines := strings.Split(body, "\n")
	fenceFlags := computeFenceFlags(lines)
	var items []CheckboxItem
	section := ""
	perSection := map[string]int{}
	// abbrevs maps a section name to its short-id prefix, assigned in the
	// order sections first hold a checkbox; taken holds the lowercased
	// prefixes already in use.
	abbrevs := map[string]string{}
	taken := map[string]bool{}
	for i, line := range lines {
		if fenceFlags[i] {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
			continue
		}
		var checked bool
		switch {
		case strings.HasPrefix(trimmed, "- [x]"), strings.HasPrefix(trimmed, "- [X]"):
			checked = true
		case strings.HasPrefix(trimmed, "- [ ]"):
			checked = false
		case strings.HasPrefix(trimmed, removedBoxMarker):
			// A removed box keeps its slot, so the boxes after it keep
			// their ids; it is not listed.
			perSection[section]++
			if _, ok := abbrevs[section]; !ok {
				abbrevs[section] = nextSectionAbbrev(section, taken)
			}
			continue
		default:
			continue
		}
		perSection[section]++
		if _, ok := abbrevs[section]; !ok {
			abbrevs[section] = nextSectionAbbrev(section, taken)
		}
		id := ""
		if a := abbrevs[section]; a != "" {
			id = fmt.Sprintf("%s%d", a, perSection[section])
		}
		items = append(items, CheckboxItem{
			Section: section,
			Index:   perSection[section],
			Text:    strings.TrimSpace(trimmed[5:]),
			Checked: checked,
			Line:    i,
			ID:      id,
		})
	}
	return items
}

// CheckByIndex ticks the checkbox addressed by index. When section is
// non-empty, index is the 1-based position within that "## " section; when
// section is empty, index is the 1-based position in the whole body. Returns
// the updated body, the matched item, whether it was already checked (a
// no-op), and ok=false when no checkbox has that index.
func CheckByIndex(body, section string, index int) (newBody string, item CheckboxItem, alreadyChecked, ok bool) {
	items := ListCheckboxes(body)
	var target *CheckboxItem
	if strings.TrimSpace(section) == "" {
		if index >= 1 && index <= len(items) {
			target = &items[index-1]
		}
	} else {
		for i := range items {
			if strings.EqualFold(items[i].Section, section) && items[i].Index == index {
				target = &items[i]
				break
			}
		}
	}
	if target == nil {
		return body, CheckboxItem{}, false, false
	}
	if target.Checked {
		return body, *target, true, true
	}
	lines := strings.Split(body, "\n")
	lines[target.Line] = strings.Replace(lines[target.Line], "- [ ]", "- [x]", 1)
	return strings.Join(lines, "\n"), *target, false, true
}

// EditCheckboxText replaces the text of the box ref names (short "D3" or
// long "Design#3", as ResolveCheckboxRef reads them), keeping its tick state
// and indentation. A leading copy of the box's own id in text is dropped.
// Returns the new body and the box as it was.
func EditCheckboxText(body, ref, text string) (string, CheckboxItem, error) {
	items := ListCheckboxes(body)
	item, st := ResolveCheckboxRef(items, ref)
	if st != RefFound {
		var ids []string
		for _, it := range items {
			if it.ID != "" {
				ids = append(ids, it.ID)
			}
		}
		return body, CheckboxItem{}, fmt.Errorf("no checkbox %q; ids in this issue: %s", ref, strings.Join(ids, " "))
	}
	text = StripCheckboxID(item.ID, strings.TrimSpace(text))
	lines := strings.Split(body, "\n")
	line := lines[item.Line]
	marker := strings.Index(line, "- [")
	lines[item.Line] = line[:marker+len("- [ ]")] + " " + text
	return strings.Join(lines, "\n"), item, nil
}

// removedBoxMarker starts the line check-remove leaves in place of a box:
// "<!-- removed AC6: <text> -->". ListCheckboxes counts it as a slot.
const removedBoxMarker = "<!-- removed "

// resolveBoxes resolves every ref against body's boxes, all or nothing: an
// error names the refs that match no box and lists the issue's ids.
func resolveBoxes(body string, refs []string) ([]CheckboxItem, error) {
	items := ListCheckboxes(body)
	var found []CheckboxItem
	var missing []string
	seen := map[int]bool{}
	for _, ref := range refs {
		it, st := ResolveCheckboxRef(items, ref)
		if st != RefFound {
			missing = append(missing, ref)
			continue
		}
		if !seen[it.Line] {
			seen[it.Line] = true
			found = append(found, it)
		}
	}
	if len(missing) > 0 {
		var ids []string
		for _, it := range items {
			if it.ID != "" {
				ids = append(ids, it.ID)
			}
		}
		return nil, fmt.Errorf("no checkbox %s; ids in this issue: %s", strings.Join(missing, ", "), strings.Join(ids, " "))
	}
	return found, nil
}

// UncheckBoxes unticks the boxes refs name (short or long ids), all or
// nothing. An unticked box drops its N/A claim (WithoutNA): the claim was
// the tick's reason, so a later tick must not inherit it. Returns the new
// body, the boxes it unticked as they were, and the ones already open.
func UncheckBoxes(body string, refs []string) (string, []CheckboxItem, []CheckboxItem, error) {
	found, err := resolveBoxes(body, refs)
	if err != nil {
		return body, nil, nil, err
	}
	lines := strings.Split(body, "\n")
	var unticked, already []CheckboxItem
	for _, it := range found {
		if !it.Checked {
			already = append(already, it)
			continue
		}
		line := lines[it.Line]
		i := strings.Index(line, "- [")
		lines[it.Line] = line[:i] + "- [ ]" + WithoutNA(line[i+len("- [x]"):])
		unticked = append(unticked, it)
	}
	return strings.Join(lines, "\n"), unticked, already, nil
}

// RemoveBoxes replaces the boxes refs name with a removed-box line
// ("<!-- removed AC6: <text> -->") that keeps each one's slot, so every other
// box keeps its id and no id is reused. All or nothing.
func RemoveBoxes(body string, refs []string) (string, []CheckboxItem, error) {
	found, err := resolveBoxes(body, refs)
	if err != nil {
		return body, nil, err
	}
	lines := strings.Split(body, "\n")
	for _, it := range found {
		line := lines[it.Line]
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		text := strings.ReplaceAll(it.Text, "--", "- -")
		id := it.ID
		if id == "" {
			id = fmt.Sprintf("#%d", it.Index)
		}
		lines[it.Line] = fmt.Sprintf("%s%s%s: %s -->", indent, removedBoxMarker, id, text)
	}
	return strings.Join(lines, "\n"), found, nil
}

// MatchUncheckedByText returns every unchecked checkbox whose label contains
// query (case-insensitive), scoped to a "## " section when section is
// non-empty. Boxes inside fenced code blocks are ignored.
func MatchUncheckedByText(body, section, query string) []CheckboxItem {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []CheckboxItem
	for _, it := range ListCheckboxes(body) {
		if it.Checked {
			continue
		}
		if strings.TrimSpace(section) != "" && !strings.EqualFold(it.Section, section) {
			continue
		}
		if strings.Contains(strings.ToLower(it.Text), q) {
			out = append(out, it)
		}
	}
	return out
}

// HasTestPlan checks if the body has ## Test Plan with ### Automated and ### Manual.
func HasTestPlan(body string) (hasAutomated, hasManual bool) {
	inTestPlan := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "## Test Plan") {
			inTestPlan = true
			continue
		}
		if inTestPlan && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if inTestPlan {
			if strings.EqualFold(trimmed, "### Automated") {
				hasAutomated = true
			}
			if strings.EqualFold(trimmed, "### Manual") {
				hasManual = true
			}
		}
	}
	return
}
