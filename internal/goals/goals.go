// Package goals reads the goals / KPIs of a project's HQ and departments
// (company: and departments: in projects.yaml), finds each goal's newest
// value and rolls the department goals up into HQ's business goals. The
// CLI (issue-cli goals, goal set, department show) and the company page
// read the same Report. See docs/departments.md.
package goals

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// HQKey and ProductKey are the owner keys of HQ's and Product's goals in a
// goal key (hq/clients, product/uptime).
const (
	HQKey      = "hq"
	ProductKey = "product"
)

// Periods and Directions are the values a goal's period: and direction:
// may take ("" is the default: none and up).
var (
	Periods    = []string{"", "none", "week", "month", "quarter"}
	Directions = []string{"", "up", "down"}
)

// Entry is one line of the values log: a value set by hand.
type Entry struct {
	Goal  string  `json:"goal"`
	Value float64 `json:"value"`
	Date  string  `json:"date"`
	Note  string  `json:"note,omitempty"`
}

// Goal is a configured goal with its value and progress.
type Goal struct {
	// Key is <owner key>/<goal id>, what issue-cli goal set takes.
	Key     string       `json:"key"`
	Owner   string       `json:"owner"`
	Config  tracker.Goal `json:"-"`
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Target  *float64     `json:"target,omitempty"`
	Unit    string       `json:"unit,omitempty"`
	Period  string       `json:"period,omitempty"`
	Source  string       `json:"source"`
	RollsUp string       `json:"rolls_up,omitempty"`
	// Value is the newest value (nil: none yet); Date is when it was taken.
	Value *float64 `json:"value,omitempty"`
	Date  string   `json:"date,omitempty"`
	Note  string   `json:"note,omitempty"`
	// Stale: the value is from before the current period.
	Stale bool `json:"stale,omitempty"`
	// Progress is 0–100 towards the target; nil without a target or value.
	Progress *int `json:"progress,omitempty"`
	// RolledUp: an HQ goal whose progress is the mean of its Children's;
	// RolledFrom is how many of them had a progress to average.
	RolledUp   bool `json:"rolled_up,omitempty"`
	RolledFrom int  `json:"rolled_from,omitempty"`
	// Children are the department goals rolling up into this HQ (or
	// group) goal.
	Children []*Goal `json:"children,omitempty"`
	// Err says why the source could not be read.
	Err string `json:"error,omitempty"`
}

// Label is the goal's name, else its id.
func (g *Goal) Label() string {
	if g.Name != "" {
		return g.Name
	}
	return g.ID
}

// SetCommand is the command that records a value for a manual goal.
func (g *Goal) SetCommand() string {
	return fmt.Sprintf("issue-cli goal set %s <value>", g.Key)
}

// Manual reports whether the goal's value is set by hand.
func (g *Goal) Manual() bool { return isManual(g.Source) }

// ValueText is the value with its unit, "—" without one.
func (g *Goal) ValueText() string {
	if g.Value == nil {
		return "—"
	}
	return format(*g.Value, g.Unit)
}

// TargetText is the target with its unit, "" without one.
func (g *Goal) TargetText() string {
	if g.Target == nil {
		return ""
	}
	return format(*g.Target, g.Unit)
}

// Pct is the progress, 0 without one (for bars).
func (g *Goal) Pct() int {
	if g.Progress == nil {
		return 0
	}
	return *g.Progress
}

func format(v float64, unit string) string {
	s := tracker.FormatMetricValue(v, unit)
	switch strings.ToLower(unit) {
	case "", "count", "pct", "share", "%", "seconds", "s", "ms", "points":
		return s
	}
	return s + " " + unit
}

// Owner is HQ, Product or a department, with its goals.
type Owner struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Mission string  `json:"mission,omitempty"`
	Role    string  `json:"role,omitempty"`
	Prompt  string  `json:"prompt,omitempty"`
	Goals   []*Goal `json:"goals"`
	// Parent is the key of the group this department sits under ("" when
	// none): its goals may roll up into the group's before HQ's.
	Parent string `json:"parent,omitempty"`
}

