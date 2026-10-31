package tracker

import (
	"reflect"
	"strings"
	"testing"
)

func TestSiblingAskFormats(t *testing.T) {
	text := AskComment("cli/consumer", " base_branch: or base:? ")
	if text != "ask: from cli/consumer: base_branch: or base:?" {
		t.Fatalf("ask = %q", text)
	}
	from, words, ok := ParseAsk(text)
	if !ok || from != "cli/consumer" || words != "base_branch: or base:?" {
		t.Errorf("ParseAsk = %q %q %v", from, words, ok)
	}
	ans := AnswerComment("cli/producer", []int{2, 5}, "base_branch:")
	if ans != "answer: from cli/producer (ask #2, #5): base_branch:" {
		t.Fatalf("answer = %q", ans)
	}
	from, ids, words, ok := ParseAnswer(ans)
	if !ok || from != "cli/producer" || !reflect.DeepEqual(ids, []int{2, 5}) || words != "base_branch:" {
		t.Errorf("ParseAnswer = %q %v %q %v", from, ids, words, ok)
	}
	// Plain comments that merely start with the word are not the channel,
	// and neither form is ever an approval.
	for _, s := range []string{"ask: the lead about it", "answer: yes"} {
		if _, _, ok := ParseAsk(s); ok {
			t.Errorf("ParseAsk(%q) ok", s)
		}
		if _, _, _, ok := ParseAnswer(s); ok {
			t.Errorf("ParseAnswer(%q) ok", s)
		}
	}
	for _, s := range []string{text, ans, AnswerComment("x", []int{1}, `approval: "go" (viewer, 2026-10-31) → in progress`)} {
		if _, ok := ParseApprovalComment(s); ok {
			t.Errorf("%q parses as an approval", s)
		}
	}
}

func TestOpenAsksAndAnswers(t *testing.T) {
	comments := []Comment{
		{ID: 1, Text: AskComment("a", "one"), Status: "done"},
		{ID: 2, Text: AskComment("b", "two"), Status: "open"},
		{ID: 3, Text: "progress: unrelated", Status: "open"},
		{ID: 4, Text: AnswerComment("c", []int{9}, "yes"), Status: "open"},
	}
	if got := OpenAsksOn("me", comments); len(got) != 1 || got[0].From != "b" || got[0].ID != 2 || got[0].On != "me" {
		t.Errorf("open asks = %+v", got)
	}
	if got := AnswersOn(comments); len(got) != 1 || got[0].From != "c" || got[0].Delivered {
		t.Errorf("answers = %+v", got)
	}
}

// The worker briefing names the channel and says it is no approval.
func TestLeadPromptSectionNamesSiblingChannel(t *testing.T) {
	got := LeadPromptSection(&Issue{Slug: "cli/consumer"}, LeadRoute{Lead: "lead-p", CEO: "lead-p"})
	for _, want := range []string{"`issue-cli ask <sibling> \"...\"`", "`issue-cli answer` shows on yours", NotAnApproval} {
		if !strings.Contains(got, want) {
			t.Errorf("briefing misses %q:\n%s", want, got)
		}
	}
}
