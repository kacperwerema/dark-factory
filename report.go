package main

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/privacy"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The Report (L1, /p/<project>/status) is the project's status in the
// Workshop Board look: what waits on the owner, the agents, what is next,
// the bottleneck in one sentence, where the project is, what shipped, code
// health and what to enable for a richer report. It is built from the
// Status model (status.go), so it, status.md and status.json say the same
// thing. Design: issues/Factory/designs/workshop-board/L1-status-report.
// The classic page stays at ?classic=1.

// reportShippedRows is how many systems the Shipped card lists.
const reportShippedRows = 6

// reportNextSteps is how many of the CEO's next steps the card quotes.
const reportNextSteps = 3

// reportVersions is how many upcoming versions Where we are lists.
const reportVersions = 4

// reportEpics is how many open epics Where we are lists.
const reportEpics = 4

// Swatch colours of the Waiting and Agents cards (the design's): orange for
// gates, light orange for the owner's testing, ink for questions.
const (
	reportGateColor     = "#F57A1F"
	reportTestColor     = "#FFC08F"
	reportQuestionColor = "#1B1F2A"
	reportRedColor      = "#D7263D"
	reportWorkingColor  = "#2BB673"
	reportAgentColor    = "#7C4DFF"
	reportIdleColor     = "#DCE1E9"
	reportBugColor      = "#9AA3B2"
)

// ReportData is the whole Report page.
type ReportData struct {
	Header     WbHeader
	Title      WbReportTitle
	Waiting    WbCountCard
	Agents     WbCountCard
	Next       WbCountCard
	Bottleneck WbBottleneck
	Where      WbWhere
	Shipped    WbShipped
	Health     WbHealth
	// Bench is the Benchmark card; nil without a benchmark for the project.
	Bench  *WbBench
	Unlock WbUnlock
	// Active is Working on: each issue in flight and its agent.
	Active WbActiveList
	// Released is Recently released.
	Released WbReleased
	// Status is the model the classic Status page renders; wb/release and
	// wb/ci read its ReleaseWait and CI (the release flow).
	Status *StatusModel
}

// WbViewTab is one side of a segmented toggle (Board | Report).
type WbViewTab struct {
	Label, URL, TestID string
	Current            bool
}

// wbViewToggle is the project's Board | Report toggle with current
// ("board" or "report") on.
func wbViewToggle(prefix, current string) []WbViewTab {
	return []WbViewTab{
		{Label: "Board", URL: prefix + "/company", TestID: "board", Current: current == "board"},
		{Label: "Report", URL: prefix + "/status", TestID: "report", Current: current == "report"},
	}
}

// WbBadge is a small pill with an icon, linking to where it is explained.
// Tone is red, green or idle.
type WbBadge struct {
	Tone, Label, Title, URL string
}

// WbReportTitle is the Report's title band: the title, badges, the line
// under it and the page's buttons.
type WbReportTitle struct {
	Title   string
	Badges  []WbBadge
	Sub     string
	Session string
	Live    bool
	AsOf    string
	MDURL   string
	Classic string
	// Views is Project page | Report when workbench.status_page is set.
	Views []WbViewTab
}

// WbActiveList is Working on: every issue between in progress and shipping
// with its status, age and agent, then the agents on issues not started.
type WbActiveList struct {
	Rows      []WbActiveRow
	More      []WbActiveRow
	Design    []WbActiveChip
	AgentsURL string
}

// Total is how many issues are in flight.
func (l WbActiveList) Total() int { return len(l.Rows) + len(l.More) }

// WbActiveChip is an agent on an issue not in flight yet, linking to it.
type WbActiveChip struct {
	Text, Tone, URL string
}

// WbActiveRow is one issue in flight. Tone colours the age: idle, or red
// when it has sat in its status a week or more.
type WbActiveRow struct {
	Title, Slug, URL, Status string
	Age, AgeTitle, Tone      string
	Agent                    string
}

// WbReleased is Recently released: the newest releases from the changelog
// or tags, or what the first one will be.
type WbReleased struct {
	Rows   []WbReleaseRow
	Source string
	URL    string
	// First is "No releases yet: v0.1.0 will be the first." without any.
	First string
	// Hint asks for workbench.changelog when there is neither changelog
	// nor tag.
	Hint bool
}

// WbReleaseRow is one release.
type WbReleaseRow struct {
	Version, Date string
	Summary       template.HTML
}

// WbSegment is one part of a stacked bar.
type WbSegment struct {
	Value int
	Color string
	Label string
}

// WbLegendRow is a line under a count: a swatch (Color) or a dot (Dot: a
// state colour), a bold count and words. Count < 0 hides the number.
type WbLegendRow struct {
	Color, Dot   string
	Count        int
	Label, Note  string
	URL, Command string
	Title        string
}

// WbSquare is one agent as a coloured square.
type WbSquare struct {
	Color, Title string
}

