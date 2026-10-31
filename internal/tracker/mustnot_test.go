package tracker

import (
	"strings"
	"testing"
)

func TestMustNotReadsLinesWithoutThePlaceholder(t *testing.T) {
	body := "## Acceptance Criteria\n\n- [ ] x\n\n## Must not\n\n- " + MustNotPlaceholder +
		"\n- Change the issue file format (`TestParseIssue` stays green)\n- [ ] Touch projects.yaml\n\nNot a bullet.\n\n## Notes\n- other\n"
	got := MustNot(body)
	want := []string{"Change the issue file format (`TestParseIssue` stays green)", "Touch projects.yaml"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("MustNot = %q, want %q", got, want)
	}
	if got := MustNot("## Must not\n\n- " + MustNotPlaceholder + "\n"); len(got) != 0 {
		t.Errorf("placeholder only: MustNot = %q, want none", got)
	}
	if got := MustNot("## Acceptance Criteria\n- [ ] x\n"); got != nil {
		t.Errorf("no section: MustNot = %q, want nil", got)
	}
}
