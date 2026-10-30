package main

import (
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The lead chat (/p/<project>/lead) is the project lead's tmux pane in the
// board: a live capture of the pane, a message box that types into the lead
// the way Re-send prompt does (agentrun.Paste), a few keys (Esc to interrupt,
// menu keys for its questions) and what waits on the owner. What is typed
// there is the owner typing in the lead's chat: nothing is ever sent without
// a click or Enter in the page. The tmux target always comes from the
// project (tracker.LeadSessionName, agentrun.Target), never from the request.

const (
	// leadChatScrollback is how many lines above the visible screen the pane
	// capture includes.
	leadChatScrollback = 2000
	// leadChatMaxMessage caps one message, in bytes.
	leadChatMaxMessage = 20 * 1024
	// leadChatQuestions is how many of the lead's latest questions the side
	// panel lists.
	leadChatQuestions = 5
)

// leadChatKeys are the keys the page may press in the lead's pane, by the
// name the page sends, mapped to the tmux keys pressed in order (a chord such
// as Claude Code's ctrl+x ctrl+s is two). Only these: the request never
// carries text that reaches send-keys.
var leadChatKeys = map[string][]string{
	"Escape":     {"Escape"},
	"Enter":      {"Enter"},
	"Up":         {"Up"},
	"Down":       {"Down"},
	"1":          {"1"},
	"2":          {"2"},
	"3":          {"3"},
	"SendNow":    {"C-x", "C-s"},
	"Expand":     {"C-o"},
	"Background": {"C-b", "C-b"},
	"CtrlC":      {"C-c"},
	// ShiftTab cycles the session's permission mode.
	"ShiftTab": {"BTab"},
}

// leadChatKeyNames lists leadChatKeys for error messages, in a fixed order.
const leadChatKeyNames = "Escape, Enter, Up, Down, 1, 2, 3, SendNow, Expand, Background, CtrlC, ShiftTab"

// leadChatCtrlCGap is how long after one Ctrl+C a second is refused: Claude
// Code exits on Ctrl+C pressed twice, which would end the lead.
var leadChatCtrlCGap = 5 * time.Second

var (
	leadChatCtrlCMu   sync.Mutex
	leadChatLastCtrlC = map[string]time.Time{}
)

// leadChatCtrlCAllowed reports whether a Ctrl+C may go to target now. The
// caller holds leadChatLock(target), and records the press with
// leadChatRecordCtrlC once it was sent.
func leadChatCtrlCAllowed(target string, now time.Time) bool {
	leadChatCtrlCMu.Lock()
	defer leadChatCtrlCMu.Unlock()
	last, ok := leadChatLastCtrlC[target]
	return !ok || now.Sub(last) >= leadChatCtrlCGap
}

// leadChatRecordCtrlC starts the guard window for target.
func leadChatRecordCtrlC(target string, now time.Time) {
	leadChatCtrlCMu.Lock()
	defer leadChatCtrlCMu.Unlock()
	leadChatLastCtrlC[target] = now
}

// leadChatChordGap separates the keys of a chord so the TUI reads them as one.
var leadChatChordGap = 60 * time.Millisecond

// leadChatPaste pastes a message into the live lead and presses Enter, and
// leadChatConfirm then checks it arrived; seams for tests.
var (
	leadChatPaste   = agentrun.PasteSubmit
	leadChatConfirm = agentrun.PromptDeliveryStep
)

// LeadWaiting is one "Waiting on you" row in the chat's side panel.
type LeadWaiting struct {
	// Kind is gate, playtest, checks or question (WaitingItem.Kind).
	Kind   string `json:"kind"`
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Detail string `json:"detail"`
	// Action is the button's label and ActionURL where it goes: the issue's
	// approve button for a gate (#approve-<status>), else the issue.
	Action    string `json:"action"`
	ActionURL string `json:"action_url"`
	Age       string `json:"age"`
}

// LeadChatModel is the chat page, also served as /p/<project>/lead.json.
type LeadChatModel struct {
	Project string `json:"project"`
	Prefix  string `json:"prefix"`
	// Session is the lead's bare name (lead-<project>), Display where it
	// lives in tmux.
	Session string `json:"session"`
	Display string `json:"display"`
	Live    bool   `json:"live"`
	// Pane is the captured pane text, escape sequences stripped.
	Pane string `json:"pane"`
	// Questions are the texts of OpenQuestions: what the CEO still waits
	// on the owner for. Answered questions are never in it.
	Questions []string `json:"questions"`
	// OpenQuestions are the CEO's open questions (openCEOQuestions):
	// unanswered AskUserQuestion calls, then the "?" sentences of its last
	// message when the owner hasn't written since.
	OpenQuestions []CEOOpenQuestion `json:"open_questions"`
	// Waiting is filled on the page and in lead.json?waiting=1; WaitingIn
	// says it was computed (so an empty list means nothing waits).
	Waiting   []LeadWaiting `json:"waiting"`
	WaitingIn bool          `json:"waiting_included"`
	AttachCmd string        `json:"attach_cmd"`
	MaxBytes  int           `json:"max_bytes"`
	At        string        `json:"at"`
	// Turns and Older are the conversation: from the lead's transcript
	// when one is found (Source "transcript", ceo_transcript.go), else the
	// pane read as a conversation (Source "pane", parseCEOPane). Working,
	// Prompt, Ask (an open AskUserQuestion) and Mode always come from the
	// pane.
	Source  string      `json:"source"`
	Turns   []CEOTurn   `json:"turns"`
	Older   bool        `json:"older"`
	Working *CEOWorking `json:"working,omitempty"`
	Prompt  *CEOPrompt  `json:"prompt,omitempty"`
	Ask     *CEOPaneAsk `json:"ask,omitempty"`
	Mode    string      `json:"mode"`
}

// chatLead is the lead a chat request talks to: the CEO, or with
// ?dept=<name> that department's lead. The session name is built from
// projects.yaml, never taken from the request: an unknown department is
// ok=false, and the handler answers 404.
func chatLead(proj *tracker.Project, r *http.Request) (name string, dept *tracker.Department, ok bool) {
	key := r.URL.Query().Get("dept")
	if key == "" {
		return tracker.LeadSessionName(proj), nil, true
	}
	if dept = proj.DepartmentByKey(key); dept == nil {
		return "", nil, false
	}
	return tracker.DepartmentLeadName(proj, dept.Name), dept, true
}

// chatQuery is the query a department lead's chat URLs carry
// (?dept=<key>), "" for the CEO's.
func chatQuery(dept *tracker.Department) string {
	if dept == nil {
		return ""
	}
	return "?dept=" + url.QueryEscape(tracker.DepartmentKey(dept.Name))
}

// chatWho is who the chat talks to, in words: "CEO" or "<Dept> lead".
func chatWho(dept *tracker.Department) string {
	if dept == nil {
		return "CEO"
	}
	return dept.Name + " lead"
}

// buildLeadChat reads lead session name's chat (the CEO's or a department
// lead's, see chatLead).
func (s *Server) buildLeadChat(proj *tracker.Project, prefix, name string, withWaiting bool) LeadChatModel {
	m := LeadChatModel{
		Project:   proj.Name,
		Prefix:    prefix,
		Session:   name,
		Display:   agentDisplayName(proj, name),
		AttachCmd: agentrun.AttachCmd(proj, name),
		MaxBytes:  leadChatMaxMessage,
		At:        time.Now().Format(time.RFC3339),
		Questions: []string{},
		Waiting:   []LeadWaiting{},
		Source:    "pane",
	}
	target := agentrun.Target(proj, name)
	m.Live = agentrun.HasSession(target)
	if m.Live {
		if pane, err := agentrun.CaptureHistory(target, leadChatScrollback); err == nil {
			m.Pane = cleanPane(pane)
		}
	}
	cp := parseCEOPane(m.Pane)
	m.Turns, m.Older, m.Working, m.Prompt, m.Ask, m.Mode = cp.Turns, cp.Older, cp.Working, cp.Prompt, cp.Ask, cp.Mode
	if m.Live {
		if path, ok := leadTranscript(projectRoot(proj), name); ok {
			if t, err := readLeadTranscript(path); err == nil && len(t.Turns) > 0 {
				m.Turns, m.Older, m.Source = t.Turns, t.Older, "transcript"
			}
		}
	}
	withTurnImages(proj, prefix, m.Turns)
	m.OpenQuestions = openCEOQuestions(m.Turns, m.Ask)
	for _, q := range m.OpenQuestions {
		m.Questions = append(m.Questions, q.Text)
	}
	if withWaiting {
		m.Waiting, m.WaitingIn = leadWaiting(proj, prefix, time.Now()), true
	}
	return m
}

// leadWaiting is the Status page's "Waiting on you" for the project, as rows
// that link to where the owner acts.
func leadWaiting(proj *tracker.Project, prefix string, now time.Time) []LeadWaiting {
	rows := []LeadWaiting{}
	for _, it := range waitingOnOwner(proj, openIssuesOf(proj)) {
		url := prefix + "/issue/" + it.Issue.Slug
		row := LeadWaiting{Kind: it.Kind, Slug: it.Issue.Slug, Title: it.Issue.Title, URL: url, ActionURL: url, Age: shortDuration(now.Sub(it.Since))}
		if it.Approx {
			row.Age = "≈ " + row.Age
		}
		switch it.Kind {
		case "gate":
			row.Detail = it.Issue.Status + " → " + it.Target
			row.Action = "Approve"
			row.ActionURL = url + "#approve-" + strings.ReplaceAll(strings.TrimSpace(it.Target), " ", "-")
		case "playtest":
			row.Detail = "playtest " + strings.Join(it.Sessions, ", ")
			row.Action = "Playtest"
		case "checks":
			row.Detail = "check script " + strings.Join(it.Sessions, ", ")
			row.Action = "Run checks"
		default:
			row.Detail = "open question"
			row.Action = "Answer"
		}
		rows = append(rows, row)
	}
	return rows
}

// openIssuesOf loads the project's issues that are not done.
func openIssuesOf(proj *tracker.Project) []*tracker.Issue {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		log.Printf("lead chat: %s: load issues: %v", proj.Slug, err)
	}
	var open []*tracker.Issue
	for _, issue := range issues {
		if issue.Status != "done" {
			open = append(open, issue)
		}
	}
	return open
}