// WbAction is a button or link: URL opens a page, Spawn starts the CEO
// (POST), Copy copies a command. Tone is you, ink, paper, ghost or link.
type WbAction struct {
	Label, Tone, TestID string
	URL, Spawn, Copy    string
	Count               int
	Title               string
}

// WbCountCard is a card with a big number: Waiting on you, Agents in the
// project, What's next. Icon is you, agents or plan.
type WbCountCard struct {
	ID, Icon, Title, TestID string
	Count                   int
	Bar                     []WbSegment
	Squares                 []WbSquare
	Legend                  []WbLegendRow
	Steps                   []string
	// Full is the whole text behind Steps (the CEO's ## Next), in a fold.
	Full     template.HTML
	Note     string
	NoteLink *WbAction
	Action   *WbAction
}

// BarTotal is the sum of the bar's segments.
func (c WbCountCard) BarTotal() int {
	n := 0
	for _, s := range c.Bar {
		n += s.Value
	}
	return n
}

// WbBottleneck is the dark banner: the one thing that holds the project up.
type WbBottleneck struct {
	Eyebrow  string
	Sentence string
	Text     string
	Actions  []WbAction
	// Calm: nothing holds the project up (no orange shadow).
	Calm bool
}

// WbBar is a labelled done/total bar; Current marks the current milestone.
type WbBar struct {
	Label, Note, URL string
	Done, Total      int
	Current          bool
}

// Pct is the bar's fill, 0–100.
func (b WbBar) Pct() int {
	if b.Total == 0 {
		return 0
	}
	return b.Done * 100 / b.Total
}

// WbWhere is Where we are: every issue, then milestones (or why there are
// none) and open epics.
type WbWhere struct {
	All        WbBar
	Milestones []WbBar
	MoreVers   int
	// MoreMilestones are the versions past reportVersions, in a fold.
	MoreMilestones []WbBar
	// VersionsTotal is done/total over every upcoming version.
	VersionsTotal string
	// Segments, Legend and Areas break the current milestone down by
	// status and by area.
	Segments []WbSegment
	Legend   []WbLegendRow
	Areas    []WbChip
	// Locked is the note shown instead of milestones; "" when there are some.
	Locked    string
	LockedWhy string
	Epics     []WbBar
	MoreEpics int
	HallURL   string
}

// WbShipped is Shipped in 7 days, by system.
type WbShipped struct {
	Count   int
	Rows    []WbShippedRow
	More    int
	ListURL string
}

// WbShippedRow is one system's shipped work: its newest issue and how many
// more.
type WbShippedRow struct {
	Area, Slug, URL, Date string
	More                  int
	MoreURL               string
}

// WbHealth is Code health: the two scores, the weakest spot and lint.
type WbHealth struct {
	URL      string
	Snapshot bool
	Derived  WbScoreBox
	Opinion  WbScoreBox
	Weakest  *HealthWeakest
	Lint     string
	LintOK   bool
	LintCmd  string
	// Recs counts the review's recommendations waiting for the owner.
	Recs int
	// Departments is the departments family's line, "" without one.
	Departments string
}

// WbScoreBox is a score in a small box: dashed when there is none.
type WbScoreBox struct {
	Label, Value, Line string
	Has                bool
}

// WbUnlock is To unlock: what to enable so the Report shows more.
type WbUnlock struct {
	Done, Total int
	Items       []WbUnlockItem
}

