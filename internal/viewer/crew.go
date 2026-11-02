package viewer

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Crew page (L1, /p/<project>/agents): who works on the project as an
// org chart — the CEO, a column per department (Product first) with its lead
// and its workers — then the stopped jobs and the sessions not bound to an
// issue. ?agent=<session> opens the side panel: how to reach the agent, its
// issue and gate, its branch. The classic canvas stays at ?classic=1. No
// owner mockup: drafted from the design system (factory/agents-screen D-1).

// crewAsked is a worker's state words while its question waits.
const crewAsked = "asked a question"

// CrewData is the whole Crew page.
type CrewData struct {
	Header     WbHeader
	Project    string
	Count      WbStat
	Summary    WbSummaryBox
	ClassicURL string
	CEO        WbCrewCard
	Columns    []WbCrewColumn
	// NoWorkers: no live worker anywhere; the page says how to dispatch one.
	NoWorkers bool
	Stopped   WbCrewRow
	Others    WbCrewRow
	Panel     *WbCrewPanel
	// Checked is when tmux was read for this page (unix ms): the page
	// says "live · tmux checked Ns ago" from it.
	Checked int64
	// Subs are the agent CLIs' subscriptions: working, resting until
	// HH:MM, or unknown (routing).
	Subs []WbCrewSub
}

// WbCrewSub is one agent CLI's subscription on the Crew page.
type WbCrewSub struct {
	Agent, State, Tone string
	Installed          bool
}

// WbCrewRow is a titled row of cards; hidden when it has none.
type WbCrewRow struct {
	Key, Title, Note string
	Cards            []WbCrewCard
}

// WbCrewColumn is one department under the CEO: its lead (none for Product,
// which the CEO runs) and its live workers.
type WbCrewColumn struct {
	Name, Color, Key string
	Lead             *WbCrewCard
	Workers          []WbCrewCard
	// Children are a group's departments, as columns inside its own.
	Children []WbCrewColumn
}

// WbCrewCard is one agent: the CEO, a lead, a worker, a stopped job or a
// session not bound to an issue. URL selects it (?agent=).
type WbCrewCard struct {
	Name, URL string
	// Kind is ceo, lead, worker, stopped or other.
	Kind  string
	Robot WbRobot
	// State is the agent's state in words; Tone colours its chip.
	State, Tone string
	Issue       *WbLink
	Status      string
	Epic        string
	// LastCall is "last issue-cli call 4 m ago", empty when unknown.
	LastCall string
	// Agent is the CLI the live session runs (claude, codex); Limit the
	// usage-limit message its pane shows now (the agent is left running).
	Agent, Limit string
	Notes        []string
	// Command restarts a stopped job (issue-cli dispatch <slug>).
	Command string
	// SpawnURL starts a lead that is not running; SpawnWhat names it.
	SpawnURL, SpawnWhat string
	Selected            bool
	// gate and slug feed the panel.
	gate, slug string
	live       bool
	lastCmd    string
}

// WbCrewPanel is the selected agent: how to reach it, its issue and branch.
type WbCrewPanel struct {
	Card WbCrewCard
	// OpenURL opens the live session in a terminal (POST agents/open).
	OpenURL string
	// Command reaches the session by hand: attach from a shell, or switch
	// from inside tmux on a terminal: none project.
	Command, CommandLabel string
	// Display is where the agent lives in tmux when that differs from its
	// name (shared:name in shared mode).
	Display          string
	LastCmd          string
	Gate, ApproveURL string
	Branch, Against  string
	DiffStat         string
	Comments         []WbCrewComment
}

// WbCrewComment is one of the issue's last comments.
type WbCrewComment struct {
	Date, Text string
}

