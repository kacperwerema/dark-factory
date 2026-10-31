package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/cmd/issue-cli/templates"
	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The map (L0, "Project map") is the board's home page: one lot per
// project, the CEO's latest line, its agents as robots, CI, what waits on the
// owner, and on the right the owner's queue across every project. Design:
// "Workshop Board" (issues/Factory/designs/workshop-board in dark-factory).

// mapProjectColors are the lots' colours when a project sets none
// (projects.yaml color:). Orange is never one: it is the owner's.
var mapProjectColors = []string{"#13A386", "#5B5FE0", "#5DAE3B", "#2A5BD7", "#E2559B", "#17A2B8", "#B07A4F", "#6C7A96"}

// mapQuoteMax is the longest CEO line on a lot, in characters.
const mapQuoteMax = 140

// mapLeadScrollback is how much of a live lead's pane the quote reads.
const mapLeadScrollback = 300

// mapDoneWindow is "this week" for the done counters.
const mapDoneWindow = 7 * 24 * time.Hour

// MapData is the whole map page.
type MapData struct {
	Header WbHeader
	Lots   []MapLot
	// Totals across projects, for the header.
	Working, Idle, AgentWait int
	DoneWeek                 int
	Queue                    []WbQueueCard
	Updates                  int
	// Engine is the engine project's slug when it is on the map: its lot
	// stands in the middle and a road joins it to every project.
	Engine string
	// Active and Sites count the projects at work and the building sites.
	Active, Sites int
	// RC is the engine's release candidate being tested; nil without one.
	RC *MapRC
	// NewProject is the "+" tile after the lots.
	NewProject WbNewTile
}

// MapRC is the engine's release candidate and the projects running it: the
// roads from the engine's lot carry it.
type MapRC struct {
	Tag    string
	Engine string
	// Testing are the slugs of the projects whose issue-cli ran the candidate.
	Testing []string
}

// MapLot is one project on the map.
type MapLot struct {
	Name, Slug, Kind, Color string
	// Engine: this lot is the workflow engine itself (dark-factory).
	Engine bool
	// Site: a project not kicked off yet (no issues): a building site with
	// its Description and a button to start its CEO.
	Site        bool
	Description string
	Buildings   []WbBuilding
	Robots      []WbRobot
	Crew        WbCrew
	// CI is "", "green" or "red"; CILine says what failed.
	CI, CILine string
	Working    int
	Idle       int
	AgentWait  int
	You        int
	CEO        WbCEO
	Progress   *WbProgress
	// Chips are CI, the agent counts and privacy problems, in that order.
	Chips []WbChip
	// Waiting is what waits on the owner here: the "!" on the lot.
	Waiting  int
	DoneWeek int
	// Connections are the open items passing between its departments.
	Connections int
	// RC is the candidate tag when this project is testing it.
	RC      string
	Updates int
	Privacy []PrivacyBadge
	// YOLO is the project's YOLO window and after-the-fact queue: the lot in
	// stripes and the counter that replaces the "!" (yolo_wb.go).
	YOLO *YOLOBand

	// issues and agents are what buildLot read, for the company page to
	// reuse instead of reading them again.
	issues []*tracker.Issue
	agents *AgentsModel
	// waits is waitingOnOwner over the open issues, for the company page.
	waits []WaitingItem
}

func (s *Server) handleMap(w http.ResponseWriter, r *http.Request) {
	data := s.buildMap(time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "map.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildMap reads every project at once, one goroutine each.
func (s *Server) buildMap(now time.Time) MapData {
	lots := make([]MapLot, len(s.projects))
	queues := make([][]WbQueueCard, len(s.projects))
	var rc *MapRC
	var wg sync.WaitGroup
	for i := range s.projects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := &s.projects[i]
			color := p.Color
			if color == "" {
				color = mapProjectColors[i%len(mapProjectColors)]
			}
			lots[i], queues[i] = s.buildLot(p, color, now)
			if p.Slug == s.engine {
				rc = s.engineCandidate(p)
			}
		}(i)
	}
	wg.Wait()

	data := MapData{Lots: lots, RC: rc, NewProject: mapNewProjectTile()}
	for i := range data.Lots {
		lot := &data.Lots[i]
		lot.Engine = lot.Slug == s.engine
		if lot.Engine {
			data.Engine = lot.Slug
		}
		if lot.Site {
			data.Sites++
		} else {
			data.Active++
		}
		data.Working += lot.Working
		data.Idle += lot.Idle
		data.AgentWait += lot.AgentWait
		data.DoneWeek += lot.DoneWeek
		data.Updates += lot.Updates
		data.Queue = appendNeedsYou(data.Queue, queues[i])
		if rc != nil {
			for _, slug := range rc.Testing {
				if slug == lot.Slug && !lot.Engine {
					lot.RC = rc.Tag
				}
			}
		}
	}
	sort.SliceStable(data.Lots, func(i, j int) bool { return !data.Lots[i].Site && data.Lots[j].Site })
	sortMapQueue(data.Queue)
	data.Header = WbHeader{Crumbs: wbCrumbs(0, "", ""), Working: data.Working, Idle: data.Idle, AgentWait: data.AgentWait, DoneWeek: data.DoneWeek, Waiting: len(data.Queue), WaitingURL: "/needs-you"}
	return data
}