// WbUnlockItem is one thing to enable: why, and the action. Risk marks a
// gap that can lose work; On marks one already enabled.
type WbUnlockItem struct {
	Key, Title, Text, Code string
	Risk, On               bool
	Action                 WbAction
	// Note is quieter advice under the text (going public from a private
	// repo).
	Note string
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	m, issues, agents, err := s.loadStatusWith(proj, prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := buildReport(proj, s.projectColor(proj), m, issues, agents)
	if proj.LoadWorkflow().Workbench.StatusPage != "" {
		data.Title.Views = []WbViewTab{
			{Label: "Project page", URL: prefix + "/status", TestID: "project"},
			{Label: "Report", URL: prefix + "/status?view=builtin", TestID: "builtin", Current: true},
		}
		data.Title.Classic = prefix + "/status?view=builtin&classic=1"
	}
	if proj.SupportsGitHub && proj.Repo != "" {
		data.Released.URL = "https://github.com/" + proj.Repo + "/releases"
	}
	data.Header.YOLO = headerYOLO(yoloBand(proj, issues, m.AsOf), m.Prefix)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "report.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// projectColor is the project's colour: its own, else the map's by its
// place in projects.yaml.
func (s *Server) projectColor(p *tracker.Project) string {
	if p.Color != "" {
		return p.Color
	}
	for i := range s.projects {
		if s.projects[i].Slug == p.Slug {
			return mapProjectColors[i%len(mapProjectColors)]
		}
	}
	return mapProjectColors[0]
}

// buildReport turns the Status model into the Report. agents may be nil
// (no tmux): the Agents card then shows none.
func buildReport(p *tracker.Project, color string, m *StatusModel, issues []*tracker.Issue, agents *AgentsModel) ReportData {
	prefix := m.Prefix
	live := m.LeadState == "live"
	session := m.LeadName
	if session == "" {
		session = tracker.LeadSessionName(p)
	}
	d := ReportData{}
	d.Header = WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(1, p.Name, prefix+"/company")[:2], Toggle: wbViewToggle(prefix, "report"), Quiet: true, Waiting: m.WaitingCount()}
	d.Header.Crumbs[1].Color = color

	d.Title = WbReportTitle{
		Title: "Report · " + p.Name, Session: session, Live: live,
		AsOf: m.AsOf.Format("Mon 2 Jan, 15:04"), MDURL: prefix + "/status.md", Classic: prefix + "/status?classic=1",
		Sub: fmt.Sprintf("%d/%d done", m.All.Done, m.All.Total),
	}
	if p.Kind != "" {
		d.Title.Sub = p.Kind + " · " + d.Title.Sub
	}
	for _, b := range m.Privacy.badges() {
		tone := "idle"
		switch b.Level {
		case privacy.Amber, privacy.Red:
			tone = "red"
		case privacy.Green:
			tone = "green"
		}
		d.Title.Badges = append(d.Title.Badges, WbBadge{Tone: tone, Label: b.Label, Title: b.Sentence, URL: "#unlock-" + map[string]string{"work": "tracker", "code": "code"}[b.Kind]})
	}

	robots := reportRobots(agents, issues)
	d.Waiting = reportWaiting(m)
	d.Agents = reportAgents(robots, agents, live)
	d.Next = reportNext(m, prefix, live)
	if m.Next.Found {
		d.Next.Full = template.HTML(linkCodeSlugs(renderMarkdown(m.Next.Text), prefix, issues))
	}
	d.Active = reportActive(m, prefix)
	d.Released = reportReleased(m)
	d.Bottleneck = reportBottleneck(m, robots, prefix, live)
	d.Where = reportWhere(m, issues, prefix)
	d.Shipped = reportShipped(m, prefix)
	d.Health = reportHealth(m, p, prefix)
	d.Bench = reportBench(p, m.AsOf)
	d.Unlock = reportUnlock(m, p, issues, prefix)
	d.Unlock = unlockBench(d.Unlock, d.Bench != nil)
	d.Status = m
	return d
}

// reportRobot is one agent and what it is doing: gate, question, working,
// agent (waits on another agent's issue) or idle.
type reportRobot struct {
	State, Label string
}

// reportRobots are the project's live agents: its workers and the agent
// sessions not bound to an issue (counted as working). The CEO is not one.
func reportRobots(agents *AgentsModel, issues []*tracker.Issue) []reportRobot {
	if agents == nil {
		return nil
	}
	working := map[string]bool{}
	for _, w := range agents.Workers {
		if w.Issue != nil {
			working[strings.ToLower(w.Issue.Slug)] = true
		}
	}
	var out []reportRobot
	for _, w := range agents.Workers {
		r := reportRobot{State: "working", Label: w.Name}
		if w.Issue != nil {
			r.Label = w.Issue.Slug
		}
		switch w.State {
		case "at-gate":
			r.State = "gate"
		case "question":
			r.State = "question"
		case "idle":
			r.State = "idle"
		}
		if r.State == "working" && w.Issue != nil {
			if issue := tracker.FindIssue(issues, w.Issue.Slug); issue != nil {
				for _, dep := range tracker.BlockedOn(issue) {
					if working[strings.ToLower(dep)] {
						r.State, r.Label = "agent", r.Label+" → "+dep
						break
					}
				}
			}
		}
		out = append(out, r)
	}
	for _, o := range agents.Others {
		out = append(out, reportRobot{State: "working", Label: o.Name})
	}
	return out
}

// reportRobotOrder and reportRobotColor draw the agents' squares: what
// waits on the owner first.
var (
	reportRobotOrder = map[string]int{"gate": 0, "question": 1, "working": 2, "agent": 3, "idle": 4}
	reportRobotColor = map[string]string{"gate": reportGateColor, "question": reportQuestionColor, "working": reportWorkingColor, "agent": reportAgentColor, "idle": reportIdleColor}
	reportRobotWords = map[string]string{"gate": "at a gate", "question": "asked a question", "working": "working", "agent": "waiting on an agent", "idle": "idle"}
)

// reportWaiting is the Waiting on you card: the Status page's waiting
// items by kind, plus CI red and unsent bug reports.
func reportWaiting(m *StatusModel) WbCountCard {
	c := WbCountCard{ID: "waiting", Icon: "you", Title: "Waiting on you", TestID: "waiting", Count: m.WaitingCount()}
	gates := len(m.WaitingOf("gate"))
	tests := len(m.WaitingOf("playtest")) + len(m.WaitingOf("checks"))
	questions := len(m.WaitingOf("question"))
	add := func(n int, color, label, note string) {
		if n == 0 {
			return
		}
		c.Bar = append(c.Bar, WbSegment{Value: n, Color: color, Label: label})
		c.Legend = append(c.Legend, WbLegendRow{Color: color, Count: n, Label: label, Note: note})
	}
	add(gates, reportGateColor, "to approve", "gates")
	add(tests, reportTestColor, "to check", "human testing")
	add(questions, reportQuestionColor, plural0(questions, "question from an agent", "questions from agents"), "")
	if m.CI.Red() {
		c.Bar = append(c.Bar, WbSegment{Value: 1, Color: reportRedColor, Label: "CI red"})
		c.Legend = append(c.Legend, WbLegendRow{Color: reportRedColor, Count: -1, Label: "Local CI red on " + m.CI.Branch, Command: "issue-cli ci status"})
	}
	if m.UnsentBugReports > 0 {
		c.Bar = append(c.Bar, WbSegment{Value: 1, Color: reportBugColor, Label: "bug reports"})
		c.Legend = append(c.Legend, WbLegendRow{Color: reportBugColor, Count: m.UnsentBugReports, Label: plural0(m.UnsentBugReports, "issue-cli bug report not sent", "issue-cli bug reports not sent"), Command: "issue-cli report-bug --send"})
	}
	for _, sw := range m.Shelf {
		c.Bar = append(c.Bar, WbSegment{Value: sw.Count, Color: sw.Color, Label: sw.Name + " outputs"})
		c.Legend = append(c.Legend, WbLegendRow{Color: sw.Color, Count: sw.Count, Label: plural0(sw.Count, sw.Name+" output to review", sw.Name+" outputs to review"), Note: "the shelf", URL: sw.URL})
	}
	if c.Count > 0 {
		c.Action = &WbAction{Label: "See all and approve", Tone: "you", TestID: "needs_you", URL: m.Prefix + "/needs-you"}
	}
	if c.Count == 0 {
		c.Note = "Nothing waits on you: no gate, playtest, check script or open question."
	} else if m.NotReady > 0 {
		c.Note = plural(m.NotReady, "more issue is", "more issues are") + " at a gate with its checklist still open."
		c.NoteLink = &WbAction{Label: "See the hall", Tone: "link", TestID: "not_ready", URL: m.Prefix + "/hall"}
	}
	return c
}

// plural0 is the word alone, singular for one.
func plural0(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// reportAgents is the Agents in the project card: one square per agent.
func reportAgents(robots []reportRobot, agents *AgentsModel, live bool) WbCountCard {
	c := WbCountCard{ID: "agents", Icon: "agents", Title: "Agents in the project", TestID: "agents", Count: len(robots)}
	sorted := append([]reportRobot(nil), robots...)
	sort.SliceStable(sorted, func(i, j int) bool { return reportRobotOrder[sorted[i].State] < reportRobotOrder[sorted[j].State] })
	count := map[string]int{}
	for _, r := range sorted {
		count[r.State]++
		c.Squares = append(c.Squares, WbSquare{Color: reportRobotColor[r.State], Title: r.Label + " · " + reportRobotWords[r.State]})
	}
	if n := count["gate"]; n > 0 {
		c.Legend = append(c.Legend, WbLegendRow{Count: n, Label: "at a gate", Note: "waiting on you"})
	}
	for _, st := range []string{"question", "working", "agent", "idle"} {
		if n := count[st]; n > 0 {
			c.Legend = append(c.Legend, WbLegendRow{Count: n, Label: reportRobotWords[st]})
		}
	}
	if agents != nil && len(agents.Unfinished) > 0 {
		c.Legend = append(c.Legend, WbLegendRow{Count: len(agents.Unfinished), Label: "stopped", Note: "work started, no live agent"})
	}
	if agents == nil {
		c.Note = "tmux could not be read, so no agent is shown."
	} else if len(robots) == 0 {
		c.Note = "No agent is running in this project."
	}
	ceo := WbLegendRow{Dot: "idle", Count: -1, Label: "CEO not running"}
	if live {
		ceo = WbLegendRow{Dot: "working", Count: -1, Label: "CEO live"}
	}
	c.Legend = append(c.Legend, ceo)
	return c
}

// reportNext is the What's next card: the CEO's ## Next, the backlog and
// the parked issues.
func reportNext(m *StatusModel, prefix string, live bool) WbCountCard {
	c := WbCountCard{ID: "next", Icon: "plan", Title: "What's next", TestID: "next"}
	if m.Next.Found {
		c.Steps = nextSteps(m.Next.Text)
		c.Count = len(c.Steps)
		if len(c.Steps) > reportNextSteps {
			c.Steps = c.Steps[:reportNextSteps]
		}
		c.Legend = append(c.Legend, WbLegendRow{Count: -1, Label: "From the CEO's handoff note", Note: m.Next.Age.Text + " ago", Title: m.Next.File})
	} else if m.Next.File != "" {
		c.Legend = append(c.Legend, WbLegendRow{Count: -1, Label: "The CEO's ## Next is empty", Note: m.Next.File})
	} else {
		c.Legend = append(c.Legend, WbLegendRow{Count: -1, Label: "No handoff note from the CEO yet"})
	}
	c.Legend = append(c.Legend, WbLegendRow{Count: len(m.Backlog), Label: "in backlog", URL: prefix + "/?status=backlog", Title: "The backlog in the issue list"})
	c.Legend = append(c.Legend, WbLegendRow{Count: len(m.Parked), Label: "parked", URL: prefix + "/hall/station/parked", Title: "Parked issues and why"})
	if c.Count == 0 && len(m.Backlog) == 0 {
		c.Note = "The queue is empty: when you clear the gates, the CEO has nothing to hand out."
	}
	c.Action = &WbAction{Label: "Plan with the CEO", Tone: "paper", TestID: "next_plan"}
	if live {
		c.Action.URL = prefix + "/lead"
	} else {
		c.Action.Spawn, c.Action.Label = prefix+"/lead/spawn", "Start the CEO to plan"
	}
	return c
}

// nextSteps splits a ## Next section into its steps: list items, else
// paragraphs. Markdown marks are dropped.
func nextSteps(text string) []string {
	var items, paras []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			paras = append(paras, strings.Join(para, " "))
			para = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			flush()
			continue
		}
		if item, ok := listItem(t); ok && line == strings.TrimLeft(line, " \t") {
			items = append(items, item)
			continue
		}
		para = append(para, t)
	}
	flush()
	out := items
	if len(out) == 0 {
		out = paras
	}
	for i, s := range out {
		out[i] = clip(strings.ReplaceAll(markdownMarks.ReplaceAllString(s, ""), "`", ""), 110)
	}
	return out
}

