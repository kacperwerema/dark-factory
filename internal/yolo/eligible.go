package yolo

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/experiment"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ExperimentNote is what an experiment's gate says while YOLO is on.
const ExperimentNote = "YOLO is on, but " + tracker.YOLOOwnerAdopts + ", even in YOLO: this gate waits for the owner's approval in the issue viewer."

// OwnerOnlyNote is what an owner_only gate says while YOLO is on.
const OwnerOnlyNote = "YOLO is on, but this gate is the owner's even in YOLO: " + tracker.YOLOSpendsOwnerOnly + ". It waits for the owner's approval in the issue viewer, where it is in Needs you. Stop here and tell your lead."

// DesignFirstNote is what a design-first issue's design gate says while
// YOLO is on.
const DesignFirstNote = "YOLO is on, but this issue is labelled " + tracker.DesignFirstLabel + ": the owner looks at the design before the build, even in YOLO. This gate waits for the owner's approval in the issue viewer, where it is in Needs you. Stop here and tell your lead."

// OwnerBoxFirstNote is what a gate up to the build says while YOLO is on
// and boxes, an (owner) box due before the build, are open.
func OwnerBoxFirstNote(boxes []tracker.CheckboxItem) string {
	var b strings.Builder
	for i, it := range boxes {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s %q", it.ID, it.Label())
	}
	return "YOLO is on, but the owner ticks this before the build, even in YOLO: " + b.String() +
		". This gate waits until the owner ticks it in the issue viewer, where the issue is in Needs you; then YOLO passes it. Stop here and tell your lead."
}

// UpToBuild reports whether from → to is a step up to the build: it ends at
// or before wf's work status.
func UpToBuild(wf *tracker.WorkflowConfig, from, to string) bool {
	work := wf.GetStatusIndex(wf.RoleStatus(tracker.StatusRoleWork))
	fi, ti := wf.GetStatusIndex(from), wf.GetStatusIndex(to)
	return work >= 0 && fi >= 0 && fi < work && ti >= 0 && ti <= work
}

// OwnerReason is why the from → to gate is the owner's even with YOLO on, ""
// when it isn't: an owner_only gate (it spends money or publishes outside
// the machine), any gate of an experiment (the owner adopts experiments,
// factory/factory-experiment-loop D-6), and the gate out of the design
// status of an issue labelled design-first, and each gate up to the build
// while an (owner) box due before the build is open (OwnerBoxFirstNote,
// tracker.OwnerBoxesBeforeBuild). The Human Testing approval passes
// like any gate and waits for the owner's review after the fact (D-1 of
// cli/under-yolo-transition-human-testing-documentation-dry-run-says-ready-the).
func OwnerReason(wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string) string {
	switch {
	case wf.OwnerOnlyGate(from, to):
		return OwnerOnlyNote
	case issue != nil && experiment.Is(issue.Labels):
		return ExperimentNote
	case issue != nil && tracker.IsDesignFirst(issue.Labels) && wf.HasRole(from, tracker.StatusRoleDesign):
		return DesignFirstNote
	case issue != nil && UpToBuild(wf, from, to):
		if boxes := tracker.OwnerBoxesBeforeBuild(wf, issue.BodyRaw); len(boxes) > 0 {
			return OwnerBoxFirstNote(boxes)
		}
	}
	return ""
}

// Eligible reports whether YOLO may pass the from → to gate: the one rule,
// which the transition asks and Gates lists by. wf is the workflow scoped
// to the issue's type; a nil issue asks about the step alone. A forward
// step that needs an approval, to a status that isn't optional, and never
// a gate that is the owner's even in YOLO (OwnerReason).
func Eligible(wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string) bool {
	if wf.RequiredHumanApproval(from, to) == "" {
		return false
	}
	if OwnerReason(wf, issue, from, to) != "" {
		return false
	}
	fi, ti := wf.GetStatusIndex(from), wf.GetStatusIndex(to)
	if fi < 0 || ti <= fi {
		return false
	}
	if s := wf.GetStatus(to); s == nil || s.Optional {
		return false
	}
	return true
}
