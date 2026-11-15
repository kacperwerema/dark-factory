package lint

import (
	"strings"
	"testing"
)

func footerIssue(title, status, body string) string {
	return "---\ntitle: \"" + title + "\"\nstatus: " + status + "\ncreated: \"2026-11-04\"\n---\n\n" + body + "\n"
}

// The real paste: show's block inside ## Test Plan, on an issue already
// done. The finding names the issue and the section, once per section.
func TestShowFooterNamesTheIssueAndSection(t *testing.T) {
	pasted := "## Test Plan\n### Automated\n- [x] recorded\n\n== Checklist (11/19) ==\n  TP1 [ ] recorded\n" +
		"Tick done boxes by id, several at once: issue-cli check s/a <id> [<id>...]\n" +
		"Box text wrong? issue-cli check-edit s/a <id> --text \"...\"\n\n" +
		"== Decisions in force ==\n- x\nDisagree with one? Challenge it: issue-cli decision challenge <ref>\n\n" +
		"== Comments (3) ==\n  [2026-11-05] approval: \"YOLO\"\n\n## Testing\n- [x] passed\n"
	env := lintProject(t, minimalWorkflow, map[string]string{
		"S/a.md":    footerIssue("a", "in progress", pasted),
		"S/done.md": footerIssue("done", "done", pasted),
	})
	got := run(t, env, "show-footer")
	if len(got) != 2 {
		t.Fatalf("findings = %+v, want one per issue", got)
	}
	if !has(got, "s/a ## Test Plan", "6 line(s)", "== Checklist (11/19) ==", `replace s/a --section 'Test Plan'`) {
		t.Errorf("no finding naming s/a's section: %+v", got)
	}
	if !has(got, "s/done ## Test Plan") {
		t.Errorf("a closed issue's paste is not named: %+v", got)
	}
}

// Quoting show is fine: a code span, a blockquote, an indented example or
// a fence names the lines without being them.
func TestShowFooterLeavesQuotesAlone(t *testing.T) {
	body := "## Design\n" +
		"- `show` prints `== Checklist (1/2) ==` after the body.\n" +
		"`== Comments (3) ==`\n" +
		"`Tick done boxes by id, several at once: issue-cli check a <id>`\n" +
		"> == Decisions in force ==\n" +
		"    == Test Plan ==\n" +
		"The == Body == block.\n" +
		"== Comments on s/a (2 of 2) ==\n\n" +
		"```\n== Checklist (1/2) ==\nBox text wrong? issue-cli check-edit a <id>\n```\n\n" +
		"~~~\n== Test Plan ==\n~~~\n"
	env := lintProject(t, minimalWorkflow, map[string]string{"S/a.md": footerIssue("a", "in progress", body)})
	if got := run(t, env, "show-footer"); len(got) != 0 {
		t.Fatalf("quotes warned: %+v", got)
	}
}

func TestIsShowFooterLine(t *testing.T) {
	for line, want := range map[string]bool{
		"== Shared files (1, with 1 other branch) ==":    true,
		"== Shared files (4, with 4 other branches) ==":  true,
		"Merge main often and coordinate on these files": true,
		"== Body ==":      true,
		"== Next ==":      false, // start's and transition's, and quoted bare in briefs
		"## Test Plan":    false,
		"== Checklist ==": false,
	} {
		if IsShowFooterLine(line) != want {
			t.Errorf("IsShowFooterLine(%q) = %v, want %v", line, !want, want)
		}
	}
}

func TestShowFooterIsABoardCheck(t *testing.T) {
	if !boardChecks["show-footer"] {
		t.Error("show-footer reads issues: it belongs in boardChecks")
	}
	if !strings.Contains(strings.Join(Names(), ","), "show-footer") {
		t.Error("show-footer is not a check")
	}
}
