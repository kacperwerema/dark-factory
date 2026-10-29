package main

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/chart"
	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/lint"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The hall's Health page (/p/<project>/health), L2 of the Workshop Board:
// the score from measurements and the weight behind it, the agent opinion,
// every sensor on its good → bad scale, where to look, the trends and the
// measurement setup. Every number comes from the health snapshots, the
// newest opinion and a live issue-cli health lint, as on the classic page
// (?classic=1, health.html); an element without data says what it lacks and
// how to get it. Design: issues/Factory/designs/workshop-board/L2-health.
// Components: templates/_workshop_health.html.

// healthSensorGroups puts each score component in a group of the page: the
// code's shape, then the tests. A component missing here goes to Code.
var healthSensorGroups = []struct {
	Name       string
	Components []string
}{
	{"Code", []string{"big_files", "long_funcs", "hotspots", "lint"}},
	{"Tests", []string{"unit_wait", "unit_cost_trend", "e2e_wait", "e2e_coverage", "flaky", "test_balance"}},
}

// healthUnlock is how to give a component data, for the "shortest way"
// hint: the next step after its Missing reason.
var healthUnlock = map[string]string{
	"unit_wait":       `A ci.run command needs a results: entry of kind unit (docs/workbench-files.md, "Test results").`,
	"unit_cost_trend": "It compares with the unit results of 30 days ago: it unlocks 30 days after unit results first land.",
	"e2e_wait":        `Run the slow tier, issue-cli ci run --tier slow, with a results: entry of kind e2e (docs/workbench-files.md, "Test results").`,
	"e2e_coverage":    "Playtest sessions under playtest/sessions/ and done issues with a system: field.",
	"flaky":           "It needs the playtest telemetry (health-check-speed).",
	"test_balance":    "It unlocks once the code grows within 30 days.",
	"big_files":       "Take a new measurement: issue-cli health snapshot.",
	"long_funcs":      "Take a new measurement: issue-cli health snapshot.",
	"hotspots":        "It needs commits in the last 30 days, then issue-cli health snapshot.",
	"lint":            "Take a new measurement: issue-cli health snapshot.",
}

// healthWaits are the sensors only time unlocks (a 30-day comparison).
var healthWaits = map[string]bool{"unit_cost_trend": true, "test_balance": true}

// healthLookRows is how many rows each "Where to look" list shows.
const healthLookRows = 5

// HealthWbPage is the whole page.
type HealthWbPage struct {
	Header      WbHeader
	Project     string
	Prefix      string
	Latest      string
	Snapshots   int
	Ranges      []statsOption
	RangeLabel  string
	ClassicURL  string
	Flash       string
	FlashErr    bool
	Score       WbHealthScore
	Opinion     WbHealthOpinion
	Groups      []WbSensorGroup
	Look        WbHealthLook
	TrendsLabel string
	Trends      []WbTrendCard
	Setup       WbHealthSetup
}

// WbHallTabs is the hall's view toggle in the header: Line | Report |
// Health.
type WbHallTabs struct {
	Items []WbTab
}

// WbTab is one view in a toggle; the current one has no link.
type WbTab struct {
	Key, Label, URL string
	Current         bool
}

// wbHallTabs is the toggle with current ("line", "report" or "health") on.
func wbHallTabs(prefix, current string) *WbHallTabs {
	t := &WbHallTabs{Items: []WbTab{
		{Key: "line", Label: "Line", URL: prefix + "/hall"},
		{Key: "report", Label: "Report", URL: prefix + "/status"},
		{Key: "health", Label: "Health", URL: prefix + "/health"},
	}}
	for i := range t.Items {
		t.Items[i].Current = t.Items[i].Key == current
	}
	return t
}

// WbHealthScore is the "Score from measurements" card.
type WbHealthScore struct {
	// Has: the derived score exists; Value is it ("89"), else the share of
	// the weight measured ("36%").
	Has         bool
	Value       string
	Caption     string
	Note        string
	Change      string
	Bar         WbWeightBar
	Hint        *WbHealthHint
	Empty       string
	Measured    int
	Sensors     int
	CoveragePct int
}

// WbWeightBar is the score's weight as a bar: one segment per sensor, width
// its weight, coloured by its sub-score or hatched without data; Threshold
// marks the share that must be measured for a score.
type WbWeightBar struct {
	Segs      []WbWeightSeg
	Threshold int
	Label     string
}

