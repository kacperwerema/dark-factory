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
