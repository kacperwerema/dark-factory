package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The company (L1) is one project as a building: HQ with the CEO and the
// Product department, the software factory, whose line is the project's
// workflow.yaml. Other departments come later: each will be a new pillar
// with its own agents. Design: issues/Factory/designs/workshop-board/L1.

// companyHQColor and companyProductColor are the two departments' colours
// from the design system.
const (
	companyHQColor      = "#F2B233"
	companyProductColor = "#3D7BF7"
)

// companyRadioMax is how many of the CEO's lines the Radio shows.
const companyRadioMax = 8

// companyStationBots is how many robots a station draws before "+N".
const companyStationBots = 3

// companyQueueRows is how many "waiting on you" rows the panel lists; the
// rest are on the map's queue.
const companyQueueRows = 6

// companyGoals is how many open epics stand as goals in the header.
const companyGoals = 2

// CompanyData is the whole L1 page.
type CompanyData struct {
	Header WbHeader
	Lot    MapLot
	// Departments is how many departments the project has (HQ and Product).
	Departments int
	HQ          WbRoomHQ
	Product     WbRoomFactory
	NewDept     WbNewTile
	Queue       []WbQueueCard
	// QueueMore is how many more wait on the owner than the panel lists.
	QueueMore int
	Waiting   int
	Radio     []WbMessage
	Message   WbMessageBox
}

