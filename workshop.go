package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Workshop Board components: the view models of the new UI's building
// blocks. Each has one template in templates/_workshop.html ({{define
// "wb/<name>"}}) and one CSS block in static/workshop.css; pages (map.html for
// L0, then the company L1 and the hall L2) only compose them. A new element
// is a new component first.

// WbHeader is the bar on top of every level: brand, where you are (Map ›
// Company › Hall), the agents, what was done this week and what waits on you.
type WbHeader struct {
	Crumbs []WbCrumb
	// Goals replace the agent counters when set (L1: the company's goals).
	Goals []WbGoal
	// Chips follow the counters (L2: CI); HideDone drops "done this week".
	Chips    []WbChip
	HideDone bool
	// Toggle is the project's Board | Report switch (L1); Quiet drops the
	// agent counters (the Report has its own cards).
	Toggle []WbViewTab
	Quiet  bool
	// Tabs is the hall's view toggle after the crumbs (L2: Line | Report |
	// Health); nil elsewhere.
	Tabs                     *WbHallTabs
	Working, Idle, AgentWait int
	DoneWeek                 int
	Waiting                  int
	// WaitingURL is where "N waiting on you" goes: the project's Needs you
	// list, or /needs-you across projects.
	WaitingURL string
	// YOLO is the switch (L1), the after-the-fact count and the hazard band
	// under the header on every project page while YOLO is on (yolo_wb.go).
	YOLO *WbYOLO
}

// CrewURL is where the agent counters go: the project's Crew page, read off
// WaitingURL (<prefix>/needs-you); "" across projects, where they stay text.
func (h WbHeader) CrewURL() string {
	prefix, ok := strings.CutSuffix(h.WaitingURL, "/needs-you")
	if !ok || !strings.HasPrefix(prefix, "/p/") {
		return ""
	}
	return prefix + "/agents"
}

// WbCrumb is one level in the header. URL is empty for the current level
// and for a level not reachable from here (drawn dashed: still to choose).
type WbCrumb struct {
	Label, URL string
	// Color is the project's square on its crumb.
	Color   string
	Current bool
	Hint    string
}

// wbCrumbs is Map › Company › Hall with level (0, 1, 2) current; project
// links the company when the page is inside one.
func wbCrumbs(level int, projectName, projectURL string) []WbCrumb {
	crumbs := []WbCrumb{
		{Label: "Map", URL: "/"},
		{Label: "Company", Hint: "Pick a project on the map"},
		{Label: "Hall", Hint: "Pick a project, then its hall"},
	}
	if projectName != "" {
		crumbs[1] = WbCrumb{Label: projectName, URL: projectURL}
	}
	crumbs[level].Current, crumbs[level].URL = true, ""
	return crumbs
}

// WbChip is a small pill: a state or a count. Tone is red, green, agent, idle
// or you; Dot draws the coloured dot, Blink makes it blink (CI red).
// TestID, when set, names the chip as chip.<TestID>.
type WbChip struct {
	Tone, Text, Title, TestID string
	Dot, Blink                bool
}

// WbRobot is one agent. State is working, idle, you (waits on the owner),
// agent (waits on another agent's issue) or off (no session: a stopped job,
// a lead not running).
type WbRobot struct {
	State string
	Label string
}

// WbCrew is a row of robots: at most wbCrewMax drawn, the rest counted.
type WbCrew struct {
	Robots []WbRobot
	More   int
}

// wbCrewMax is how many robots a row draws before "+N".
const wbCrewMax = 16

// wbCrew orders robots by what they need (waiting on you, working, waiting on
// an agent, idle) and keeps the first wbCrewMax.
func wbCrew(robots []WbRobot) WbCrew { return wbCrewOf(robots, wbCrewMax) }

