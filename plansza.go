package main

import (
	"fmt"
	"sort"
	"time"
)

// Plansza warsztatowa components: the view models of the new UI's building
// blocks. Each has one template in templates/_plansza.html ({{define
// "pl/<name>"}}) and one CSS block in static/plansza.css; pages (map.html for
// L0, then the company L1 and the hall L2) only compose them. A new element
// is a new component first.

// PlHeader is the bar on top of every level: brand, where you are (Mapa ›
// Firma › Hala), the agents, what was done this week and what waits on you.
type PlHeader struct {
	Crumbs                   []PlCrumb
	Working, Idle, AgentWait int
	DoneWeek                 int
	Waiting                  int
}

// PlCrumb is one level in the header. URL is empty for the current level
// and for a level not reachable from here (drawn dashed: still to choose).
type PlCrumb struct {
	Label, URL string
	Current    bool
	Hint       string
}

// plCrumbs is Mapa › Firma › Hala with level (0, 1, 2) current; project
// links the company when the page is inside one.
func plCrumbs(level int, projectName, projectURL string) []PlCrumb {
	crumbs := []PlCrumb{
		{Label: "Mapa", URL: "/"},
		{Label: "Firma", Hint: "Wejdź w projekt na mapie"},
		{Label: "Hala", Hint: "Wejdź w projekt, potem w halę"},
	}
	if projectName != "" {
		crumbs[1] = PlCrumb{Label: projectName, URL: projectURL}
	}
	crumbs[level].Current, crumbs[level].URL = true, ""
	return crumbs
}

// PlChip is a small pill: a state or a count. Tone is red, green, agent, idle
// or you; Dot draws the coloured dot, Blink makes it blink (CI red).
// TestID, when set, names the chip as chip.<TestID>.
type PlChip struct {
	Tone, Text, Title, TestID string
	Dot, Blink                bool
}

// PlRobot is one agent. State is working, idle, you (waits on the owner) or
// agent (waits on another agent's issue).
type PlRobot struct {
	State string
	Label string
}

// PlCrew is a row of robots: at most plCrewMax drawn, the rest counted.
type PlCrew struct {
	Robots []PlRobot
	More   int
}

// plCrewMax is how many robots a row draws before "+N".
const plCrewMax = 16

// plCrew orders robots by what they need (waiting on you, working, waiting on
// an agent, idle) and keeps the first plCrewMax.
func plCrew(robots []PlRobot) PlCrew {
	rank := map[string]int{"you": 0, "working": 1, "agent": 2, "idle": 3}
	sorted := append([]PlRobot(nil), robots...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank[sorted[i].State] < rank[sorted[j].State] })
	if len(sorted) <= plCrewMax {
		return PlCrew{Robots: sorted}
	}
	return PlCrew{Robots: sorted[:plCrewMax], More: len(sorted) - plCrewMax}
}

// StateLabel is the robot's state in words, for its tooltip.
func (r PlRobot) StateLabel() string {
	switch r.State {
	case "working":
		return "pracuje"
	case "idle":
		return "bezczynny"
	case "you":
		return "czeka na Ciebie"
	}
	return "czeka na agenta"
}

// PlCEO is the project's lead (the CEO: a robot with a crown) and its latest
// line.
type PlCEO struct {
	Session string
	Live    bool
	Quote   string
}

// PlBuilding is one department (a system) on a lot: height is its open work.
type PlBuilding struct {
	Name          string
	Open          int
	Color         string
	Width, Height int
}

// PlProgress is a labelled bar: a goal or, until projects have goals, the
// most advanced open epic.
type PlProgress struct {
	Label       string
	Done, Total int
}

// Pct is the bar's fill, 0–100.
func (p PlProgress) Pct() int {
	if p.Total == 0 {
		return 0
	}
	return p.Done * 100 / p.Total
}

// PlQueueCard is one thing the owner has to do: the "Czeka na Ciebie" cards.
type PlQueueCard struct {
	Kind, Label          string
	Project, ProjectSlug string
	Color                string
	Slug, Note, Age      string
	CTA, URL             string
	Blocking             bool
	Since                time.Time
	Tone                 string // "red" for CI, "you" otherwise
}

// plCountChips are the agent counts as chips: N czeka na Ciebie, N pracuje,
// N czeka na agenta, N bezczynny.
func plCountChips(you, working, agentWait, idle int) []PlChip {
	var out []PlChip
	if you > 0 {
		out = append(out, PlChip{Tone: "you", Text: fmt.Sprintf("%d czeka na Ciebie", you)})
	}
	if working > 0 {
		out = append(out, PlChip{Tone: "green", Text: fmt.Sprintf("%d pracuje", working)})
	}
	if agentWait > 0 {
		out = append(out, PlChip{Tone: "agent", Text: fmt.Sprintf("%d czeka na agenta", agentWait)})
	}
	if idle == 1 {
		out = append(out, PlChip{Tone: "idle", Text: "1 bezczynny"})
	} else if idle > 1 {
		out = append(out, PlChip{Tone: "idle", Text: fmt.Sprintf("%d bezczynnych", idle)})
	}
	return out
}

// plCIChip is CI's state as a chip; nil when the project has no CI.
func plCIChip(state, line string) *PlChip {
	switch state {
	case "red":
		return &PlChip{Tone: "red", Text: "CI czerwony", Title: line, Dot: true, Blink: true}
	case "green":
		return &PlChip{Tone: "green", Text: "CI zielony", Dot: true}
	}
	return nil
}
