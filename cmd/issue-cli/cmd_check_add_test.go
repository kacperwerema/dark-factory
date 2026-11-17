package main

import (
	"os"
	"strings"
	"testing"
)

func TestCheckAddPrintsTheNewIDAndKeepsTheOthers(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "D2"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runCheckAdd(ctx, []string{slug, "--section", "design", "--text", "third task"}); err != nil {
		t.Fatalf("runCheckAdd: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Added D3 [Design #3]: third task\n  The other boxes keep their ids.\n  Tick it when done: issue-cli check "+slug+" D3\n")
	assertContains(t, readFile(t, issuePath), "- [ ] first task\n- [x] second task\n- [ ] third task\n")

	stdout.Reset()
	ctx.JSONOutput = true
	if err := runCheckAdd(ctx, []string{slug, "--section", "Design", "--text", "fourth"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), `"id": "D4"`)
}

func TestCheckAddErrors(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	before, _ := os.ReadFile(issuePath)
	ctx, _, _ := newTestContext(proj, false)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{slug, "--section", "Acceptance Criteria", "--text", "x"}, `no section "Acceptance Criteria"; sections in this issue: "Design", "Test Plan"`},
		{[]string{slug, "--text", "x"}, "requires --section"},
		{[]string{slug, "--section", "Design"}, "requires --text"},
		{[]string{slug, "--section", "Design", "--text", `two\nlines`}, "one line"},
		{[]string{slug, "--section", "Design", "stray", "--text", "x"}, "no words after the slug"},
	} {
		err := runCheckAdd(ctx, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want error containing %q, got %v", c.args, c.want, err)
		}
	}
	if after, _ := os.ReadFile(issuePath); string(after) != string(before) {
		t.Error("a failed check-add changed the file")
	}
}

func TestAgentsCantAddTheOwnersBoxes(t *testing.T) {
	proj, slug, issuePath := ownerBoxesProject(t)
	data := readFile(t, issuePath)
	asWorker(t)
	ctx, _, _ := newTestContext(proj, false)
	for _, args := range [][]string{
		{slug, "--section", "Notes", "--text", "Looks right on a phone (owner)"},
		{slug, "--section", "Human Testing", "--text", "Tried it"},
	} {
		err := runCheckAdd(ctx, args)
		if err == nil || !strings.Contains(err.Error(), "can't add it, and nothing changed") {
			t.Errorf("%v: want the owner-box refusal, got %v", args, err)
		}
	}
	if got := readFile(t, issuePath); got != data {
		t.Fatalf("a refused check-add changed the file:\n%s", got)
	}
	if err := runCheckAdd(ctx, []string{slug, "--section", "Notes", "--text", "Logged twice"}); err != nil {
		t.Fatalf("a worker adding its own box: %v", err)
	}

	asOwner(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheckAdd(ctx, []string{slug, "--section", "Human Testing", "--text", "Tried it"}); err != nil {
		t.Fatalf("the owner adding a Human Testing box: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Added HT2 [Human Testing #2]: Tried it")
}

// A worker's own issue: the slug comes from ISSUE_VIEWER_ISSUE_SLUG, so the
// call starts with --section.
func TestCheckAddTakesTheWorkersOwnSlug(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheckAdd(ctx, []string{"--section", "Design", "--text", "own slug"}); err != nil {
		t.Fatalf("runCheckAdd without a slug: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Added D3")
	assertContains(t, readFile(t, issuePath), "- [ ] own slug\n")
}
