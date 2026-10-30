package main

import (
	"os"
	"strings"
	"testing"
)

func readPaneFixture(t *testing.T, name string) CEOPane {
	t.Helper()
	b, err := os.ReadFile("testdata/ceo-pane/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return parseCEOPane(cleanPane(string(b)))
}

func TestParseCEOPaneWorking(t *testing.T) {
	p := readPaneFixture(t, "working.txt")
	if p.Mode != "auto" {
		t.Errorf("mode = %q", p.Mode)
	}
	if p.Working == nil || !strings.HasPrefix(p.Working.Text, "Pondering… (32s") {
		t.Errorf("working = %+v", p.Working)
	}
	if p.Prompt != nil {
		t.Errorf("no menu on the pane, got %+v", p.Prompt)
	}
	if p.Older {
		t.Error("the pane starts with the owner's message: nothing older")
	}
	var got []string
	for _, turn := range p.Turns {
		got = append(got, turn.From+": "+strings.Join(turn.Text, " / ")+" ["+turn.ToolSummary()+"]")
	}
	want := []string{
		"you: You are the project lead. Read the handoff and resume. []",
		"ceo: Read the handoff. Two workers were running. / Both workers are back. Open: / - merge waits on CI / - rename waits on merge / Should I start the rename now? [used 3 tools: Bash ×2, Read]",
		"you: Review those and take over. Of course please offload any work to workers. []",
		"ceo: On it. []",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("turns:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, turn := range p.Turns {
		for _, text := range turn.Text {
			if strings.Contains(text, "Welcome") || strings.Contains(text, "shift+tab") || strings.Contains(text, "esc to interrupt") {
				t.Errorf("chrome reached a turn: %q", text)
			}
		}
	}
}

func TestParseCEOPaneMenu(t *testing.T) {
	p := readPaneFixture(t, "menu.txt")
	if p.Prompt == nil {
		t.Fatal("no menu found")
	}
	if p.Prompt.Title != "Bash command" || p.Prompt.Question != "Do you want to proceed?" {
		t.Errorf("prompt = %+v", p.Prompt)
	}
	if len(p.Prompt.Detail) == 0 || !strings.HasPrefix(p.Prompt.Detail[0], "git fetch origin") {
		t.Errorf("detail = %q", p.Prompt.Detail)
	}
	if len(p.Prompt.Choices) != 3 || p.Prompt.Choices[0].Text != "Yes" || !p.Prompt.Choices[0].Selected || p.Prompt.Choices[2].Key != "3" {
		t.Errorf("choices = %+v", p.Prompt.Choices)
	}
	// The pane starts with the CEO: the owner's message scrolled off.
	if !p.Older || len(p.Turns) != 1 || p.Turns[0].ToolSummary() != "used 1 tool: Bash" {
		t.Errorf("turns = %+v older=%v", p.Turns, p.Older)
	}
	for _, text := range p.Turns[0].Text {
		if strings.Contains(text, "proceed") {
			t.Errorf("the menu reached the turn: %q", text)
		}
	}
}

func TestParseCEOPaneEdges(t *testing.T) {
	if p := parseCEOPane(""); len(p.Turns) != 0 || p.Working != nil || p.Prompt != nil || p.Mode != "" {
		t.Errorf("empty pane = %+v", p)
	}
	// A numbered list in the CEO's answer is not a menu: no question above.
	p := parseCEOPane("⏺ Plan:\n1. merge\n2. rename")
	if p.Prompt != nil {
		t.Errorf("a list became a menu: %+v", p.Prompt)
	}
	// A menu whose numbers skip is not one either.
	if p := parseCEOPane("Pick one?\n1. a\n3. b"); p.Prompt != nil {
		t.Errorf("skipped numbers became a menu: %+v", p.Prompt)
	}
	if p := parseCEOPane("⏺ hi\n  ⏸ plan mode on (shift+tab to cycle)"); p.Mode != "plan" {
		t.Errorf("mode = %q", p.Mode)
	}
	if p := parseCEOPane("⏺ hi\n  ⏵⏵ accept edits on (shift+tab to cycle)"); p.Mode != "accept edits" {
		t.Errorf("mode = %q", p.Mode)
	}
	// Text typed but not sent stays out of the conversation.
	p = parseCEOPane("⏺ Done.\n\n────────\n> half a thought\n────────")
	if len(p.Turns) != 1 || p.Turns[0].From != "ceo" {
		t.Errorf("the input box became a turn: %+v", p.Turns)
	}
}
