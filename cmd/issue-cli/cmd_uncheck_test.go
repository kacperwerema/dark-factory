package main

import (
	"os"
	"strings"
	"testing"
)

func TestUncheckByID(t *testing.T) {
	asOwner(t)
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "D1", "D2"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runUncheck(ctx, []string{slug, "d2"}); err != nil {
		t.Fatalf("uncheck: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Unchecked: D2 [Design #2] second task\n  Progress: 1/2\n")
	assertContains(t, readFile(t, issuePath), "- [x] first task\n- [ ] second task\n")

	stdout.Reset()
	if err := runUncheck(ctx, []string{slug, "D2"}); err != nil {
		t.Fatalf("uncheck an open box: %v", err)
	}
	assertContains(t, stdout.String(), "  Already open: D2 [Design #2] second task")

	before := readFile(t, issuePath)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{slug, "D1", "D9"}, "no checkbox D9; ids in this issue: D1 D2"},
		{[]string{slug}, "uncheck needs box ids"},
	} {
		if err := runUncheck(ctx, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want error containing %q, got %v", c.args, c.want, err)
		}
	}
	if readFile(t, issuePath) != before {
		t.Error("a failed uncheck changed the file")
	}
}

func TestCheckRemoveKeepsTheOtherIDs(t *testing.T) {
	asOwner(t)
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheckRemove(ctx, []string{slug, "D1"}); err != nil {
		t.Fatalf("check-remove: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Removed: D1 [Design #1] first task\n  The other boxes keep their ids.\n  Progress: 0/1\n")
	assertContains(t, readFile(t, issuePath), "## Design\n<!-- removed D1: first task -->\n- [ ] second task\n")

	stdout.Reset()
	if err := runChecklist(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "  D2 [ ] second task\n")
	assertNotContains(t, stdout.String(), "D1 ")

	// D2 still answers to D2, and D1 is gone for good.
	if err := runCheck(ctx, []string{slug, "D2"}); err != nil {
		t.Fatalf("check D2 after removing D1: %v", err)
	}
	if err := runCheckRemove(ctx, []string{slug, "D1"}); err == nil || !strings.Contains(err.Error(), "no checkbox D1; ids in this issue: D2") {
		t.Fatalf("removing D1 twice: %v", err)
	}
}

func TestCheckRemoveRefusesTheWorkflowsGateBoxes(t *testing.T) {
	asOwner(t)
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	// The default workflow gates ## Design and writes "Approach documented" into it.
	if err := runCheckEdit(ctx, []string{slug, "D1", "--text", "Approach documented"}); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, issuePath)
	err := runCheckRemove(ctx, []string{slug, "D1"})
	if err == nil {
		t.Fatal("removing a gate box: want a refusal")
	}
	assertContains(t, err.Error(), "these boxes are the workflow's, in a section a gate waits on; nothing was removed:\n  D1 Approach documented")
	assertContains(t, err.Error(), "issue-cli check cli/sample D1 --na \"no page changes\"")
	if readFile(t, issuePath) != before {
		t.Error("a refused check-remove changed the file")
	}
	// The author's own box in the same section can go.
	if err := runCheckRemove(ctx, []string{slug, "D2"}); err != nil {
		t.Fatalf("removing the author's box: %v", err)
	}
}

func TestAgentsCantUntickOrRemoveTheOwnersBoxes(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "human-testing")
	data := readFile(t, issuePath) + "\n## Human Testing\n- [x] Owner looked at it\n\n## Acceptance Criteria\n- [ ] Glows on hover (owner)\n- [x] Counts\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	asWorker(t)
	ctx, _, _ := newTestContext(proj, false)
	err := runUncheck(ctx, []string{slug, "HT1", "AC2"})
	if err == nil {
		t.Fatal("a worker unticking HT1: want a refusal")
	}
	assertContains(t, err.Error(), "these boxes are the owner's; agent-calc-other can't untick them, and nothing changed:\n  HT1 Owner looked at it (a Human Testing box)")
	assertContains(t, err.Error(), "The owner ticks them on the Owner checks page:\n  ")
	assertContains(t, err.Error(), "/owner-checks?epic=")
	err = runCheckRemove(ctx, []string{slug, "AC1"})
	if err == nil {
		t.Fatal("a worker removing an (owner) criterion: want a refusal")
	}
	assertContains(t, err.Error(), `AC1 Glows on hover (owner) (marked "(owner)")`)
	if readFile(t, issuePath) != data {
		t.Fatal("a refused call changed the file")
	}

	asOwner(t)
	ctx, _, _ = newTestContext(proj, false)
	if err := runUncheck(ctx, []string{slug, "HT1"}); err != nil {
		t.Fatalf("the owner unticking HT1: %v", err)
	}
	if err := runCheckRemove(ctx, []string{slug, "AC1"}); err != nil {
		t.Fatalf("the owner removing AC1: %v", err)
	}
}

func TestChecklistAfterCheckEditShowsNoProofForAnUnprovenBox(t *testing.T) {
	asOwner(t)
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheckEdit(ctx, []string{"cli/sample", "AC2", "--text", "The card glows on hover (visual)"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runChecklist(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "not by hand: 0/2 proven. See each link")

	if err := runProve(ctx, []string{"cli/sample", "AC1", "--value", "3 shown"}); err != nil {
		t.Fatal(err)
	}
	if err := runCheckEdit(ctx, []string{"cli/sample", "AC2", "--text", "The card glows (visual)"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runChecklist(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "not by hand: 1/2 proven (AC1). See each link")
}

func TestInDesignAddsNoStarterCriteriaToAFullSection(t *testing.T) {
	asOwner(t)
	proj, slug, issuePath := makeSimpleProject(t, "idea")
	body := strings.Replace(readFile(t, issuePath), "## Design", "## Acceptance criteria\n- [ ] Real criterion one\n- [ ] Real criterion two\n\n## Design", 1)
	if err := os.WriteFile(issuePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{slug, "--to", "in design"}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	got := readFile(t, issuePath)
	assertNotContains(t, got, "observable requirement")
	assertContains(t, got, "## Acceptance criteria\n- [ ] Real criterion one\n- [ ] Real criterion two\n")
	assertContains(t, stdout.String(), "## Acceptance Criteria existed with its own boxes — kept them, left out the 2 starter box(es)")
	if strings.Count(got, "## Acceptance") != 1 {
		t.Fatalf("criteria section duplicated:\n%s", got)
	}
}
