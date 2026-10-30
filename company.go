package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/outputs"
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

// companyGoals is how many goals the header shows: HQ's configured goals,
// else the open epics that stand in for them.
const companyGoals = 2

// companyRoomGoals is how many goals a department room shows.
const companyRoomGoals = 3

// CompanyData is the whole L1 page.
type CompanyData struct {
	Header WbHeader
	Lot    MapLot
	// Departments is how many departments the project has (HQ and Product).
	Departments int
	HQ          WbRoomHQ
	Product     WbRoomFactory
	// Rooms are the departments projects.yaml adds; none by default.
	Rooms   []WbRoomDept
	NewDept WbNewTile
	Queue   []WbQueueCard
	// QueueMore is how many more wait on the owner than the panel lists.
	QueueMore int
	// Shelf is the departments with outputs waiting for the owner's review.
	Shelf   []WbShelfWait
	Waiting int
	Radio   []WbMessage
	Message WbMessageBox
	// Toggle is Board | Report, on the title line (the header is full here).
	Toggle []WbViewTab
	// YOLOQueue is "To review after YOLO": the passes the owner has not
	// reviewed; YOLOMore how many more than listed.
	YOLOQueue []WbYOLORow
	YOLOMore  int
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
	rep := goals.Resolve(p, now)
	d := CompanyData{Lot: lot, Departments: 2, Queue: queue, Waiting: len(queue)}
	if len(d.Queue) > companyQueueRows {
		d.Queue, d.QueueMore = d.Queue[:companyQueueRows], len(queue)-companyQueueRows
	}
	d.Header = WbHeader{
		Crumbs:  wbCrumbs(1, p.Name, prefix+"/company"),
		Goals:   companyHeaderGoals(rep, issues, color),
		Waiting: len(queue), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek,
	}
	d.Header.Crumbs[1].Color = color
	d.Header.Crumbs[2] = WbCrumb{Label: "Hall", URL: prefix + "/hall"}
	d.Toggle = wbViewToggle(prefix, "board")
	d.Header.YOLO = wbYOLO(lot.YOLO, prefix)
	if y := d.Header.YOLO; y != nil {
		y.Switch = true
		if !y.On {
			y.Dialog = yoloDialog(p, wf)
		}
	}
	d.YOLOQueue, d.YOLOMore = yoloQueueRows(lot.YOLO, prefix, companyQueueRows)

	d.HQ = WbRoomHQ{
		Head: WbRoomHead{Title: "HQ · CEO", Color: companyHQColor, Meta: "1/1 desk"},
		Desk: WbCEODesk{CEO: lot.CEO, Color: color, ChatURL: prefix + "/lead", SpawnURL: prefix + "/lead/spawn"},
		Foot: []WbFootItem{{Text: fmt.Sprintf("%d open", open)}, {Text: fmt.Sprintf("queue: %d in backlog", backlog)}},
	}
	if rep.HQ != nil {
		d.HQ.Mission = rep.HQ.Mission
		d.HQ.Goals = companyRollUp(p, rep.HQ, color)
	}
	shelf, hqShelf, shelfWaits := companyShelf(p, prefix, now)
	d.HQ.Shelf, d.Shelf = hqShelf, shelfWaits
	d.Waiting += len(shelfWaits)
	d.Header.Waiting += len(shelfWaits)

	// Workers by department: Product's go on its line, the others' sit in
	// their department's room.
	var workers []AgentNode
	deptWorkers := map[string][]AgentNode{}
	if agents != nil {
		for _, w := range agents.Workers {
			dept := ""
			if w.Issue != nil {
				if issue := tracker.FindIssue(issues, w.Issue.Slug); issue != nil {
					dept = strings.ToLower(p.DepartmentOf(issue))
				}
			}
			if dept == "" {
				workers = append(workers, w)
			} else {
				deptWorkers[dept] = append(deptWorkers[dept], w)
			}
		}
	}
	product := WbRoomFactory{
		Head:     WbRoomHead{Title: "Product · software factory", Color: companyProductColor, Meta: ""},
		Stations: companyStations(wf, productIssues(p, issues), workers, waitingOnOwner(p, openOnly(productIssues(p, issues)))),
		Waiting:  lot.Waiting,
		YOLO:     lot.YOLO,
		HallURL:  prefix + "/hall",
		Foot:     []WbFootItem{{Text: fmt.Sprintf("✓ %d this week", lot.DoneWeek), Tone: "green"}},
	}
	if rep.Product != nil {
		product.Mission, product.Goals = rep.Product.Mission, roomGoals(rep.Product, companyProductColor)
	}
	product.Shelf = shelf[goals.ProductKey]
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		product.Head.Chips = append(product.Head.Chips, *c)
	}
	product.Head.Chips = append(product.Head.Chips, WbChip{Tone: "idle", Text: plural(len(workers), "worker", "workers")})
	d.Product = product

	for i, dept := range p.Departments {
		room := companyDeptRoom(p, dept, i, issues, deptWorkers[strings.ToLower(dept.Name)], now)
		if o := rep.Find(goals.DeptKey(dept.Name)); o != nil {
			room.Mission, room.Goals = o.Mission, roomGoals(o, deptColor(dept, i))
		}
		room.Shelf = shelf[outputs.DeptKey(dept.Name)]
		d.Rooms = append(d.Rooms, room)
	}
	d.Departments = 2 + len(p.Departments)

	d.NewDept = WbNewTile{
		Title: "New department", Sub: "Sales · Finance · SEO · Custom", TestID: "new_department",
		Note: "Add one under departments: in projects.yaml (name, color, role, systems, mission, prompt, goals, workflow); its issues carry department: <name> or sit in its systems, and walk its workflow: file (issue-cli workflow init --template content --out workflows/<name>.yaml).",
	}
	if lot.CEO.Live {
		if pane, err := agentrun.CaptureHistory(agentrun.Target(p, tracker.LeadSessionName(p)), leadChatScrollback); err == nil {
			d.Radio = leadRadio(cleanPane(pane), lot.CEO.Session, companyRadioMax)
		}
	}
	d.Message = WbMessageBox{Session: lot.CEO.Session, SendURL: prefix + "/lead/send", Live: lot.CEO.Live, SpawnURL: prefix + "/lead/spawn"}
	return d
}

