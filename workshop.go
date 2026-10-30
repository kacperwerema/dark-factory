package main

import (
	"fmt"
	"sort"
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
	// YOLO is the switch (L1), the after-the-fact count and the hazard band
	// under the header on every project page while YOLO is on (yolo_wb.go).
	YOLO *WbYOLO
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

// WbRobot is one agent. State is working, idle, you (waits on the owner) or
// agent (waits on another agent's issue).
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
}

// WbProgress is a labelled bar: a goal or, until projects have goals, the
// most advanced open epic.
type WbProgress struct {
	Label       string
	Done, Total int
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
	Kind, Label          string
	Project, ProjectSlug string
	Color                string
	Slug, Note, Age      string
	CTA, URL             string
	Blocking             bool
	Since                time.Time
	Tone                 string // "red" for CI, "you" otherwise
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
type WbGoal struct {
	Progress WbProgress
	Color    string
}

// WbRoomHead is a department room's coloured roof: its name, a note on the
// right ("1/1 desk") and chips.
type WbRoomHead struct {
	Title, Color, Meta string
	Chips              []WbChip
}

// WbFootItem is one line in a room's footer; Tone green for done work.
type WbFootItem struct {
	Text, Tone string
}

// WbCEODesk is the CEO at its desk: the big crowned robot, its latest line
// and the way to talk to it (or start it).
type WbCEODesk struct {
	CEO               WbCEO
	Color             string
	ChatURL, SpawnURL string
}

// WbRoomHQ is HQ: the CEO's room.
type WbRoomHQ struct {
	Head WbRoomHead
	Desk WbCEODesk
	Foot []WbFootItem
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
	Head    WbRoomHead
	Crew    WbCrew
	Role    string
	Systems string
	Foot    []WbFootItem
}

// WbRoomFactory is Product, the software factory: its line of stations.
type WbRoomFactory struct {
	Head     WbRoomHead
	Stations []WbStation
	Waiting  int
	HallURL  string
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
type WbMessage struct {
	From, To, Initials, Tone, Text string
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
	Name, Status string
	Waiting      int
	// Raised: YOLO is on, the barrier is up and crates pass without the owner.
	Raised bool
}

// WbHallStation is a station on the hall's line with its crates.
type WbHallStation struct {
	Name, Status, Who string
	Count, More       int
	Owner             bool
	Crates            []WbCrate
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
	Count int
	Stack []int
	Who   string
}

// WbCIScanner is local CI on the line.
type WbCIScanner struct {
	Red                            bool
	Command, Branch, Issue, Detail string
}

// WbParking is the parked issues and why.
type WbParking struct {
	Count int
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
