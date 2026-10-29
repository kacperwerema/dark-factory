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
	Crumbs                   []WbCrumb
	Working, Idle, AgentWait int
	DoneWeek                 int
	Waiting                  int
}

// WbCrumb is one level in the header. URL is empty for the current level
// and for a level not reachable from here (drawn dashed: still to choose).
type WbCrumb struct {
	Label, URL string
	Current    bool
	Hint       string
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
func wbCrew(robots []WbRobot) WbCrew {
	rank := map[string]int{"you": 0, "working": 1, "agent": 2, "idle": 3}
	sorted := append([]WbRobot(nil), robots...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank[sorted[i].State] < rank[sorted[j].State] })
	if len(sorted) <= wbCrewMax {
		return WbCrew{Robots: sorted}
	}
	return WbCrew{Robots: sorted[:wbCrewMax], More: len(sorted) - wbCrewMax}
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

// plCIChip is CI's state as a chip; nil when the project has no CI.
func plCIChip(state, line string) *WbChip {
	switch state {
	case "red":
		return &WbChip{Tone: "red", Text: "CI czerwony", Title: line, Dot: true, Blink: true}
	case "green":
		return &WbChip{Tone: "green", Text: "CI zielony", Dot: true}
	}
	return nil
}