// companyDeptRoom is a department projects.yaml adds, as a room: its
// workers at their desks, its role, its open and done work.
func companyDeptRoom(p *tracker.Project, dept tracker.Department, i int, issues []*tracker.Issue, workers []AgentNode, now time.Time) WbRoomDept {
	open, done := 0, 0
	var own []*tracker.Issue
	for _, issue := range issues {
		if p.DepartmentOf(issue) != dept.Name {
			continue
		}
		own = append(own, issue)
		if !strings.EqualFold(issue.Status, "done") {
			open++
		} else if at, _ := doneAt(issue); now.Sub(at) <= mapDoneWindow {
			done++
		}
	}
	var robots []WbRobot
	for _, w := range workers {
		robots = append(robots, workerRobot(w))
	}
	room := WbRoomDept{
		Head: WbRoomHead{Title: dept.Name, Color: deptColor(dept, i), Meta: plural(len(workers), "agent", "agents")},
		Crew: wbCrew(robots),
		Role: dept.Role,
		Foot: []WbFootItem{{Text: fmt.Sprintf("✓ %d this week", done), Tone: "green"}, {Text: fmt.Sprintf("%d open", open)}},
	}
	if len(dept.Systems) > 0 {
		room.Systems = strings.Join(dept.Systems, ", ")
	}
	// A department on its own workflow file shows that file's line.
	if strings.TrimSpace(dept.Workflow) != "" {
		room.WorkflowFile = filepath.Base(dept.Workflow)
		if wf, err := p.WorkflowFor(dept.Name); err != nil {
			room.WorkflowError = err.Error()
			room.Head.Chips = append(room.Head.Chips, WbChip{Tone: "red", Text: "workflow error"})
		} else {
			room.Stations = companyStations(wf, openOnly(own), workers, waitingOnOwner(p, openOnly(own)))
		}
	}
	return room
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

// companyHeaderGoals is HQ's configured goals, else the open epics.
func companyHeaderGoals(rep *goals.Report, issues []*tracker.Issue, color string) []WbGoal {
	if rep.HQ == nil || len(rep.HQ.Goals) == 0 {
		return companyGoalsOf(issues, color)
	}
	var out []WbGoal
	for _, g := range rep.HQ.Goals {
		if len(out) == companyGoals {
			break
		}
		out = append(out, goalChip(g, color))
	}
	return out
}

// companyRollUp is HQ's goals, each with the department goals rolling up
// into it in their department's colour.
func companyRollUp(p *tracker.Project, hq *goals.Owner, color string) []WbHQGoal {
	colors := map[string]string{tracker.ProductName: companyProductColor}
	for i, d := range p.Departments {
		colors[d.Name] = deptColor(d, i)
	}
	var out []WbHQGoal
	for _, g := range hq.Goals {
		row := WbHQGoal{Goal: goalChip(g, color)}
		for _, c := range g.Children {
			chip := goalChip(c, colors[c.Owner])
			chip.Progress.Label = c.Owner + " · " + c.Label()
			row.Children = append(row.Children, chip)
		}
		out = append(out, row)
	}
	return out
}

// roomGoals is a department's first goals as chips.
func roomGoals(o *goals.Owner, color string) []WbGoal {
	var out []WbGoal
	for _, g := range o.Goals {
		if len(out) == companyRoomGoals {
			break
		}
		out = append(out, goalChip(g, color))
	}
	return out
}

// goalChip is a configured goal as a goal chip: "30 / 40" and its
// progress; the tooltip says why it is muted (stale, unreadable).
func goalChip(g *goals.Goal, color string) WbGoal {
	chip := WbGoal{Color: color, Progress: WbProgress{Label: g.Label(), Done: g.Pct(), Total: 100}, Text: g.ValueText()}
	if t := g.TargetText(); t != "" {
		chip.Text += " / " + t
	}
	var title []string
	if g.RolledUp {
		chip.Text = fmt.Sprintf("%d%%", g.Pct())
		title = append(title, fmt.Sprintf("mean progress of %d department goals", g.RolledFrom))
	}
	switch {
	case g.Err != "":
		chip.Off = true
		title = append(title, g.Err)
	case g.Stale:
		chip.Off = true
		title = append(title, "stale: from "+g.Date)
	case g.Value == nil && !g.RolledUp:
		if g.Manual() {
			title = append(title, "no value yet: "+g.SetCommand())
		}
	}
	chip.Title = strings.Join(title, " · ")
	return chip
}

// companyGoalsOf is the open epics furthest along, as the header's goals
// when HQ has no goals configured.
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

// lineStop is one stop of the factory line read from workflow.yaml: a
// status, whether the owner approves entry into it (a gate stands before
// it) and whether it is the owner's own station (human testing).
type lineStop struct {
	Name, Key, Description string
	Gated, Owner           bool
}

// factoryLine is the project's workflow.yaml as the factory line: every
// status up to done (parked is off the line; optional statuses are stations
// too). Shared by the company's line (L1) and the hall (L2).
func factoryLine(wf *tracker.WorkflowConfig) []lineStop {
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
	var out []lineStop
	for _, st := range wf.Statuses {
		k := statusKey(st.Name)
		if k == cs.final || k == "done" || k == "parked" {
			continue
		}
		owner := strings.Contains(k, "human")
		out = append(out, lineStop{Name: st.Name, Key: k, Description: st.Description, Gated: gated[k] && !owner, Owner: owner})
	}
	return out
}

// workerRobot is a live worker as a robot.
func workerRobot(w AgentNode) WbRobot {
	r := WbRobot{State: "working", Label: w.Name}
	if w.Issue != nil {
		r.Label = w.Issue.Slug
	}
	switch w.State {
	case "idle":
		r.State = "idle"
	case "at-gate", "question":
		r.State = "you"
	}
	return r
}

// companyStations is the factory line with how many issues stand at each
// stop and the robots working them; a gate counts the issues ready for the
// owner's approval.
func companyStations(wf *tracker.WorkflowConfig, issues []*tracker.Issue, workers []AgentNode, waiting []WaitingItem) []WbStation {
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
		if w.Issue != nil {
			robots[statusKey(w.Issue.Status)] = append(robots[statusKey(w.Issue.Status)], workerRobot(w))
		}
	}
	var out []WbStation
	for _, stop := range factoryLine(wf) {
		if stop.Gated {
			out = append(out, WbStation{Label: "gate", Title: "Your approval to enter " + stop.Name, Gate: true, Count: atGate[stop.Key]})
		}
		label := stop.Name
		if stop.Owner {
			label = "human test"
		}
		out = append(out, WbStation{Label: label, Title: stop.Description, Count: count[stop.Key], Crew: wbCrewOf(robots[stop.Key], companyStationBots), Owner: stop.Owner})
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