// ansiSeq matches terminal escape sequences: CSI, OSC and two-byte escapes.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;?<=>]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

// cleanPane strips escape sequences and trailing spaces (capture-pane -J
// keeps them) and drops trailing blank lines.
func cleanPane(pane string) string {
	pane = ansiSeq.ReplaceAllString(pane, "")
	lines := strings.Split(strings.ReplaceAll(pane, "\r", ""), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// questionSentence is one sentence that ends in "?": a "." or "!" ends a
// sentence only before a space, so file names (workflow.yaml) stay whole.
var questionSentence = regexp.MustCompile(`(?:[^.!?]|[.!][^\s.!?])+\?`)

// paneParagraphs joins the pane's lines into paragraphs the way the TUI
// wrapped them: an indented line continues the one above unless it starts a
// new bullet. Box borders are trimmed off.
func paneParagraphs(pane string) []string {
	var paras []string
	for _, raw := range strings.Split(pane, "\n") {
		line := strings.TrimRight(strings.Trim(strings.TrimSpace(raw), "│┃|"), " ")
		text := strings.TrimSpace(line)
		cont := strings.HasPrefix(raw, " ") && text != "" && len(paras) > 0 && paras[len(paras)-1] != "" &&
			!strings.HasPrefix(text, "- ") && !strings.HasPrefix(text, "⏺") && !strings.HasPrefix(text, "•")
		if cont {
			paras[len(paras)-1] += " " + text
			continue
		}
		paras = append(paras, text)
	}
	return paras
}

// leadQuestions picks the lead's latest questions out of its pane: the
// sentences ending in "?", newest first, without bullets. What the owner
// typed (the "> " / "❯" prompt echo) is skipped, and so are repeats,
// fragments under 8 characters and tool output.
func leadQuestions(pane string, n int) []string {
	paras := paneParagraphs(pane)
	out := []string{}
	seen := map[string]bool{}
	for i := len(paras) - 1; i >= 0 && len(out) < n; i-- {
		para := paras[i]
		if strings.HasPrefix(para, ">") || strings.HasPrefix(para, "❯") {
			continue
		}
		matches := questionSentence.FindAllString(para, -1)
		for j := len(matches) - 1; j >= 0 && len(out) < n; j-- {
			q := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(matches[j]), "⏺●•*-– "))
			// Over 240 characters is tool output run together, not a question.
			if l := len([]rune(q)); l < 8 || l > 240 || strings.Contains(q, "⎿") || seen[q] {
				continue
			}
			seen[q] = true
			out = append(out, q)
		}
	}
	return out
}

