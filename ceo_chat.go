package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The CEO chat (L1 · HQ, /p/<project>/lead) is the owner talking to the
// project's lead: the conversation read out of the lead's pane (ceo_pane.go),
// what it is doing, a menu it waits on, the message box and the session's
// keys, and beside it what waits on the owner, the CEO's questions and the
// workers. Design: issues/Factory/designs/workshop-board/L1-ceo-chat. It
// replaced the classic lead chat; lead.json and the send/key endpoints are
// handlers_lead_chat.go's.

// CEOChatPage is the whole page; the live part (?part=live) re-renders
// Live alone.
type CEOChatPage struct {
	// Who is who the page talks to: "CEO" or "<Dept> lead".
	Who string
	// LiveURL is the polled part's URL (?part=live, plus dept=).
	LiveURL string
	Header  WbHeader
	Title   CEOTitle
	Live    CEOLive
	// Composer and the controls are drawn once: polling never touches what
	// the owner is typing.
	Composer CEOComposer
	Controls []CEOControl
	Keys     []CEOControl
}

// CEOTitle is the band above the conversation: the CEO, live or not, the
// session mode, the view toggle and Open in tmux.
type CEOTitle struct {
	Who              string
	Session, Display string
	Color            string
	Live             bool
	Mode             string
	OpenURL          string
	SpawnURL         string
}

// CEOLive is what the page polls: the conversation, the raw pane, the menu,
// and the side panel.
type CEOLive struct {
	Who      string
	Live     bool
	SpawnURL string
	// Pane is the conversation (Turns from the transcript, or the pane),
	// and the pane's working line, menu and mode.
	Pane CEOPane
	// Source says where the turns came from: "transcript" or "pane".
	Source string
	// AnswerURL answers Pane.Ask, the question card above the message box.
	AnswerURL string
	// Raw is the cleaned pane for the Terminal view.
	Raw  string
	Side CEOSide
}

// CEOSide is the right panel.
type CEOSide struct {
	Waiting   []LeadWaiting
	Questions []CEOOpenQuestion
	Workers   []CEOWorker
	AttachCmd string
}

// CEOWorker is one of the project's live workers.
type CEOWorker struct {
	Robot WbRobot
	Name  string
	// State is the worker's state in words; Issue its issue, when it has one.
	State string
	Issue *AgentIssue
}

// CEOComposer is the message box.
type CEOComposer struct {
	Who             string
	SendURL, KeyURL string
	// ImageURL takes an image for the next message (POST …/lead/image).
	ImageURL string
	Session  string
	Live     bool
	MaxBytes int
}

// CEOControl is one key button. Key is the name /lead/key takes
// (leadChatKeys), Hint the key as the TUI knows it.
type CEOControl struct {
	Label, Key, Hint, Title, TestID string
	// Danger draws it red and asks before pressing (Ctrl+C).
	Danger bool
}

// ceoControls are the session controls, in the design's order.
var ceoControls = []CEOControl{
	{Label: "Send now · interrupt", Key: "SendNow", Hint: "ctrl+x ctrl+s", Title: "interrupts the CEO and sends what is queued at once", TestID: "send_now"},
	{Label: "Interrupt", Key: "Escape", Hint: "Esc", Title: "stops the current step", TestID: "escape"},
	{Label: "Show details", Key: "Expand", Hint: "ctrl+o", Title: "expands the latest output", TestID: "expand"},
	{Label: "To background", Key: "Background", Hint: "ctrl+b", Title: "moves the running command to the background", TestID: "background"},
	{Label: "Stop session", Key: "CtrlC", Hint: "Ctrl+C", Title: "hard stop: asks first, and a second press within 5 s is refused", TestID: "ctrl_c", Danger: true},
}

// ceoKeys are the plain keys: moving in a menu and answering it.
var ceoKeys = []CEOControl{
	{Label: "↑", Key: "Up", Title: "up", TestID: "up"},
	{Label: "↓", Key: "Down", Title: "down", TestID: "down"},
	{Label: "↵", Key: "Enter", Title: "Enter", TestID: "enter"},
	{Label: "1", Key: "1", Title: "choice 1", TestID: "1"},
	{Label: "2", Key: "2", Title: "choice 2", TestID: "2"},
	{Label: "3", Key: "3", Title: "choice 3", TestID: "3"},
}

// ceoModeLabel is the mode chip's text.
func ceoModeLabel(mode string) string {
	if mode == "" {
		return "default"
	}
	return mode
}

