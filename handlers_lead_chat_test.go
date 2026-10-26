package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// leadChatPaneText is a Claude Code pane as capture-pane prints it: the
// lead's answer, a question, the owner's echo and the prompt box.
const leadChatPaneText = "\x1b[1m⏺ Merged ui/child.\x1b[0m   \n" +
	"⏺ Should I dispatch ui/idle next?\n" +
	"> what is left?\n" +
	"│ > Is this mine?                │\n" +
	"⏺ Do you want the epic closed?\n\n\n"

// stubLeadChat makes the lead live (or not), with leadChatPaneText as its
// pane, and records the targets asked about, the keys pressed and the
// pastes made. No real tmux is touched.
type leadChatStub struct {
	live    bool
	asked   []string
	keys    []string
	pastes  []string
	session string
	file    string
}

func stubLeadChat(t *testing.T, live bool) *leadChatStub {
	t.Helper()
	st := &leadChatStub{live: live}
	origHas, origCap, origKey, origPaste := agentrun.HasSession, agentrun.CaptureHistory, agentrun.SendKey, leadChatPaste
	agentrun.HasSession = func(target string) bool {
		st.asked = append(st.asked, target)
		return st.live
	}
	agentrun.CaptureHistory = func(target string, lines int) (string, error) {
		if lines != leadChatScrollback {
			t.Errorf("capture asked for %d lines", lines)
		}
		return leadChatPaneText, nil
	}
	agentrun.SendKey = func(target, key string) error {
		st.keys = append(st.keys, target+" "+key)
		return nil
	}
	leadChatPaste = func(proj *tracker.Project, session, text, textFile, hint string) agentrun.Response {
		if !st.live {
			return agentrun.Paste(proj, session, text, textFile, hint)
		}
		st.pastes = append(st.pastes, text)
		st.session, st.file = session, textFile
		return agentrun.Response{Status: "sent", Session: session, Prompt: text}
	}
	withMockTmuxSessions(t, nil)
	t.Cleanup(func() {
		agentrun.HasSession, agentrun.CaptureHistory, agentrun.SendKey, leadChatPaste = origHas, origCap, origKey, origPaste
	})
	return st
}