// WbWeightSeg is one sensor on the bar. Tone is good, medium, bad or none.
type WbWeightSeg struct {
	Name, Tone, Title string
	Weight            float64
}

// WbHealthHint is the shortest way to (more of) a score: the unmeasured
// sensor with the most weight, why it has no data and how to get it.
type WbHealthHint struct {
	Eyebrow, Sensor, Why, How string
	Weight                    float64
	Reach                     int
	Give                      WbGiveAgent
}

// WbGiveAgent hands a task to the project's CEO: the text is pasted into
// the lead's session (POST lead/send), only when the lead runs.
type WbGiveAgent struct {
	Label, Text, SendURL, Session string
	Live                          bool
}

// WbHealthOpinion is the "Agent opinion" card.
type WbHealthOpinion struct {
	Robot WbRobot
	// Session is the review agent's tmux session; Live while it runs.
	Session, StateText, StateTone string
	Live                          bool
	AttachCmd                     string
	Has                           bool
	Score, Age, Context           string
	Stale                         bool
	Assessment, Empty             string
	Recs                          []WbHealthRec
	Decided                       int
	ReviewURL, Command            string
}

// WbHealthRec is one proposed recommendation with its actions.
type WbHealthRec struct {
	RecView
	FileURL, DismissURL string
}

// WbSensorGroup is a group of sensors (Code, Tests).
type WbSensorGroup struct {
	Name, Key, Weight string
	Tag               WbChip
	Rows              []WbSensor
}

// WbSensor is one score component: its value on the good → bad scale, its
// sub-score and weight, or why it has no data.
type WbSensor struct {
	Name, Help, Range string
	Has               bool
	Value, Good, Bad  string
	Pos               string // the marker, "37.5%"
	Score, Tone, Why  string
	Weight            string
}

// WbHealthLook is "Where to look": the latest snapshot's ranked lists, and
// a file on both the hotspots and the longest functions.
type WbHealthLook struct {
	Lists    []WbLookList
	Both     string
	Refactor *WbRefactor
	Empty    string
}

// WbLookList is one ranked list with bars scaled to its top row.
type WbLookList struct {
	Title, Sub string
	Rows       []WbLookRow
}

// WbLookRow is one entry; Hot when its file is on both lists.
type WbLookRow struct {
	Label, Value string
	Pct          int
	Hot          bool
}

// WbRefactor files a refactor issue for a file on both lists (POST
// issues/create): the owner's click is the decision.
type WbRefactor struct {
	Path, Base, CreateURL, Title, Body string
}

// WbTrendCard is one trend: its chart, or locked with what it lacks and the
// command that unlocks it.
type WbTrendCard struct {
	Key, Title string
	Locked     bool
	Need, Cmd  string
	Summary    string
	Charts     []template.HTML
	Legend     []LegendItem
}

// WbHealthSetup is "Measurement setup": the newest snapshot's family
// warnings and issue-cli health lint run live.
type WbHealthSetup struct {
	Warnings []HealthWarning
	Lint     *LintPanel
	// Rows are the first lint findings, More the rest's count.
	Rows []lint.Finding
	More int
}

// healthSetupRows is how many lint findings the setup lists.
const healthSetupRows = 5

// healthWbIn is what the page reads besides the classic page's model.
type healthWbIn struct {
	healthIn
	Header   WbHeader
	CEO      WbMessageBox
	Snapshot *health.Snapshot
	Opinion  *health.Opinion
	Review   string // the review agent's session
	Live     bool
	Attach   string
}

func buildHealthWb(in healthWbIn, classic *HealthPage) *HealthWbPage {
	p := &HealthWbPage{Header: in.Header, Project: in.Proj.Slug, Prefix: in.Prefix, Latest: healthWhen(in.Snapshot), Snapshots: len(in.Snaps),
		Ranges: classic.Ranges, RangeLabel: classic.RangeLabel, ClassicURL: in.Prefix + "/health?classic=1"}
	for _, r := range p.Ranges {
		if r.On && r.Key != "30d" {
			p.ClassicURL += "&range=" + r.Key
		}
	}
	var cfg *tracker.HealthConfig
	if in.WF != nil {
		cfg = in.WF.Health
	}
	sensors := healthSensors(in.Snapshot, cfg)
	p.Score = healthScoreCard(in.Snapshot, sensors, classic.Derived, in.CEO)
	p.Groups = healthSensorGroupsOf(sensors)
	p.Opinion = healthOpinionCard(in, classic.Review)
	p.Look = healthLook(in.Snapshot, in.Prefix)
	p.TrendsLabel = "Trends · " + classic.RangeLabel
	p.Trends = healthTrends(classic)
	p.Setup = WbHealthSetup{Warnings: classic.Warnings, Lint: classic.Lint}
	if l := classic.Lint; l != nil && l.Err == "" {
		p.Setup.Rows = l.Findings
		if len(p.Setup.Rows) > healthSetupRows {
			p.Setup.Rows = p.Setup.Rows[:healthSetupRows]
		}
		p.Setup.More = l.Warnings - len(p.Setup.Rows)
	}
	return p
}

