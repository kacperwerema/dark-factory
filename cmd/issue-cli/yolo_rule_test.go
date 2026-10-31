package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const (
	yoloIdeaPrompt    = "Clarify the issue with the human before moving into structured design.\n      Ask focused questions to remove ambiguity around goals, scope, and success criteria."
	yoloBacklogPrompt = "This is a handoff state.\n      Tell the human in chat that this needs `in progress` approval in the issue viewer, then stop and end your turn."
)

// yoloRuleProject is makeYOLOProject with the bundled idea and backlog
// prompts on its in design and backlog statuses.
func yoloRuleProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeYOLOProject(t)
	wfText := strings.Replace(yoloTestWorkflow, "  - name: \"in design\"\n", "  - name: \"in design\"\n    prompt: |\n      "+yoloIdeaPrompt+"\n", 1)
	wfText = strings.Replace(wfText, "  - name: \"backlog\"\n", "  - name: \"backlog\"\n    prompt: |\n      "+yoloBacklogPrompt+"\n", 1)
	if err := os.WriteFile(proj.WorkflowFile, []byte(wfText), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

func runAt(t *testing.T, proj *tracker.Project, now time.Time, run func(*Context, []string) error, args ...string) string {
	t.Helper()
	ctx, out, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := run(ctx, args); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestYOLORuleInStartShowAndTransition(t *testing.T) {
	proj := yoloRuleProject(t)
	now := time.Now()
	const clarify = "Clarify the issue with the human before moving into structured design.\nAsk focused questions"

	// Off: the guidance prints as written, no rule.
	out := runAt(t, proj, now, runStart, "design")
	assertContains(t, out, "== Current Status Guidance ==\n- "+clarify)
	if strings.Contains(out, "YOLO rule") {
		t.Fatalf("YOLO off printed the rule:\n%s", out)
	}
	if out := runAt(t, proj, now, runShow, "design"); strings.Contains(out, "YOLO rule") {
		t.Fatalf("YOLO off: show printed the rule:\n%s", out)
	}

	yoloOn(t, proj, now, time.Hour)
	out = runAt(t, proj, now, runStart, "design")
	assertContains(t, out, "== Current Status Guidance ==\n- YOLO rule: don't ask the owner and don't stop at gates.")
	assertContains(t, out, "issue-cli --project proj decide design --provisional")
	assertContains(t, out, "Still the owner's, even in YOLO: "+tracker.YOLOOwnerOnly)
	if strings.Contains(out, "Clarify the issue with the human") || strings.Contains(out, "Ask focused questions") {
		t.Fatalf("YOLO on: start still says to clarify with the human:\n%s", out)
	}

	out = runAt(t, proj, now, runShow, "design")
	assertContains(t, out, "YOLO rule: don't ask the owner")
	assertContains(t, out, tracker.YOLOOwnerOnly)

	// transition into backlog: the handoff line stays, the stop line goes.
	out = runAt(t, proj, now, runTransition, "design", "--to", "backlog")
	assertContains(t, out, "== Guidance ==\n- This is a handoff state.\n- YOLO rule: ")
	if strings.Contains(out, "then stop and end your turn") {
		t.Fatalf("YOLO on: transition still says to stop:\n%s", out)
	}
}

func TestDecideProvisionalAndConfirm(t *testing.T) {
	proj, samplePath, _ := makeDecisionFixture(t)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.Local)
	runAt(t, proj, now, runDecide, "cli/sample", "--provisional", "--how", "chat", "--text", "Keep the healer slot.")
	want := "- **D-1** · 2026-10-30 · chat, provisional: Keep the healer slot."
	assertContains(t, readFile(t, samplePath), want)
	assertContains(t, runAt(t, proj, now, runDecisions, "--issue", "cli/sample"), "needs owner's words")

	ctx, _, _ := newTestContext(proj, false)
	if err := runDecide(ctx, []string{"cli/sample", "--confirm", "D-1", "--provisional", "--quote", "yes"}); err == nil || !strings.Contains(err.Error(), "--confirm takes only --quote") {
		t.Fatalf("--confirm with --provisional: err = %v", err)
	}

	runAt(t, proj, now, runDecide, "cli/sample", "--confirm", "D-1", "--quote", "Yes, keep it.")
	assertContains(t, readFile(t, samplePath), `- **D-1** · 2026-10-30 · chat, confirmed by owner 2026-10-30: Keep the healer slot, "Yes, keep it."`)
	if out := runAt(t, proj, now, runDecisions, "--issue", "cli/sample"); strings.Contains(out, "needs owner's words") {
		t.Fatalf("confirmed decision still waits:\n%s", out)
	}
}

func TestDecideProvisionalUnderYOLO(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)
	runAt(t, proj, now, runDecide, "design", "--provisional", "--text", "Group the list by aisle.")
	body, _ := os.ReadFile(loadLeadIssue(t, proj, "design").FilePath)
	if !strings.Contains(string(body), "· YOLO, provisional: Group the list by aisle.") {
		t.Fatalf("decision not marked YOLO, provisional:\n%s", body)
	}
}
