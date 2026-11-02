package viewer

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/chart"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Report's "Factory last night" card, status.md's Last night: line and
// the Needs you row for a pass-rate drop, all from the nights the nightly
// loop records (internal/bench nightly.go). Read only: the viewer never
// starts a night.

// WbNightly is the card; nil when no benchmark has a task in the project.
type WbNightly struct {
	Rows []WbNightlyRow
	// Cmd turns the nightly job's standing start on (the empty state).
	Cmd string
}

// WbNightlyRow is one benchmark's last night.
type WbNightlyRow struct {
	Name string
	// NoNight: no night recorded yet.
	NoNight bool
	Date    string
	Main    string
	Held    string
	Stop    string
	Numbers []WbNightlyNumber
	// Experiment is "experiment x: rejected" or "none queued".
	Experiment string
	ExpTone    string
	RunURL     string // the job's run issue
	RunCmd     string // issue-cli bench report <b>/<run>
	Line       string
}

// WbNightlyNumber is one number: last night, the night before, the mark
// and the 7-night sparkline.
type WbNightlyNumber struct {
	Key, Label  string
	Now, Before string
	Mark        string // better | same | worse | "" first night
	Spark       template.HTML
	Range       string
}

// nightlyBenchmarks are the benchmarks with a task in p.
func nightlyBenchmarks(p *tracker.Project) []string {
	var out []string
	for _, name := range bench.List() {
		b, err := bench.Load(name)
		if err != nil {
			continue
		}
		for _, t := range b.Tasks {
			if t.Project == p.Slug {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// nightlyJobKey is the project's job on builtin:nightly-improve, or the
// key the docs suggest.
func nightlyJobKey(p *tracker.Project) string {
	for _, r := range jobs.List(p) {
		if strings.TrimSpace(r.Job.Brief) == jobs.BuiltinPrefix+"nightly-improve" {
			return r.Key
		}
	}
	return "product/nightly-improve"
}

// reportNightly builds the card.
func reportNightly(p *tracker.Project, prefix string) *WbNightly {
	names := nightlyBenchmarks(p)
	if len(names) == 0 {
		return nil
	}
	card := &WbNightly{Cmd: tracker.InjectProject(p, fmt.Sprintf("issue-cli job auto on %s --quote \"<your words>\"", nightlyJobKey(p)))}
	for _, name := range names {
		card.Rows = append(card.Rows, nightlyRow(name, bench.MorningOf(name, bench.LoadNights(name)), prefix))
	}
	return card
}

func nightlyRow(name string, m *bench.Morning, prefix string) WbNightlyRow {
	row := WbNightlyRow{Name: name}
	if m == nil {
		row.NoNight = true
		return row
	}
	row.Line = m.Line()
	row.Date, row.Held = m.Last.Date, m.Last.Held
	if m.Last.JobRun != "" {
		row.RunURL = prefix + "/issue/" + m.Last.JobRun
	}
	switch e := m.Last.Experiment; {
	case e == nil:
		row.Experiment = "no experiment queued"
	default:
		row.Experiment = e.Line()
		switch {
		case e.Skipped:
			row.ExpTone = "idle"
		case e.Verdict == "promising":
			row.ExpTone = "green"
		case e.Verdict == "rejected":
			row.ExpTone = "you"
		}
	}
	n := m.Measured
	if n == nil {
		return row
	}
	row.Main = shortSHA(n.MainSHA)
	if n.Stop != "" && n.Stop != bench.StopDone {
		row.Stop = n.Stop
	}
	if n.Run != "" {
		row.RunCmd = fmt.Sprintf("issue-cli bench report %s/%s", name, n.Run)
	}
	var before *bench.Night
	if m.Before != nil {
		before = m.Before
	}
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }
	tok := func(v float64) string { return bench.TokensText(int64(v)) }
	add := func(key, label string, c bench.Change, now, prev string, f func(float64) string) {
		num := WbNightlyNumber{Key: key, Label: label, Now: now, Before: prev, Mark: c.Mark}
		if len(c.Trend) > 1 {
			num.Spark = chart.Spark{Label: label + ", last " + plural(len(c.Trend), "night", "nights"), Values: c.Trend, Class: "s-all"}.SVG()
			lo, hi := c.Trend[0], c.Trend[0]
			for _, v := range c.Trend {
				lo, hi = min(lo, v), max(hi, v)
			}
			num.Range = f(lo) + "–" + f(hi)
		}
		row.Numbers = append(row.Numbers, num)
	}
	prev := func(f func(bench.Night) string) string {
		if before == nil {
			return ""
		}
		return f(*before)
	}
	add("pass", "Pass rate", m.Pass, fmt.Sprintf("%d/%d", n.Passed, n.Scored), prev(func(x bench.Night) string { return fmt.Sprintf("%d/%d", x.Passed, x.Scored) }), pct)
	add("time", "Median time per task", m.Wall, bench.Duration(n.MedianWallSec), prev(func(x bench.Night) string { return bench.Duration(x.MedianWallSec) }), bench.Duration)
	tokNow := "unknown"
	if n.TokensKnown > 0 {
		tokNow = bench.TokensText(n.MedianTokens)
	}
	add("tokens", "Median tokens per task", m.Tokens, tokNow, prev(func(x bench.Night) string { return bench.TokensText(x.MedianTokens) }), tok)
	return row
}

// nightlyStatusLine is status.md's Last night: line, one per benchmark
// with nights; "" without.
func nightlyStatusLine(p *tracker.Project) string {
	var parts []string
	for _, name := range nightlyBenchmarks(p) {
		if m := bench.MorningOf(name, bench.LoadNights(name)); m != nil {
			parts = append(parts, name+" "+m.Last.Date+": "+m.Line())
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Last night: " + strings.Join(parts, "; ")
}

// nightlyNeedsYou is a row per benchmark whose last measured night's pass
// rate is under the median of the week before it (at least 3 nights).
func nightlyNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	var rows []WbQueueCard
	for _, name := range nightlyBenchmarks(p) {
		r := bench.RegressionOf(bench.LoadNights(name))
		if r == nil {
			continue
		}
		url := prefix + "/status#nightly"
		if r.Night.JobRun != "" {
			url = prefix + "/issue/" + r.Night.JobRun
		}
		rows = append(rows, WbQueueCard{
			Kind: "nightly", Label: "Factory pass rate", Tone: "red",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: name + "/" + r.Night.Run,
			Note: fmt.Sprintf("Last night (%s) %s passed %.0f%%, under last week's median %.0f%% (%d nights). Read: issue-cli bench report %s/%s.",
				r.Night.Date, name, r.Night.PassRate*100, r.Median*100, r.Nights, name, r.Night.Run),
			Since: r.Night.At, Age: shortDuration(now.Sub(r.Night.At)),
			CTA: "Open the night", URL: url,
		})
	}
	return rows
}