// wbCrewOf is wbCrew with its own cap (a station on the line draws 3).
func wbCrewOf(robots []WbRobot, max int) WbCrew {
	rank := map[string]int{"you": 0, "working": 1, "agent": 2, "idle": 3}
	sorted := append([]WbRobot(nil), robots...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank[sorted[i].State] < rank[sorted[j].State] })
	if len(sorted) <= max {
		return WbCrew{Robots: sorted}
	}
	return WbCrew{Robots: sorted[:max], More: len(sorted) - max}
}

// StateLabel is the robot's state in words, for its tooltip.
func (r WbRobot) StateLabel() string {
	switch r.State {
	case "working":
		return "working"
	case "idle":
		return "idle"
	case "you":
		return "waiting on you"
	case "off":
		return "not running"
	}
	return "waiting on an agent"
}

// WbCEO is the project's lead (the CEO: a robot with a crown) and its latest
// line.
type WbCEO struct {
	Session string
	Live    bool
	Quote   string
}

// WbBuilding is one department (a system) on a lot: height is its open work.
type WbBuilding struct {
	Name          string
	Open          int
	Color         string
	Width, Height int
	// Rooms are a group's departments, small buildings inside its own.
	Rooms []WbBuilding
}

// WbProgress is a labelled bar: a goal or, for a project without goals, the
// most advanced open epic. Text replaces Done/Total ("30 / 40"); Title is
// its hover text.
type WbProgress struct {
	Label       string
	Done, Total int
	Text, Title string
}

// Pct is the bar's fill, 0–100.
func (p WbProgress) Pct() int {
	if p.Total == 0 {
		return 0
	}
	return p.Done * 100 / p.Total
}

// WbQueueCard is one thing the owner has to do: the "Waiting on you" cards.
type WbQueueCard struct {
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	Project     string    `json:"project"`
	ProjectSlug string    `json:"project_slug"`
	Color       string    `json:"color"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title,omitempty"`
	Note        string    `json:"note"`
	Age         string    `json:"age"`
	CTA         string    `json:"action"`
	URL         string    `json:"url"`
	Blocking    bool      `json:"blocking"`
	Since       time.Time `json:"since"`
	Tone        string    `json:"tone"` // "red" for CI, "you" otherwise
	// From and Target are a gate's from → to; Ready marks a gate whose
	// approval is all it waits on, so it can be stamped from the list.
	From   string `json:"from,omitempty"`
	Target string `json:"target,omitempty"`
	Ready  bool   `json:"ready,omitempty"`
	// RunURL is the POST that starts the row's playtest or check scripts
	// in tmux, RunLabel its button.
	RunURL   string `json:"run_url,omitempty"`
	RunLabel string `json:"run_label,omitempty"`
}

// wbCountChips are the agent counts as chips: N waiting on you, N working,
// N waiting on an agent, N idle.
func wbCountChips(you, working, agentWait, idle int) []WbChip {
	var out []WbChip
	if you > 0 {
		out = append(out, WbChip{Tone: "you", Text: fmt.Sprintf("%d waiting on you", you)})
	}
	if working > 0 {
		out = append(out, WbChip{Tone: "green", Text: fmt.Sprintf("%d working", working)})
	}
	if agentWait > 0 {
		out = append(out, WbChip{Tone: "agent", Text: fmt.Sprintf("%d waiting on an agent", agentWait)})
	}
	if idle > 0 {
		out = append(out, WbChip{Tone: "idle", Text: fmt.Sprintf("%d idle", idle)})
	}
	return out
}

// wbCIChip is CI's state as a chip; nil when the project has no CI.
func wbCIChip(state, line string) *WbChip {
	switch state {
	case "red":
		return &WbChip{Tone: "red", Text: "CI red", Title: line, Dot: true, Blink: true}
	case "green":
		return &WbChip{Tone: "green", Text: "CI green", Dot: true}
	}
	return nil
}

// WbGoal is a goal in the header: a labelled bar in the project's colour.
// Text replaces Done/Total for a configured goal ("30 / 40"); Title is its
// tooltip (stale, source error, the department).
type WbGoal struct {
	Progress    WbProgress
	Color       string
	Text, Title string
	// Off: stale or unreadable, drawn muted.
	Off bool
}

