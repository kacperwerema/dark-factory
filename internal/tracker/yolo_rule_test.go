package tracker

import (
	"strings"
	"testing"
)

func TestYOLOGuidanceDropsAskAndStopSentences(t *testing.T) {
	cases := []struct{ name, prompt, want string }{
		{"idea", "Clarify the issue with the human before moving into structured design.\nAsk focused questions to remove ambiguity around goals, scope, and success criteria.", ""},
		{"backlog", "This is a handoff state.\nTell the human in chat that this needs `in progress` approval in the issue viewer, then stop and end your turn; continue when the human messages you that it is approved.", "This is a handoff state."},
		{"in design entry", "Write the design into the issue body. When the design is complete, tell the human in chat that it needs approval in the issue viewer: `backlog` to queue it. Then stop and end your turn. When the human says it is approved, run `issue-cli context <slug>`.", "Write the design into the issue body."},
		{"human-testing is no human", "Run the tests, e.g. the suite, and state whether manual checks are needed before human-testing.", "Run the tests, e.g. the suite, and state whether manual checks are needed before human-testing."},
	}
	for _, c := range cases {
		got, dropped := YOLOGuidance(c.prompt)
		if got != c.want || dropped != (got != c.prompt) {
			t.Errorf("%s: got %q (dropped %v), want %q", c.name, got, dropped, c.want)
		}
	}
}

func TestYOLORuleNamesWhatWaitsForTheOwner(t *testing.T) {
	rule := YOLORule("api/fix")
	for _, want := range []string{"issue-cli decide api/fix --provisional", "question: comment", "experiments", "product-policy", "spends money or publishes outside the machine"} {
		if !strings.Contains(rule, want) {
			t.Errorf("rule lacks %q: %s", want, rule)
		}
	}
}

// Under YOLO the briefing drops "stop at every gate" for the YOLO rule; off,
// it is BuildAgentPrompt's.
func TestBuildDispatchPromptYOLO(t *testing.T) {
	old := YOLOOn
	t.Cleanup(func() { YOLOOn = old })
	wf := &WorkflowConfig{Statuses: []WorkflowStatus{{Name: "backlog", Prompt: "This is a handoff state.\nTell the human in chat that this needs `in progress` approval in the issue viewer, then stop and end your turn."}}}
	proj := &Project{Slug: "proj"}
	issue := &Issue{Slug: "api/fix", Title: "Fix", Status: "backlog", BodyRaw: "Body text."}

	YOLOOn = func(workDir, project string) bool { return false }
	off := BuildDispatchPrompt(proj, issue, wf, "/w", nil).Prompt
	if !strings.HasPrefix(off, BuildAgentPrompt(proj, issue, wf, "", "")) || !strings.Contains(off, "At a human-approval gate, tell the user in chat") {
		t.Fatalf("YOLO off changed the prompt:\n%s", off)
	}

	YOLOOn = func(workDir, project string) bool { return workDir == "/w" && project == "proj" }
	on := BuildDispatchPrompt(proj, issue, wf, "/w", nil).Prompt
	for _, gone := range []string{"At a human-approval gate, tell the user", "If approval is missing, tell the human", "Stop and ask the user", "If the transition needs human approval, tell the human", "Tell the human in chat that this needs"} {
		if strings.Contains(on, gone) {
			t.Errorf("YOLO prompt still says %q", gone)
		}
	}
	for _, want := range []string{"YOLO is on: gates pass without the owner", "This is a handoff state.\nYOLO rule: ", "issue-cli --project proj decide api/fix --provisional", "YOLO passes the approval this status waits for", YOLOOwnerOnly} {
		if !strings.Contains(on, want) {
			t.Errorf("YOLO prompt lacks %q", want)
		}
	}
}
