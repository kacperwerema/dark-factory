package main

import (
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
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
	// RC is the candidate tag when this project is testing it.
	RC      string
	Updates int
	Privacy []PrivacyBadge
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

	data := MapData{Lots: lots, RC: rc, NewProject: WbNewTile{
		Title: "New project", Sub: "Templates: app · game · service", TestID: "new_project",
		Note: "Run issue-cli init in the project's folder, then add it to projects.yaml (kind:, color:, description:).",
	}}
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
		data.Queue = append(data.Queue, queues[i]...)
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
	data.Header = WbHeader{Crumbs: wbCrumbs(0, "", ""), Working: data.Working, Idle: data.Idle, AgentWait: data.AgentWait, DoneWeek: data.DoneWeek, Waiting: len(data.Queue)}
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
	lot.Buildings = mapDepartmentsOf(p, open)
	lot.Progress = mapProgressOf(issues)

	if agents, err := s.buildAgentsModel(p, prefix); err == nil {
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

	var queue []WbQueueCard
	if st, enabled := ciState(p); enabled && st != nil {
		lot.CI = "green"
		if st.IsRed() {
			lot.CI = "red"
			lot.CILine = "make validate"
			if st.Failed != "" {
				lot.CILine = st.Failed
			}
			slug := st.RedIssue
			if slug == "" {
				slug = st.Branch
			}
			since := st.Updated
			queue = append(queue, WbQueueCard{
				Kind: "ci", Label: "CI red", Tone: "red", Blocking: true,
				Project: p.Name, ProjectSlug: p.Slug, Color: color,
				Slug: slug, Note: lot.CILine + " fails on " + st.Branch + ".",
				Since: since, Age: shortDuration(now.Sub(since)),
				CTA: "View status", URL: prefix + "/status",
			})
		}
	}
	for _, it := range waitingOnOwner(p, open) {
		queue = append(queue, mapQueueRow(p, color, prefix, it, now))
	}
	lot.Waiting = len(queue)

	if plan := projectUpgradePlan(p); plan != nil {
		lot.Updates = len(plan.Proposals)
	}
	lot.Privacy = privacyView(p, prefix, now).Badges
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		lot.Chips = append(lot.Chips, *c)
	}
	lot.Chips = append(lot.Chips, wbCountChips(lot.You, lot.Working, lot.AgentWait, lot.Idle)...)
	for _, b := range lot.Privacy {
		if b.Level == "amber" || b.Level == "red" {
			lot.Chips = append(lot.Chips, WbChip{Tone: "idle", Text: b.Marker, Title: b.Sentence, TestID: "privacy_" + b.Kind})
		}
	}
	return lot, queue
}

// mapQueueRow words one waiting item for the owner, with its action.
func mapQueueRow(p *tracker.Project, color, prefix string, it WaitingItem, now time.Time) WbQueueCard {
	row := WbQueueCard{
		Kind: it.Kind, Tone: "you", Project: p.Name, ProjectSlug: p.Slug, Color: color,
		Slug: it.Issue.Slug, Since: it.Since, Age: shortDuration(now.Sub(it.Since)),
		URL: prefix + "/issue/" + it.Issue.Slug,
	}
	if it.Approx {
		row.Age = "≈ " + row.Age
	}
	switch it.Kind {
	case "gate":
		row.Label, row.CTA = "Gate: "+it.Target, "Review"
		row.Note = it.Issue.Title + " · " + it.Issue.Status + " → " + it.Target
		row.URL += "#approve-" + strings.ReplaceAll(strings.TrimSpace(it.Target), " ", "-")
	case "playtest":
		row.Label, row.CTA = "Playtest", "Play"
		row.Note = it.Issue.Title + " · " + strings.Join(it.Sessions, ", ")
	case "checks":
		row.Label, row.CTA = "Human testing", "Check"
		row.Note = it.Issue.Title + " · " + strings.Join(it.Sessions, ", ")
	case "question":
		row.Label, row.CTA = "Question", "Answer"
		row.Note = it.Issue.Title
	default:
		row.Label, row.CTA, row.Note = it.Kind, "Open", it.Issue.Title
	}
	return row
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

// mapDepartmentsOf is the project's departments as buildings on its lot:
// a small HQ, Product (the software factory, every issue no other
// department takes) and each department projects.yaml adds. Height and
// width grow with a department's open work.
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
	for i, d := range p.Departments {
		n := count[strings.ToLower(d.Name)]
		out = append(out, WbBuilding{Name: d.Name, Open: n, Color: deptColor(d, i), Width: 30 + min(n, 10)*2, Height: 20 + min(n, 30)})
	}
	return out
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

// mapProgressOf is the open epic furthest along (most done children), the
// lot's bar until projects have goals.
func mapProgressOf(issues []*tracker.Issue) *WbProgress {
	var best *WbProgress
	for _, issue := range issues {
		if strings.EqualFold(issue.Status, "done") || !tracker.IsEpic(issue, issues) {
			continue
		}
		p := WbProgress{Label: "Epic: " + issue.Title}
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

// mapRobotsOf is the project's live agents as robots. A worker whose issue is
// blocked on another issue that has a live worker waits on that agent.
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
				for _, dep := range blockedOn(issue) {
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