// WbHQGoal is an HQ goal in the HQ room with the department goals rolling
// up into it.
type WbHQGoal struct {
	Goal     WbGoal
	Children []WbGoal
}

// WbRoomHead is a department room's coloured roof: its name, a note on the
// right ("1/1 desk") and chips.
type WbRoomHead struct {
	Title, Color, Meta string
	// ID names the head for tests (company.room.head.<ID>); optional.
	ID    string
	Chips []WbChip
}

// WbFootItem is one line in a room's footer; Tone green for done work, URL
// makes it a link.
type WbFootItem struct {
	Text, Tone, URL string
	// Title is the item's tooltip; optional.
	Title string
}

// WbCEODesk is the CEO at its desk: the big crowned robot, its latest line
// and the way to talk to it (or start it).
type WbCEODesk struct {
	CEO               WbCEO
	Color             string
	ChatURL, SpawnURL string
}

// WbDeptLead is a department's lead at its desk in the room: Talk to it
// when it is live (its chat), else Start it.
type WbDeptLead struct {
	Who, Session      string
	Live              bool
	Color             string
	ChatURL, SpawnURL string
}

// WbRoomHQ is HQ: the CEO's room, its mission and the business goals.
type WbRoomHQ struct {
	// Box is the room's connections key (hq); static/connections.js finds
	// the room by it.
	Box  string
	Head WbRoomHead
	Desk WbCEODesk
	// Open is the project's open issues (the foot's "N open"; the floor's
	// HQ number).
	Open    int
	Mission string
	Goals   []WbHQGoal
	Shelf   *WbHQShelf
	// Spend is what HQ and everything rolling up into it spent.
	Spend *WbSpend
	// Handoffs: what waits for the CEO's forward, what HQ asked for.
	Handoffs *WbHandoffs
	Foot     []WbFootItem
}

// WbStation is one stop on the factory line: a status from workflow.yaml,
// or a gate (the owner's stamp) before a status the owner approves.
type WbStation struct {
	Label, Title string
	Count        int
	Gate         bool
	// Owner: the owner's own station (human testing).
	Owner bool
	Crew  WbCrew
}

// WbRoomDept is a department projects.yaml adds, as a room in the company.
type WbRoomDept struct {
	// Box is the department's connections key.
	Box  string
	Head WbRoomHead
	// Added: the owner just added it from the New department tile.
	Added bool
	// Lead is the department's lead: live or not, and Talk or Start.
	Lead    WbDeptLead
	Crew    WbCrew
	Role    string
	Systems string
	// Stations are the department's line from its own workflow file
	// (workflow: in projects.yaml); none when it runs on Product's.
	Stations []WbStation
	// WorkflowFile is that file's name; WorkflowError why it didn't load.
	WorkflowFile, WorkflowError string
	Mission                     string
	Goals                       []WbGoal
	// AllGoals is every goal (Goals shows the first few), for the floor's
	// inspector.
	AllGoals []WbGoal
	Shelf    *WbShelfLink
	// Connectors are the department's outside tools; nil when none.
	Connectors *WbConnectors
	// Jobs are the department's recurring jobs (jobs_view.go).
	Jobs []WbJobLine
	// Spend is what it spent by week (spend_view.go).
	Spend *WbSpend
	// Handoffs are what it asked others for and its inbox (handoffs_view.go).
	Handoffs *WbHandoffs
	Foot     []WbFootItem
	// Children are a group's departments, as rooms inside its room;
	// Waiting is how many of the group's and its departments' issues wait
	// on the owner (shown only on a group).
	Children []WbRoomDept
	Waiting  int
	// Week is a group's latest week report (week_view.go); nil on a
	// department.
	Week *WbWeekReport
}

