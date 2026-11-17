package tracker

import (
	"regexp"
	"strings"
)

// ownerBeforeBuild is box text that says the owner's check comes before
// the build: "A design sketch shown to the owner before building",
// "before the build", "before implementation", "before any code".
var ownerBeforeBuild = regexp.MustCompile(`(?i)\bbefore\s+(the\s+|any\s+)?(build|building|built|implementation|implementing|coding|code|work\s+starts)\b`)

// OwnerBoxesBeforeBuild is body's unticked boxes marked the owner's
// (IsOwnerMarked) that the owner ticks before the build: one whose text
// says so ("… shown to the owner before building (owner)"), or one in the
// section of a status before wf's work status ("## Design" for "in
// design"). Under YOLO the gates up to the build wait for them
// (yolo.OwnerReason); every other owner box waits in the after-the-fact
// queue as before.
func OwnerBoxesBeforeBuild(wf *WorkflowConfig, body string) []CheckboxItem {
	if wf == nil {
		return nil
	}
	work := wf.GetStatusIndex(wf.RoleStatus(StatusRoleWork))
	early := map[string]bool{}
	for i, s := range wf.GetStatusOrder() {
		if work >= 0 && i < work {
			early[statusSectionName(s)] = true
		}
	}
	var out []CheckboxItem
	for _, it := range ListCheckboxes(body) {
		if it.Checked || it.ID == "" || !IsOwnerMarked(it.Label()) {
			continue
		}
		if ownerBeforeBuild.MatchString(unquoted(it.Label())) || early[statusSectionName(it.Section)] {
			out = append(out, it)
		}
	}
	return out
}

// statusSectionName folds a status or section name to compare the two:
// "in design" and "Design" both read "design".
func statusSectionName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimSpace(strings.TrimPrefix(s, "in "))
}
