package tracker

import (
	"strings"
	"testing"
)

func TestYOLOGuidanceDropsAskAndStopSentences(t *testing.T) {
	cases := []struct{ name, prompt, want string }{
		{"idea", "Clarify the issue with the human before moving into structured design.\nAsk focused questions to remove ambiguity around goals, scope, and success criteria.", ""},
		{"backlog", "This is a handoff state.\nTell the human in chat that this needs `in progress` approval in the issue viewer, then stop and end your turn; continue when the human messages you that it is approved.", "This is a handoff state."},
		{"in design entry", "Write the design into the issue body. When the design is complete, tell the human in chat that it needs approval in the issue viewer: `backlog` to queue it. Then stop and end your turn. When the human says it is approved, run `issue-cli context <slug>`.", "Write the design into the issue body."},
		{"human-testing is no human", "Run the tests, e.g. the suite, and state whether manual checks are needed before human-testing.", "Run the tests, e.g. the suite, and state whether manual checks are needed before human-testing."},
	}
	for _, c := range cases {
		got, dropped := YOLOGuidance(c.prompt)
		if got != c.want || dropped != (got != c.prompt) {
			t.Errorf("%s: got %q (dropped %v), want %q", c.name, got, dropped, c.want)
		}
	}
}

func TestYOLORuleNamesWhatWaitsForTheOwner(t *testing.T) {
	rule := YOLORule(nil, "api/fix")
	for _, want := range []string{"issue-cli decide api/fix --provisional", "question: comment", "experiments", "product-policy", "spends money or publishes outside the machine"} {
		if !strings.Contains(rule, want) {
			t.Errorf("rule lacks %q: %s", want, rule)
		}
	}
}