// WbRoomFactory is Product, the software factory: its line of stations.
type WbRoomFactory struct {
	// Box is Product's connections key (product).
	Box      string
	Head     WbRoomHead
	Mission  string
	Goals    []WbGoal
	Stations []WbStation
	// AllGoals is every goal (Goals shows the first few), for the floor's
	// inspector.
	AllGoals []WbGoal
	Waiting  int
	HallURL  string
	Shelf    *WbShelfLink
	Spend    *WbSpend
	Handoffs *WbHandoffs
	Foot     []WbFootItem
	// YOLO: while on, the gates on the line stand open and the room carries
	// "YOLO · N" instead of the "!".
	YOLO *YOLOBand
}

// WbNewTile is a dashed "+" tile for something still to build.
type WbNewTile struct {
	Title, Sub, Note string
	TestID           string
}

// WbMessage is one line on the Radio. Tone is ceo, you, agent or system.
// A comment line also has Kind (progress or question), URL (its issue's
// page, To is the slug) and When (its age); a CEO pane line has none.
type WbMessage struct {
	From, To, Initials, Tone, Text string
	Kind, URL, When                string
}

// WbMessageBox is the box to message the project's CEO.
type WbMessageBox struct {
	Session, SendURL, SpawnURL string
	// Prefill is the text the box starts with (L2: "<slug>: " for the
	// issue the owner picked; never approval words).
	Prefill string
	Live    bool
}

// WbHallStop is one stop on the hall's line: a station or a gate.
type WbHallStop struct {
	Station *WbHallStation
	Gate    *WbGateCard
}

// WbGateCard is a gate on the hall's line: the owner's stamp before Status.
type WbGateCard struct {
	// URL is the gate's drill-in page (/hall/gate/<status>).
	Name, Status, URL string
	Waiting           int
	// Raised: YOLO is on, the barrier is up and crates pass without the owner.
	Raised bool
}

// WbHallStation is a station on the hall's line with its crates.
type WbHallStation struct {
	// URL is the station's drill-in page (/hall/station/<status>).
	Name, Status, Who, URL string
	Count, More            int
	Owner                  bool
	Crates                 []WbCrate
}

// WbCrate is an issue on the line, carried by its worker (Robot) if it has
// one. Question: it asks the owner; CI: CI went red since it landed; YOLO:
// it passed a gate in YOLO (the yellow marker, to the end of the line).
type WbCrate struct {
	Slug, Title, URL, Note       string
	Robot                        *WbRobot
	Question, CI, YOLO, Selected bool
}

// WbDoneStation is the end of the line: what was done this week.
type WbDoneStation struct {
	Count    int
	Stack    []int
	Who, URL string
}

// WbCIScanner is local CI on the line.
type WbCIScanner struct {
	Red                                 bool
	Command, Branch, Issue, Detail, URL string
}

// WbParking is the parked issues and why.
type WbParking struct {
	Count int
	URL   string
	Items []WbParked
}

// WbParked is one parked issue.
type WbParked struct {
	Slug, URL, Reason string
}

// WbPlanCard is a blueprint card for something still to build: a project
// (building site), a department or a stage of the line.
type WbPlanCard struct {
	Label, Title, Text string
	LinkText, LinkURL  string
	SpawnURL           string
	TestID             string
}

// WbFact is a label and a value (Mono for code, Tone green, red or you).
type WbFact struct {
	Label, Value, Tone string
	Mono               bool
}

// WbPathStep is one status on an issue's way along the line: done, now or
// todo.
type WbPathStep struct {
	Status, Tone string
}

// WbStat is a big number with a label.
type WbStat struct {
	Value, Label string
}

// WbAgentPanel is the selected issue and its worker (L2's right panel).
type WbAgentPanel struct {
	Slug, Title, URL string
	Robot            WbRobot
	StateLabel       string
	Session          string
	Path             []WbPathStep
	Facts            []WbFact
	Stats            []WbStat
}

// WbNumbers is a titled list of figures.
type WbNumbers struct {
	Title string
	Rows  []WbFact
}
