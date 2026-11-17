package viewer

import (
	"os"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
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

// permission.txt is a real Claude Code 2.1 capture of a permission prompt
// for a Bash call (internal/agentrun/testdata/panes/claude-approval.txt).
func TestParseCEOPanePermissionPrompt(t *testing.T) {
	p := readPaneFixture(t, "permission.txt")
	if p.Prompt == nil || !p.Prompt.Permission {
		t.Fatalf("prompt = %+v, want a permission prompt", p.Prompt)
	}
	if p.Prompt.Tool != "Bash" || strings.Join(p.Prompt.Command, "\n") != "touch made-by-agent" || strings.Join(p.Prompt.Note, "\n") != "Make a file" {
		t.Errorf("tool %q, command %q, note %q", p.Prompt.Tool, p.Prompt.Command, p.Prompt.Note)
	}
	for _, l := range p.Prompt.Detail {
		if strings.Contains(l, "╌") || strings.HasPrefix(l, "Tip:") {
			t.Errorf("box drawing in the detail: %q", l)
		}
	}
	if len(p.Prompt.Choices) != 4 || p.Prompt.Choices[3].Text != "No" {
		t.Errorf("choices = %+v", p.Prompt.Choices)
	}
	if p.Working == nil || !p.Working.Permission || p.Working.Text != "Waiting on a permission prompt: Bash · touch made-by-agent" {
		t.Errorf("working = %+v", p.Working)
	}
	if paneLeadState(cleanPane(string(mustRead(t, "testdata/ceo-pane/permission.txt")))) != leadYou {
		t.Error("an open permission prompt is a lead waiting on you")
	}
	// The older box shape (menu.txt): the command first, no dashed rules.
	m := readPaneFixture(t, "menu.txt")
	if !m.Prompt.Permission || m.Prompt.Tool != "Bash" || !strings.HasPrefix(strings.Join(m.Prompt.Command, ""), "git fetch origin") || strings.Join(m.Prompt.Note, "") != "Fetch and list the branch's commits" {
		t.Errorf("menu.txt prompt = %+v", m.Prompt)
	}
	// A file's box names the file; its content stays in the terminal.
	c := parseCEOPane(cleanPane(string(mustRead(t, "../agentrun/testdata/panes/claude-create.txt"))))
	if c.Prompt == nil || !c.Prompt.Permission || c.Prompt.Tool != "Create file" || strings.Join(c.Prompt.Command, "") != "new.txt" || len(c.Prompt.Note) != 0 {
		t.Errorf("create prompt = %+v", c.Prompt)
	}
	// A menu that isn't a permission prompt keeps its working line and place.
	q := parseCEOPane("⏺ Which model?\n\n Pick a model?\n ❯ 1. Opus\n   2. Sonnet")
	if q.Prompt == nil || q.Prompt.Permission || q.Working != nil {
		t.Errorf("a plain menu = %+v working %+v", q.Prompt, q.Working)
	}
}

// blocked-safety.txt is a real capture (a lead's pane, 2026-11-15, paths and
// site renamed) of a call a built-in safety check refused; blocked-classifier
// is the same frame with the auto mode classifier's refusal text as the
// transcripts on this machine hold it.
func TestParseCEOPaneBlocked(t *testing.T) {
	p := readPaneFixture(t, "blocked-safety.txt")
	var blocked []CEOBlocked
	for _, turn := range p.Turns {
		blocked = append(blocked, turn.Blocked...)
	}
	if len(blocked) != 1 {
		t.Fatalf("blocked = %+v", blocked)
	}
	b := blocked[0]
	if b.Tool != "Bash" || b.By != "safety check" || !strings.HasPrefix(b.Command, "for u in / /poradnik") ||
		b.Reason != "This command could not be checked for dangerous rm operations: the shell may read it differently than it was parsed. This requires explicit approval." {
		t.Errorf("blocked = %+v", b)
	}
	if p.Turns[0].ToolSummary() != "used 3 tools: Bash ×3" {
		t.Errorf("tools = %q", p.Turns[0].ToolSummary())
	}

	c := readPaneFixture(t, "blocked-classifier.txt")
	if len(c.Turns) != 1 || len(c.Turns[0].Blocked) != 1 {
		t.Fatalf("turns = %+v", c.Turns)
	}
	if b := c.Turns[0].Blocked[0]; b.By != "auto mode classifier" || b.Reason != "Production Deploy" || b.Command != "cd /Users/me/Repo/x && npm run deploy -w @x/landing 2>&1 | tail -25" {
		t.Errorf("blocked = %+v", b)
	}
	if got := strings.Join(c.Turns[0].Text, " / "); got != "Deploying the landing page now. / The deploy was blocked by the auto mode classifier; it needs your go." {
		t.Errorf("text = %q", got)
	}
	// A failed command or the owner's own No is not a block.
	for _, s := range []string{"Bash(false) ⎿ Error: Exit code 1", "Bash(rm x) ⎿ The user doesn't want to proceed with this tool use."} {
		if b := paneBlocked(s); b != nil {
			t.Errorf("%q became %+v", s, b)
		}
	}
}

// partOf is one <template data-part> of the live part's HTML.
func partOf(t *testing.T, html, name string) string {
	t.Helper()
	open := `<template data-part="` + name + `">`
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatalf("no %s part in %s", name, html)
	}
	rest := html[i+len(open):]
	return rest[:strings.Index(rest, "</template>")]
}