// listItem is a top-level "- x", "* x" or "1. x" line's text.
func listItem(t string) (string, bool) {
	for _, p := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(t, p) {
			return strings.TrimSpace(t[len(p):]), true
		}
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(t) && (t[i] == '.' || t[i] == ')') && t[i+1] == ' ' {
		return strings.TrimSpace(t[i+2:]), true
	}
	return "", false
}

// reportBottleneck says in one sentence what holds the project up, with
// the actions that clear it.
func reportBottleneck(m *StatusModel, robots []reportRobot, prefix string, live bool) WbBottleneck {
	b := WbBottleneck{Eyebrow: "BOTTLENECK"}
	gates, tests, questions := m.WaitingOf("gate"), append(m.WaitingOf("playtest"), m.WaitingOf("checks")...), m.WaitingOf("question")
	sort.SliceStable(gates, func(i, j int) bool { return gates[i].Since.Before(gates[j].Since) })
	sort.SliceStable(tests, func(i, j int) bool { return tests[i].Since.Before(tests[j].Since) })
	sort.SliceStable(questions, func(i, j int) bool { return questions[i].Since.Before(questions[j].Since) })
	if len(gates) > 0 {
		it := gates[0]
		b.Actions = append(b.Actions, WbAction{Label: "Approve gates", Count: len(gates), Tone: "you", TestID: "gates",
			URL: prefix + "/issue/" + it.Issue.Slug + "#approve-" + strings.ReplaceAll(strings.TrimSpace(it.Target), " ", "-"), Title: "Opens the oldest gate: " + it.Issue.Slug})
		if len(gates) > 1 {
			a := &b.Actions[len(b.Actions)-1]
			a.URL, a.Title = prefix+"/needs-you", "Lists every gate, to approve them together"
		}
	}
	if len(tests) > 0 {
		it := tests[0]
		b.Actions = append(b.Actions, WbAction{Label: "Human testing", Count: len(tests), Tone: "ghost", TestID: "testing",
			URL: prefix + "/issue/" + it.Issue.Slug, Title: "Opens the oldest one waiting: " + it.Issue.Slug})
	}
	if len(questions) > 0 {
		it := questions[0]
		b.Actions = append(b.Actions, WbAction{Label: plural0(len(questions), "Answer the question", "Answer the questions"), Count: len(questions), Tone: "link", TestID: "question",
			URL: prefix + "/issue/" + it.Issue.Slug, Title: "Opens the oldest question: " + it.Issue.Slug})
	}

	waitYou, working := 0, 0
	for _, r := range robots {
		switch r.State {
		case "gate", "question":
			waitYou++
		case "working":
			working++
		}
	}
	n := len(robots)
	switch {
	case m.CI.Red():
		b.Sentence = "Local CI is red on " + m.CI.Branch + ". Nothing is safe to ship until it is green again."
		b.Text = "Failing: " + m.CI.Failed + ". Agents that land on a red main only make it harder to find the cause."
		if m.CI.Failed == "" {
			b.Text = "Agents that land on a red main only make it harder to find the cause."
		}
		b.Actions = append([]WbAction{{Label: "issue-cli ci status", Tone: "you", Copy: "issue-cli ci status", TestID: "ci", Title: "Copy the command"}}, b.Actions...)
	case n > 0 && waitYou*2 >= n:
		b.Sentence = fmt.Sprintf("%d of %s wait on you.", waitYou, plural(n, "agent", "agents"))
		if len(questions) > len(gates) {
			b.Sentence += " The factory is stuck on questions, not on code."
		} else {
			b.Sentence += " The factory is stuck on gates, not on code."
		}
		b.Text = "Every gate is one decision. Start with the oldest: its page shows the evidence next to the approval."
	case n == 0 && m.WaitingCount() > 0:
		b.Sentence = fmt.Sprintf("No agent is running and %s.", plural(m.WaitingCount(), "thing waits on you", "things wait on you"))
		b.Text = "The line starts again with your decisions."
	case m.WaitingCount() > 0:
		b.Sentence = fmt.Sprintf("%s working, %s on you.", plural(working, "agent", "agents"), plural(m.WaitingCount(), "thing waits", "things wait"))
		b.Text = "The agents are not held up yet; clearing your queue keeps it that way."
	case n == 0 && len(m.Backlog) == 0 && !m.Next.Found:
		b.Sentence = "Nothing waits on you and the queue is empty. The factory needs a plan."
		b.Text = "Ask the CEO what comes next; it writes the plan to its handoff note."
		b.Calm = true
		a := WbAction{Label: "Plan with the CEO", Tone: "you", TestID: "plan", URL: prefix + "/lead"}
		if !live {
			a.URL, a.Spawn, a.Label = "", prefix+"/lead/spawn", "Start the CEO"
		}
		b.Actions = append(b.Actions, a)
	default:
		b.Eyebrow = "NO BOTTLENECK"
		b.Sentence = "Nothing waits on you."
		if working > 0 {
			b.Sentence += fmt.Sprintf(" %s working.", plural(working, "agent is", "agents are"))
		}
		b.Text = "The factory runs by itself; this report shows when that changes."
		b.Calm = true
	}
	return b
}