// Report is every owner's goals; HQ's goals carry their Children.
type Report struct {
	HQ          *Owner   `json:"hq,omitempty"`
	Product     *Owner   `json:"product,omitempty"`
	Departments []*Owner `json:"departments"`
}

// Empty reports whether no goal is configured anywhere.
func (r *Report) Empty() bool {
	for _, o := range r.Owners() {
		if len(o.Goals) > 0 {
			return false
		}
	}
	return true
}

// Owners lists HQ, Product and the departments that are configured.
func (r *Report) Owners() []*Owner {
	var out []*Owner
	if r.HQ != nil {
		out = append(out, r.HQ)
	}
	if r.Product != nil {
		out = append(out, r.Product)
	}
	return append(out, r.Departments...)
}

// Find is the owner by key or name (case-insensitive); nil if none.
func (r *Report) Find(name string) *Owner {
	for _, o := range r.Owners() {
		if strings.EqualFold(o.Key, name) || strings.EqualFold(o.Name, name) {
			return o
		}
	}
	return nil
}

// Goal is the goal by key; nil if none.
func (r *Report) Goal(key string) *Goal {
	for _, o := range r.Owners() {
		for _, g := range o.Goals {
			if strings.EqualFold(g.Key, key) {
				return g
			}
		}
	}
	return nil
}

// Keys lists every goal key, in report order.
func (r *Report) Keys() []string {
	var out []string
	for _, o := range r.Owners() {
		for _, g := range o.Goals {
			out = append(out, g.Key)
		}
	}
	return out
}

// DeptKey is a department's owner key: its slugified name.
func DeptKey(name string) string { return tracker.Slugify(name) }

// ValuesPath is the values log: with the work files (under the issues
// folder), so a private tracker keeps business numbers private too.
func ValuesPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".goals", "values.jsonl")
}

// Append adds one entry to the values log.
func Append(p *tracker.Project, e Entry) error {
	path := ValuesPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readLog is the values log by goal key (lower case), oldest first. A
// missing log is empty; a bad line is skipped.
func readLog(p *tracker.Project) map[string][]Entry {
	out := map[string][]Entry{}
	f, err := os.Open(ValuesPath(p))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Goal == "" {
			continue
		}
		k := strings.ToLower(e.Goal)
		out[k] = append(out[k], e)
	}
	return out
}

// Resolve reads every goal's value and rolls the department goals up into
// their group's, then group and department goals into HQ's. It never fails:
// a source it cannot read sets the goal's Err.
func Resolve(p *tracker.Project, now time.Time) *Report {
	log := readLog(p)
	rep := &Report{Departments: []*Owner{}}
	if c := p.Company; c != nil {
		rep.HQ = newOwner(p, HQKey, "HQ", c.Mission, "", c.Prompt, c.Goals, log, now)
	}
	if d := p.Product; d != nil {
		rep.Product = newOwner(p, ProductKey, tracker.ProductName, d.Mission, d.Role, d.Prompt, d.Goals, log, now)
	}
	for _, d := range p.Departments {
		o := newOwner(p, DeptKey(d.Name), d.Name, d.Mission, d.Role, d.Prompt, d.Goals, log, now)
		if g := p.GroupOf(d.Name); g != nil {
			o.Parent = DeptKey(g.Name)
		}
		rep.Departments = append(rep.Departments, o)
	}
	rollUp(rep)
	return rep
}

