package tracker

import (
	"strings"
	"testing"
)

// howToWorkLimit is the most the briefing's one How to work section may
// take, rendered for an 80-char slug with --project injected: the seven
// sections it replaced took ≈8.1k chars on the same issues.
const howToWorkLimit = 3000

// howToWork renders the briefing for an 80-char slug, YOLO on or off, and
// returns the whole prompt and its How to work section.
func howToWork(t *testing.T, yoloOn bool) (prompt, section string) {
	t.Helper()
	slug := "factory/" + strings.Repeat("x", 72)
	issue := &Issue{Slug: slug, Title: "T", Status: "idea", System: "Factory", BodyRaw: "Body."}
	prompt = BuildAgentPromptYOLO(&Project{Slug: "dark-factory"}, issue, nil, "", "", yoloOn)
	_, rest, ok := strings.Cut(prompt, "## How to work\n")
	if !ok {
		t.Fatalf("YOLO %v: no ## How to work in:\n%s", yoloOn, prompt)
	}
	section, _, _ = strings.Cut(rest, "\n## ")
	return prompt, "## How to work\n" + section
}

// AC1: one How to work section instead of the seven, ≤ howToWorkLimit chars.
func TestBriefingHasOneShortHowToWorkSection(t *testing.T) {
	for _, on := range []bool{false, true} {
		prompt, section := howToWork(t, on)
		for _, gone := range []string{"## Before you start", "## Your goal", "## How to work on this issue", "commands you can use freely", "## CRITICAL", "## If you hit a bug", "## Workflow review", "## Commands that require user approval"} {
			if strings.Contains(prompt, gone) {
				t.Errorf("YOLO %v: the briefing still has %q", on, gone)
			}
		}
		if n := strings.Count(prompt, "## How to work"); n != 1 {
			t.Errorf("YOLO %v: %d How to work sections", on, n)
		}
		if len(section) > howToWorkLimit {
			t.Errorf("YOLO %v: How to work is %d chars, over %d:\n%s", on, len(section), howToWorkLimit, section)
		}
	}
}

// AC2: every command and rule the seven sections carried is still there,
// once.
func TestHowToWorkKeepsEveryCommandOnce(t *testing.T) {
	for _, on := range []bool{false, true} {
		_, section := howToWork(t, on)
		slug := "factory/" + strings.Repeat("x", 72)
		cli := "issue-cli --project dark-factory "
		once := []string{
			cli + "process workflow", "process transitions",
			cli + "start " + slug,
			"show re-reads it", "update --title",
			"(checklist lists ids)", cli + "check " + slug + " <id>",
			"append [--section", cli + "transition " + slug + ` --to "<next-status>" --check <id>`, "--dry-run",
			"--provisional", "Questions for the owner go only in the design's clarify round", "a question: comment is for a blocker only",
			cli + "report-bug", cli + "create",
			cli + "retrospective " + slug, "Subsystem workflow for Factory: ...",
			"a backward transition", "creating or deleting issues",
			CloseRule, `close <dup> --duplicate-of "$ISSUE_VIEWER_ISSUE_SLUG"`,
			"This issue is your one task", "never its .md file",
		}
		for _, want := range once {
			if n := strings.Count(section, want); n != 1 {
				t.Errorf("YOLO %v: %q is in How to work %d times, want once:\n%s", on, want, n, section)
			}
		}
		if n := strings.Count(section, "question: comment"); n < 1 {
			t.Errorf("YOLO %v: How to work never says where to ask", on)
		}
	}
}

// AC3: every briefing's Reading code and docs asks for a step's reads in
// one turn.
func TestBriefingAsksToBatchReads(t *testing.T) {
	for _, on := range []bool{false, true} {
		prompt, _ := howToWork(t, on)
		_, rest, ok := strings.Cut(prompt, "## Reading code and docs\n")
		if !ok {
			t.Fatalf("YOLO %v: no Reading code and docs", on)
		}
		section, _, _ := strings.Cut(rest, "\n## ")
		for _, want := range []string{"Each turn re-reads the whole context", "in one turn", "several tool calls in one message"} {
			if !strings.Contains(section, want) {
				t.Errorf("YOLO %v: Reading code and docs lacks %q:\n%s", on, want, section)
			}
		}
	}
}
