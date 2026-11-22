package main

import (
	"os"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// comment --amend is comment edit <slug> last: the caller's own latest
// comment, its number and date kept; another author's never.
func TestCommentAmendOwnLastOnly(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	asWorker(t) // agent-calc-other

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, "--text", "report: 3 tset failing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Wrong? issue-cli comment "+slug+" --amend --text")
	if err := tracker.AddCommentBy(issuePath, 0, "progress: someone else's", "cli", "agent-someone-else"); err != nil {
		t.Fatal(err)
	}
	before, _ := tracker.LoadComments(issuePath)

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, "--amend", "--text", "report: 3 tests failing"}); err != nil {
		t.Fatalf("amend: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Comment #1 on "+slug+" edited by agent-calc-other")
	after, _ := tracker.LoadComments(issuePath)
	if len(after) != 2 {
		t.Fatalf("amend added or dropped a comment: %+v", after)
	}
	if got := after[0]; got.Text != "report: 3 tests failing" || got.ID != before[0].ID || got.Date != before[0].Date {
		t.Fatalf("amended comment %+v, want the new text with id %d and date %s", got, before[0].ID, before[0].Date)
	}
	if after[1].Text != "progress: someone else's" || len(after[1].Edits) != 0 {
		t.Fatalf("the other author's newer comment changed: %+v", after[1])
	}

	// The other author, amending: its own latest is #2, never ours; with
	// none of its own, refused.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "calc/third")
	file := readFile(t, issuePath)
	ctx, _, _ = newTestContext(proj, false)
	err := runComment(ctx, []string{slug, "--amend", "--text", "x"})
	if err == nil || !strings.Contains(err.Error(), "agent-calc-third has no comment on "+slug) {
		t.Fatalf("amend with no comment of its own: %v", err)
	}
	if readFile(t, issuePath) != file {
		t.Fatal("a refused amend changed the file")
	}

	// A locked comment of its own stays as it is: --amend skips it and
	// amends its own words, #1; by id it is refused.
	asWorker(t)
	if err := tracker.AddCommentBy(issuePath, 0, tracker.ApprovalComment("go", "viewer", "2026-11-16", "in progress"), "cli", "agent-calc-other"); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, "--amend", "--text", "x"}); err != nil {
		t.Fatalf("amend past an approval: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Comment #1 on "+slug)
	if after, _ := tracker.LoadComments(issuePath); len(after[2].Edits) != 0 {
		t.Fatalf("the approval changed: %+v", after[2])
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runComment(ctx, []string{"edit", slug, "3", "--text", "x"}); err == nil || !strings.Contains(err.Error(), "is a approval: comment") {
		t.Fatalf("edit of an approval: %v", err)
	}

	// No session: no author, nothing to amend.
	asOwner(t)
	t.Setenv("CLAUDECODE", "")
	ctx, _, _ = newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, "--amend", "--text", "x"}); err == nil || !strings.Contains(err.Error(), "no author") {
		t.Fatalf("amend with no session: %v", err)
	}
}

// The old text stays in the comment's history, oldest first.
func TestAmendKeepsOldTextInTrace(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	asWorker(t)
	for _, args := range [][]string{
		{slug, "--text", "tests: 4 pased"},
		{slug, "--amend", "--text", "tests: 4 passed"},
		{slug, "--amend", "tests: 5 passed"},
	} {
		ctx, _, _ := newTestContext(proj, false)
		if err := runComment(ctx, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	comments, _ := tracker.LoadComments(issuePath)
	if len(comments) != 1 {
		t.Fatalf("want one comment, got %+v", comments)
	}
	c := comments[0]
	if c.Text != "tests: 5 passed" || len(c.Edits) != 2 || c.Edits[0].Text != "tests: 4 pased" || c.Edits[1].Text != "tests: 4 passed" {
		t.Fatalf("history: %+v", c)
	}
	assertContains(t, readFile(t, issuePath), "tests: 4 pased")
}

// append --replace swaps the one line of the section that reads the old
// line; none or several are refused with the section's lines named.
func TestAppendReplaceOneExactLine(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	data := readFile(t, issuePath) + "\n## Notes\n- Risk: tbd\n- uses `a.b` (x)\n- same\n- same\n\n## Later\n- Risk: tbd\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug, "--section", "Notes", "--replace", "- Risk: tbd ", "--body", "- Risk: none"}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	assertContains(t, stdout.String(), "was: - Risk: tbd")
	body := issueBody(t, issuePath)
	assertContains(t, body, "## Notes\n- Risk: none\n- uses `a.b` (x)\n")
	assertContains(t, body, "## Later\n- Risk: tbd\n")

	// Regexp characters in the line are literal.
	ctx, _, _ = newTestContext(proj, false)
	if err := runAppend(ctx, []string{slug, "--section", "Notes", "--replace", "- uses `a.b` (x)", "--body", "- uses `a.b` (y)"}); err != nil {
		t.Fatalf("literal line: %v", err)
	}
	assertContains(t, issueBody(t, issuePath), "- uses `a.b` (y)\n")

	before := readFile(t, issuePath)
	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"two matches", []string{"--section", "Notes", "--replace", "- same", "--body", "x"},
			[]string{"--replace '- same' matches 2 lines in section \"Notes\"", "    3 | - same\n    4 | - same", "--line <n>"}},
		{"no match", []string{"--section", "Notes", "--replace", "- Risk", "--body", "x"},
			[]string{"--replace '- Risk' matches no line", "1 | - Risk: none"}},
		{"no section", []string{"--replace", "- same", "--body", "x"}, []string{"needs --section"}},
		{"with force", []string{"--section", "Notes", "--force", "--replace", "- same", "--body", "x"}, []string{"takes no --force"}},
		{"two new lines", []string{"--section", "Notes", "--replace", "- Risk: none", "--body", "a\nb"}, []string{"single line"}},
	} {
		ctx, _, _ := newTestContext(proj, false)
		err := runAppend(ctx, append([]string{slug}, c.args...))
		if err == nil {
			t.Errorf("%s: want a refusal", c.name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q lacks %q", c.name, err, w)
			}
		}
		if readFile(t, issuePath) != before {
			t.Fatalf("%s: a refused replace changed the file", c.name)
		}
	}
}