func (s *Server) handleCrew(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	model, err := s.buildAgentsModel(proj, prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	selected := r.URL.Query().Get("agent")
	if !validAgentName(selected) {
		selected = ""
	}
	data := buildCrew(model, proj.Departments, prefix, selected, now)
	lot, waiting := s.decisionsLot(proj, now)
	data.Header = WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(1, proj.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, Waiting: waiting, HideDone: true}
	data.Header.Crumbs[1].Color = lot.Color
	data.Header.Crumbs[1].Current, data.Header.Crumbs[1].URL = false, prefix+"/company"
	data.Header.Crumbs[2] = WbCrumb{Label: "Crew", Current: true}
	data.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	if p := data.Panel; p != nil && p.Card.slug != "" {
		d := s.agentDetailOf(proj, p.Card.Name, p.Card.slug)
		p.Branch, p.Against, p.DiffStat = d.Branch, d.Against, d.DiffStat
		for _, c := range d.Comments {
			p.Comments = append(p.Comments, WbCrewComment{Date: tracker.CommentDay(c.Date), Text: c.Text})
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "crew.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildCrew lays the agents model out as the org chart. depts are the
// project's departments in projects.yaml order; selected is the ?agent=
// session (already validated), "" for none.
func buildCrew(m *AgentsModel, depts []tracker.Department, prefix, selected string, now time.Time) CrewData {
	d := CrewData{Project: m.Project, ClassicURL: prefix + "/agents?classic=1", Checked: now.UnixMilli()}
	epics := map[string]string{}
	for _, e := range m.Epics {
		epics[e.Issue.Slug] = e.Issue.Title
	}
	card := func(n AgentNode, kind string) WbCrewCard {
		c := crewCard(n, kind, prefix, now)
		if n.Epic != "" && (n.Issue == nil || n.Epic != n.Issue.Slug) {
			c.Epic = epics[n.Epic]
		}
		return c
	}

	d.CEO = card(m.ProjectLead, "ceo")
	d.CEO.SpawnURL, d.CEO.SpawnWhat = prefix+"/lead/spawn", "the CEO"

	product := WbCrewColumn{Name: "Product", Color: companyProductColor, Key: "product"}
	byDept := map[string]int{}
	cols := []WbCrewColumn{product}
	for i, dept := range depts {
		col := WbCrewColumn{Name: dept.Name, Color: deptColorOf(&tracker.Project{Departments: depts}, dept, i), Key: strings.ToLower(dept.Name)}
		for _, l := range m.DepartmentLeads {
			if strings.EqualFold(l.Department, dept.Name) {
				lc := card(l, "lead")
				lc.SpawnURL, lc.SpawnWhat = prefix+"/lead/spawn?dept="+url.QueryEscape(dept.Name), "the "+dept.Name+" lead"
				col.Lead = &lc
			}
		}
		byDept[strings.ToLower(dept.Name)] = len(cols)
		cols = append(cols, col)
	}
	for _, w := range m.Workers {
		i := byDept[strings.ToLower(w.Department)] // unknown or "": Product
		cols[i].Workers = append(cols[i].Workers, card(w, "worker"))
	}
	rank := map[string]int{"you": 0, "working": 1, "agent": 2, "idle": 3}
	for i := range cols {
		sort.SliceStable(cols[i].Workers, func(a, b int) bool {
			return rank[cols[i].Workers[a].Robot.State] < rank[cols[i].Workers[b].Robot.State]
		})
	}
	d.Columns = crewGroupColumns(depts, cols, byDept)
	d.NoWorkers = len(m.Workers) == 0

	d.Stopped = WbCrewRow{Key: "stopped", Title: "Stopped jobs", Note: "Work in flight with no agent on it: a worktree or an active status."}
	for _, n := range m.Unfinished {
		d.Stopped.Cards = append(d.Stopped.Cards, card(n, "stopped"))
	}
	d.Others = WbCrewRow{Key: "others", Title: "Other sessions", Note: "Live agent sessions not bound to an issue."}
	for _, n := range m.Others {
		d.Others.Cards = append(d.Others.Cards, card(n, "other"))
	}

	live, gates, questions := 0, 0, 0
	all := []*WbCrewCard{&d.CEO}
	var addCol func(c *WbCrewColumn)
	addCol = func(c *WbCrewColumn) {
		if c.Lead != nil {
			all = append(all, c.Lead)
		}
		for j := range c.Workers {
			all = append(all, &c.Workers[j])
		}
		for j := range c.Children {
			addCol(&c.Children[j])
		}
	}
	for i := range d.Columns {
		addCol(&d.Columns[i])
	}
	for i := range d.Stopped.Cards {
		all = append(all, &d.Stopped.Cards[i])
	}
	for i := range d.Others.Cards {
		all = append(all, &d.Others.Cards[i])
	}
	for _, c := range all {
		if c.live {
			live++
		}
		if c.Kind == "worker" && c.Tone == "you" {
			if c.State == crewAsked {
				questions++
			} else {
				gates++
			}
		}
		if selected != "" && c.Name == selected && d.Panel == nil {
			c.Selected = true
			d.Panel = crewPanel(*c, m, prefix)
		}
	}
	d.Count = WbStat{Value: fmt.Sprint(live), Label: plural0(live, "live agent", "live agents")}
	for _, sv := range m.Subscriptions {
		tone := "idle"
		switch {
		case sv.Resting:
			tone = "sand"
		case sv.Live > 0:
			tone = "green"
		}
		state := sv.State
		if !sv.Installed {
			state, tone = "not installed", "idle"
		}
		d.Subs = append(d.Subs, WbCrewSub{Agent: sv.Agent, State: state, Tone: tone, Installed: sv.Installed})
	}
	d.Summary = crewSummary(gates, questions, len(d.Stopped.Cards))
	return d
}

// crewGroupColumns moves a group's departments' columns into the group's
// column, in projects.yaml order. byDept is each department's index in cols.
func crewGroupColumns(depts []tracker.Department, cols []WbCrewColumn, byDept map[string]int) []WbCrewColumn {
	p := &tracker.Project{Departments: depts}
	// column is dept's column with its departments' inside, a nested
	// group's own inside it (factory/nested-groups-…#D-1).
	var column func(dept tracker.Department) WbCrewColumn
	column = func(dept tracker.Department) WbCrewColumn {
		col := cols[byDept[strings.ToLower(dept.Name)]]
		for _, c := range p.Children(dept.Name) {
			col.Children = append(col.Children, column(c))
		}
		return col
	}
	out := []WbCrewColumn{cols[0]}
	for _, dept := range depts {
		if p.GroupOf(dept.Name) != nil {
			continue
		}
		out = append(out, column(dept))
	}
	return out
}

// Chip is the card's state as a pill.
func (c WbCrewCard) Chip() WbChip { return WbChip{Tone: c.Tone, Text: c.State} }

// crewCard is one agent node as a card.
func crewCard(n AgentNode, kind, prefix string, now time.Time) WbCrewCard {
	c := WbCrewCard{Name: n.Name, URL: prefix + "/agents?agent=" + url.QueryEscape(n.Name), Kind: kind, gate: n.Gate, live: n.Live, lastCmd: n.LastCmd, Agent: n.Agent}
	if n.Live {
		c.Limit = n.Limit
	}
	if n.Issue != nil {
		c.Issue = &WbLink{Label: n.Issue.Title, URL: n.Issue.URL}
		c.Status, c.slug = n.Issue.Status, n.Issue.Slug
	}
	if n.LastCall != "" {
		if at, err := time.Parse(time.RFC3339, n.LastCall); err == nil {
			c.LastCall = "last issue-cli call " + agoText(now.Sub(at)) + " ago"
			if now.Sub(at) < time.Minute {
				c.LastCall = "last issue-cli call just now"
			}
		}
	}
	switch kind {
	case "ceo", "lead":
		c.Robot = WbRobot{State: "working", Label: n.Name}
		c.State, c.Tone = "live", "green"
		if !n.Live {
			c.Robot.State, c.State, c.Tone = "off", "not running", "idle"
		}
	case "stopped":
		c.Robot = WbRobot{State: "off", Label: n.Name}
		c.State, c.Tone = "stopped", "idle"
		if n.Gate != "" {
			c.State, c.Tone = "stopped · ready for the "+n.Gate+" gate", "you"
		}
		if n.RetroNotSwept {
			c.Notes = append(c.Notes, "retro not swept")
		}
		if n.Issue != nil {
			c.Command = "issue-cli dispatch " + n.Issue.Slug
		}
	case "other":
		c.Robot = WbRobot{State: "working", Label: n.Name}
		c.State, c.Tone = "live · not bound to an issue", "idle"
	default:
		c.Robot = workerRobot(n)
		c.Robot.Label = n.Name
		switch n.State {
		case "at-gate":
			c.State, c.Tone = "waiting on you at the "+n.Gate+" gate", "you"
		case "question":
			c.State, c.Tone = crewAsked, "you"
		case "idle":
			c.State, c.Tone = "idle", "idle"
			if c.LastCall != "" {
				c.State = "idle · " + strings.TrimPrefix(c.LastCall, "last ")
				c.LastCall = ""
			}
		default:
			c.State, c.Tone = "working", "green"
		}
		if c.Limit != "" && n.State != "at-gate" && n.State != "question" {
			c.State, c.Tone = "hit its "+valueOrAgent(n.Agent)+" usage limit · left running", "sand"
		}
	}
	return c
}

// Chip is the subscription's state chip.
func (s WbCrewSub) Chip() WbChip { return WbChip{Tone: s.Tone, Text: s.State} }

func valueOrAgent(agent string) string {
	if agent == "" {
		return "agent's"
	}
	return agent
}

// crewPanel is the side panel for the selected card.
func crewPanel(c WbCrewCard, m *AgentsModel, prefix string) *WbCrewPanel {
	p := &WbCrewPanel{Card: c, LastCmd: c.lastCmd}
	if c.live {
		p.OpenURL = prefix + "/agents/open"
	}
	for _, n := range crewNodes(m) {
		if n.Name != c.Name {
			continue
		}
		if n.Display != "" && n.Display != n.Name {
			p.Display = n.Display
		}
		// The way in by hand; for an agent that is not running it works
		// once its session is back (the classic panel offered it too).
		p.Command, p.CommandLabel = n.AttachCmd, "From a shell"
		if m.TerminalNone && n.SwitchCmd != "" {
			p.Command, p.CommandLabel = n.SwitchCmd, "Inside tmux"
		}
		if !c.live && p.Command != "" {
			p.CommandLabel += ", once it runs again"
		}
	}
	if c.gate != "" && c.slug != "" {
		p.Gate = c.gate
		p.ApproveURL = prefix + "/issue/" + c.slug + "#approve-" + strings.ReplaceAll(strings.TrimSpace(c.gate), " ", "-")
	}
	return p
}

// crewNodes is every node of the model.
func crewNodes(m *AgentsModel) []AgentNode {
	all := append([]AgentNode{m.ProjectLead}, m.DepartmentLeads...)
	all = append(all, m.Workers...)
	all = append(all, m.Unfinished...)
	return append(all, m.Others...)
}

// crewSummary is the hero's one-line verdict: what waits on the owner among
// the live workers, and how many jobs stopped.
func crewSummary(gates, questions, stopped int) WbSummaryBox {
	var parts []string
	if gates > 0 {
		parts = append(parts, fmt.Sprintf("%d at a gate", gates))
	}
	if questions > 0 {
		parts = append(parts, fmt.Sprintf("%d with a question", questions))
	}
	box := WbSummaryBox{Key: "crew", OK: len(parts) == 0}
	if box.OK {
		parts = append(parts, "Nobody here waits on you")
	}
	if stopped > 0 {
		parts = append(parts, plural(stopped, "stopped job", "stopped jobs"))
	}
	box.Text = strings.Join(parts, " · ")
	return box
}
