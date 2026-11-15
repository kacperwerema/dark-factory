package briefing

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Human Testing approval passes in YOLO like any gate (D-1 of
// cli/under-yolo-transition-human-testing-documentation-dry-run-says-ready-the):
// no text built from the rule names it as the owner's, on any path, even
// where a gate asks for it.
func TestYOLOTextsNeverNameHumanTesting(t *testing.T) {
	const words = "Human Testing"
	gate := func(status string) []tracker.WorkflowAction {
		return []tracker.WorkflowAction{{Type: "require_human_approval", Status: status}}
	}
	statuses := []tracker.WorkflowStatus{{Name: "testing"}, {Name: "human-testing"}, {Name: "documentation"}, {Name: "done"}}
	passing := &tracker.WorkflowConfig{Statuses: statuses, Transitions: []tracker.WorkflowTransition{
		{From: "human-testing", To: "documentation", Actions: gate("documentation")},
	}}
	asking := &tracker.WorkflowConfig{Statuses: statuses, Transitions: []tracker.WorkflowTransition{
		{From: "testing", To: "human-testing", Actions: gate("")},
	}}
	// The gate of one type's path, on an edge named after another status.
	typed := &tracker.WorkflowConfig{Statuses: statuses, DefaultType: "feature", Types: map[string]tracker.WorkflowType{
		"feature": {},
		"report": {Transitions: []tracker.WorkflowTransition{
			{From: "testing", To: "done", Actions: gate("Human Testing")},
		}},
	}}
	// A status's own validation rule is a gate too.
	validated := &tracker.WorkflowConfig{Statuses: []tracker.WorkflowStatus{{Name: "testing"}, {Name: "human-testing", Validation: []string{"human_approval: human-testing"}}}}

	var none *tracker.WorkflowConfig
	for name, wf := range map[string]*tracker.WorkflowConfig{
		"no workflow":                     none,
		"the status, another approval":    passing,
		"a gate asks for it":              asking,
		"a status validation asks for it": validated,
		"the base of a typed workflow":    typed,
		"the type that asks":              typed.ForType("report"),
		"the type that doesn't":           typed.ForType("feature"),
	} {
		if got := tracker.YOLOOwnerOnly(wf); strings.Contains(got, words) {
			t.Errorf("%s: YOLOOwnerOnly names it: %s", name, got)
		}
		if got := tracker.YOLORule(wf, "api/fix"); strings.Contains(got, words) {
			t.Errorf("%s: YOLORule names it: %s", name, got)
		}
		if got := tracker.DispatchGateLines(wf, true)["gate_rules"]; strings.Contains(got, words) {
			t.Errorf("%s: the briefing's stop lines name it: %s", name, got)
		}
	}

	// The whole briefing, on the path whose gate asks for it.
	old := tracker.YOLOOn
	t.Cleanup(func() { tracker.YOLOOn = old })
	tracker.YOLOOn = func(string, string) bool { return true }
	issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: "testing", BodyRaw: "Body text."}
	prompt := BuildDispatchPrompt(&tracker.Project{Slug: "proj"}, issue, typed.ForType("report"), "/w", nil).Prompt
	if strings.Contains(prompt, "the Human Testing approval") {
		t.Errorf("the briefing names the Human Testing approval as the owner's:\n%s", prompt)
	}
}

// Under YOLO the briefing drops "stop at every gate" for the YOLO rule; off,
// it is BuildAgentPrompt's.
func TestBuildDispatchPromptYOLO(t *testing.T) {
	old := tracker.YOLOOn
	t.Cleanup(func() { tracker.YOLOOn = old })
	wf := &tracker.WorkflowConfig{Statuses: []tracker.WorkflowStatus{{Name: "backlog", Prompt: "This is a handoff state.\nTell the human in chat that this needs `in progress` approval in the issue viewer, then stop and end your turn."}}}
	proj := &tracker.Project{Slug: "proj"}
	issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: "backlog", BodyRaw: "Body text."}

	tracker.YOLOOn = func(workDir, project string) bool { return false }
	off := BuildDispatchPrompt(proj, issue, wf, "/w", nil).Prompt
	if !strings.HasPrefix(off, tracker.BuildAgentPrompt(proj, issue, wf, "", "")) || !strings.Contains(off, "At a human-approval gate, tell the user in chat") {
		t.Fatalf("YOLO off changed the prompt:\n%s", off)
	}

	tracker.YOLOOn = func(workDir, project string) bool { return workDir == "/w" && project == "proj" }
	on := BuildDispatchPrompt(proj, issue, wf, "/w", nil).Prompt
	for _, gone := range []string{"At a human-approval gate, tell the user", "If approval is missing, tell the human", "Stop and ask the user", "If the transition needs human approval, tell the human", "Tell the human in chat that this needs"} {
		if strings.Contains(on, gone) {
			t.Errorf("YOLO prompt still says %q", gone)
		}
	}
	for _, want := range []string{"YOLO is on: gates pass without the owner", "This is a handoff state.\nYOLO rule: ", "issue-cli --project proj decide api/fix --provisional", "YOLO passes the approval this status waits for", tracker.YOLOOwnerOnly(wf)} {
		if !strings.Contains(on, want) {
			t.Errorf("YOLO prompt lacks %q", want)
		}
	}
}

// The briefing's ask line (step 4) names the issue's slug, YOLO on or off,
// as the YOLO rule does, never a literal <slug>.
func TestBriefingAskLineFillsInTheSlug(t *testing.T) {
	old := tracker.YOLOOn
	t.Cleanup(func() { tracker.YOLOOn = old })
	wf := &tracker.WorkflowConfig{Statuses: []tracker.WorkflowStatus{{Name: "backlog"}}}
	proj := &tracker.Project{Slug: "proj"}
	issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: "backlog", BodyRaw: "Body text."}
	for _, on := range []bool{false, true} {
		tracker.YOLOOn = func(string, string) bool { return on }
		prompt := BuildDispatchPrompt(proj, issue, wf, "/w", nil).Prompt
		start := strings.Index(prompt, "\n4. If you are unsure")
		end := strings.Index(prompt, "\n5. ")
		if start < 0 || end < start {
			t.Fatalf("YOLO %v: no ask line in:\n%s", on, prompt)
		}
		ask := prompt[start:end]
		if strings.Contains(ask, "<slug>") {
			t.Errorf("YOLO %v: ask line keeps <slug>:\n%s", on, ask)
		}
		for _, want := range []string{"issue-cli --project proj decide api/fix ", "issue-cli --project proj clarify api/fix)"} {
			if !strings.Contains(ask, want) {
				t.Errorf("YOLO %v: ask line lacks %q:\n%s", on, want, ask)
			}
		}
	}
}