func newOwner(p *tracker.Project, key, name, mission, role, prompt string, cfg []tracker.Goal, log map[string][]Entry, now time.Time) *Owner {
	o := &Owner{Key: key, Name: name, Mission: mission, Role: role, Prompt: prompt, Goals: []*Goal{}}
	for _, c := range cfg {
		g := &Goal{
			Key: key + "/" + c.ID, Owner: name, Config: c, ID: c.ID, Name: c.Name, Target: c.Target,
			Unit: c.Unit, Period: strings.ToLower(c.Period), Source: c.Source, RollsUp: c.RollsUp,
		}
		if g.Source == "" {
			g.Source = "manual"
		}
		fill(p, g, log[strings.ToLower(g.Key)], now)
		o.Goals = append(o.Goals, g)
	}
	return o
}

// fill sets the goal's value, staleness and progress from its source.
func fill(p *tracker.Project, g *Goal, entries []Entry, now time.Time) {
	switch {
	case isManual(g.Source):
		start := PeriodStart(g.Period, now)
		// The newest by date (log order breaks ties); stale when it is from
		// before the current period.
		sorted := append([]Entry(nil), entries...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
		if len(sorted) > 0 {
			e := sorted[len(sorted)-1]
			v := e.Value
			g.Value, g.Date, g.Note = &v, e.Date, e.Note
		}
		if g.Value != nil && !start.IsZero() {
			if d, err := time.ParseInLocation("2006-01-02", g.Date, now.Location()); err == nil && d.Before(start) {
				g.Stale = true
			}
		}
	case strings.HasPrefix(g.Source, "file:"):
		v, date, err := readFile(p, strings.TrimPrefix(g.Source, "file:"))
		if err != nil {
			g.Err = err.Error()
			return
		}
		g.Value, g.Date = &v, date
		if start := PeriodStart(g.Period, now); !start.IsZero() && date != "" {
			if d, err := time.ParseInLocation("2006-01-02", date, now.Location()); err == nil && d.Before(start) {
				g.Stale = true
			}
		}
	default:
		g.Err = fmt.Sprintf("unknown source %q (manual or file:<path>#<metric id>)", g.Source)
		return
	}
	if g.Value != nil && g.Target != nil {
		pr := progress(*g.Value, *g.Target, g.Config.Direction)
		g.Progress = &pr
	}
}

func isManual(source string) bool { return source == "" || strings.EqualFold(source, "manual") }

// progress is value/target (up) or target/value (down), 0–100.
func progress(value, target float64, direction string) int {
	var f float64
	if strings.EqualFold(direction, "down") {
		switch {
		case value <= target:
			f = 1
		case value > 0:
			f = target / value
		}
	} else if target != 0 {
		f = value / target
	} else if value >= 0 {
		f = 1
	}
	return int(math.Round(math.Max(0, math.Min(1, f)) * 100))
}

// PeriodStart is the start of the current week (Monday), month or
// quarter; zero for none.
func PeriodStart(period string, now time.Time) time.Time {
	y, m, d := now.Date()
	loc := now.Location()
	switch strings.ToLower(period) {
	case "week":
		wd := (int(now.Weekday()) + 6) % 7
		return time.Date(y, m, d-wd, 0, 0, 0, 0, loc)
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, loc)
	case "quarter":
		return time.Date(y, m-(m-1)%3, 1, 0, 0, 0, 0, loc)
	}
	return time.Time{}
}

// metricFile is the part of the metric run format a goal reads.
type metricFile struct {
	Run *struct {
		Date string `json:"date"`
	} `json:"run"`
	Date    string `json:"date"`
	Metrics []struct {
		ID    string          `json:"id"`
		Value json.RawMessage `json:"value"`
	} `json:"metrics"`
}

// ParseFileSource splits <path>#<metric id>.
func ParseFileSource(spec string) (path, id string, err error) {
	path, id, ok := strings.Cut(spec, "#")
	if !ok || strings.TrimSpace(path) == "" || strings.TrimSpace(id) == "" {
		return "", "", fmt.Errorf("source file:%s: want file:<path>#<metric id>", spec)
	}
	return path, id, nil
}

