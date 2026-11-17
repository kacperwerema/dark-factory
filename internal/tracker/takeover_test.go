package tracker

import (
	"strings"
	"testing"
)

func TestDispatchWordsAndTakenOver(t *testing.T) {
	c := func(text string) Comment { return Comment{Text: text} }
	first := DispatchComment("start it", "CEO chat", "2026-11-01") + " — recorded by lead-x"
	second := DispatchComment(`say "hi"`, "owner's shell", "2026-11-02")
	over := TakeoverComment(`say "hi"`, "claude", "2026-11-05")

	if got := DispatchWords([]Comment{c("progress: x"), c(first), c(second), c(over)}); got != `say "hi"` {
		t.Fatalf("words: %q", got)
	}
	if got := DispatchWords([]Comment{c(TakeoverComment("only this", "codex", "2026-11-05"))}); got != "" {
		t.Fatalf("a takeover's line is not a start: %q", got)
	}
	if TakenOver([]Comment{c(first), c(second), c("dispatch: not quoted"), c("takeover from claude")}) {
		t.Fatal("no takeover here")
	}
	if !TakenOver([]Comment{c(first), c(over)}) {
		t.Fatal("takeover not seen")
	}
}

func TestLimitedSessionName(t *testing.T) {
	if got := LimitedSessionName("factory/My-Issue"); got != "limited-factory-my-issue" {
		t.Fatal(got)
	}
}

func TestHandoffUnderRoutingNamesTheTakeover(t *testing.T) {
	with := &WorkflowConfig{Routing: &RoutingConfig{}}
	if got := HandoffPromptSection(with); !strings.Contains(got, "HANDOFF.md") || !strings.Contains(got, "what's done, the next step, open questions") || !strings.Contains(got, "the other agent takes your branch over") {
		t.Fatalf("section: %q", got)
	}
	if HandoffReminder(with) == "" {
		t.Fatal("no reminder under routing:")
	}
	if strings.Contains(HandoffPromptSection(&WorkflowConfig{}), "the other agent") {
		t.Fatal("a takeover named without routing:")
	}
}

// The worktree line the briefing kept for every issue before a type that
// builds nothing got its own; a building type keeps it word for word.
const worktreeHandoffLine = "Keep " + HandoffFile + " current at the root of your worktree, at each step: what's done, the next step, open questions. Three short lists are enough, and it is never committed. "

func TestBriefingHandoffForAReportHasNoWorktree(t *testing.T) {
	for _, typ := range []string{"report", "chore"} {
		wf := DefaultWorkflow().ForType(typ)
		if !wf.BuildsNothing() {
			t.Fatalf("%s: BuildsNothing false (ship status %q)", typ, wf.ShipStatus())
		}
		got := HandoffPromptSection(wf)
		if strings.Contains(got, "root of your worktree") || strings.Contains(got, "worktree's root") {
			t.Fatalf("%s: section names a worktree root: %q", typ, got)
		}
		if !strings.Contains(got, "## Handoff") || !strings.Contains(got, "`progress:` comment on the issue") || !strings.Contains(got, "what's done, the next step, open questions") {
			t.Fatalf("%s: section doesn't say where the handoff goes: %q", typ, got)
		}
		if r := HandoffReminder(wf); strings.Contains(r, "worktree") || !strings.Contains(r, "progress:") {
			t.Fatalf("%s: reminder: %q", typ, r)
		}
		wf.Routing = &RoutingConfig{}
		if got := HandoffPromptSection(wf); strings.Contains(got, "branch over") || !strings.Contains(got, "issue trail") {
			t.Fatalf("%s under routing: %q", typ, got)
		}
	}
}

func TestBriefingHandoffForAFeatureKeepsWorktree(t *testing.T) {
	for _, typ := range []string{"feature", "tweak", "docs", "bugfix"} {
		wf := DefaultWorkflow().ForType(typ)
		if wf.BuildsNothing() {
			t.Fatalf("%s: BuildsNothing true (ship status %q)", typ, wf.ShipStatus())
		}
		want := "\n\n## Handoff\n\n" + worktreeHandoffLine + handoffWhy(wf) + "\n"
		if got := HandoffPromptSection(wf); got != want {
			t.Fatalf("%s: section changed:\n%q\nwant\n%q", typ, got, want)
		}
		if r := HandoffReminder(wf); r != "Update "+HandoffFile+" at your worktree's root: what's done, the next step, open questions." {
			t.Fatalf("%s: reminder: %q", typ, r)
		}
	}
	untyped := &WorkflowConfig{Statuses: []WorkflowStatus{{Name: "idea"}, {Name: "in progress"}, {Name: "done"}}}
	if untyped.BuildsNothing() || !strings.Contains(HandoffPromptSection(untyped), worktreeHandoffLine) || !strings.Contains(HandoffPromptSection(nil), worktreeHandoffLine) {
		t.Fatal("an untyped workflow lost the worktree line")
	}
}
