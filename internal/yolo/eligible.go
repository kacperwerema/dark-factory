package yolo

import (
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

// OwnerReason is why the from → to gate is the owner's even with YOLO on, ""
// when it isn't: an owner_only gate (it spends money or publishes outside
// the machine), any gate of an experiment (the owner adopts experiments,
// factory/factory-experiment-loop D-6), and the gate out of the design
// status of an issue labelled design-first. The Human Testing approval passes
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
