// Package weekplan is a department's week: the evidence a group's lead (the
// lead of leads) plans from, and the week report kept under the work files.
//
// Gather reads, for a box and (for a group) each of its departments, what
// is already on disk: goals and how far each moved in the window, outputs
// waiting on review / reviewed / published, jobs, handoffs (request edges)
// and what is blocked and on whom. It only reads; nothing here starts,
// dispatches or approves anything
// (factory/the-lead-of-leads-at-work-the-gtm-lead-plans-its-departments-week-and-reports-on-the-company-page#D-4).
package weekplan

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Window is how far back the week looks.
const Window = 7 * 24 * time.Hour

// PlanSection is the run issue's section the plan is written in.
const PlanSection = "Week plan"

// JobID is the group template's weekly job (its key is <group>/weekly-plan).
const JobID = "weekly-plan"

// Input is what Gather reads; the caller loads the issues and the jobs'
// statuses once (the CLI and the viewer find the jobs' state differently).
type Input struct {
	Project *tracker.Project
	Issues  []*tracker.Issue
	// Jobs are jobs.Evaluate's statuses (the viewer's read: SkipBudget).
	Jobs []jobs.Status
	// Comments loads an issue's comments; nil reads them from disk.
	Comments func(*tracker.Issue) []tracker.Comment
	Now      time.Time
}

// Week is the pack: one Section per box, the group first.
type Week struct {
	Box   string `json:"box"`
	Name  string `json:"name"`
	Group bool   `json:"group"`
	// Key is the ISO week the plan is for (2026-W44); From–To the window
	// looked back over.
	Key      string     `json:"week"`
	From     time.Time  `json:"from"`
	To       time.Time  `json:"to"`
	Sections []*Section `json:"sections"`
	// Plan is the run issue's ## Week plan for Key, PlanIssue its slug.
	Plan      string `json:"plan,omitempty"`
	PlanIssue string `json:"plan_issue,omitempty"`
	Counts    Counts `json:"counts"`
}

// Counts are the week's headline numbers, the room's line.
type Counts struct {
	GoalsMoved    int `json:"goals_moved"`
	Shipped       int `json:"shipped"`
	Reviewed      int `json:"reviewed"`
	WaitingReview int `json:"waiting_review"`
	Blocked       int `json:"blocked"`
}

// Line is the counts in words: "1 goal moved · 2 outputs shipped, 4
// reviewed · 1 blocked".
func (c Counts) Line() string {
	return fmt.Sprintf("%s moved · %s shipped, %d reviewed · %d blocked",
		plural(c.GoalsMoved, "goal", "goals"), plural(c.Shipped, "output", "outputs"), c.Reviewed, c.Blocked)
}

// Section is one box's week.
type Section struct {
	Box         string        `json:"box"`
	Name        string        `json:"name"`
	Lead        string        `json:"lead"`
	Goals       []GoalLine    `json:"goals"`
	Review      []OutputLine  `json:"waiting_review"`
	Reviewed    []OutputLine  `json:"reviewed"`
	Published   []OutputLine  `json:"published"`
	Jobs        []JobLine     `json:"jobs"`
	HandoffsIn  []HandoffLine `json:"handoffs_in"`
	HandoffsOut []HandoffLine `json:"handoffs_out"`
	Blocked     []BlockedLine `json:"blocked"`
	OpenIssues  int           `json:"open_issues"`
}

// GoalLine is a goal now and how far it moved in the window.
type GoalLine struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Target   string `json:"target,omitempty"`
	Progress *int   `json:"progress,omitempty"`
	// Move says how it moved: "+2 this week", "first value this week",
	// "no change recorded", or for a file-sourced goal that moves aren't
	// tracked.
	Move  string `json:"move"`
	Moved bool   `json:"moved"`
}

// OutputLine is one output on the shelf.
type OutputLine struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Issue string `json:"issue,omitempty"`
	Date  string `json:"date"`
}

