package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func overlapStep(id, expect string, proves ...string) *Step {
	st := &Step{ID: id, Expect: expect, Proves: proves}
	if expect != "" {
		st.Do = "look"
	}
	return st
}

// A criterion proved by a judgement step and a machine step is named once,
// with both steps, across the issue's sessions; another issue's step with
// the same id doesn't count.
func TestProofOverlapWarnings(t *testing.T) {
	got := ProofOverlapWarnings([]*Session{
		{Name: "a", Issue: "cli/one", Steps: []*Step{
			overlapStep("TP1", "reads well", "AC1", "AC2"),
			overlapStep("TP2", "", "AC2"),
			overlapStep("TP3", "", "AC3"),
		}},
		{Name: "b", Issue: "cli/one", Steps: []*Step{overlapStep("TP4", "", "AC1")}},
		{Name: "c", Issue: "cli/other", Steps: []*Step{overlapStep("TP1", "", "AC3"), overlapStep("TP2", "fine", "AC4")}},
		{Name: "d", Issue: "cli/third", Steps: []*Step{overlapStep("TP1", "", "AC4")}},
	}, nil)
	if len(got) != 2 {
		t.Fatalf("want AC1 and AC2 of cli/one only, got:\n%s", strings.Join(got, "\n"))
	}
	for i, want := range []string{
		"cli/one AC1: judgement step a/TP1 and machine step b/TP4",
		"cli/one AC2: judgement step a/TP1 and machine step a/TP2",
	} {
		if !strings.HasPrefix(got[i], want) {
			t.Errorf("warning %d = %q, want prefix %q", i, got[i], want)
		}
	}
}

// A closed issue's overlap can't be helped any more, so it isn't said.
func TestProofOverlapWarningsOnlyOpenIssues(t *testing.T) {
	sessions := []*Session{{Name: "a", Issue: "cli/one", Steps: []*Step{
		overlapStep("TP1", "reads well", "AC1"),
		overlapStep("TP2", "", "AC1"),
	}}}
	if got := ProofOverlapWarnings(sessions, func(string) bool { return false }); len(got) != 0 {
		t.Fatalf("closed issue warned: %v", got)
	}
	if got := ProofOverlapWarnings(sessions, func(string) bool { return true }); len(got) != 1 {
		t.Fatalf("open issue: %v", got)
	}
}

const overlapSession = `session: s
issue: cli/real
scenario: basic
steps:
  - id: TP1
    title: a step
    proves: [AC1]
    do: look
    expect: it reads well
  - id: TP2
    title: the same criterion, by machine
    proves: [AC1]
    checks: [{ visible: board.search }]
`

// check prints the warning and still passes: a warning, not a problem. The
// same session on a done issue says nothing.
func TestCheckWarnsOnOpenIssuesAndPasses(t *testing.T) {
	b := newCheckBoard(t, "TP1")
	writeBoardSession(t, b, overlapSession)
	code, out, errw := b.check(t)
	if code != 0 {
		t.Fatalf("done issue: code %d\nout: %s\nerr: %s", code, out, errw)
	}
	if strings.Contains(out, "warning:") {
		t.Fatalf("done issue warned:\n%s", out)
	}

	b.setStatus(t, "in progress")
	code, out, errw = b.check(t)
	if code != 0 || !strings.Contains(out, "warning: cli/real AC1: judgement step s/TP1 and machine step s/TP2") {
		t.Fatalf("open issue: code %d\nout: %s\nerr: %s", code, out, errw)
	}
}

// The rule stands in the guide a session's author reads, and in its copy.
func TestPlaytestGuideSaysWhatProvesDoes(t *testing.T) {
	for _, path := range []string{"docs/patterns/playtest/README.md", "cmd/issue-cli/guides/playtest/README.md"} {
		guide := mustRead(t, filepath.Join(repoDir(t), path))
		for _, want := range []string{"makes that criterion the owner's", "`make playtest-check` warns"} {
			if !strings.Contains(guide, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
	}
}