// healthWhen is the snapshot's date in words: "29 Oct, 00:01".
func healthWhen(s *health.Snapshot) string {
	if s == nil {
		return ""
	}
	if t := s.Time(); !t.IsZero() {
		return t.Format("2 Jan, 15:04")
	}
	return s.Run.Date
}

// healthSensor is a component with what the snapshot says about it.
type healthSensor struct {
	health.Component
	Has        bool
	Sub, Value float64
}

// healthSensors are the components the snapshot weighed, in display order.
func healthSensors(s *health.Snapshot, cfg *tracker.HealthConfig) []healthSensor {
	if s == nil {
		return nil
	}
	var out []healthSensor
	for _, c := range health.Components(cfg) {
		l := map[string]string{"component": c.Name}
		w, ok := s.Value("score.component.weight", l)
		if !ok || w <= 0 {
			continue
		}
		c.Weight = w
		hs := healthSensor{Component: c}
		if sub, ok := s.Value("score.component", l); ok {
			hs.Has, hs.Sub = true, sub
			hs.Value, _ = s.Value("score.component.value", l)
		}
		out = append(out, hs)
	}
	return out
}

// healthTone is a sub-score's colour: good from 70, bad below 40.
func healthTone(sub float64) string {
	switch {
	case sub >= 70:
		return "good"
	case sub >= 40:
		return "medium"
	}
	return "bad"
}

func healthScoreCard(s *health.Snapshot, sensors []healthSensor, derived HealthGauge, ceo WbMessageBox) WbHealthScore {
	c := WbHealthScore{Bar: WbWeightBar{Threshold: 50}}
	if s == nil {
		c.Empty = "No measurement yet. The viewer takes one daily; or run issue-cli health snapshot."
		return c
	}
	if len(sensors) == 0 {
		c.Empty = "This measurement has no score rows (it predates the score). The next daily one adds them; or run issue-cli health snapshot."
		return c
	}
	total, measured := 0.0, 0.0
	for _, hs := range sensors {
		total += hs.Weight
		if hs.Has {
			measured += hs.Weight
			c.Measured++
		}
	}
	c.Sensors = len(sensors)
	cov := measured / total
	c.CoveragePct = int(math.Round(cov * 100))
	for _, hs := range sensors {
		if hs.Has {
			c.Bar.Segs = append(c.Bar.Segs, WbWeightSeg{Name: hs.Name, Tone: healthTone(hs.Sub), Weight: hs.Weight,
				Title: fmt.Sprintf("%s: %.0f (weight %s)", hs.Name, hs.Sub, trimFloat(hs.Weight))})
		}
	}
	for _, hs := range sensors {
		if !hs.Has {
			c.Bar.Segs = append(c.Bar.Segs, WbWeightSeg{Name: hs.Name, Tone: "none", Weight: hs.Weight,
				Title: fmt.Sprintf("%s: no data (weight %s)", hs.Name, trimFloat(hs.Weight))})
		}
	}
	c.Bar.Label = fmt.Sprintf("%d%% of the score's weight measured; a score needs 50%%", c.CoveragePct)
	sensorsText := fmt.Sprintf("%d of %d sensors", c.Measured, c.Sensors)
	if derived.Has {
		c.Has, c.Value, c.Change = true, derived.Value, derived.Change
		c.Caption = fmt.Sprintf("of 100 · from %d%% of the weight · %s", c.CoveragePct, sensorsText)
		c.Note = "Each sensor gives 100 at its good value and 0 at its bad one; sensors without data (hatched) are left out and the rest reweighted."
	} else {
		c.Value = fmt.Sprintf("%d%%", c.CoveragePct)
		c.Caption = "of the weight measured · " + sensorsText
		c.Note = "A score appears once 50% of the weight is measured. Hatched fields are sensors without data."
	}

	// The shortest way: the unmeasured sensor with the most weight.
	// Sensors that only time unlocks come last: there is nothing to do.
	var best *healthSensor
	for i := range sensors {
		hs := &sensors[i]
		if hs.Has {
			continue
		}
		if best == nil || healthWaits[best.Name] && !healthWaits[hs.Name] || healthWaits[best.Name] == healthWaits[hs.Name] && hs.Weight > best.Weight {
			best = hs
		}
	}
	if best != nil {
		h := &WbHealthHint{Sensor: best.Name, Weight: best.Weight, Why: upperFirst(best.Missing), How: healthUnlock[best.Name],
			Reach: int(math.Round((measured + best.Weight) / total * 100))}
		h.Eyebrow = "SHORTEST WAY TO A SCORE"
		if c.Has {
			h.Eyebrow = "BIGGEST GAP IN THE SCORE"
		}
		h.Give = WbGiveAgent{Label: "Give it to an agent", SendURL: ceo.SendURL, Session: ceo.Session, Live: ceo.Live,
			Text: fmt.Sprintf("Health: give the %s sensor data (weight %s, now: %s). %s Then take a snapshot: issue-cli health snapshot. How the score counts: docs/workbench-files.md, \"Scores\".",
				best.Name, trimFloat(best.Weight), best.Missing, h.How)}
		c.Hint = h
	}
	return c
}

