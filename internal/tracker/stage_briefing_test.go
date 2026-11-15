package tracker

import (
	"strings"
	"testing"
)

func TestPreviousStageSkipsAQueueStatus(t *testing.T) {
	wf := DefaultWorkflow()
	prev, sections := wf.PreviousStageSections("in progress")
	if prev != "in design" || indexOf(sections, "Design") < 0 {
		t.Fatalf("previous stage = %q %v", prev, sections)
	}
	if prev, _ := wf.PreviousStageSections("idea"); prev != "" {
		t.Fatalf("idea has a previous stage %q", prev)
	}
}

func TestFreshPending(t *testing.T) {
	mark := Comment{Text: StageMarkComment("testing")}
	if s, ok := FreshPending([]Comment{mark}); !ok || s != "testing" {
		t.Fatalf("pending = %q %v", s, ok)
	}
	started := Comment{Text: DispatchComment("start it", FreshDispatchHow("testing"), "2026-11-07")}
	if _, ok := FreshPending([]Comment{mark, started}); ok {
		t.Fatal("a fresh dispatch didn't answer the mark")
	}
	// The fresh start quotes the issue's words again: it is no new start,
	// and no takeover.
	first := Comment{Text: DispatchComment("start it", "CEO chat", "2026-11-07")}
	cs := []Comment{first, mark, started}
	if DispatchWords(cs) != "start it" || TakenOver(cs) {
		t.Fatalf("words = %q, taken over = %v", DispatchWords(cs), TakenOver(cs))
	}
	if _, ok := FreshPending(nil); ok {
		t.Fatal("no comments, pending")
	}
}

func TestHandoffWithoutRouting(t *testing.T) {
	wf := DefaultWorkflow()
	wf.Routing = nil
	if !strings.Contains(HandoffPromptSection(wf), "## Handoff") || HandoffReminder(wf) == "" {
		t.Fatal("no handoff without routing:")
	}
	if !strings.Contains(HandoffPromptSection(nil), HandoffFile) {
		t.Fatal("no handoff without a workflow")
	}
}

func indexOf(xs []string, x string) int {
	for i, s := range xs {
		if s == x {
			return i
		}
	}
	return -1
}

func TestLintSessions(t *testing.T) {
	wf := DefaultWorkflow()
	wf.GetStatus("testing").Session = "new"
	wf.GetStatus("in progress").BriefingBudget = 100
	wf.GetStatus("in progress").Session = SessionFresh
	wf.GetStatus("documentation").BriefingBudget = 4000
	got := strings.Join(wf.Lint(), "\n")
	for _, want := range []string{`status "testing" has session: "new"`, `status "in progress" has briefing_budget: 100`, `status "documentation" has briefing_budget: but no session: fresh`} {
		if !strings.Contains(got, want) {
			t.Errorf("lint misses %q:\n%s", want, got)
		}
	}
	if got := DefaultWorkflow().lintSessions(); len(got) != 0 {
		t.Fatalf("the bundled workflow: %v", got)
	}
}