// cleanMessage keeps what a person can type: printable text, newlines and
// tabs. Other control characters go, ESC included, so a message can't end
// the bracketed paste early and press keys in the lead's TUI. Trailing
// whitespace is dropped (the send presses Enter itself).
func cleanMessage(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, text)
	return strings.TrimRight(text, " \t\n")
}

// handleLeadChatJSON serves GET /p/<project>/lead.json: the pane and, with
// ?waiting=1, the side panel's waiting list.
func (s *Server) handleLeadChatJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	name, _, ok := chatLead(proj, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	m := s.buildLeadChat(proj, prefix, name, r.URL.Query().Get("waiting") == "1")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(m)
}

// leadChatError answers a refused chat request as {status, error}.
func leadChatError(w http.ResponseWriter, code int, status, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"status": status, "error": msg})
}

// decodeLeadChatBody reads a JSON body of at most limit bytes into v; it
// answers the request itself and returns false when it can't.
func decodeLeadChatBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		leadChatError(w, http.StatusUnsupportedMediaType, "error", "Content-Type must be application/json")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			leadChatError(w, http.StatusRequestEntityTooLarge, "too-large", "message is over 20 KB")
		} else {
			leadChatError(w, http.StatusBadRequest, "error", "invalid JSON body")
		}
		return false
	}
	return true
}