// PlanCard is the building site's blueprint card.
func (l MapLot) PlanCard() WbPlanCard {
	text := l.Description
	if text == "" && l.Kind != "" {
		text = l.Kind + "."
	}
	c := WbPlanCard{Label: "BUILDING SITE", Title: l.Name, Text: strings.TrimSpace(text + " Planned: no CEO or departments yet."),
		LinkText: "Open the project", LinkURL: "/p/" + l.Slug + "/status", TestID: "site_" + strings.ReplaceAll(l.Slug, "-", "_")}
	if l.CEO.Live {
		c.LinkText, c.LinkURL = "Talk to the CEO", "/p/"+l.Slug+"/lead"
	} else {
		c.SpawnURL = "/p/" + l.Slug + "/lead/spawn"
	}
	return c
}

// sortMapQueue puts what blocks others first (CI red), then the oldest.
func sortMapQueue(q []WbQueueCard) {
	sort.SliceStable(q, func(i, j int) bool {
		if q[i].Blocking != q[j].Blocking {
			return q[i].Blocking
		}
		return q[i].Since.Before(q[j].Since)
	})
}

// buildLot is one project's lot and its rows of the owner's queue.
func (s *Server) buildLot(p *tracker.Project, color string, now time.Time) (MapLot, []WbQueueCard) {
	prefix := "/p/" + p.Slug
	lot := MapLot{Name: p.Name, Slug: p.Slug, Kind: p.Kind, Color: color, Description: p.Description, CEO: WbCEO{Session: tracker.LeadSessionName(p)}}
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		log.Printf("map: %s: load issues: %v", p.Slug, err)
	}
	var open []*tracker.Issue
	for _, issue := range issues {
		if strings.EqualFold(issue.Status, "done") {
			if at, _ := doneAt(issue); now.Sub(at) <= mapDoneWindow {
				lot.DoneWeek++
			}
			continue
		}
		open = append(open, issue)
	}
	lot.Site = len(issues) == 0
	lot.issues = issues
	lot.YOLO = yoloBand(p, issues, now)
	lot.Buildings = mapDepartmentsOf(p, open)
	lot.Progress = mapProgressOf(goals.Resolve(p, now), issues, color)

	if agents, err := s.buildAgentsModel(p, prefix); err == nil {
		lot.agents = agents
		lot.CEO.Live = agents.ProjectLead.State == "live"
		lot.Robots = mapRobotsOf(agents, issues)
		for _, r := range lot.Robots {
			switch r.State {
			case "working":
				lot.Working++
			case "idle":
				lot.Idle++
			case "agent":
				lot.AgentWait++
			case "you":
				lot.You++
			}
		}
	}
	lot.Crew = wbCrew(lot.Robots)
	lot.CEO.Quote = s.leadQuote(p, lot.CEO.Live, now)

	if st, enabled := ciState(p); enabled && st != nil {
		lot.CI = "green"
		if st.IsRed() {
			lot.CI, lot.CILine = "red", ciRedLine(st)
		}
	}
	lot.waits = waitingOnOwner(p, open)
	queue := needsYouRowsWith(p, color, prefix, lot.waits, now)
	lot.Waiting = len(queue)

	if plan := projectUpgradePlan(p); plan != nil {
		lot.Updates = len(plan.Proposals)
	}
	lot.Privacy = privacyView(p, prefix, now).Badges
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		lot.Chips = append(lot.Chips, *c)
	}
	lot.Chips = append(lot.Chips, wbCountChips(lot.You, lot.Working, lot.AgentWait, lot.Idle)...)
	if n := connections.OpenTotal(connections.Load(p, issues, now)); n > 0 {
		lot.Connections = n
		lot.Chips = append(lot.Chips, WbChip{Tone: "idle", Text: fmt.Sprintf("⇄ %d", n), Title: plural(n, "open item", "open items") + " passing between departments: see Connections on the company page", TestID: "connections"})
	}
	for _, b := range lot.Privacy {
		if b.Level == "amber" || b.Level == "red" {
			lot.Chips = append(lot.Chips, WbChip{Tone: "idle", Text: b.Marker, Title: b.Sentence, TestID: "privacy_" + b.Kind})
		}
	}
	return lot, queue
}

