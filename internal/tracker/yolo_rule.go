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

// YOLOOwnerOnly is what still waits for the owner while YOLO is on: the
// gates YOLO never passes and the questions too big to decide provisionally.
const YOLOOwnerOnly = "experiments, product-policy rules, and anything that spends money or publishes outside the machine (owner_only gates)"

// YOLORule is the one line a worker gets in place of "clarify with the
// human" and "stop for approval" while YOLO is on.
func YOLORule(slug string) string {
	return "YOLO rule: don't ask the owner and don't stop at gates. Answer open questions yourself: the design's go in its ## Clarify round, " +
		"each marked assumed (issue-cli clarify " + slug + " --assume); record any other answer you give yourself with " +
		"issue-cli decide " + slug + " --provisional --text \"...\"; put what you can't answer in a question: comment and go on. " +
		"Gates pass and the owner reviews them after the fact. Still the owner's, even in YOLO: " + YOLOOwnerOnly +
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
