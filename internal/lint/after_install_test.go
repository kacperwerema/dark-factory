package lint

import (
	"strings"
	"testing"
)

// A Test Plan line opening with "After install" or "Live:" is prose nobody
// runs unless it names an acceptance criterion carrying (after install: …):
// lint warns on each such line of an open issue with the check-add fix;
// the line naming a marked criterion, a line inside a fence, other Test
// Plan lines and a closed issue stay quiet.
func TestAfterInstallFlagsProseLiveChecksInTheTestPlan(t *testing.T) {
	issue := func(title, status, body string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: " + status + "\ncreated: \"2026-11-21\"\n---\n\n" + body
	}
	open := "## Acceptance Criteria\n" +
		"- [ ] AC1 lint warns (test: TestX)\n" +
		"- [ ] AC2 every lead sees the line (after install: issue-cli check-script s/open --yes)\n" +
		"\n## Test Plan\n### Automated\n" +
		"- [ ] go test ./internal/lint\n" +
		"- [ ] After install, run the commit-msg hook on a message it refuses\n" +
		"- [ ] after install: AC2, the lead runs the check script\n" +
		"### Manual\n" +
		"- **Live:** the leak warning shows on a real commit\n" +
		"- Live check: elevenlabs voices list\n" +
		"```\nAfter install run this\n```\n" +
		"- the installed build (after install) is checked by the lead\n" +
		"\n## Notes\nAfter install, something outside the Test Plan\n"
	env := lintProject(t, minimalWorkflow, map[string]string{
		"S/open.md":   issue("open", "idea", open),
		"S/closed.md": issue("closed", "done", "## Test Plan\n- After install, run it\n"),
	})
	rep, err := Run(env, "after-install")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range rep.Findings {
		if f.Where != "s/open Test Plan" || !strings.Contains(f.Fix, "issue-cli check-add s/open --section 'Acceptance Criteria'") ||
			!strings.Contains(f.Fix, "(after install: <command>)") || !strings.Contains(f.Message, "nobody runs it") {
			t.Fatalf("finding %+v: want where s/open Test Plan, a check-add fix with (after install: <command>)", f)
		}
		got = append(got, f.Message)
	}
	want := []string{"After install, run the commit-msg hook on a message it re…", "Live: the leak warning shows on a real commit", "Live check: elevenlabs voices list"}
	if len(got) != len(want) {
		t.Fatalf("findings = %q, want %d on %q", got, len(want), want)
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Fatalf("finding %d = %q, want it to quote %q", i, got[i], w)
		}
	}
}