// mapDeptColors are the colours of the departments a project adds (after
// HQ's yellow and Product's blue), when projects.yaml sets none.
var mapDeptColors = []string{"#E2559B", "#17A2B8", "#8BC34A", "#B07A4F", "#6C7A96"}

// deptColor is department i's colour: its own, else the next of the palette.
func deptColor(d tracker.Department, i int) string {
	if d.Color != "" {
		return d.Color
	}
	return mapDeptColors[i%len(mapDeptColors)]
}

// companyGroupColor is a group's colour, clearly apart from HQ's yellow
// (ui/gtms-group-room-has-the-same-colour-as-the-ceos-hq-…). The group's
// room head takes it from the --group token in workshop.css; a test keeps
// the two equal. html/template drops var() in a style attribute, so the
// other places (map building, crew column, shelf, lines) take this hex.
const companyGroupColor = "#2EC4B6"

// deptColorOf is department i's colour on p's pages: a group (a department
// with departments under it, nested ones too) takes companyGroupColor
// whatever projects.yaml sets, every other department deptColor.
func deptColorOf(p *tracker.Project, d tracker.Department, i int) string {
	if p != nil && p.IsGroup(d.Name) {
		return companyGroupColor
	}
	return deptColor(d, i)
}

// mapDepartmentsOf is the project's departments as buildings on its lot:
// a small HQ, Product (the software factory, every issue no other
// department takes) and each department projects.yaml adds, a group as one
// building with its departments inside. Height and width grow with a
// department's open work.
func mapDepartmentsOf(p *tracker.Project, open []*tracker.Issue) []WbBuilding {
	count := map[string]int{}
	for _, issue := range open {
		count[strings.ToLower(p.DepartmentOf(issue))]++
	}
	productMax := max(130-28*len(p.Departments), 60)
	out := []WbBuilding{
		{Name: "HQ · CEO", Color: companyHQColor, Width: 38, Height: 40},
		{Name: "Product", Open: count[""], Color: companyProductColor, Width: min(70+min(count[""], 30)*2, productMax), Height: 24 + min(count[""], 34)},
	}
	building := func(d tracker.Department) WbBuilding {
		n := count[strings.ToLower(d.Name)]
		return WbBuilding{Name: d.Name, Open: n, Color: deptColorOf(p, d, deptIndex(p, d.Name)), Width: 30 + min(n, 10)*2, Height: 20 + min(n, 30)}
	}
	// group is d's building with its departments as rooms inside, a nested
	// group's rooms inside its own (factory/nested-groups-…#D-1).
	var group func(d tracker.Department) WbBuilding
	group = func(d tracker.Department) WbBuilding {
		b := building(d)
		kids := p.Children(d.Name)
		if len(kids) == 0 {
			return b
		}
		b.Width, b.Height = groupPad, 0
		for _, c := range kids {
			r := group(c)
			if len(r.Rooms) == 0 {
				r.Width, r.Height = r.Width*3/4, r.Height*3/4
			}
			b.Rooms = append(b.Rooms, r)
			b.Open += r.Open
			b.Width += r.Width + groupGap
			b.Height = max(b.Height, r.Height+12)
		}
		b.Width = min(b.Width, 120)
		return b
	}
	for _, d := range p.Departments {
		if p.GroupOf(d.Name) != nil {
			continue
		}
		out = append(out, group(d))
	}
	return fitSkyline(out)
}