func trimFloat(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func healthSensorGroupsOf(sensors []healthSensor) []WbSensorGroup {
	if len(sensors) == 0 {
		return nil
	}
	groupOf := map[string]int{}
	for i, g := range healthSensorGroups {
		for _, name := range g.Components {
			groupOf[name] = i
		}
	}
	total := 0.0
	for _, hs := range sensors {
		total += hs.Weight
	}
	groups := make([]WbSensorGroup, len(healthSensorGroups))
	weights := make([]float64, len(groups))
	measured := make([]int, len(groups))
	for i, g := range healthSensorGroups {
		groups[i] = WbSensorGroup{Name: g.Name, Key: strings.ToLower(g.Name)}
	}
	for _, hs := range sensors {
		i := groupOf[hs.Name]
		weights[i] += hs.Weight
		groups[i].Rows = append(groups[i].Rows, healthSensorRow(hs))
		if hs.Has {
			measured[i]++
		}
	}
	var out []WbSensorGroup
	for i, g := range groups {
		if len(g.Rows) == 0 {
			continue
		}
		n := len(g.Rows)
		switch measured[i] {
		case n:
			g.Tag = WbChip{Tone: "green", Text: fmt.Sprintf("%d/%d measured", n, n)}
		case 0:
			g.Tag = WbChip{Tone: "idle", Text: fmt.Sprintf("0/%d — no data", n)}
		default:
			g.Tag = WbChip{Tone: "idle", Text: fmt.Sprintf("%d/%d measured", measured[i], n)}
		}
		g.Weight = fmt.Sprintf("weight %s of %s", trimFloat(weights[i]), trimFloat(total))
		out = append(out, g)
	}
	return out
}

func healthSensorRow(hs healthSensor) WbSensor {
	r := WbSensor{Name: hs.Name, Help: hs.Help, Weight: trimFloat(hs.Weight),
		Good: health.FormatValue(hs.Unit, hs.Good), Bad: health.FormatValue(hs.Unit, hs.Bad)}
	r.Range = r.Good + " → " + r.Bad
	if !hs.Has {
		r.Score, r.Tone, r.Why = "—", "none", upperFirst(hs.Missing)+"."
		return r
	}
	r.Has = true
	r.Value = health.FormatValue(hs.Unit, hs.Value)
	f := 0.0
	if hs.Bad != hs.Good {
		f = math.Max(0, math.Min(1, (hs.Value-hs.Good)/(hs.Bad-hs.Good)))
	}
	r.Pos = fmt.Sprintf("%.1f%%", 4+f*92)
	r.Score, r.Tone = fmt.Sprintf("%.0f", hs.Sub), healthTone(hs.Sub)
	return r
}

func healthOpinionCard(in healthWbIn, review *ReviewView) WbHealthOpinion {
	o := WbHealthOpinion{Session: in.Review, Live: in.Live, ReviewURL: in.Prefix + "/health/review",
		Command: "issue-cli --project " + in.Proj.Slug + " health review", Robot: WbRobot{State: "idle", Label: in.Review}}
	if in.Live {
		o.Robot.State, o.StateText, o.StateTone, o.AttachCmd = "working", "reviewing the code", "green", in.Attach
	}
	if in.Opinion == nil {
		if in.Live {
			o.Empty = "The opinion shows here once the agent records it. It reads the same rows as \"Where to look\" below."
		} else {
			o.Empty = "No review yet. A review agent reads the latest measurement, scores the project 0–100 and proposes what to change."
			o.StateText, o.StateTone = "not running", "idle"
		}
		return o
	}
	op := in.Opinion
	o.Has, o.Score = true, fmt.Sprintf("%d", op.Score)
	o.Age = reviewedAgo(op, in.Now)
	o.Stale = op.AgeDays(in.Now) > float64(health.ReviewStaleDays)
	if op.Derived != nil {
		o.Context = fmt.Sprintf("the score from measurements was %.0f then", *op.Derived)
	}
	o.Assessment = op.Assessment
	if !in.Live {
		o.StateText, o.StateTone = o.Age, "idle"
		if o.Stale {
			o.StateTone = "you"
		}
	}
	if review != nil {
		for _, r := range review.Recs {
			if !r.Proposed {
				o.Decided++
				continue
			}
			o.Recs = append(o.Recs, WbHealthRec{RecView: r, FileURL: in.Prefix + "/health/recommendation/" + r.ID + "/file",
				DismissURL: in.Prefix + "/health/recommendation/" + r.ID + "/dismiss"})
		}
	}
	return o
}

func healthLook(s *health.Snapshot, prefix string) WbHealthLook {
	var l WbHealthLook
	if s == nil {
		l.Empty = "No measurement yet: issue-cli health snapshot."
		return l
	}
	hot := sortedRows(s, "shape.hotspot")
	long := sortedRows(s, "shape.long_func.lines")
	onHot := map[string]bool{}
	for i, r := range hot {
		if i == healthLookRows {
			break
		}
		onHot[r.Labels["path"]] = true
	}
	both := map[string]bool{}
	for i, r := range long {
		if i == healthLookRows {
			break
		}
		if p := r.Labels["path"]; onHot[p] {
			both[p] = true
			if l.Both == "" {
				l.Both = p
			}
		}
	}
	count := func(v float64) string { return chart.Format(chart.Count, v) }
	lines := func(v float64) string { return chart.Format(chart.Count, v) + " lines" }
	secs := func(v float64) string { return fmt.Sprintf("%.1f s", v) }
	add := func(title, sub string, rows []health.Row, label func(health.Row) string, hotOf func(health.Row) bool, format func(float64) string) {
		if len(rows) == 0 {
			return
		}
		list := WbLookList{Title: title, Sub: sub}
		top := rows[0].Value
		for i, r := range rows {
			if i == healthLookRows {
				break
			}
			pct := 100
			if top > 0 {
				pct = int(math.Max(3, math.Round(r.Value/top*100)))
			}
			list.Rows = append(list.Rows, WbLookRow{Label: label(r), Value: format(r.Value), Pct: pct, Hot: hotOf != nil && hotOf(r)})
		}
		l.Lists = append(l.Lists, list)
	}
	inBoth := func(r health.Row) bool { return both[r.Labels["path"]] }
	add("Hotspots", "big files changed in 5+ commits in 30 days · lines × commits", hot,
		func(r health.Row) string { return r.Labels["path"] }, inBoth, count)
	add("Longest functions", "code, not tests", long,
		func(r health.Row) string { return r.Labels["path"] + " · " + r.Labels["func"] }, inBoth, lines)
	add("Slowest tests", "from the newest CI run with results", sortedRows(s, "tests.slowest.seconds"),
		func(r health.Row) string { return r.Labels["test"] }, nil, secs)
	add("Slowest parts", "a package, test class or session: the wall clock's critical path", sortedRows(s, "tests.part.seconds"),
		func(r health.Row) string {
			if p := r.Labels["part"]; p != "." {
				return p
			}
			return ". (the root package)"
		}, nil, secs)
	add("Systems without a playtest", "issues done in 90 days", sortedRows(s, "tests.e2e.uncovered"),
		func(r health.Row) string { return r.Labels["system"] }, nil, func(v float64) string { return chart.Format(chart.Count, v) + " done" })
	if len(l.Lists) == 0 {
		l.Empty = "The latest measurement has no hotspots, functions or test timings to rank."
	}
	if l.Both != "" {
		var hotV, longV float64
		var funcs []string
		for _, r := range hot {
			if r.Labels["path"] == l.Both {
				hotV = r.Value
			}
		}
		for _, r := range long {
			if r.Labels["path"] == l.Both {
				if longV == 0 {
					longV = r.Value
				}
				funcs = append(funcs, fmt.Sprintf("%s (%s lines)", r.Labels["func"], chart.Format(chart.Count, r.Value)))
			}
		}
		if len(funcs) > 3 {
			funcs = funcs[:3]
		}
		base := path.Base(l.Both)
		l.Refactor = &WbRefactor{Path: l.Both, Base: base, CreateURL: prefix + "/issues/create",
			Title: "Refactor " + l.Both,
			Body: fmt.Sprintf("## Why\n\n%s is on both lists of the Health page's \"Where to look\" (snapshot %s):\n\n- hotspot: %s (lines × commits in 30 days)\n- longest functions: %s\n\n## What\n\nSplit it so the file is smaller and its longest functions shorter; behaviour stays the same.\n",
				l.Both, s.Run.Date, chart.Format(chart.Count, hotV), strings.Join(funcs, ", "))}
	}
	return l
}

func sortedRows(s *health.Snapshot, metric string) []health.Row {
	rows := s.Rows(metric)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Value > rows[j].Value })
	return rows
}

