package outputs

import (
	"strings"
	"testing"
)

func TestFollowUps(t *testing.T) {
	for name, c := range map[string]struct {
		md   string
		want string
	}{
		"plain":        {"## Follow-ups\n\n- one\n- two\n", "one|two"},
		"numbered":     {"## 9. Follow-ups (for the lead to file)\n\n1. one\n2) two\n", "one|two"},
		"spelling":     {"### Follow ups\n\n* one\n\n# Followup\n\n+ two\n", "one|two"},
		"ends":         {"## Follow-ups\n\n- one\n\n## Sources\n\n- link\n", "one"},
		"subsection":   {"## Follow-ups\n\n### Code\n\n- one\n", "one"},
		"nested":       {"## Follow-ups\n\n- one\n    - detail\n", "one"},
		"fence":        {"## Follow-ups\n\n```\n- code\n## Not a heading\n```\n- one\n", "one"},
		"fenced head":  {"```\n## Follow-ups\n- code\n```\n", ""},
		"prose":        {"## Follow-ups\n\nCode fixes: none.\n", ""},
		"none":         {"## Follow-ups\n\n- None.\n- *n/a*\n", ""},
		"no heading":   {"# Report\n\n- one\n", ""},
		"other word":   {"## Follow-up work done\n\n- one\n", "one"},
		"not a prefix": {"## Followers\n\n- one\n", ""},
	} {
		if got := strings.Join(FollowUps(c.md), "|"); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

// An item is the owner's when it ends "(owner)", or when the sub-heading or
// lead-in above it names the owner; the next sub-heading or lead-in that
// doesn't hands the items back to the engine.
func TestFollowUpListMarksOwnerItems(t *testing.T) {
	for name, c := range map[string]struct {
		md   string
		want string // each item's text, "*" in front when the owner's
	}{
		"mark":          {"## Follow-ups\n\n- one (owner)\n- two\n- three (Owner).\n", "*one (owner)|two|*three (Owner)."},
		"mark mid-text": {"## Follow-ups\n\n- ask the (owner) later\n", "ask the (owner) later"},
		"sub-heading":   {"## Follow-ups\n\n### Owner actions\n\n- one\n\n### Code\n\n- two\n", "*one|two"},
		"lead-in": {"## Follow-ups\n\nCode fixes: **none**. Nothing to build.\n\nMarketing actions, each the owner's call because each publishes:\n1. one\n2. two\n",
			"*one|*two"},
		"lead-in ends": {"## Follow-ups\n\nFor the owner:\n- one\n\nFor the lead:\n- two\n", "*one|two"},
		"long lead-in": {"## Follow-ups\n\nThese are the owner's\ncalls, every one:\n- one\n", "*one"},
		"continuation": {"## Follow-ups\n\nThe owner's calls:\n- one\nwrapped on\n- two\n", "*one|*two"},
		"indented":     {"## Follow-ups\n\nThe owner's calls:\n- one\n\n  more on one\n- two\n", "*one|*two"},
		"not owner":    {"## Follow-ups\n\nOwnership of the code:\n- one\n", "one"},
		"new heading":  {"## Follow-ups\n\nThe owner's calls:\n- one\n\n## Follow-ups again\n\n- two\n", "*one|two"},
	} {
		var got []string
		for _, it := range FollowUpList(c.md) {
			if it.Owner {
				got = append(got, "*"+it.Text)
			} else {
				got = append(got, it.Text)
			}
		}
		if strings.Join(got, "|") != c.want {
			t.Errorf("%s: got %q, want %q", name, strings.Join(got, "|"), c.want)
		}
	}
	if got := strings.Join(AgentFollowUps("## Follow-ups\n\n- a (owner)\n- b\n"), "|"); got != "b" {
		t.Errorf("AgentFollowUps = %q", got)
	}
}