func TestCEOChatPermissionAndBlockedInConversation(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	st.pane = string(mustRead(t, "testdata/ceo-pane/permission.txt"))
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/lead?part=live")
	conv, prompt := partOf(t, html, "conv"), partOf(t, html, "prompt")
	for _, want := range []string{
		`data-testid="lead_chat.prompt"`,
		`data-testid="lead_chat.prompt.tool">permission · Bash</span>`,
		`data-testid="lead_chat.prompt.command">touch made-by-agent</code>`,
		`<div class="wb-choice-note">Make a file</div>`,
		`data-key="4" data-testid="lead_chat.prompt.choice.3"`,
		`wb-turn--working wb-turn--perm`,
		`Waiting on a permission prompt: Bash · touch made-by-agent`,
	} {
		if !strings.Contains(conv, want) {
			t.Errorf("conversation lacks %q", want)
		}
	}
	if strings.Contains(prompt, "lead_chat.prompt") || strings.Contains(conv, "╌") || strings.Contains(conv, "Tip:") {
		t.Errorf("the permission card drawn twice or with box drawing:\nprompt: %s", prompt)
	}

	// A menu that isn't a permission prompt stays above the message box.
	st.pane = "⏺ Which model?\n\n Pick a model?\n ❯ 1. Opus\n   2. Sonnet"
	_, html = getBody(t, ts.URL+"/p/agents/lead?part=live")
	if conv, prompt := partOf(t, html, "conv"), partOf(t, html, "prompt"); strings.Contains(conv, "lead_chat.prompt") || !strings.Contains(prompt, `data-testid="lead_chat.prompt"`) {
		t.Errorf("plain menu moved:\nconv: %s\nprompt: %s", conv, prompt)
	}

	st.pane = string(mustRead(t, "testdata/ceo-pane/blocked-classifier.txt"))
	_, html = getBody(t, ts.URL+"/p/agents/lead?part=live")
	conv = partOf(t, html, "conv")
	for _, want := range []string{
		`data-testid="lead_chat.blocked"`,
		`<b>Bash</b> <span class="wb-muted">refused by the auto mode classifier</span>`,
		`cd /Users/me/Repo/x &amp;&amp; npm run deploy -w @x/landing 2&gt;&amp;1 | tail -25</code>`,
		`data-testid="lead_chat.blocked.reason">Why: Production Deploy</div>`,
	} {
		if !strings.Contains(conv, want) {
			t.Errorf("conversation lacks %q", want)
		}
	}
}