// healthTrends are the classic page's charts as cards: locked with what each
// lacks and how to unlock it while it has nothing to draw.
func healthTrends(c *HealthPage) []WbTrendCard {
	scores := WbTrendCard{Key: "scores", Title: "Health score"}
	if c.Scores.Empty != "" {
		scores.Locked, scores.Need, scores.Cmd = true, "Measurements compute the score from now on. Compute it backwards to see the past days.", "issue-cli health backfill"
	} else {
		scores.Charts, scores.Legend = []template.HTML{c.Scores.SVG}, c.Scores.Legend
	}
	out := []WbTrendCard{scores}
	for _, pair := range c.Pairs {
		t := WbTrendCard{Key: pair.ID, Title: strings.Replace(pair.Title, ": count vs runtime", ": count and time", 1)}
		if pair.Empty != "" {
			t.Locked = true
			if strings.HasPrefix(pair.ID, "e2e") {
				t.Need, t.Cmd = "Needs a results: entry of kind e2e in ci.run. E2E runs only in the slow tier.", "issue-cli ci run --tier slow"
			} else {
				t.Need, t.Cmd = "Needs a results: entry of kind unit in ci.run (workflow.yaml).", "ci.run → results: kind unit"
			}
		} else {
			t.Summary = pair.Summary
			t.Charts = []template.HTML{pair.Count.SVG, pair.Runtime.SVG}
			t.Legend = append(append([]LegendItem{}, pair.Count.Legend...), pair.Runtime.Legend...)
		}
		out = append(out, t)
	}
	return out
}

