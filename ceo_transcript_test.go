package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Transcript records as Claude Code writes them (2.1): one JSON object per
// line; a message's blocks come one record each, sharing message.id.

func trRecord(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func trOwner(text string) string {
	return trRecord(map[string]any{"parentUuid": "p", "type": "user", "origin": map[string]any{"kind": "human"},
		"message": map[string]any{"role": "user", "content": text}})
}

func trText(msgID, text string) string {
	return trRecord(map[string]any{"parentUuid": "p", "type": "assistant",
		"message": map[string]any{"id": msgID, "role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}})
}

func trTool(msgID, id, name string, input map[string]any) string {
	return trRecord(map[string]any{"parentUuid": "p", "type": "assistant",
		"message": map[string]any{"id": msgID, "role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}})
}

func trResult(id string, result map[string]any) string {
	return trRecord(map[string]any{"parentUuid": "p", "type": "user", "toolUseResult": result,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"}}}})
}

func trAsk(msgID, id string) string {
	return trTool(msgID, id, "AskUserQuestion", map[string]any{"questions": []any{
		map[string]any{"header": "Color", "question": "Which color do you prefer?", "multiSelect": false,
			"options": []any{map[string]any{"label": "Red (warm)", "description": "A warm color"}, map[string]any{"label": "Blue (cool)", "description": "A cool color"}}},
		map[string]any{"header": "Pets", "question": "Which pets do you like?", "multiSelect": true,
			"options": []any{map[string]any{"label": "Cats"}, map[string]any{"label": "Dogs"}, map[string]any{"label": "Fish"}}},
	}})
}

// leadTranscriptLines is a conversation: the owner asks, the CEO answers in
// two blocks of one message around tool calls, asks two questions that the
// owner answered and asks again; bookkeeping, meta, peer and sidechain
// records sit between.
func leadTranscriptLines() []string {
	return []string{
		`{"type":"agent-name","agentName":"lead-agents","sessionId":"s1"}`,
		`{"type":"api-request-blob","blob":"{\"type\":\"user\",\"message\":{\"content\":\"not a turn\"}}"}`,
		trOwner("What is left?\n\n<system-reminder>\nbe brief\n</system-reminder>"),
		trRecord(map[string]any{"parentUuid": "p", "type": "user", "isMeta": true, "message": map[string]any{"role": "user", "content": "Lead tick: run status"}}),
		trRecord(map[string]any{"parentUuid": "p", "type": "user", "origin": map[string]any{"kind": "peer"}, "message": map[string]any{"role": "user", "content": "a worker's message"}}),
		trText("m1", "Two issues are left:\n\n- **ui/child** in testing\n- `ui/idle` in backlog\n\n<script>alert(1)</script>"),
		trTool("m1", "t1", "Bash", map[string]any{"command": "issue-cli status", "description": "Check the board"}),
		trTool("m1", "t2", "Read", map[string]any{"file_path": "/repo/workflow.yaml"}),
		trResult("t1", nil),
		trRecord(map[string]any{"parentUuid": "p", "type": "assistant", "isSidechain": true, "message": map[string]any{"id": "sub", "content": []any{map[string]any{"type": "text", "text": "a subagent's notes"}}}}),
		trText("m2", "Before I go on:"),
		trAsk("m2", "ask1"),
		trResult("ask1", map[string]any{"answers": map[string]any{"Which color do you prefer?": "Red (warm)", "Which pets do you like?": "Cats, Fish"}}),
		trText("m3", "Noted: red, cats and fish."),
		trAsk("m4", "ask2"),
		`{"parentUuid":"p","type":"assistant","message":{"id":"m5","content":[{"type":"te`,
	}
}

func TestParseTranscript(t *testing.T) {
	tr := parseTranscript(strings.NewReader(strings.Join(leadTranscriptLines(), "\n")), false, 60)
	if len(tr.Turns) != 2 || tr.Older {
		t.Fatalf("turns = %+v older=%v", tr.Turns, tr.Older)
	}
	you, ceo := tr.Turns[0], tr.Turns[1]
	if you.From != "you" || len(you.Text) != 1 || you.Text[0] != "What is left?" {
		t.Errorf("owner turn = %+v (reminders, ticks and peers are not the owner)", you)
	}
	if ceo.From != "ceo" || len(ceo.Bubbles) != 3 {
		t.Fatalf("CEO bubbles = %+v", ceo.Bubbles)
	}
	for _, b := range ceo.Bubbles {
		if strings.Contains(b.Markdown, "subagent") {
			t.Error("a sidechain record reached the chat")
		}
	}
	first := string(ceo.Bubbles[0].HTML)
	for _, want := range []string{"<li><strong>ui/child</strong> in testing</li>", "<code>ui/idle</code>"} {
		if !strings.Contains(first, want) {
			t.Errorf("bubble HTML lacks %q: %s", want, first)
		}
	}
	if strings.Contains(first, "<script>") {
		t.Errorf("raw HTML reached the bubble: %s", first)
	}
	if got := ceo.ToolSummary(); got != "used 2 tools: Bash, Read" {
		t.Errorf("tools = %q", got)
	}
	if ceo.Calls[0].Label != "Check the board" || ceo.Calls[1].Label != "/repo/workflow.yaml" {
		t.Errorf("calls = %+v", ceo.Calls)
	}
	if len(ceo.Asks) != 2 {
		t.Fatalf("asks = %+v", ceo.Asks)
	}
	done, open := ceo.Asks[0], ceo.Asks[1]
	if !done.Answered || done.Questions[0].Answer != "Red (warm)" || done.Questions[1].Answer != "Cats, Fish" || !done.Questions[1].MultiSelect {
		t.Errorf("answered ask = %+v", done)
	}
	if open.Answered || open.ID != "ask2" || open.Questions[0].Options[0].Description != "A warm color" {
		t.Errorf("open ask = %+v", open)
	}
}

func TestParseTranscriptCutAndCapped(t *testing.T) {
	lines := append([]string{`partial line"}`}, trOwner("one"), trText("a", "reply one"), trOwner("two"), trText("b", "reply two"))
	tr := parseTranscript(strings.NewReader(strings.Join(lines, "\n")), true, 3)
	if !tr.Older || len(tr.Turns) != 3 || tr.Turns[0].From != "ceo" || tr.Turns[2].Bubbles[0].Markdown != "reply two" {
		t.Errorf("turns = %+v older=%v", tr.Turns, tr.Older)
	}
}

func TestOpenCEOQuestions(t *testing.T) {
	tr := parseTranscript(strings.NewReader(strings.Join(leadTranscriptLines(), "\n")), false, 60)
	open := openCEOQuestions(tr.Turns)
	if len(open) != 1 || open[0].Kind != "ask" || open[0].ID != "ask2" || open[0].Text != "Which color do you prefer? (+1 more question)" {
		t.Errorf("open = %+v (the answered ask must not be in it)", open)
	}
	// A sentence in the CEO's last message waits until the owner writes.
	turns := append(tr.Turns, CEOTurn{From: "ceo", Bubbles: []CEOBubble{{Markdown: "Done. Should I close the epic?"}}})
	turns[1].Asks[1].Answered = true
	open = openCEOQuestions(turns)
	if len(open) != 1 || open[0].Kind != "sentence" || open[0].Text != "Should I close the epic?" {
		t.Errorf("open = %+v", open)
	}
	turns = append(turns, CEOTurn{From: "you", Text: []string{"yes"}})
	if open = openCEOQuestions(turns); len(open) != 0 {
		t.Errorf("after the owner's reply open = %+v", open)
	}
}

// writeLeadTranscript writes lines as the lead's transcript for a lead
// running in cwd, under a fresh CLAUDE_CONFIG_DIR, and returns its path.
func writeLeadTranscript(t *testing.T, cwd string, lines []string) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	slug := claudeSlugChars.ReplaceAllString(cwd, "-")
	if len(slug) > claudeSlugMax {
		slug = slug[:claudeSlugMax] + "-abc123"
	}
	dir := filepath.Join(claudeProjectsDir(), slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLeadTranscriptFindsTheLead(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo.main")
	path := writeLeadTranscript(t, cwd, leadTranscriptLines())
	// A newer session in the same folder that isn't the lead.
	other := filepath.Join(filepath.Dir(path), "s2.jsonl")
	os.WriteFile(other, []byte(`{"type":"agent-name","agentName":"agent-x"}`+"\n"), 0644)
	later := time.Now().Add(time.Minute)
	os.Chtimes(other, later, later)
	if got, ok := leadTranscript(cwd, "lead-agents"); !ok || got != path {
		t.Errorf("leadTranscript = %q, %v; want %q", got, ok, path)
	}
	if _, ok := leadTranscript(cwd, "lead-other"); ok {
		t.Error("found a transcript for a lead that has none")
	}
	// A long cwd's folder is cut at 200 characters and carries a hash.
	long := filepath.Join(t.TempDir(), strings.Repeat("deep-folder/", 20))
	path = writeLeadTranscript(t, long, leadTranscriptLines())
	if got, ok := leadTranscript(long, "lead-agents"); !ok || got != path {
		t.Errorf("long cwd: leadTranscript = %q, %v; want %q", got, ok, path)
	}
}

func TestReadLeadTranscriptCachesAndCopies(t *testing.T) {
	path := writeLeadTranscript(t, t.TempDir(), leadTranscriptLines())
	a, err := readLeadTranscript(path)
	if err != nil || len(a.Turns) != 2 {
		t.Fatalf("read = %+v, %v", a, err)
	}
	a.Turns[1].Asks[1].Live = true
	b, _ := readLeadTranscript(path)
	if b.Turns[1].Asks[1].Live {
		t.Error("marking a question live changed the cached read")
	}
	// An appended answer is read on the next poll.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(trResult("ask2", map[string]any{"answers": map[string]any{"Which color do you prefer?": "Blue (cool)"}}) + "\n")
	f.Close()
	later := time.Now().Add(time.Second)
	os.Chtimes(path, later, later)
	c, _ := readLeadTranscript(path)
	if !c.Turns[1].Asks[1].Answered || len(openCEOQuestions(c.Turns)) != 0 {
		t.Errorf("after the answer: %+v", c.Turns[1].Asks[1])
	}
}

// askPane is the lead's pane showing ask2's widget (Claude Code 2.1).
const askPane = "⏺ Before I go on:\n" +
	"────────────────────\n" +
	"←  ☐ Color  ☐ Pets  ✔ Submit  →\n\n" +
	"Which color do you prefer?\n\n" +
	"❯ 1. Red (warm)\n     A warm color\n  2. Blue (cool)\n     A cool color\n  3. Type something.\n" +
	"────────────────────\n" +
	"  4. Chat about this\n\n" +
	"Enter to select · Tab/Arrow keys to navigate · Esc to cancel\n"

func TestCEOAnswerSteps(t *testing.T) {
	tr := parseTranscript(strings.NewReader(strings.Join(leadTranscriptLines(), "\n")), false, 60)
	q := tr.Turns[1].Asks[1]
	steps := func(answers []CEOAnswer) string {
		checked, why := checkCEOAnswers(q, answers)
		if why != "" {
			return "refused: " + why
		}
		var out []string
		for _, s := range ceoAnswerSteps(q, checked) {
			if s.Text != "" {
				out = append(out, "text:"+s.Text)
			} else {
				out = append(out, s.Key)
			}
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		answers []CEOAnswer
		want    string
	}{
		// A single choice is its digit; several choices toggle, then Tab.
		{[]CEOAnswer{{Picks: []int{2}}, {Picks: []int{1, 3}}}, "2 1 3 Tab"},
		// Other: the digit after the options, the text, Enter; in several
		// choices, Down to its row and the text.
		{[]CEOAnswer{{Other: " Green\n please "}, {Picks: []int{2}, Other: "Birds"}}, "3 text:Green please Enter 2 Down Down Down text:Birds Tab"},
		{[]CEOAnswer{{Picks: []int{1}}}, "refused: answer each of the 2 questions"},
		{[]CEOAnswer{{Picks: []int{1, 2}}, {Picks: []int{1}}}, "refused: question 1 takes one answer"},
		{[]CEOAnswer{{Picks: []int{3}}, {Picks: []int{1}}}, "refused: question 1 has no option 3"},
		{[]CEOAnswer{{Picks: []int{1}}, {}}, "refused: question 2 has no answer"},
		{[]CEOAnswer{{Other: strings.Repeat("x", 501)}, {Picks: []int{1}}}, "refused: an Other answer is over 500 characters"},
	} {
		if got := steps(c.answers); got != c.want {
			t.Errorf("%+v: steps = %q, want %q", c.answers, got, c.want)
		}
	}
}

func TestLeadAnswerEndpoint(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	writeLeadTranscript(t, projectRoot(&proj), leadTranscriptLines())
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/answer"
	body := `{"id":"ask2","answers":[{"picks":[2]},{"picks":[1,3]}]}`

	// The pane doesn't show the widget: nothing is pressed.
	code, out := leadChatPost(t, url, "application/json", body)
	if code != 409 || out["status"] != "moved-on" || len(st.keys) != 0 {
		t.Errorf("without the widget: %d %v keys=%v", code, out, st.keys)
	}
	st.pane = askPane
	if code, out = leadChatPost(t, url, "application/json", `{"id":"ask1","answers":[{"picks":[1]},{"picks":[1]}]}`); code != 409 || out["status"] != "answered" {
		t.Errorf("answered question: %d %v", code, out)
	}
	if code, out = leadChatPost(t, url, "application/json", `{"id":"nope","answers":[]}`); code != 404 {
		t.Errorf("unknown question: %d %v", code, out)
	}
	if code, out = leadChatPost(t, url, "application/json", `{"id":"ask2","answers":[{"picks":[9]},{"picks":[1]}]}`); code != 400 {
		t.Errorf("bad pick: %d %v", code, out)
	}
	if len(st.keys) != 0 {
		t.Fatalf("refused answers pressed keys: %v", st.keys)
	}
	if code, out = leadChatPost(t, url, "application/json", body); code != 200 || out["status"] != "sent" {
		t.Fatalf("answer: %d %v", code, out)
	}
	target := st.asked[len(st.asked)-1]
	want := []string{target + " 2", target + " 1", target + " 3", target + " Tab"}
	if strings.Join(st.keys, "|") != strings.Join(want, "|") {
		t.Errorf("keys = %q, want %q", st.keys, want)
	}
	// On the review tab, the answers are submitted with 1.
	st.keys = nil
	st.pane = askPane + "Review your answers\nReady to submit your answers?\n❯ 1. Submit answers\n  2. Cancel\n"
	leadChatPost(t, url, "application/json", body)
	if n := len(st.keys); n == 0 || st.keys[n-1] != target+" 1" || n != 5 {
		t.Errorf("review tab keys = %q", st.keys)
	}
}

func TestCEOChatFromTranscript(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	st.pane = askPane
	writeLeadTranscript(t, projectRoot(&proj), leadTranscriptLines())
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/lead")
	for _, want := range []string{
		`data-testid="lead_chat.bubble"`,
		"<strong>ui/child</strong>",
		`<details class="wb-turn-tools" data-testid="lead_chat.tools"><summary>used 2 tools: Bash, Read</summary>`,
		"<b>Bash</b> · Check the board",
		`data-testid="lead_chat.ask.answered"`,
		"→ Cats, Fish",
		`data-testid="lead_chat.ask.open"`,
		`data-ask-id="ask2" data-ask-url="/p/agents/lead/answer"`,
		`data-testid="lead_chat.ask_card.1.opt.2"`,
		`data-testid="lead_chat.question.ask"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The pane's turns and its reading of the widget as a menu are gone.
	for _, bad := range []string{"Merged ui/child.", "<script>alert", `data-testid="lead_chat.source"`, `data-testid="lead_chat.prompt"`} {
		if strings.Contains(html, bad) {
			t.Errorf("page has %q", bad)
		}
	}

	_, body := getBody(t, ts.URL+"/p/agents/lead.json")
	var m LeadChatModel
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if m.Source != "transcript" || len(m.OpenQuestions) != 1 || m.OpenQuestions[0].ID != "ask2" || len(m.Questions) != 1 {
		t.Errorf("lead.json source=%q open=%+v questions=%q", m.Source, m.OpenQuestions, m.Questions)
	}

	// Off the pane, the card can't answer and says where to.
	st.pane = leadChatPaneText
	_, html = getBody(t, ts.URL+"/p/agents/lead")
	if !strings.Contains(html, "answer in the Terminal view") || !strings.Contains(html, `data-testid="lead_chat.ask_card.submit"`) || !strings.Contains(html, `disabled data-testid="lead_chat.ask_card.submit"`) {
		t.Error("an off-screen question should be a read-only card")
	}
}

func TestCEOChatWithoutTranscript(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/lead")
	if !strings.Contains(html, `data-testid="lead_chat.source"`) || !strings.Contains(html, "Merged ui/child.") {
		t.Error("with no transcript the chat should read the pane and say so")
	}
}