// JobLine is one recurring job now.
type JobLine struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Due      bool   `json:"due"`
	Verdict  string `json:"verdict,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Last     string `json:"last_run,omitempty"`
	Status   string `json:"last_status,omitempty"`
	Error    string `json:"error,omitempty"`
	// Missed is how many periods passed with no run.
	Missed int `json:"missed,omitempty"`
}

// HandoffLine is one open request between this box and another.
type HandoffLine struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Other is the box on the other end, by name.
	Other string `json:"other"`
	// Waiting names the lead that still has to forward it ("" when none).
	Waiting string `json:"waiting,omitempty"`
	// State and Route are an issue-cli handoff's ("at backlog", "waiting on
	// HQ to forward"; the route taken), empty on a plain request.
	State string `json:"state,omitempty"`
	Route string `json:"route,omitempty"`
}

// BlockedLine is an open issue that waits, and on whom.
type BlockedLine struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Status string `json:"status"`
	On     string `json:"on"`
	Why    string `json:"why"`
}

// Key is the ISO week t falls in: 2026-W44, as weekly job periods are keyed.
func Key(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// ValidKey reports whether key is an ISO week key (it names a report file).
func ValidKey(key string) bool {
	var y, w int
	if n, err := fmt.Sscanf(key, "%4d-W%02d", &y, &w); err != nil || n != 2 {
		return false
	}
	return key == fmt.Sprintf("%d-W%02d", y, w) && w >= 1 && w <= 53 && y >= 2000
}

// Resolve is the department a week is for: a department or group by name or
// key. HQ and Product are refused: they have the Status page and the Report.
func Resolve(p *tracker.Project, name string) (*tracker.Department, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return nil, fmt.Errorf("name a department or group: %s", deptList(p))
	case connections.HQ, connections.Product:
		return nil, fmt.Errorf("%s has no week plan: HQ has the Status page and Product the Report. Departments: %s", name, deptList(p))
	}
	d := p.DepartmentByKey(name)
	if d == nil {
		return nil, fmt.Errorf("no department %q. Departments: %s", name, deptList(p))
	}
	return d, nil
}

func deptList(p *tracker.Project) string {
	var names []string
	for _, d := range p.Departments {
		k := tracker.DepartmentKey(d.Name)
		if p.IsGroup(d.Name) {
			k += " (group)"
		}
		names = append(names, k)
	}
	if len(names) == 0 {
		return "none yet (issue-cli department list)"
	}
	return strings.Join(names, ", ")
}

// Gather is dept's week: dept's own section, then (for a group) each
// department under it, nested groups' departments included.
func Gather(in Input, dept *tracker.Department) *Week {
	in = withComments(in)
	p := in.Project
	now := in.Now
	w := &Week{
		Box: tracker.DepartmentKey(dept.Name), Name: dept.Name, Group: p.IsGroup(dept.Name),
		Key: Key(now), From: now.Add(-Window), To: now,
	}
	boxes := []tracker.Department{*dept}
	boxes = append(boxes, p.Descendants(dept.Name)...)
	w.Sections = gatherSections(in, boxes, w.From)
	for _, s := range w.Sections {
		for _, g := range s.Goals {
			if g.Moved {
				w.Counts.GoalsMoved++
			}
		}
		w.Counts.Shipped += len(s.Published)
		w.Counts.Reviewed += len(s.Reviewed)
		w.Counts.WaitingReview += len(s.Review)
		w.Counts.Blocked += len(s.Blocked)
	}
	w.PlanIssue, w.Plan = findPlan(p, in.Issues, dept.Name, w.Key)
	return w
}

// Sections is every department's week section, in projects.yaml order,
// over the same window as Gather: goals, history, the shelf and the
// connections are read once for all of them (the HQ report).
func Sections(in Input) []*Section {
	in = withComments(in)
	return gatherSections(in, in.Project.Departments, in.Now.Add(-Window))
}

func withComments(in Input) Input {
	if in.Comments == nil {
		in.Comments = func(is *tracker.Issue) []tracker.Comment {
			c, _ := tracker.LoadComments(is.FilePath)
			return c
		}
	}
	return in
}

// gatherSections is one Section per box, reading the shared logs once.
func gatherSections(in Input, boxes []tracker.Department, from time.Time) []*Section {
	p := in.Project
	rep := goals.Resolve(p, in.Now)
	history := goals.History(p)
	outs, _ := outputs.Load(p)
	edges := connections.Load(p, in.Issues, in.Now)
	var out []*Section
	for _, d := range boxes {
		s := &Section{Box: tracker.DepartmentKey(d.Name), Name: d.Name, Lead: tracker.DepartmentLeadName(p, d.Name)}
		if o := rep.Find(goals.DeptKey(d.Name)); o != nil {
			for _, g := range o.Goals {
				s.Goals = append(s.Goals, goalLine(g, history[strings.ToLower(g.Key)], from))
			}
		}
		s.Review, s.Reviewed, s.Published = outputLines(outs, s.Box, from)
		for _, j := range in.Jobs {
			if strings.EqualFold(j.Department, d.Name) {
				s.Jobs = append(s.Jobs, jobLine(j))
			}
		}
		s.HandoffsIn, s.HandoffsOut = handoffLines(p, in, edges, s.Box)
		for _, is := range in.Issues {
			if p.DepartmentOf(is) != d.Name || closed(is) {
				continue
			}
			s.OpenIssues++
			s.Blocked = append(s.Blocked, blockedLines(p, in, is)...)
		}
		out = append(out, s)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func closed(is *tracker.Issue) bool {
	switch strings.ToLower(strings.TrimSpace(is.Status)) {
	case "done", "parked", "closed", "archived":
		return true
	}
	return false
}

// goalLine says where g is and how far it moved since from: the newest
// value in the window against the newest before it. Only manual goals keep
// a log; a file-sourced goal's moves aren't tracked.
func goalLine(g *goals.Goal, log []goals.Entry, from time.Time) GoalLine {
	l := GoalLine{Key: g.Key, Name: g.Name, Value: g.ValueText(), Target: g.TargetText(), Progress: g.Progress}
	if g.Value == nil {
		l.Value = "no value yet"
	}
	if !g.Manual() {
		l.Move = "moves are tracked for manual goals only"
		return l
	}
	entries := append([]goals.Entry(nil), log...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Date < entries[j].Date })
	var before, inWin *goals.Entry
	for i := range entries {
		t, ok := goals.EntryTime(entries[i].Date)
		if !ok {
			continue
		}
		if t.Before(from) {
			before = &entries[i]
		} else {
			inWin = &entries[i]
		}
	}
	switch {
	case inWin == nil:
		l.Move = "no change recorded this week"
	case before == nil:
		l.Move, l.Moved = "first value this week", true
	default:
		d := inWin.Value - before.Value
		if d == 0 {
			l.Move = "set again this week, unchanged"
		} else {
			l.Move, l.Moved = fmt.Sprintf("%s this week (from %s)", signed(d, g.Unit), num(before.Value, g.Unit)), true
		}
	}
	return l
}

func num(v float64, unit string) string {
	s := fmt.Sprintf("%g", v)
	if unit != "" {
		s += " " + unit
	}
	return s
}

func signed(v float64, unit string) string {
	if v > 0 {
		return "+" + num(v, unit)
	}
	return num(v, unit)
}

// outputLines are box's outputs waiting on review, reviewed in the window
// and published in the window.
func outputLines(all []*outputs.Output, box string, from time.Time) (review, reviewed, published []OutputLine) {
	for _, o := range all {
		if o.Department != box {
			continue
		}
		line := OutputLine{ID: o.ID, Title: o.Title, Kind: o.Kind, State: o.State, Issue: o.Issue}
		switch {
		case o.State == outputs.StateReview:
			line.Date = o.Added.Format("2006-01-02")
			review = append(review, line)
		case o.State == outputs.StatePublished && !o.Published.Before(from):
			line.Date = o.Published.Format("2006-01-02")
			published = append(published, line)
		}
		if !o.Reviewed.IsZero() && !o.Reviewed.Before(from) {
			r := line
			r.Date = o.Reviewed.Format("2006-01-02")
			if o.State == outputs.StatePublished {
				r.State = outputs.StateApproved + ", then published"
			}
			reviewed = append(reviewed, r)
		}
	}
	return
}

func jobLine(j jobs.Status) JobLine {
	l := JobLine{Key: j.Key, Name: j.Name, Schedule: j.Schedule, Due: j.Due, Error: j.Error, Missed: j.Missed}
	if j.Due {
		l.Verdict, l.Reason = j.Verdict, j.Reason
	}
	if j.LastSlug != "" {
		l.Last, l.Status = j.LastSlug, j.LastStatus
	}
	return l
}

// handoffLines are the open request items into and out of box. A request
// is a handoff: an issue one box filed with another (requested_by:).
func handoffLines(p *tracker.Project, in Input, edges []connections.Edge, box string) (into, out []HandoffLine) {
	for _, e := range edges {
		if e.Kind != connections.Request || (e.To != box && e.From != box) {
			continue
		}
		for _, it := range e.Open {
			l := HandoffLine{Slug: it.Slug, Title: it.Title, Status: it.Status, State: it.State, Route: it.Route}
			if is := tracker.FindIssue(in.Issues, it.Slug); is != nil {
				if via := connections.RelayPending(p, is, in.Comments(is)); via != "" {
					l.Waiting = connections.RelayLead(p, via)
				}
			}
			if e.To == box {
				l.Other = connections.BoxName(p, e.From)
				into = append(into, l)
			} else {
				l.Other = connections.BoxName(p, e.To)
				out = append(out, l)
			}
		}
	}
	return
}

// blockedLines say what an open issue waits on, and on whom: another issue
// (blocked_on:), the owner (a ready gate, an open question), or the lead that
// still has to forward it.
func blockedLines(p *tracker.Project, in Input, is *tracker.Issue) []BlockedLine {
	base := BlockedLine{Slug: is.Slug, Title: is.Title, Status: is.Status}
	var out []BlockedLine
	for _, slug := range tracker.BlockedOn(is) {
		l := base
		l.On, l.Why = slug, "blocked_on: "+slug
		if dep := tracker.FindIssue(in.Issues, slug); dep != nil {
			if closed(dep) {
				continue
			}
			l.On = connections.BoxName(p, connections.BoxOf(p, dep)) + " (" + dep.Slug + ", " + dep.Status + ")"
		}
		out = append(out, l)
	}
	comments := in.Comments(is)
	wf := p.LoadWorkflowForIssue(is)
	if gate := tracker.WaitingOnGate(wf, is, comments); gate != "" {
		l := base
		l.On, l.Why = "the owner", "approval for "+gate
		out = append(out, l)
	}
	if n := len(comments); n > 0 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(comments[n-1].Text)), "question:") {
		l := base
		l.On, l.Why = "the owner", "an open question: comment"
		out = append(out, l)
	}
	if via := connections.RelayPending(p, is, comments); via != "" {
		l := base
		l.On, l.Why = connections.RelayLead(p, via), "forward of a request routed via "+connections.BoxName(p, via)
		out = append(out, l)
	}
	return out
}

// findPlan is the run issue of dept's week key with a ## Week plan section:
// slug and section text ("" when none).
func findPlan(p *tracker.Project, issues []*tracker.Issue, dept, key string) (string, string) {
	for _, is := range issues {
		if p.DepartmentOf(is) != dept || !strings.EqualFold(is.Field("run"), key) {
			continue
		}
		if text := BodySection(is.BodyRaw, PlanSection); text != "" {
			return is.Slug, text
		}
	}
	return "", ""
}

// BodySection is the text under "## <title>" in a markdown body, up to the next
// "## " heading, trimmed; "" when there is none or it is empty.
func BodySection(body, title string) string {
	var out []string
	in := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			if in {
				break
			}
			in = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(t, "## ")), title)
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