// reportWhere is Where we are: every issue, the upcoming versions (or why
// there are none) and the open epics furthest along.
func reportWhere(m *StatusModel, issues []*tracker.Issue, prefix string) WbWhere {
	w := WbWhere{All: WbBar{Label: "All issues", Done: m.All.Done, Total: m.All.Total}, HallURL: prefix + "/hall"}
	versioned := false
	for _, issue := range issues {
		if strings.TrimSpace(issue.Version) != "" {
			versioned = true
			break
		}
	}
	for i, v := range m.Versions {
		bar := WbBar{Label: "Version " + v.Label, Done: v.Done, Total: v.Total, Current: m.IsCurrent(v.Label)}
		if bar.Current {
			bar.Note = "current milestone"
			if ms := m.Milestone; ms != nil && ms.Cut != "" {
				bar.Note += " · becomes " + ms.Cut
			}
		}
		if i >= reportVersions {
			w.MoreMilestones = append(w.MoreMilestones, bar)
			continue
		}
		w.Milestones = append(w.Milestones, bar)
	}
	w.MoreVers = len(w.MoreMilestones)
	if len(m.Versions) > 1 {
		w.VersionsTotal = fmt.Sprintf("%d/%d done over %s", m.VersionsProgress.Done, m.VersionsProgress.Total, plural(len(m.Versions), "version", "versions"))
	}
	if ms := m.Milestone; ms != nil {
		color := funcMap["statusColor"].(func(string) string)
		for _, sg := range ms.Segments {
			c := color(sg.Status)
			switch {
			case sg.Done:
				c = reportWorkingColor
			case sg.Track:
				c = reportIdleColor
			}
			w.Segments = append(w.Segments, WbSegment{Value: sg.Count, Color: c, Label: sg.Status})
		}
		for _, sg := range ms.Legend {
			c := color(sg.Status)
			switch {
			case sg.Done:
				c = reportWorkingColor
			case sg.Track:
				c = reportIdleColor
			}
			w.Legend = append(w.Legend, WbLegendRow{Color: c, Count: sg.Count, Label: sg.Status})
		}
		if ms.OpenEpics > 0 {
			w.Areas = append(w.Areas, WbChip{Text: plural(ms.OpenEpics, "epic", "epics") + " open", Tone: "agent", Title: "Open epics count as open issues: the milestone is done when they are closed too"})
		}
		for _, a := range ms.OpenAreas {
			w.Areas = append(w.Areas, WbChip{Text: fmt.Sprintf("%s %d", a.Area, a.Count), Tone: "idle"})
		}
	}
	switch {
	case !versioned:
		w.Locked = "Milestones locked."
		w.LockedWhy = "No issue has a version: set one and you get a bar and a row per version."
	case len(w.Milestones) == 0:
		w.Locked = "No upcoming milestone."
		w.LockedWhy = "Every version on the board is released: set a version on new work for the next one."
	}
	epics := append([]EpicProgress(nil), m.Epics...)
	sort.SliceStable(epics, func(i, j int) bool {
		a, b := epics[i].Progress, epics[j].Progress
		return a.Done*b.Total > b.Done*a.Total
	})
	for i, e := range epics {
		if i == reportEpics {
			w.MoreEpics = len(epics) - i
			break
		}
		w.Epics = append(w.Epics, WbBar{Label: e.Issue.Title, Note: e.Note(), URL: prefix + "/issue/" + e.Issue.Slug, Done: e.Progress.Done, Total: e.Progress.Total})
	}
	return w
}