// The skyline's room on a lot (300px wide, less its border and padding):
// buildings sit skylineGap apart, the free lot (.wb-bld--plan) takes
// skylinePlan, a group adds groupPad around its rooms and groupGap between
// them. A building is never narrower than skylineMin.
const (
	skylineWidth = 260
	skylineGap   = 6
	skylinePlan  = 36
	groupPad     = 10
	groupGap     = 4
	skylineMin   = 6
)

// skylineTotal is how wide the buildings stand, with the free lot.
func skylineTotal(bs []WbBuilding) int {
	w := skylinePlan + skylineGap*len(bs)
	for _, b := range bs {
		w += b.Width
	}
	return w
}

// fitSkyline narrows the buildings, rooms of a group (and of a group
// inside it) included, until they fit the lot: 15 departments stay on a
// 300px lot (and on a phone) instead of running off it. A skyline that
// fits is returned as it was.
func fitSkyline(bs []WbBuilding) []WbBuilding {
	for try := 0; try < 4 && skylineTotal(bs) > skylineWidth; try++ {
		fixed, scalable := skylinePlan+skylineGap*len(bs), 0
		for _, b := range bs {
			f, s := skylineParts(b)
			fixed, scalable = fixed+f, scalable+s
		}
		room := skylineWidth - fixed
		if scalable == 0 || room <= 0 {
			break
		}
		scale := func(w int) int { return max(w*room/scalable, skylineMin) }
		for i := range bs {
			scaleBuilding(&bs[i], scale)
		}
	}
	return bs
}

// skylineParts splits building b's width into what stays (a group's
// padding and gaps, at every level) and what scales (plain buildings and
// rooms).
func skylineParts(b WbBuilding) (fixed, scalable int) {
	if len(b.Rooms) == 0 {
		return 0, b.Width
	}
	fixed = groupPad + groupGap*len(b.Rooms)
	for _, r := range b.Rooms {
		f, s := skylineParts(r)
		fixed, scalable = fixed+f, scalable+s
	}
	return fixed, scalable
}

// scaleBuilding scales b's plain width, or its rooms' and sums them back.
func scaleBuilding(b *WbBuilding, scale func(int) int) {
	if len(b.Rooms) == 0 {
		b.Width = scale(b.Width)
		return
	}
	b.Width = groupPad
	for j := range b.Rooms {
		scaleBuilding(&b.Rooms[j], scale)
		b.Width += b.Rooms[j].Width + groupGap
	}
}

// productIssues is the issues that belong to Product: those no department
// of projects.yaml takes.
func productIssues(p *tracker.Project, issues []*tracker.Issue) []*tracker.Issue {
	if len(p.Departments) == 0 {
		return issues
	}
	var out []*tracker.Issue
	for _, issue := range issues {
		if p.DepartmentOf(issue) == "" {
			out = append(out, issue)
		}
	}
	return out
}

// mapProgressOf is the lot's bar: the first goal of HQ, else of Product or a
// department (projects.yaml), else the open epic furthest along (most done
// children).
func mapProgressOf(rep *goals.Report, issues []*tracker.Issue, color string) *WbProgress {
	for _, o := range rep.Owners() {
		if len(o.Goals) == 0 {
			continue
		}
		g := o.Goals[0]
		chip := goalChip(g, color)
		p := chip.Progress
		p.Label, p.Text, p.Title = "Goal: "+g.Label(), chip.Text, chip.Title
		if o.Key != goals.HQKey {
			p.Label = "Goal: " + o.Name + " · " + g.Label()
		}
		if g.Value == nil && !g.RolledUp {
			p.Text = "no value yet"
		}
		return &p
	}
	var best *WbProgress
	for _, issue := range issues {
		if strings.EqualFold(issue.Status, "done") || !tracker.IsEpic(issue, issues) {
			continue
		}
		p := WbProgress{Label: "Epic: " + issue.Title, Title: "No goal in projects.yaml (company: goals:): the open epic furthest along"}
		for _, c := range tracker.EpicChildren(issues, issue.Slug) {
			p.Total++
			if strings.EqualFold(c.Status, "done") {
				p.Done++
			}
		}
		if p.Total == 0 {
			continue
		}
		if best == nil || p.Done*best.Total > best.Done*p.Total || (p.Done*best.Total == best.Done*p.Total && p.Total > best.Total) {
			b := p
			best = &b
		}
	}
	return best
}

