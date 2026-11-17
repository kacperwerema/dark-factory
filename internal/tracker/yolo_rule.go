package tracker

import (
	"regexp"
	"strings"
	"unicode"
)

// YOLOOn reports whether YOLO mode is on for project, whose checkout is at
// workDir. internal/yolo sets it (tracker can't import yolo); nil reads as
// off.
var YOLOOn func(workDir, project string) bool

// What YOLO never passes, one name each. YOLONever* are the reasons
// yolo.OwnerGates lists and YOLOOwnerOnly is the same rules for a briefing;
// docs/Workflow/yolo.md quotes both (TestYOLOOwnerTextsReadOneSource). The
// Human Testing approval is in neither: YOLO passes it like any gate (D-1 of
// cli/under-yolo-transition-human-testing-documentation-dry-run-says-ready-the).
const (
	yoloSpends             = "spends money or publishes outside the machine"
	YOLOOwnerAdopts        = "the owner adopts experiments"
	yoloExperiments        = "experiments"
	yoloReleasePublish     = "release publish"
	yoloOptionalGate       = "a gate into an optional status"
	yoloDesignFirst        = "the design gate of an issue labelled " + DesignFirstLabel
	yoloOwnerBoxFirst      = "the gates up to the build while an (owner) box due before the build is open"
	YOLOSpendsOwnerOnly    = "the step " + yoloSpends + " (owner_only in the workflow)"
	YOLONeverOwnerOnly     = "owner_only: " + yoloSpends
	YOLONeverExperiment    = "every gate of an issue labelled experiment: " + YOLOOwnerAdopts
	YOLONeverRelease       = yoloReleasePublish + ": external releases wait for the owner"
	YOLONeverOptional      = yoloOptionalGate + ": a side step the owner chooses"
	YOLONeverDesignFirst   = yoloDesignFirst + ": the owner looks at the design before the build"
	YOLONeverOwnerBoxFirst = yoloOwnerBoxFirst + ": the owner ticks it first (tracker.OwnerBoxesBeforeBuild)"
)

// DesignFirstLabel marks an issue whose design the owner sees before the
// build: under YOLO its gate out of the design status still waits for the
// owner (yolo.OwnerReason).
const DesignFirstLabel = "design-first"

// IsDesignFirst reports whether labels mark the issue design-first.
func IsDesignFirst(labels []string) bool {
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l), DesignFirstLabel) {
			return true
		}
	}
	return false
}

// YOLOOwnerOnly is what still waits for the owner while YOLO is on: the
// gates YOLO never passes, and product-policy rules, which name no gate: a
// worker judges those itself (the owner's question from the 2026-11-03
// audit, enforce or drop, is open). wf is the workflow the reader walks;
// the list reads the same on every one today.
func YOLOOwnerOnly(wf *WorkflowConfig) string {
	return yoloExperiments + ", " + yoloReleasePublish + ", " + yoloOptionalGate +
		", " + yoloDesignFirst + ", " + yoloOwnerBoxFirst + ", product-policy rules, and anything that " + yoloSpends + " (owner_only gates)"
}

// YOLORulePointer is what show and transition print while YOLO is on, in
// place of YOLORule: the paragraph prints once a session, in the briefing
// and in start.
const YOLORulePointer = "YOLO on: the rule is in your briefing."

// YOLORule is the one line a worker gets in place of "clarify with the
// human" and "stop for approval" while YOLO is on; wf is the issue's
// workflow.
func YOLORule(wf *WorkflowConfig, slug string) string {
	return "YOLO rule: don't ask the owner and don't stop at gates. Answer open questions yourself: the design's go in its ## Clarify round, " +
		"each marked assumed (issue-cli clarify " + slug + " --assume); record any other answer you give yourself with " +
		"issue-cli decide " + slug + " --provisional --text \"...\"; put what you can't answer in a question: comment and go on. " +
		"Gates pass and the owner reviews them after the fact. Still the owner's, even in YOLO: " + YOLOOwnerOnly(wf) +
		"; for those post a question: comment and stop."
}

var (
	// yoloStopRe: a sentence that stops the agent for the owner. "Clarify …
	// with the human" stops; the clarify round (## Clarify) does not.
	yoloStopRe = regexp.MustCompile(`(?i)\bend your turn\b|^\s*ask\b|\bclarif\w*\s+(?:\S+\s+){0,3}?with\b`)
	// A sentence naming the human together with one of these asks or waits.
	yoloHumanRe = regexp.MustCompile(`(?i)\b(human|owner|user)s?\b([^-]|$)`) // not "human-testing"
	yoloWaitRe  = regexp.MustCompile(`(?i)\b(ask|tell|wait|approv\w*|confirm\w*|messages you)\b`)
)

// YOLOGuidance drops the sentences of a status or entry prompt that tell the
// agent to ask, clarify with or tell the human, wait for approval, or stop
// and end its turn; YOLORule says what to do instead. The other sentences
// are kept as written. dropped reports whether any sentence went.
func YOLOGuidance(prompt string) (kept string, dropped bool) {
	var lines []string
	for _, line := range strings.Split(prompt, "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, line)
			continue
		}
		var keep []string
		for _, s := range splitSentences(line) {
			if yoloStopRe.MatchString(s) || (yoloHumanRe.MatchString(s) && yoloWaitRe.MatchString(s)) {
				dropped = true
				continue
			}
			keep = append(keep, s)
		}
		if len(keep) > 0 {
			lines = append(lines, strings.Join(keep, " "))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), dropped
}

// splitSentences splits line after '.', '!' or '?' followed by a space and
// a capital letter or a backtick, so "e.g. foo" and "`x.y`" stay whole.
func splitSentences(line string) []string {
	var out []string
	rs := []rune(line)
	start := 0
	for i := 0; i+2 < len(rs); i++ {
		if (rs[i] == '.' || rs[i] == '!' || rs[i] == '?') && rs[i+1] == ' ' && (unicode.IsUpper(rs[i+2]) || rs[i+2] == '`') {
			out = append(out, strings.TrimSpace(string(rs[start:i+1])))
			start = i + 2
		}
	}
	if rest := strings.TrimSpace(string(rs[start:])); rest != "" {
		out = append(out, rest)
	}
	return out
}
