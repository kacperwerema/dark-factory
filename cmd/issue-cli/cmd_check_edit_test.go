package main

import (
	"os"
	"strings"
	"testing"
)

func TestCheckEditKeepsTickAndID(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "D2"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runCheckEdit(ctx, []string{slug, "d2", "--text", "D2: second task, reworded"}); err != nil {
		t.Fatalf("runCheckEdit: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Edited D2 [Design #2]\n  was: second task\n  now: second task, reworded")
	data, _ := os.ReadFile(issuePath)
	assertContains(t, string(data), "- [ ] first task\n- [x] second task, reworded\n")
}

// The text can follow the id, as check's and comment's do, with the slug
// given or left out for the worker's own issue.
func TestCheckEditTakesTheTextAfterTheID(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheckEdit(ctx, []string{slug, "D1", "first task, positional"}); err != nil {
		t.Fatalf("runCheckEdit: %v", err)
	}
	assertContains(t, stdout.String(), "now: first task, positional")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
	if err := runCheckEdit(ctx, []string{"D2", "second task, own slug"}); err != nil {
		t.Fatalf("runCheckEdit without the slug: %v", err)
	}
	data, _ := os.ReadFile(issuePath)
	assertContains(t, string(data), "- [ ] first task, positional\n- [ ] second task, own slug\n")
}

func TestCheckEditErrors(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	before, _ := os.ReadFile(issuePath)
	ctx, _, _ := newTestContext(proj, false)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{slug, "D9", "--text", "x"}, "ids in this issue: D1 D2"},
		{[]string{slug, "D1"}, "requires the box's new text (--text, or after the id)"},
		{[]string{slug, "--text", "x"}, "exactly one box id"},
		{[]string{slug, "D1", "D2", "--text", "x"}, "in --text or after the id, not both"},
		{[]string{slug, "D1", "two", "words"}, "quote the text"},
		{[]string{slug, "D1", "--text", `two\nlines`}, "one line"},
	} {
		err := runCheckEdit(ctx, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want error containing %q, got %v", c.args, c.want, err)
		}
	}
	if after, _ := os.ReadFile(issuePath); string(after) != string(before) {
		t.Error("a failed check-edit changed the file")
	}
}

func TestReplaceLine(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runReplace(ctx, []string{slug, "--section", "Test Plan", "--match", "thing one", "--text", "- thing 1"}); err != nil {
		t.Fatalf("--match: %v", err)
	}
	assertContains(t, stdout.String(), "was: - thing one\n  now: - thing 1")
	// Lines count from the one after "## Design": line 2 is the second box.
	if err := runReplace(ctx, []string{slug, "--section", "Design", "--line", "2", "--text", "- [ ] second task, by line"}); err != nil {
		t.Fatalf("--line: %v", err)
	}
	data, _ := os.ReadFile(issuePath)
	assertContains(t, string(data), "- [ ] first task\n- [ ] second task, by line\n")
	assertContains(t, string(data), "- thing 1\n")

	before := string(data)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--line", "99"}, "no line 99:\n    1 | - [ ] first task"},
		{[]string{"--match", "task"}, "matches 2 lines"},
		{[]string{"--match", "nope"}, "matches no line"},
		{[]string{"--match", "("}, "not a valid regexp"},
		{[]string{"--line", "1", "--match", "x"}, "--line or --match, not both"},
	} {
		args := append([]string{slug, "--section", "Design", "--text", "x"}, c.args...)
		err := runReplace(ctx, args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want error containing %q, got %v", c.args, c.want, err)
		}
	}
	if after, _ := os.ReadFile(issuePath); string(after) != before {
		t.Error("a failed replace changed the file")
	}
}

func TestReplaceWarnsWhenItDropsASubheading(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	keep := "### Automated\n- [ ] go test\n\n### Manual\n- [ ] look"
	if err := runReplace(ctx, []string{slug, "--section", "Test Plan", "--body", keep}); err != nil {
		t.Fatalf("replace keeping the subheadings: %v", err)
	}
	if strings.Contains(stdout.String(), "⚠") {
		t.Errorf("a replace that keeps every ### heading warned:\n%s", stdout)
	}

	stdout.Reset()
	if err := runReplace(ctx, []string{slug, "--section", "Test Plan", "--body", "- [ ] only a box"}); err != nil {
		t.Fatalf("replace dropping the subheadings: %v", err)
	}
	assertContains(t, stdout.String(), "⚠ the new body drops `### Automated`, `### Manual`, which the old section had")
	assertContains(t, stdout.String(), "has_test_plan needs `### Automated` and `### Manual`")
	data, _ := os.ReadFile(issuePath)
	assertContains(t, string(data), "## Test Plan\n- [ ] only a box")

	// Another section: the warning names the heading, without the Test Plan note.
	if err := runReplace(ctx, []string{slug, "--section", "Design", "--body", "### Risks\n- none"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runReplace(ctx, []string{slug, "--section", "Design", "--body", "- plain"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "⚠ the new body drops `### Risks`")
	if strings.Contains(stdout.String(), "has_test_plan") {
		t.Errorf("a Design replace got the Test Plan note:\n%s", stdout)
	}
}

func TestReplaceWarnsWhenItDropsCheckboxes(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, "D2"}); err != nil {
		t.Fatal(err)
	}

	// Keeping every box (reworded prose around them, ticks kept) is quiet.
	stdout.Reset()
	keep := "New approach.\n\n- [ ] first task\n- [x] second task"
	if err := runReplace(ctx, []string{slug, "--section", "Design", "--body", keep}); err != nil {
		t.Fatalf("replace keeping the boxes: %v", err)
	}
	if strings.Contains(stdout.String(), "⚠") {
		t.Errorf("a replace that keeps every box warned:\n%s", stdout)
	}

	// Prose only: both template boxes are gone, and the warning names
	// each one by its old id, with the way to put it back.
	stdout.Reset()
	if err := runReplace(ctx, []string{slug, "--section", "## Design", "--body", "Just prose now."}); err != nil {
		t.Fatalf("replace dropping the boxes: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "⚠ the new body drops 2 boxes the old section had; D1 D2 no longer name them:\n")
	assertContains(t, out, "  D1 [ ] first task\n  D2 [x] second task\n")
	assertContains(t, out, "  put one back: issue-cli check-add "+slug+" --section Design --text '<text>'")
	data, _ := os.ReadFile(issuePath)
	assertContains(t, string(data), "## Design\nJust prose now.")
}

func TestReplaceWarnsWhenItDropsOneCheckbox(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runReplace(ctx, []string{slug, "--section", "Design", "--body", "- [ ] second task\n- [ ] a new one"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "⚠ the new body drops 1 box the old section had; D1 no longer names it:\n  D1 [ ] first task\n")
}