// leadChatSendMu keeps two sends to the same lead from interleaving their
// paste and Enter.
var leadChatSendMu sync.Map // lead target -> *sync.Mutex

func leadChatLock(target string) func() {
	mu, _ := leadChatSendMu.LoadOrStore(target, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}

// handleLeadChatSend serves POST /p/<project>/lead/send, body {text,
// images}: the owner's message, pasted into the live lead as one bracketed
// paste plus Enter (agentrun.Paste, the way Re-send prompt delivers a
// briefing). Images are paths POST …/lead/image answered; each becomes an
// "[Image: <path>]" line at the end (ceoImageLines). 409 with status
// no-session when the lead isn't running, 413 over 20 KB, 400 for an empty
// message or a path that isn't a chat image.
func (s *Server) handleLeadChatSend(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var body struct {
		Text   string   `json:"text"`
		Images []string `json:"images"`
	}
	// JSON escaping can grow a message up to 6x; the text itself is
	// checked below.
	name, _, ok := chatLead(proj, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !decodeLeadChatBody(w, r, 6*leadChatMaxMessage+1024, &body) {
		return
	}
	text := cleanMessage(body.Text)
	if len(body.Images) > 0 {
		lines, err := ceoImageLines(proj, body.Images)
		if err != nil {
			leadChatError(w, http.StatusBadRequest, "bad-image", err.Error())
			return
		}
		if strings.TrimSpace(text) != "" {
			lines = text + "\n\n" + lines
		}
		text = lines
	}
	if len(text) > leadChatMaxMessage {
		leadChatError(w, http.StatusRequestEntityTooLarge, "too-large", "message is over 20 KB")
		return
	}
	if strings.TrimSpace(text) == "" {
		leadChatError(w, http.StatusBadRequest, "empty", "message is empty")
		return
	}
	target := agentrun.Target(proj, name)
	textFile := filepath.Join(projectRoot(proj), ".agent-logs", name, "chat", time.Now().Format("20060102-150405.000")+".txt")
	// The lock covers the paste and its Enter, not the delivery poll after
	// it: keys (Esc, Ctrl+C) must not queue behind a 20 s wait.
	unlock := leadChatLock(target)
	resp := leadChatPaste(proj, name, text, textFile)
	unlock()
	if resp.Status == "sent" {
		leadChatConfirm(&resp.Steps, target, text, "")
	}
	w.Header().Set("Content-Type", "application/json")
	switch resp.Status {
	case "no-session":
		resp.Steps = []DispatchStep{{Name: resp.Session + " is not running", Status: "error", Detail: "start the CEO first; nothing was sent"}}
		w.WriteHeader(http.StatusConflict)
	case "error":
		w.WriteHeader(http.StatusBadGateway)
	}
	json.NewEncoder(w).Encode(resp)
}

// handleLeadChatKey serves POST /p/<project>/lead/key, body {key}: presses
// one allowed key (leadChatKeys) in the live lead. Esc interrupts it.
func (s *Server) handleLeadChatKey(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var body struct {
		Key string `json:"key"`
	}
	name, _, ok := chatLead(proj, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !decodeLeadChatBody(w, r, 1024, &body) {
		return
	}
	keys, ok := leadChatKeys[body.Key]
	if !ok {
		leadChatError(w, http.StatusBadRequest, "error", "key must be one of "+leadChatKeyNames)
		return
	}
	target := agentrun.Target(proj, name)
	if !agentrun.HasSession(target) {
		leadChatError(w, http.StatusConflict, "no-session", agentDisplayName(proj, name)+" is not running")
		return
	}
	// The guard is checked and recorded under the lead's lock, so two
	// presses can't both pass, and only a press that reached the pane
	// starts the window.
	defer leadChatLock(target)()
	if body.Key == "CtrlC" && !leadChatCtrlCAllowed(target, time.Now()) {
		leadChatError(w, http.StatusTooManyRequests, "too-soon", "a second Ctrl+C within a few seconds would close the CEO; wait and try again")
		return
	}
	for i, key := range keys {
		if i > 0 {
			time.Sleep(leadChatChordGap)
		}
		if err := agentrun.SendKey(target, key); err != nil {
			leadChatError(w, http.StatusBadGateway, "error", err.Error())
			return
		}
	}
	if body.Key == "CtrlC" {
		leadChatRecordCtrlC(target, time.Now())
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent", "key": body.Key, "session": agentDisplayName(proj, name)})
}

// LeadRow is one project on /leads.
type LeadRow struct {
	Name    string
	Slug    string
	Session string
	Live    bool
	Waiting int
	ChatURL string
	// Color is the project's colour, the CEO robot's body.
	Color string
	// Departments are the project's department leads under its CEO.
	Departments []LeadDeptRow
}

// LeadDeptRow is one department lead on /leads.
type LeadDeptRow struct {
	Department, Session, ChatURL string
	Live                         bool
}

// handleLeads serves GET /leads: every configured project's lead, live or
// not, with what waits on the owner there and a link to its chat, as
// Workshop Board cards (wb/ceo-card).
func (s *Server) handleLeads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rows := make([]LeadRow, len(s.projects))
	var wg sync.WaitGroup
	for i := range s.projects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := &s.projects[i]
			name := tracker.LeadSessionName(p)
			rows[i] = LeadRow{
				Name:    p.Name,
				Slug:    p.Slug,
				Session: agentDisplayName(p, name),
				Live:    agentrun.IsLive(p, name),
				Waiting: len(waitingOnOwner(p, openIssuesOf(p))),
				ChatURL: "/p/" + p.Slug + "/lead",
				Color:   s.projectColor(p),
			}
			for _, d := range p.Departments {
				lead := tracker.DepartmentLeadName(p, d.Name)
				rows[i].Departments = append(rows[i].Departments, LeadDeptRow{
					Department: d.Name, Session: agentDisplayName(p, lead), Live: agentrun.IsLive(p, lead),
					ChatURL: "/p/" + p.Slug + "/lead" + chatQuery(&d),
				})
			}
		}(i)
	}
	wg.Wait()
	header := WbHeader{Crumbs: []WbCrumb{{Label: "Map", URL: "/"}, {Label: "All CEOs", Current: true}}, Quiet: true, WaitingURL: "/needs-you"}
	for _, row := range rows {
		header.Waiting += row.Waiting
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Header WbHeader
		Rows   []LeadRow
	}{header, rows}
	if err := s.tmpl.ExecuteTemplate(w, "leads.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