// mapNewProjectTile is the "+" tile: the workflow templates issue-cli
// bundles, read from its embedded files so the list cannot drift.
func mapNewProjectTile() WbNewTile {
	var names []string
	entries, _ := templates.FS.ReadDir("workflow")
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	return WbNewTile{
		Title: "New project", Sub: "Templates: " + strings.Join(names, " · "), TestID: "new_project",
		Note: "Run issue-cli workflow init --template <name> in the project's folder, then add it to projects.yaml (kind:, color:, description:).",
	}
}

// mapRobotsOf is the project's live agents as robots. A worker whose issue is
// blocked on (blocked_on) or depends on (depends_on) another issue that has a
// live worker waits on that agent.
func mapRobotsOf(agents *AgentsModel, issues []*tracker.Issue) []WbRobot {
	working := map[string]bool{}
	for _, w := range agents.Workers {
		if w.Issue != nil {
			working[strings.ToLower(w.Issue.Slug)] = true
		}
	}
	var out []WbRobot
	for _, w := range agents.Workers {
		r := WbRobot{State: "working", Label: w.Name}
		if w.Issue != nil {
			r.Label = w.Issue.Slug
		}
		switch w.State {
		case "at-gate", "question":
			r.State = "you"
		case "idle":
			r.State = "idle"
		}
		if r.State != "you" && w.Issue != nil {
			if issue := tracker.FindIssue(issues, w.Issue.Slug); issue != nil {
				for _, dep := range append(tracker.BlockedOn(issue), tracker.DependsOn(issue)...) {
					if working[strings.ToLower(dep)] {
						r.State = "agent"
						r.Label += " → " + dep
						break
					}
				}
			}
		}
		out = append(out, r)
	}
	for _, o := range agents.Others {
		out = append(out, WbRobot{State: "working", Label: o.Name})
	}
	return out
}

// leadQuote is the CEO's latest line: its newest paragraph in the pane when
// it is live, else the first line of its handoff note's ## Next.
func (s *Server) leadQuote(p *tracker.Project, live bool, now time.Time) string {
	if live {
		if pane, err := agentrun.CaptureHistory(agentrun.Target(p, tracker.LeadSessionName(p)), mapLeadScrollback); err == nil {
			if q := lastLeadParagraph(cleanPane(pane)); q != "" {
				return q
			}
		}
	}
	next := leadNextSteps(projectRoot(p), tracker.LeadSessionName(p), now)
	if next.Found {
		for _, line := range strings.Split(next.Text, "\n") {
			if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*0123456789. ")); line != "" {
				return clipRunes(line, mapQuoteMax)
			}
		}
	}
	return ""
}

// markdownMarks are inline markdown marks dropped from a quote.
var markdownMarks = regexp.MustCompile("\\*\\*|`")

// toolCall is a pane paragraph that is a tool call ("⏺ Bash(…)"), not
// something the lead said.
var toolCall = regexp.MustCompile(`^[A-Z][A-Za-z]*\(`)

// lastLeadParagraph is the lead's newest message paragraph ("⏺ …") in its
// pane, clipped; "" when there is none.
func lastLeadParagraph(pane string) string {
	paras := paneParagraphs(pane)
	for i := len(paras) - 1; i >= 0; i-- {
		para := paras[i]
		if !strings.HasPrefix(para, "⏺") {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(para, "⏺"))
		if text == "" || toolCall.MatchString(text) {
			continue
		}
		return clipRunes(markdownMarks.ReplaceAllString(text, ""), mapQuoteMax)
	}
	return ""
}

// clipRunes cuts s to n characters, ending in "…" when it was longer.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// engineCandidate is the engine's release candidate and the projects that
// ran it; nil without one.
func (s *Server) engineCandidate(p *tracker.Project) *MapRC {
	m, err := s.loadStatus(p, "/p/"+p.Slug)
	if err != nil {
		return nil
	}
	c := m.Candidate()
	if c == nil {
		return nil
	}
	rc := &MapRC{Tag: c.Tag, Engine: p.Slug}
	if c.Soak != nil {
		for _, sp := range c.Soak.Projects {
			if sp.Calls > 0 {
				rc.Testing = append(rc.Testing, sp.Slug)
			}
		}
	}
	return rc
}

// deptIndex is department name's position in projects.yaml (for deptColor),
// 0 when it is not there.
func deptIndex(p *tracker.Project, name string) int {
	for i, d := range p.Departments {
		if d.Name == name {
			return i
		}
	}
	return 0
}
