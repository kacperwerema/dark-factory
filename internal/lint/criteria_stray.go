package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// strayLabel is a line that starts a list of acceptance boxes outside
// ## Acceptance Criteria: "**Acceptance:**" (alone or as a list item, as
// briefs write it) or "Extra criteria:".
var strayLabel = regexp.MustCompile(`(?i)^(?:[-*]\s+)?\**\s*(acceptance|extra (?:acceptance )?criteria)\s*:?\s*\**\s*:?\s*$`)

// criteriaGated reports whether the issue's path runs a criteria gate:
// criteria_checkboxes or criteria_proven on any of its transitions.
func criteriaGated(wf *tracker.WorkflowConfig) bool {
	if wf == nil {
		return false
	}
	for _, t := range wf.Transitions {
		for _, a := range t.Actions {
			if a.Type != "validate" {
				continue
			}
			if name, _ := tracker.SplitRule(a.Rule); name == "criteria_checkboxes" || name == "criteria_proven" {
				return true
			}
		}
	}
	return false
}

// strayBox is one acceptance box found outside the criteria section.
type strayBox struct {
	section, label, id, text string
}

// strayCriteriaBoxes lists the boxes under an **Acceptance:** or Extra
// criteria label outside ## Acceptance Criteria, in body order. A label's
// list runs until a heading or a line that is neither blank nor a box.
func strayCriteriaBoxes(body string) []strayBox {
	ids := map[int]string{}
	for _, it := range tracker.ListCheckboxes(body) {
		ids[it.Line] = it.ID
	}
	var out []strayBox
	section, label, fence := "", "", false
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = !fence
			label = ""
			continue
		}
		if fence {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "#"):
			label = ""
			if strings.HasPrefix(trimmed, "## ") {
				section = strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
			}
		case strayLabel.MatchString(trimmed):
			label = ""
			if !strings.EqualFold(section, tracker.CriteriaSection) {
				label = strayLabel.FindStringSubmatch(trimmed)[1]
			}
		case trimmed == "":
		case label != "" && (strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]")):
			out = append(out, strayBox{section: section, label: label, id: ids[i], text: strings.TrimSpace(trimmed[5:])})
		default:
			label = ""
		}
	}
	return out
}

// strayCriteria is one finding for an open issue whose path has a criteria
// gate and whose acceptance boxes sit outside ## Acceptance Criteria:
// prove refuses them, a Proves: trailer naming them is refused, and
// criteria_proven passes without them. The fix moves them in.
func strayCriteria(issue *tracker.Issue, wf *tracker.WorkflowConfig) (Finding, bool) {
	if !criteriaGated(wf) {
		return Finding{}, false
	}
	boxes := strayCriteriaBoxes(issue.BodyRaw)
	if len(boxes) == 0 {
		return Finding{}, false
	}
	var ids, moved []string
	var sections []string
	seen := map[string]bool{}
	for _, b := range boxes {
		if b.id != "" {
			ids = append(ids, b.id)
		}
		moved = append(moved, "- [ ] "+b.text)
		if !seen[b.section] {
			seen[b.section] = true
			sections = append(sections, b.section)
		}
	}
	where := "## " + sections[0]
	if sections[0] == "" {
		where = "above the first heading"
	}
	list := ""
	if len(ids) > 0 {
		list = " (" + strings.Join(ids, ", ") + ")"
	}
	body := strings.Join(moved, `\n`)
	add := fmt.Sprintf(`issue-cli append %s --section %s --body %s`, issue.Slug, shell.Quote(tracker.CriteriaSection), shell.Quote(body))
	if _, ok := tracker.SectionText(issue.BodyRaw, tracker.CriteriaSection); !ok {
		add = fmt.Sprintf(`issue-cli append %s --body "## %s\n%s"`, issue.Slug, tracker.CriteriaSection, body)
	}
	remove := fmt.Sprintf(`issue-cli replace %s --section %s --body-file - (the section without them)`, issue.Slug, shell.Quote(sections[0]))
	return Finding{
		Check: "criteria",
		Where: issue.Slug + " " + where,
		Message: fmt.Sprintf("%d box(es) under %q%s sit outside ## %s: prove refuses them, a Proves: trailer naming them is refused and criteria_proven passes without them",
			len(boxes), boxes[0].label, list, tracker.CriteriaSection),
		Fix: add + ", then " + remove,
	}, true
}