func leadChatPost(t *testing.T, url, contentType, body string, header ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestLeadChatPage(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/lead")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	for _, want := range []string{
		`data-testid="nav.tab.lead" aria-current="page"`,
		`data-testid="lead_chat.pane"`,
		"Merged ui/child.",
		"Do you want the epic closed?",
		// The gate links to the issue's approve button.
		`href="/p/agents/issue/ui/child#approve-testing"`,
		`data-testid="lead_chat.waiting.question.`,
		`data-testid="lead_chat.key.escape"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(html, "\x1b") {
		t.Error("escape sequences reached the page")
	}
}

func TestLeadChatPageNotLive(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, false)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/lead")
	if !strings.Contains(html, `data-testid="lead_chat.spawn"`) || !strings.Contains(html, "not running") {
		t.Error("a lead that isn't running should offer Spawn lead")
	}
	if strings.Contains(html, "Merged ui/child.") {
		t.Error("a lead that isn't running has no pane")
	}
}

func TestLeadChatJSON(t *testing.T) {
	proj := setupAgentsProject(t)
	proj.TmuxSession = "work"
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getBody(t, ts.URL+"/p/agents/lead.json")
	var m LeadChatModel
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if !m.Live || m.Session != "lead-agents" || m.Display != "work:lead-agents" {
		t.Errorf("model = %+v", m)
	}
	// The target comes from the project: the lead's window in the shared session.
	if len(st.asked) == 0 || st.asked[len(st.asked)-1] != "=work:=lead-agents" {
		t.Errorf("asked tmux about %v", st.asked)
	}
	if m.WaitingIn || len(m.Waiting) != 0 {
		t.Errorf("waiting without ?waiting=1: %+v", m.Waiting)
	}
	want := []string{"Do you want the epic closed?", "Should I dispatch ui/idle next?"}
	if strings.Join(m.Questions, "|") != strings.Join(want, "|") {
		t.Errorf("questions = %q", m.Questions)
	}

	_, body = getBody(t, ts.URL+"/p/agents/lead.json?waiting=1")
	m = LeadChatModel{}
	json.Unmarshal([]byte(body), &m)
	kinds := map[string]bool{}
	for _, w := range m.Waiting {
		kinds[w.Kind] = true
	}
	if !m.WaitingIn || !kinds["gate"] || !kinds["question"] {
		t.Errorf("waiting = %+v", m.Waiting)
	}
}

func TestLeadChatSend(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	msg := "yes, dispatch ui/idle\n\x1b[201~\x03second line  \n"
	b, _ := json.Marshal(map[string]string{"text": msg})
	code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(b))
	if code != 200 || out["status"] != "sent" {
		t.Fatalf("send: %d %v", code, out)
	}
	if len(st.pastes) != 1 || st.pastes[0] != "yes, dispatch ui/idle\n[201~second line" {
		t.Errorf("pasted %q (control characters must go, the newline stays)", st.pastes)
	}
	if st.session != "lead-agents" {
		t.Errorf("pasted into %q", st.session)
	}
	if dir := filepath.Join(proj.WorkDir, ".agent-logs", "lead-agents", "chat"); filepath.Dir(st.file) != dir {
		t.Errorf("message kept at %q, want under %s", st.file, dir)
	}
}

func TestLeadChatSendRefusals(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/send"

	big, _ := json.Marshal(map[string]string{"text": strings.Repeat("x", leadChatMaxMessage+1)})
	cases := []struct {
		name, contentType, body string
		header                  []string
		code                    int
	}{
		{"empty", "application/json", `{"text":"  \n "}`, nil, http.StatusBadRequest},
		{"too large", "application/json", string(big), nil, http.StatusRequestEntityTooLarge},
		{"form post", "application/x-www-form-urlencoded", "text=hi", nil, http.StatusUnsupportedMediaType},
		{"bad json", "application/json", `{"text":`, nil, http.StatusBadRequest},
		{"cross-origin", "application/json", `{"text":"hi"}`, []string{"Origin", "http://evil.example"}, http.StatusForbidden},
		{"cross-site", "application/json", `{"text":"hi"}`, []string{"Sec-Fetch-Site", "cross-site"}, http.StatusForbidden},
	}
	for _, c := range cases {
		if code, out := leadChatPost(t, url, c.contentType, c.body, c.header...); code != c.code {
			t.Errorf("%s: %d %v, want %d", c.name, code, out, c.code)
		}
	}
	if code, _ := getBody(t, url); code != http.StatusNotFound {
		t.Errorf("GET lead/send: %d, want 404", code)
	}
	if code, _ := leadChatPost(t, ts.URL+"/p/nope/lead/send", "application/json", `{"text":"hi"}`); code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", code)
	}
	if len(st.pastes) != 0 {
		t.Errorf("a refused request pasted %q", st.pastes)
	}
}

func TestLeadChatSendNotLive(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, false)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", `{"text":"hello"}`)
	if code != http.StatusConflict || out["status"] != "no-session" {
		t.Errorf("send to a lead that isn't running: %d %v", code, out)
	}
}

func TestLeadChatKey(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/key"

	if code, out := leadChatPost(t, url, "application/json", `{"key":"Escape"}`); code != 200 || out["status"] != "sent" {
		t.Fatalf("Escape: %d %v", code, out)
	}
	for _, key := range []string{"C-c", "q; tmux kill-server", "", "Escape Enter"} {
		b, _ := json.Marshal(map[string]string{"key": key})
		if code, _ := leadChatPost(t, url, "application/json", string(b)); code != http.StatusBadRequest {
			t.Errorf("key %q: %d, want 400", key, code)
		}
	}
	if strings.Join(st.keys, ",") != "lead-agents Escape" {
		t.Errorf("pressed %v", st.keys)
	}
	st.live = false
	if code, out := leadChatPost(t, url, "application/json", `{"key":"Escape"}`); code != http.StatusConflict || out["status"] != "no-session" {
		t.Errorf("key to a lead that isn't running: %d %v", code, out)
	}
}

func TestLeadChatKeyError(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	agentrun.SendKey = func(string, string) error { return errors.New("no pane") }
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	if code, _ := leadChatPost(t, ts.URL+"/p/agents/lead/key", "application/json", `{"key":"Escape"}`); code != http.StatusBadGateway {
		t.Errorf("failed send-keys: %d, want 502", code)
	}
}

func TestLeadsOverview(t *testing.T) {
	a := setupAgentsProject(t)
	b, _ := setupTestProject(t)
	st := stubLeadChat(t, false)
	agentrun.HasSession = func(target string) bool {
		st.asked = append(st.asked, target)
		return target == "lead-agents"
	}
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/leads")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	for _, want := range []string{
		`data-testid="leads.row.agents.live"`,
		`href="/p/agents/lead" data-testid="leads.row.agents.chat"`,
		`data-testid="leads.row.test-project"`,
		`href="/p/test-project/lead"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("leads page lacks %q", want)
		}
	}
	if strings.Contains(html, `leads.row.test-project.live`) {
		t.Error("test-project's lead is not running")
	}
}

func TestLeadQuestionsAndCleanPane(t *testing.T) {
	pane := cleanPane(leadChatPaneText)
	if strings.Contains(pane, "\x1b") || strings.HasSuffix(pane, "\n") || strings.Contains(pane, "child.   ") {
		t.Errorf("cleanPane left %q", pane)
	}
	got := leadQuestions(pane, 1)
	if len(got) != 1 || got[0] != "Do you want the epic closed?" {
		t.Errorf("leadQuestions = %q", got)
	}
	if got := leadQuestions("ok?\n⏺ Short?", 5); len(got) != 0 {
		t.Errorf("too-short lines counted: %q", got)
	}
	// Claude Code wraps its own lines: a question split over two lines, one
	// followed by more text, and one inside a bullet.
	wrapped := "⏺ Emma: the change is one file. Should I limit it like that and start\n" +
		"  the worker? It would be the second of two.\n" +
		"  - Branch: align main with chat-agents? That decides the base.\n" +
		"  - Commit: commit workflow.yaml?\n" +
		"❯ can you check?\n"
	got = leadQuestions(wrapped, 5)
	want := "Commit: commit workflow.yaml?|Branch: align main with chat-agents?|Should I limit it like that and start the worker?"
	if strings.Join(got, "|") != want {
		t.Errorf("wrapped questions = %q", got)
	}
}