func (s *Server) handleCEOChat(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	name, dept, ok := chatLead(proj, r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("part") == "live" {
		w.Header().Set("Cache-Control", "no-store")
		live := s.buildCEOLive(proj, prefix, dept, s.buildLeadChat(proj, prefix, name, true))
		if err := s.tmpl.ExecuteTemplate(w, "wb/ceo-live", live); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, "ceo_chat.html", s.buildCEOChat(proj, prefix, dept, time.Now())); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildCEOChat is the chat page with the CEO, or with dept's lead when dept
// is set.
func (s *Server) buildCEOChat(p *tracker.Project, prefix string, dept *tracker.Department, now time.Time) CEOChatPage {
	color := s.projectColor(p)
	lot, queue := s.buildLot(p, color, now)
	name, q, who := tracker.LeadSessionName(p), chatQuery(dept), chatWho(dept)
	if dept != nil {
		name = tracker.DepartmentLeadName(p, dept.Name)
	}
	m := s.buildLeadChat(p, prefix, name, true)
	liveURL := "?part=live"
	if q != "" {
		liveURL += "&" + strings.TrimPrefix(q, "?")
	}
	d := CEOChatPage{
		Who:     who,
		LiveURL: liveURL,
		Header:  WbHeader{WaitingURL: prefix + "/needs-you", Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, Waiting: len(queue), HideDone: true},
		Live:    s.buildCEOLive(p, prefix, dept, m),
		Composer: CEOComposer{
			Who:     who,
			SendURL: prefix + "/lead/send" + q, KeyURL: prefix + "/lead/key" + q, ImageURL: prefix + "/lead/image",
			Session: m.Display, Live: m.Live, MaxBytes: m.MaxBytes,
		},
		Controls: ceoControls,
		Keys:     ceoKeys,
	}
	d.Header.Crumbs = []WbCrumb{
		{Label: "Map", URL: "/"},
		{Label: p.Name, URL: prefix + "/company", Color: color},
		{Label: "HQ · CEO", Color: companyHQColor, Current: true},
	}
	if dept != nil {
		d.Header.Crumbs[2] = WbCrumb{Label: dept.Name + " · lead", Color: deptColor(*dept, deptIndex(p, dept.Name)), Current: true}
	}
	d.Title = CEOTitle{
		Who: who, Session: m.Session, Display: m.Display, Color: color, Live: m.Live,
		Mode: ceoModeLabel(d.Live.Pane.Mode), OpenURL: prefix + "/agents/open", SpawnURL: prefix + "/lead/spawn" + q,
	}
	return d
}

// buildCEOLive is the polled part, from the lead chat model. A department
// lead's side panel lists only its department's workers.
func (s *Server) buildCEOLive(p *tracker.Project, prefix string, dept *tracker.Department, m LeadChatModel) CEOLive {
	q := chatQuery(dept)
	live := CEOLive{
		Who: chatWho(dept), Live: m.Live, SpawnURL: prefix + "/lead/spawn" + q, Raw: m.Pane, Source: m.Source, AnswerURL: prefix + "/lead/answer" + q,
		Pane: CEOPane{Turns: m.Turns, Older: m.Older, Working: m.Working, Prompt: m.Prompt, Ask: m.Ask, Mode: m.Mode},
		Side: CEOSide{Waiting: m.Waiting, Questions: m.OpenQuestions, AttachCmd: m.AttachCmd},
	}
	if agents, _ := s.buildAgentsModel(p, prefix); agents != nil {
		for _, w := range agents.Workers {
			if !w.Live || (dept != nil && w.Department != dept.Name) {
				continue
			}
			live.Side.Workers = append(live.Side.Workers, CEOWorker{
				Robot: WbRobot{State: ceoWorkerRobot(w.State), Label: w.Name},
				Name:  w.Name, State: ceoWorkerState(w), Issue: w.Issue,
			})
		}
	}
	return live
}

// ceoWorkerRobot is the robot's state for a worker's (AgentNode.State).
func ceoWorkerRobot(state string) string {
	switch state {
	case "working":
		return "working"
	case "at-gate", "question":
		return "you"
	}
	return "idle"
}

// ceoWorkerState is a worker's state in words.
func ceoWorkerState(w AgentNode) string {
	switch w.State {
	case "at-gate":
		if w.Gate != "" {
			return "waits on you: " + w.Gate
		}
		return "waits on your approval"
	case "question":
		return "asked you a question"
	case "working", "idle":
		return w.State
	}
	return "stopped"
}
