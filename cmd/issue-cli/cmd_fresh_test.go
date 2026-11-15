package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// freshProject is makeAutostartProject with in progress set to session:
// fresh, high dispatched on the given words and its worker live with the
// given pane. Closes are recorded, not run.
func freshProject(t *testing.T, session, pane string) (*tracker.Project, *[]string) {
	t.Helper()
	proj := makeAutostartProject(t)
	wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n"
	if session != "" {
		wf += "    session: \"" + session + "\"\n"
	}
	mustWrite(t, proj.WorkflowFile, wf+"  - name: \"done\"\n")
	if err := tracker.AddCommentBy(filepath.Join(proj.IssueDir, "high.md"), 0, tracker.DispatchComment("start high", "CEO chat", "2026-11-07"), "cli", "lead-proj"); err != nil {
		t.Fatal(err)
	}
	stubPanes(t, map[string]string{"agent-high": pane})
	live := map[string]bool{"agent-high": true}
	origHas, origClose := agentrun.HasSession, agentrun.CloseSession
	agentrun.HasSession = func(target string) bool {
		return live[strings.TrimSuffix(strings.TrimPrefix(target, "="), ":")]
	}
	var closed []string
	agentrun.CloseSession = func(_ *tracker.Project, name string) error {
		closed = append(closed, name)
		live[name] = false
		return nil
	}
	t.Cleanup(func() { agentrun.HasSession, agentrun.CloseSession = origHas, origClose })
	return proj, &closed
}

func highComments(t *testing.T, proj *tracker.Project) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(proj.IssueDir, "high.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestFresh_TransitionMarksAndStartsNothing(t *testing.T) {
	proj, closed := freshProject(t, "fresh", "⏺ Done.\n")
	spec := captureStart(t, "dispatched")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"high", "--to", "in progress"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "in progress starts a fresh session (session: fresh in the workflow)")
	assertContains(t, stdout.String(), "Write HANDOFF.md at your worktree's root now")
	assertContains(t, stdout.String(), "issue-cli --project proj dispatch high --fresh")
	assertContains(t, highComments(t, proj), "stage: fresh session at in progress")
	if spec.Session != "" || len(*closed) != 0 {
		t.Fatalf("transition started %+v or closed %v", spec, *closed)
	}
}

// Unset or continue: the transition marks nothing and says nothing new.
func TestFresh_ContinueStatusMarksNothing(t *testing.T) {
	for _, session := range []string{"", "continue"} {
		proj, _ := freshProject(t, session, "⏺ Done.\n")
		ctx, stdout, _ := newTestContext(proj, false)
		if err := runTransition(ctx, []string{"high", "--to", "in progress"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stdout.String(), "fresh session") || strings.Contains(highComments(t, proj), "stage:") {
			t.Fatalf("session %q marked a fresh session:\n%s", session, stdout)
		}
	}
}

func TestFresh_TickOffersTheStartAndDispatchRunsIt(t *testing.T) {
	proj, closed := freshProject(t, "fresh", "⏺ Done.\n")
	spec := captureStart(t, "dispatched")
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"high", "--to", "in progress"}); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	printTakeovers(ctx)
	assertContains(t, stdout.String(), "high entered in progress (session: fresh): start its new session: issue-cli --project proj dispatch high --fresh")

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"high", "--fresh"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*closed, ",") != "agent-high" {
		t.Fatalf("closed %v", *closed)
	}
	if spec.Session != "agent-high" || !strings.Contains(spec.Prompt, "## You start this stage fresh") || strings.Contains(spec.Prompt, "Body of High") {
		t.Fatalf("started %q on %.300s", spec.Session, spec.Prompt)
	}
	assertContains(t, highComments(t, proj), `start high\" (fresh session at in progress, `)

	// Answered: the tick goes quiet and a second --fresh starts nothing.
	ctx, stdout, _ = newTestContext(proj, false)
	printTakeovers(ctx)
	if strings.Contains(stdout.String(), "--fresh") {
		t.Fatalf("the tick still offers it: %s", stdout)
	}
	*spec = agentrun.Spec{}
	err := runDispatch(ctx, []string{"high", "--fresh"})
	if exitOf(err) != 1 || spec.Session != "" {
		t.Fatalf("second --fresh: %v, started %q", err, spec.Session)
	}
}

// A worker still in a turn holds the start: two writers in one worktree.
func TestFresh_HoldsWhileThePaneIsInATurn(t *testing.T) {
	proj, closed := freshProject(t, "fresh", "✶ Thinking… (esc to interrupt)\n")
	spec := captureStart(t, "dispatched")
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"high", "--to", "in progress"}); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	printTakeovers(ctx)
	assertContains(t, stdout.String(), "hold: its worker is still in a turn")
	err := runDispatch(ctx, []string{"high", "--fresh"})
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "still in a turn") || spec.Session != "" || len(*closed) != 0 {
		t.Fatalf("--fresh mid-turn: %v, started %q, closed %v", err, spec.Session, *closed)
	}
}

func TestFresh_WorkersAreRefused(t *testing.T) {
	proj, closed := freshProject(t, "fresh", "⏺ Done.\n")
	spec := captureStart(t, "dispatched")
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"high", "--to", "in progress"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	ctx, _, _ = newTestContext(proj, false)
	err := runDispatch(ctx, []string{"high", "--fresh"})
	if err == nil || !strings.Contains(err.Error(), "workers can't dispatch") || spec.Session != "" || len(*closed) != 0 {
		t.Fatalf("a worker's --fresh: %v", err)
	}
}

func TestFresh_FlagsDontMix(t *testing.T) {
	proj, _ := freshProject(t, "fresh", "⏺ Done.\n")
	ctx, _, _ := newTestContext(proj, false)
	err := runDispatch(ctx, []string{"high", "--fresh", "--quote", "x"})
	if exitOf(err) != 2 {
		t.Fatalf("--fresh --quote: %v", err)
	}
}

func TestFresh_ProcessWorkflowNamesTheStatus(t *testing.T) {
	proj, _ := freshProject(t, "fresh", "⏺ Done.\n")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProcess(ctx, []string{"workflow"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "[role: work] [session: fresh, briefing budget 3000 tokens]")
	assertContains(t, stdout.String(), "the lead's issue-cli dispatch <slug> --fresh starts a new session")
}

// dispatch-prompt --json carries the stage briefing's size against its
// budget: what the bench sandbox and the trace's briefing record read.
func TestFresh_DispatchPromptJSONCarriesTheStage(t *testing.T) {
	proj, _ := freshProject(t, "fresh", "⏺ Done.\n")
	mustWrite(t, filepath.Join(proj.IssueDir, "base.md"), "---\ntitle: \"Base\"\nstatus: \"in progress\"\n---\n\nBody of Base.\n")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDispatchPrompt(ctx, []string{"base", "--json"}); err != nil {
		t.Fatal(err)
	}
	var dp tracker.DispatchPrompt
	if err := json.Unmarshal(stdout.Bytes(), &dp); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	if dp.Stage == nil || dp.Stage.Budget != tracker.DefaultBriefingBudget || dp.Stage.Tokens == 0 || dp.Stage.Tokens > dp.Stage.Budget {
		t.Fatalf("stage = %+v", dp.Stage)
	}
	if !strings.Contains(dp.Prompt, "Briefing: ") || strings.Contains(dp.Prompt, "Body of Base") {
		t.Fatalf("prompt: %.300s", dp.Prompt)
	}
	// A continue status carries no stage.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runDispatchPrompt(ctx, []string{"high", "--json"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), `"stage"`) {
		t.Fatalf("backlog carries a stage: %s", stdout)
	}
}