// readFile reads metric id's value from a JSON file in the metric run
// format, relative to the project root.
func readFile(p *tracker.Project, spec string) (float64, string, error) {
	path, id, err := ParseFileSource(spec)
	if err != nil {
		return 0, "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Root(), path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, "", fmt.Errorf("reading %s: %v", path, err)
	}
	var mf metricFile
	if err := json.Unmarshal(raw, &mf); err != nil {
		return 0, "", fmt.Errorf("parsing %s: %v", path, err)
	}
	date := mf.Date
	if mf.Run != nil && mf.Run.Date != "" {
		date = mf.Run.Date
	}
	for _, m := range mf.Metrics {
		if m.ID != id {
			continue
		}
		var v float64
		if err := json.Unmarshal(m.Value, &v); err != nil {
			return 0, "", fmt.Errorf("%s: metric %q is not a number (%s)", path, id, string(m.Value))
		}
		return v, date, nil
	}
	return 0, "", fmt.Errorf("%s: no metric %q", path, id)
}

// rollUp hangs every goal of a department under a group on the group goal
// its rolls_up: names, level by level up nested groups, then every other goal of Product and the departments
// (groups' included) under the HQ goal it names. A parent goal with no
// value of its own gets the mean of its children's progress, so a group
// goal averaged from its departments counts in HQ's mean like any other.
func rollUp(rep *Report) {
	hung := map[*Goal]bool{}
	// Deepest first, so a nested group's goals are averaged from its
	// departments before they hang on its own group's goal
	// (factory/nested-groups-…#D-1). depth is bounded: Parent is set only
	// for a good chain.
	depth := func(o *Owner) int {
		n := 0
		for cur := o; cur != nil && cur.Parent != "" && n <= len(rep.Departments); n++ {
			cur = rep.Find(cur.Parent)
		}
		return n
	}
	order := append([]*Owner{}, rep.Departments...)
	sort.SliceStable(order, func(i, j int) bool { return depth(order[i]) > depth(order[j]) })
	for _, o := range order {
		average(o.Goals)
		if o.Parent == "" {
			continue
		}
		group := rep.Find(o.Parent)
		if group == nil {
			continue
		}
		for _, g := range o.Goals {
			if parent := goalByID(group.Goals, g.RollsUp); parent != nil {
				parent.Children = append(parent.Children, g)
				hung[g] = true
			}
		}
	}
	if rep.HQ == nil {
		return
	}
	var others []*Owner
	if rep.Product != nil {
		others = append(others, rep.Product)
	}
	others = append(others, rep.Departments...)
	for _, o := range others {
		for _, g := range o.Goals {
			if parent := goalByID(rep.HQ.Goals, g.RollsUp); parent != nil && !hung[g] {
				parent.Children = append(parent.Children, g)
			}
		}
	}
	average(rep.HQ.Goals)
}

// goalByID is the goal of gs with id (any case); nil for "" or none.
func goalByID(gs []*Goal, id string) *Goal {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	for _, g := range gs {
		if strings.EqualFold(g.ID, id) {
			return g
		}
	}
	return nil
}

// average gives every goal of gs with children and no value of its own the
// mean of its children's progress.
func average(gs []*Goal) {
	for _, g := range gs {
		if g.Value != nil || g.Err != "" || len(g.Children) == 0 {
			continue
		}
		sum, n := 0, 0
		for _, c := range g.Children {
			if c.Progress != nil {
				sum += *c.Progress
				n++
			}
		}
		if n > 0 {
			pr := int(math.Round(float64(sum) / float64(n)))
			g.Progress, g.RolledUp, g.RolledFrom = &pr, true, n
		}
	}
}

// History is the values log by goal key (lower case), oldest first in log
// order: what the week plan reads to say how far a goal moved.
func History(p *tracker.Project) map[string][]Entry { return readLog(p) }

// EntryTime parses a values-log date (RFC3339 or a day); false when it
// doesn't parse.
func EntryTime(date string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(date), time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