// reportShipped is Shipped in 7 days by system: each system's newest issue.
func reportShipped(m *StatusModel, prefix string) WbShipped {
	s := WbShipped{Count: len(m.DoneRecent), ListURL: prefix + "/?status=done"}
	for i, g := range m.Shipped {
		if i == reportShippedRows {
			s.More = len(m.Shipped) - i
			break
		}
		top := g.Rows[0]
		row := WbShippedRow{Area: g.Area, Slug: top.Issue.Slug, URL: prefix + "/issue/" + top.Issue.Slug, Date: top.At.Format("2 Jan"), More: len(g.Rows) - 1}
		if g.Merged {
			row.Area = plural(g.Areas, "more system", "more systems")
		}
		if top.Approx {
			row.Date = "≈ " + row.Date
		}
		if row.More > 0 {
			row.MoreURL = prefix + "/?status=done"
			if sys := top.Issue.System; !g.Merged && sys != "" {
				row.MoreURL += "&system=" + sys
			}
		}
		s.Rows = append(s.Rows, row)
	}
	return s
}

// reportHealth is Code health from the Health card; empty boxes without a
// snapshot or a review.
func reportHealth(m *StatusModel, p *tracker.Project, prefix string) WbHealth {
	h := WbHealth{
		URL:     prefix + "/health",
		Derived: WbScoreBox{Label: "FROM MEASUREMENTS", Value: "—", Line: "too little data"},
		Opinion: WbScoreBox{Label: "AGENT OPINION", Value: "—", Line: "no review yet"},
		LintCmd: lintCommandFor(p),
	}
	c := m.Health
	if c == nil {
		h.Derived.Line = "no snapshot yet"
		return h
	}
	h.Snapshot = true
	h.Recs = c.Proposed
	if c.HasDerived {
		h.Derived.Has, h.Derived.Value, h.Derived.Line = true, c.Derived, c.DerivedLine
	}
	if c.HasOpinion {
		h.Opinion.Has, h.Opinion.Value, h.Opinion.Line = true, c.Opinion, c.OpinionLine
		if c.OpinionStale {
			h.Opinion.Line += " · stale"
		}
	}
	h.Weakest = c.WeakestPart
	h.Lint, h.LintOK = c.Lint, c.Lint == "no lint warnings"
	h.Departments = c.Departments
	return h
}

