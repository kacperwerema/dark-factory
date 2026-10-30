package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	mu      sync.Mutex // handlers may call the stubs from several requests at once
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
	origHas, origCap, origKey, origPaste, origConfirm := agentrun.HasSession, agentrun.CaptureHistory, agentrun.SendKey, leadChatPaste, leadChatConfirm
	agentrun.HasSession = func(target string) bool {
		st.mu.Lock()
		defer st.mu.Unlock()
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
		st.mu.Lock()
		defer st.mu.Unlock()
		st.keys = append(st.keys, target+" "+key)
		return nil
	}
	leadChatPaste = func(proj *tracker.Project, session, text, textFile string) agentrun.Response {
		if !st.live {
			return agentrun.PasteSubmit(proj, session, text, textFile)
		}
		st.pastes = append(st.pastes, text)
		st.session, st.file = session, textFile
		return agentrun.Response{Status: "sent", Session: session, Prompt: text}
	}
	leadChatConfirm = func(steps *[]agentrun.Step, target, text, hint string) {
		*steps = append(*steps, agentrun.Step{Name: "Prompt delivered", Status: "ok"})
	}
	withMockTmuxSessions(t, nil)
	leadChatCtrlCMu.Lock()
	leadChatLastCtrlC = map[string]time.Time{}
	leadChatCtrlCMu.Unlock()
	t.Cleanup(func() {
		agentrun.HasSession, agentrun.CaptureHistory, agentrun.SendKey, leadChatPaste, leadChatConfirm = origHas, origCap, origKey, origPaste, origConfirm
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
		// The Workshop Board page: Map › project › HQ · CEO.
		`data-testid="lead_chat.page"`,
		`aria-current="page"><i class="wb-crumb-sq" style="background:#F2B233"></i>HQ · CEO</span>`,
		`<a class="wb-crumb wb-crumb--link" href="/p/agents/company">`,
		`data-testid="lead_chat.pane"`,
		`data-testid="lead_chat.turn.you"`,
		`data-testid="lead_chat.turn.ceo"`,
		`data-testid="lead_chat.key.ctrl_c"`,
		`data-confirm=`,
		`data-testid="lead_chat.open_tmux"`,
		`data-answer="Do you want the epic closed?"`,
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
	if !strings.Contains(html, `placeholder="Start the CEO first" disabled`) {
		t.Error("the composer should be disabled while the CEO isn't running")
	}
	// The old ?classic=1 has no classic page left: it is the same page.
	_, classic := getBody(t, ts.URL+"/p/agents/lead?classic=1")
	if !strings.Contains(classic, `data-testid="lead_chat.page"`) || !strings.Contains(classic, "wb-app") {
		t.Error("?classic=1 should serve the CEO chat")
	}
}

func TestCEOChatLivePart(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/lead?part=live")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	for _, want := range []string{
		`data-live="1"`,
		`<template data-part="conv">`,
		`<template data-part="term">`,
		`<template data-part="prompt">`,
		`<template data-part="side">`,
		"Merged ui/child.",
		`data-testid="lead_chat.waiting.count">Waiting on you: `,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("live part lacks %q", want)
		}
	}
	if strings.Contains(html, "<html") || strings.Contains(html, "lead_chat.text") {
		t.Error("the live part must not carry the page or the composer")
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
	if len(m.Turns) == 0 || m.Turns[len(m.Turns)-1].From != "ceo" {
		t.Errorf("turns = %+v", m.Turns)
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

func TestLeadChatKeyChord(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	orig := leadChatChordGap
	leadChatChordGap = 0
	t.Cleanup(func() { leadChatChordGap = orig })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/key"

	for _, key := range []string{"SendNow", "Expand", "Background", "ShiftTab"} {
		b, _ := json.Marshal(map[string]string{"key": key})
		if code, out := leadChatPost(t, url, "application/json", string(b)); code != 200 || out["key"] != key {
			t.Fatalf("%s: %d %v", key, code, out)
		}
	}
	want := "lead-agents C-x,lead-agents C-s,lead-agents C-o,lead-agents C-b,lead-agents C-b,lead-agents BTab"
	if got := strings.Join(st.keys, ","); got != want {
		t.Errorf("pressed %s, want %s", got, want)
	}
}

func TestLeadChatCtrlCGuard(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	leadChatCtrlCMu.Lock()
	leadChatLastCtrlC = map[string]time.Time{}
	leadChatCtrlCMu.Unlock()
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/key"

	if code, out := leadChatPost(t, url, "application/json", `{"key":"CtrlC"}`); code != 200 || out["status"] != "sent" {
		t.Fatalf("first Ctrl+C: %d %v", code, out)
	}
	// A second one right after would close Claude Code: refused, nothing pressed.
	if code, out := leadChatPost(t, url, "application/json", `{"key":"CtrlC"}`); code != http.StatusTooManyRequests || out["status"] != "too-soon" {
		t.Errorf("second Ctrl+C: %d %v, want 429 too-soon", code, out)
	}
	if got := strings.Join(st.keys, ","); got != "lead-agents C-c" {
		t.Errorf("pressed %s, want one C-c", got)
	}
	if !leadChatCtrlCAllowed("lead-other", time.Now()) {
		t.Error("the guard is per lead: another lead's Ctrl+C must pass")
	}
	if !leadChatCtrlCAllowed("lead-agents", time.Now().Add(leadChatCtrlCGap+time.Second)) {
		t.Error("Ctrl+C after the gap must pass")
	}
}

// TestLeadChatCtrlCDuringSend: a send waiting on its delivery check doesn't
// hold the lead's lock, so a Ctrl+C goes through at once and a second one
// inside the window is refused (the bypass was: both queued behind the
// send, then both fired).
func TestLeadChatCtrlCDuringSend(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	confirming, release := make(chan struct{}), make(chan struct{})
	leadChatConfirm = func(steps *[]agentrun.Step, target, text, hint string) {
		close(confirming)
		<-release
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	sent := make(chan int)
	go func() {
		code, _ := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", `{"text":"hello"}`)
		sent <- code
	}()
	<-confirming
	url := ts.URL + "/p/agents/lead/key"
	done := make(chan [2]int)
	go func() {
		a, _ := leadChatPost(t, url, "application/json", `{"key":"CtrlC"}`)
		b, _ := leadChatPost(t, url, "application/json", `{"key":"CtrlC"}`)
		done <- [2]int{a, b}
	}()
	select {
	case got := <-done:
		if got != [2]int{200, http.StatusTooManyRequests} {
			t.Errorf("Ctrl+C twice during a send: %v, want 200 then 429", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+C waited behind the send's delivery check")
	}
	close(release)
	if code := <-sent; code != 200 {
		t.Errorf("send: %d", code)
	}
	if got := strings.Join(st.keys, ","); got != "lead-agents C-c" {
		t.Errorf("pressed %s, want one C-c", got)
	}
}

// TestLeadChatCtrlCRace: two presses at once, only one reaches the pane.
func TestLeadChatCtrlCRace(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	agentrun.SendKey = func(target, key string) error {
		time.Sleep(20 * time.Millisecond)
		st.mu.Lock()
		defer st.mu.Unlock()
		st.keys = append(st.keys, target+" "+key)
		return nil
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/key"

	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			code, _ := leadChatPost(t, url, "application/json", `{"key":"CtrlC"}`)
			codes <- code
		}()
	}
	got := []int{<-codes, <-codes}
	if got[0]+got[1] != 200+http.StatusTooManyRequests {
		t.Errorf("two presses at once: %v, want one 200 and one 429", got)
	}
	if n := len(st.keys); n != 1 {
		t.Errorf("pressed %v, want one C-c", st.keys)
	}
}

// TestLeadChatCtrlCFailedPress: a Ctrl+C that didn't reach the pane doesn't
// start the window, so the owner can press again.
func TestLeadChatCtrlCFailedPress(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	fail := true
	agentrun.SendKey = func(target, key string) error {
		if fail {
			return errors.New("no pane")
		}
		st.keys = append(st.keys, target+" "+key)
		return nil
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/key"

	if code, _ := leadChatPost(t, url, "application/json", `{"key":"CtrlC"}`); code != http.StatusBadGateway {
		t.Fatalf("failed Ctrl+C: %d, want 502", code)
	}
	fail = false
	if code, out := leadChatPost(t, url, "application/json", `{"key":"CtrlC"}`); code != 200 {
		t.Errorf("Ctrl+C after a failed one: %d %v, want 200", code, out)
	}
}

func TestLeadsOverview(t *testing.T) {
	a := setupAgentsProject(t)
	b, _ := setupTestProject(t)
	st := stubLeadChat(t, false)
	agentrun.HasSession = func(target string) bool {
		st.mu.Lock()
		defer st.mu.Unlock()
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
		// The Workshop Board look: Map › Every CEO, one card per project.
		`aria-current="page">Every CEO</span>`,
		`class="wb-ceo-card"`,
		"Talk to the CEO",
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