// handleHealthWb renders the page; ?classic=1 keeps the classic one.
func (s *Server) handleHealthWb(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string, classic *HealthPage) {
	now := time.Now()
	company := s.buildCompany(proj, prefix, now)
	lot := company.Lot
	h := WbHeader{Crumbs: wbCrumbs(2, proj.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait,
		DoneWeek: lot.DoneWeek, Waiting: company.Waiting, HideDone: true, Tabs: wbHallTabs(prefix, "health")}
	h.Crumbs[1].Color = lot.Color
	h.Crumbs[2] = WbCrumb{Label: "Product hall", URL: prefix + "/hall", Color: companyProductColor}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		h.Chips = append(h.Chips, *c)
	}
	root := ciRootFor(proj)
	snaps := health.Range(root, time.Time{}, now)
	var latest *health.Snapshot
	if n := len(snaps); n > 0 {
		latest = snaps[n-1]
	}
	op, _ := health.LatestOpinion(root, time.Time{})
	session := "review-" + proj.Slug
	in := healthWbIn{
		healthIn: healthIn{Proj: proj, Prefix: prefix, Range: r.URL.Query().Get("range"), Snaps: snaps, Root: root, WF: proj.LoadWorkflow(), Now: now},
		Header:   h, CEO: company.Message, Snapshot: latest, Opinion: op, Review: session,
		Live: agentSessionLive(proj, session), Attach: agentAttachCmd(proj, session),
	}
	page := buildHealthWb(in, classic)
	page.Flash, page.FlashErr = classic.Flash, classic.FlashErr
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "health_wb.html", page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