// unlockBench adds the benchmark to To unlock: on once a benchmark has a
// task in the project, else the command that lists candidates.
func unlockBench(u WbUnlock, on bool) WbUnlock {
	cmd := "issue-cli bench candidates"
	it := WbUnlockItem{Key: "benchmark", Title: "Factory benchmark", On: on,
		Text: "Done issues solved again in a sandbox measure how well the factory works; the owner picks the golden tasks.", Code: cmd,
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper", TestID: "unlock_benchmark"}}
	if on {
		it.Text = "A benchmark has golden tasks from this project; its runs are on the Benchmark card."
	}
	u.Total++
	if on {
		u.Done++
		u.Items = append(u.Items, it)
		return u
	}
	// Locked items come before the enabled ones.
	i := 0
	for i < len(u.Items) && !u.Items[i].On {
		i++
	}
	u.Items = append(u.Items[:i], append([]WbUnlockItem{it}, u.Items[i:]...)...)
	return u
}

// reportUnlock is To unlock: each thing that gives the Report more to
// show, whether it is on, why it matters and how to turn it on.
func reportUnlock(m *StatusModel, p *tracker.Project, issues []*tracker.Issue, prefix string) WbUnlock {
	var items []WbUnlockItem
	updates := prefix + "/updates"

	work := WbUnlockItem{Key: "tracker", Title: "Tracker backup", Text: "issues/, sessions/, retros/ and .health/snapshots/ live outside git; a private tracker repo backs them up.",
		Action: WbAction{Label: "Set up", URL: updates, Tone: "paper"}}
	if b := m.Privacy.Badge("work"); b != nil {
		work.On = b.Level == privacy.Green || (b.Level == privacy.Grey && !b.NotSetUp)
		work.Risk = b.Level == privacy.Amber || b.Level == privacy.Red
		if !work.On {
			work.Text = b.Sentence + "."
		}
		if b.FixIsCommand() {
			work.Code, work.Action = b.Fix, WbAction{Label: "Copy", Copy: b.Fix, Tone: "paper"}
		}
		if work.On {
			work.Text = b.Sentence + "."
		}
		work.Note = b.Note
	}
	items = append(items, work)

	code := WbUnlockItem{Key: "code", Title: "Code privacy", Text: "Develop in a private repo and publish releases only.", Action: WbAction{Label: "Enable", URL: updates, Tone: "paper"}}
	if b := m.Privacy.Badge("code"); b != nil {
		code.On = b.Level == privacy.Green || (b.Level == privacy.Grey && !b.NotSetUp)
		code.Risk = b.Level == privacy.Red
		if code.On || b.Level != privacy.Grey {
			code.Text = b.Sentence + "."
		}
		if b.FixIsCommand() {
			code.Code, code.Action = b.Fix, WbAction{Label: "Copy", Copy: b.Fix, Tone: "paper"}
		}
		code.Note = b.Note
	}
	items = append(items, code)

	versioned := false
	for _, issue := range issues {
		if strings.TrimSpace(issue.Version) != "" {
			versioned = true
			break
		}
	}
	cmd := "issue-cli set-meta <slug> --key version --value 0.1"
	items = append(items, WbUnlockItem{Key: "milestones", Title: "Milestones", On: versioned,
		Text: "Give issues a version: Where we are gets a bar per version and the current milestone.", Code: cmd,
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper"}})

	cmd = "issue-cli set-meta <child> --key parent --value <epic>"
	items = append(items, WbUnlockItem{Key: "epics", Title: "Epics", On: len(m.Epics) > 0,
		Text: "An issue whose children name it in parent: becomes an epic, with its own bar.", Code: cmd,
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper"}})

	cmd = "workbench:\n  changelog: CHANGELOG.md"
	items = append(items, WbUnlockItem{Key: "releases", Title: "Releases", On: len(m.Releases) > 0,
		Text: "Point workbench.changelog in workflow.yaml at your changelog (or tag releases in git).", Code: "workbench.changelog",
		Action: WbAction{Label: "Copy", Copy: cmd, Tone: "paper"}})

	review := WbUnlockItem{Key: "review", Title: "Health review by an agent", Text: "No agent has reviewed the code yet.",
		Action: WbAction{Label: "Order", URL: prefix + "/health", Tone: "paper"}}
	if c := m.Health; c != nil && c.HasOpinion {
		review.On = !c.OpinionStale
		if line := c.OpinionLine; line != "" {
			review.Text = strings.ToUpper(line[:1]) + line[1:] + "."
		}
		if c.OpinionStale {
			review.Text += " It is stale: order a new one."
		}
	}
	items = append(items, review)

	u := WbUnlock{Total: len(items)}
	// What is still locked first, risks on top.
	sort.SliceStable(items, func(i, j int) bool {
		rank := func(it WbUnlockItem) int {
			switch {
			case it.Risk:
				return 0
			case !it.On:
				return 1
			}
			return 2
		}
		return rank(items[i]) < rank(items[j])
	})
	for i := range items {
		items[i].Action.TestID = "unlock_" + items[i].Key
		if items[i].On {
			u.Done++
		}
	}
	u.Items = items
	return u
}

// reportActiveRows is how many in-flight issues Working on lists before
// the fold.
const reportActiveRows = 6

// reportActive is Working on: the Status model's in-flight issues with
// their age and agent, and the agents on issues not started yet.
func reportActive(m *StatusModel, prefix string) WbActiveList {
	l := WbActiveList{AgentsURL: prefix + "/agents"}
	for i, a := range m.Active {
		r := WbActiveRow{Title: a.Issue.Title, Slug: a.Issue.Slug, URL: prefix + "/issue/" + a.Issue.Slug, Status: a.Issue.Status,
			Age: a.Age.Text, Tone: "idle", Agent: "no agent"}
		if a.Age.Approx {
			r.Age = "≈ " + r.Age
			r.AgeTitle = "No transition recorded: the file's last change"
		} else if !a.Age.Since.IsZero() {
			r.AgeTitle = "Since " + a.Age.Since.Format("2 Jan 15:04")
		}
		if a.Age.Level != "" {
			r.Tone = "red"
		}
		if a.Agent != "" {
			r.Agent = "agent " + a.Agent
			if a.Gate != "" {
				r.Agent += " → " + a.Gate
			}
		}
		if i < reportActiveRows {
			l.Rows = append(l.Rows, r)
		} else {
			l.More = append(l.More, r)
		}
	}
	for _, c := range m.InDesign {
		tone := "agent"
		if c.State == "at gate" || c.State == "question" {
			tone = "you"
		}
		l.Design = append(l.Design, WbActiveChip{Text: c.Issue.Title + " · " + c.State, Tone: tone, URL: prefix + "/issue/" + c.Issue.Slug})
	}
	return l
}

// reportReleasedRows is how many releases Recently released lists.
const reportReleasedRows = 3

// reportReleased is Recently released from the Status model's releases.
func reportReleased(m *StatusModel) WbReleased {
	r := WbReleased{Source: m.ReleaseSource}
	for i, rel := range m.Releases {
		if i == reportReleasedRows {
			break
		}
		r.Rows = append(r.Rows, WbReleaseRow{Version: rel.Version, Date: rel.Date, Summary: rel.SummaryHTML()})
	}
	if len(r.Rows) == 0 {
		if ms := m.Milestone; ms != nil && ms.Cut != "" {
			r.First = "No releases yet: " + ms.Cut + " will be the first."
		}
		r.Hint = !m.ChangelogDeclined
	}
	return r
}
