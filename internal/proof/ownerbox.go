package proof

import (
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The owner-box rule, one function for every path that ticks or changes a
// box: issue-cli check, append, replace and update --body, prove's ticking,
// playtest report -apply (through check), and the viewer's toggle and body
// save. Each asks OwnerBoxWhy (is this box the owner's?) or OwnerTicks
// (would this write tick an owner box?), and refuses an agent; who counts as
// an agent is the caller's (issue-cli: the worker guard's identity, the
// viewer: a request not from the owner's browser).

// OwnerBoxWhy says why a box is the owner's, or "" when anyone may tick or
// change it: a Human Testing box, a box marked "(owner)", a criterion only
// the owner proves, or a box a guided check or a playtest judgement step
// leaves to the owner. A zero Report (a workflow without proof links) still
// knows the first two.
func (r Report) OwnerBoxWhy(it tracker.CheckboxItem) string {
	switch {
	case strings.EqualFold(it.Section, humanTestingSection):
		return "a Human Testing box"
	case IsOwnerCriterion(it.Label()):
		return `marked "(owner)"`
	}
	if it.ID == "" {
		return ""
	}
	for _, c := range r.Criteria {
		if strings.EqualFold(c.ID, it.ID) && c.Owner != "" {
			return c.Owner
		}
	}
	for _, o := range r.OwnerBoxes {
		if strings.EqualFold(o.ID, it.ID) {
			return o.Why
		}
	}
	return ""
}

// OwnerTicks is the owner's boxes a write from before to after would tick:
// ticked in after, and open or missing in before. A box counts as already
// ticked when before had it ticked under the same id or the same text, so
// a rewrite that only moves a ticked box is not a tick. r is the report of
// the issue as before was.
func (r Report) OwnerTicks(before, after string) []OwnerItem {
	ticked := map[string]bool{}
	for _, it := range tracker.ListCheckboxes(before) {
		if it.Checked {
			ticked["id:"+strings.ToUpper(it.ID)] = it.ID != ""
			ticked["text:"+strings.TrimSpace(it.Label())] = true
		}
	}
	var out []OwnerItem
	for _, it := range tracker.ListCheckboxes(after) {
		if !it.Checked || ticked["id:"+strings.ToUpper(it.ID)] || ticked["text:"+strings.TrimSpace(it.Label())] {
			continue
		}
		if why := r.OwnerBoxWhy(it); why != "" {
			out = append(out, OwnerItem{ID: it.ID, Text: it.Label(), Why: why})
		}
	}
	return out
}