func (s *Server) handleCompany(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	data := s.buildCompany(proj, prefix, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "company.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildCompany reuses the project's lot from the map and adds the factory
// line, the goals and the Radio.
func (s *Server) buildCompany(p *tracker.Project, prefix string, now time.Time) CompanyData {
	color := p.Color
	if color == "" {
		for i := range s.projects {
			if s.projects[i].Slug == p.Slug {
				color = mapProjectColors[i%len(mapProjectColors)]
			}
		}
	}
	lot, queue := s.buildLot(p, color, now)
	sortMapQueue(queue)
	issues, _ := tracker.LoadIssues(p.IssueDir)
	wf := p.LoadWorkflow()
	agents, _ := s.buildAgentsModel(p, prefix)

	open, backlog := 0, 0
	for _, issue := range issues {
		if !strings.EqualFold(issue.Status, "done") {
			open++
		}
		if strings.EqualFold(issue.Status, "backlog") {
			backlog++
		}
	}
	d := CompanyData{Lot: lot, Departments: 2, Queue: queue, Waiting: len(queue)}
	if len(d.Queue) > companyQueueRows {
		d.Queue, d.QueueMore = d.Queue[:companyQueueRows], len(queue)-companyQueueRows
	}
	d.Header = WbHeader{
		Crumbs:  wbCrumbs(1, p.Name, prefix+"/company"),
		Goals:   companyGoalsOf(issues, color),
		Waiting: len(queue), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek,
	}
	d.Header.Crumbs[1].Color = color
	d.Header.Crumbs[2].Hint = "The hall (L2) comes next"

	d.HQ = WbRoomHQ{
		Head: WbRoomHead{Title: "HQ · CEO", Color: companyHQColor, Meta: "1/1 desk"},
		Desk: WbCEODesk{CEO: lot.CEO, Color: color, ChatURL: prefix + "/lead", SpawnURL: prefix + "/lead/spawn"},
		Foot: []WbFootItem{{Text: fmt.Sprintf("%d open", open)}, {Text: fmt.Sprintf("queue: %d in backlog", backlog)}},
	}

	var workers []AgentNode
	if agents != nil {
		workers = agents.Workers
	}
	product := WbRoomFactory{
		Head:     WbRoomHead{Title: "Product · software factory", Color: companyProductColor, Meta: ""},
		Stations: companyStations(wf, issues, workers, waitingOnOwner(p, openOnly(issues))),
		Waiting:  lot.Waiting,
		HallURL:  prefix + "/board",
		Foot:     []WbFootItem{{Text: fmt.Sprintf("✓ %d this week", lot.DoneWeek), Tone: "green"}},
	}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		product.Head.Chips = append(product.Head.Chips, *c)
	}
	product.Head.Chips = append(product.Head.Chips, WbChip{Tone: "idle", Text: plural(len(workers), "worker", "workers")})
	d.Product = product

	d.NewDept = WbNewTile{
		Title: "New department", Sub: "Sales · Finance · SEO · Custom", TestID: "new_department",
		Note: "Coming later: a new department is a new pillar with its own agents, roles and connectors.",
	}
	if lot.CEO.Live {
		if pane, err := agentrun.CaptureHistory(agentrun.Target(p, tracker.LeadSessionName(p)), leadChatScrollback); err == nil {
			d.Radio = leadRadio(cleanPane(pane), lot.CEO.Session, companyRadioMax)
		}
	}
	d.Message = WbMessageBox{Session: lot.CEO.Session, SendURL: prefix + "/lead/send", Live: lot.CEO.Live, SpawnURL: prefix + "/lead/spawn"}
	return d
}

// openOnly is the issues that are not done.
func openOnly(issues []*tracker.Issue) []*tracker.Issue {
	var out []*tracker.Issue
	for _, issue := range issues {
		if !strings.EqualFold(issue.Status, "done") {
			out = append(out, issue)
		}
	}
	return out
}

// companyGoalsOf is the open epics furthest along, as the header's goals
// until projects have goals of their own.
func companyGoalsOf(issues []*tracker.Issue, color string) []WbGoal {
	var goals []WbGoal
	for _, issue := range issues {
		if strings.EqualFold(issue.Status, "done") || !tracker.IsEpic(issue, issues) {
			continue
		}
		g := WbGoal{Color: color, Progress: WbProgress{Label: issue.Title}}
		for _, c := range tracker.EpicChildren(issues, issue.Slug) {
			g.Progress.Total++
			if strings.EqualFold(c.Status, "done") {
				g.Progress.Done++
			}
		}
		if g.Progress.Total > 0 {
			goals = append(goals, g)
		}
	}
	sort.SliceStable(goals, func(i, j int) bool {
		a, b := goals[i].Progress, goals[j].Progress
		return a.Done*b.Total > b.Done*a.Total
	})
	if len(goals) > companyGoals {
		goals = goals[:companyGoals]
	}
	return goals
}

// companyStations is the factory line: every status of workflow.yaml up to
// the final one, with how many issues stand there and the robots working
// them. A status the owner approves entry into has a gate (a stamp) before
// it, counting the issues ready for that approval; human testing is the
// owner's own station.
func companyStations(wf *tracker.WorkflowConfig, issues []*tracker.Issue, workers []AgentNode, waiting []WaitingItem) []WbStation {
	cs := newCanvasStatuses(wf)
	gated := map[string]bool{}
	for _, t := range wf.Transitions {
		for _, a := range t.Actions {
			if a.Type == "require_human_approval" {
				target := a.Status
				if target == "" {
					target = t.To
				}
				gated[statusKey(target)] = true
			}
		}
	}
	atGate := map[string]int{}
	for _, it := range waiting {
		if it.Kind == "gate" {
			atGate[statusKey(it.Target)]++
		}
	}
	count := map[string]int{}
	for _, issue := range issues {
		count[statusKey(issue.Status)]++
	}
	robots := map[string][]WbRobot{}
	for _, w := range workers {
		if w.Issue == nil {
			continue
		}
		r := WbRobot{State: "working", Label: w.Issue.Slug}
		switch w.State {
		case "idle":
			r.State = "idle"
		case "at-gate", "question":
			r.State = "you"
		}
		k := statusKey(w.Issue.Status)
		robots[k] = append(robots[k], r)
	}
	var out []WbStation
	for _, st := range wf.Statuses {
		k := statusKey(st.Name)
		// The line runs up to done; parked is off the line. Optional
		// statuses are stations too (skippable, still on the line).
		if k == cs.final || k == "done" || k == "parked" {
			continue
		}
		if gated[k] && !strings.Contains(k, "human") {
			out = append(out, WbStation{Label: "gate", Title: "Your approval to enter " + st.Name, Gate: true, Count: atGate[k]})
		}
		label := st.Name
		if strings.Contains(k, "human") {
			label = "human test"
		}
		out = append(out, WbStation{Label: label, Title: st.Description, Count: count[k], Crew: wbCrewOf(robots[k], companyStationBots), Owner: strings.Contains(k, "human")})
	}
	return out
}

// leadRadio is the lead's newest message paragraphs, newest first: what the
// CEO said, for the Radio.
func leadRadio(pane, session string, n int) []WbMessage {
	paras := paneParagraphs(pane)
	var out []WbMessage
	for i := len(paras) - 1; i >= 0 && len(out) < n; i-- {
		para := paras[i]
		if !strings.HasPrefix(para, "⏺") {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(para, "⏺"))
		if text == "" || toolCall.MatchString(text) {
			continue
		}
		out = append(out, WbMessage{From: session, To: "you", Initials: "CEO", Tone: "ceo", Text: clipRunes(markdownMarks.ReplaceAllString(text, ""), 400)})
	}
	return out
}
