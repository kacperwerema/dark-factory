package bench

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
)

// The nightly loop (factory/nightly-self-improvement-loop-…): every night
// the benchmark runs on main's head, at most one queued experiment runs
// against that baseline, and one Night line records the morning numbers.
// Nights sit with the runs in the owner's bench folder, outside every repo.

// Night is one night of one benchmark: the baseline run on main and its
// numbers, or why it was held.
type Night struct {
	Date      string    `json:"date"` // local date the night started, 2026-11-01
	At        time.Time `json:"at"`
	Benchmark string    `json:"benchmark"`
	Project   string    `json:"project,omitempty"` // the project whose job ran it
	JobRun    string    `json:"job_run,omitempty"` // the run issue's slug
	MainSHA   string    `json:"main_sha,omitempty"`
	// Held is why nothing ran (memory, load, mem-guard); the numbers are
	// then zero and not compared.
	Held string `json:"held,omitempty"`
	Run  string `json:"run,omitempty"`
	Stop string `json:"stop,omitempty"`
	// Numbers over the run's non-holdout, scored tasks.
	PassRate      float64 `json:"pass_rate"`
	Passed        int     `json:"passed"`
	Scored        int     `json:"scored"`
	MedianWallSec float64 `json:"median_wall_sec"`
	// MedianTokens is over the TokensKnown tasks whose usage was read.
	MedianTokens int64   `json:"median_tokens"`
	TokensKnown  int     `json:"tokens_known"`
	Holdout      Holdout `json:"holdout"`
	// Experiment is the queued experiment that ran (or was skipped).
	Experiment *NightExperiment `json:"experiment,omitempty"`
}

// NightExperiment is the night's experiment: its slug, the candidate run
// and the verdict, or Skipped with why. Never adopted here.
type NightExperiment struct {
	Slug    string `json:"slug"`
	Run     string `json:"run,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Why     string `json:"why,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// Ran reports a night that measured something (not held, a run with
// scored tasks).
func (n Night) Ran() bool { return n.Held == "" && n.Run != "" && n.Scored > 0 }

// NightsPath is benchmark's nights log, next to its runs.
func NightsPath(benchmark string) string { return filepath.Join(RunsDir(benchmark), "nights.jsonl") }

// NightReportPath is a night's morning report.
func NightReportPath(benchmark, date string) string {
	return filepath.Join(RunsDir(benchmark), "nights", date+".md")
}

// LoadNights reads benchmark's nights, oldest first; bad lines are skipped.
func LoadNights(benchmark string) []Night {
	f, err := os.Open(NightsPath(benchmark))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Night
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var n Night
		if json.Unmarshal([]byte(strings.TrimSpace(sc.Text())), &n) == nil && n.Date != "" {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// AppendNight adds n to benchmark's nights log in one write.
func AppendNight(n Night) error {
	path := NightsPath(n.Benchmark)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(n)
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

// NightOn is benchmark's night that ran on date, if any (a held night
// doesn't count: the night may be tried again).
func NightOn(nights []Night, date string) (Night, bool) {
	for i := len(nights) - 1; i >= 0; i-- {
		if nights[i].Date == date && nights[i].Held == "" {
			return nights[i], true
		}
	}
	return Night{}, false
}

// FillNight sets n's numbers from run r: pass rate as its summary, median
// wall time and median tokens per scored task.
func FillNight(n *Night, r *Run) {
	n.Run, n.Stop = r.ID, r.Stop
	n.PassRate, n.Passed, n.Scored = r.Summary.PassRate, r.Summary.Passed, r.Summary.Scored
	n.Holdout = r.Holdout
	var walls []float64
	var tokens []float64
	for _, t := range r.Tasks {
		if t.Holdout || t.Outcome == OutcomeBroken {
			continue
		}
		walls = append(walls, t.WallSec)
		if t.Usage.Known {
			tokens = append(tokens, float64(t.Usage.Tokens()))
		}
	}
	n.MedianWallSec = median(walls)
	n.MedianTokens = int64(math.Round(median(tokens)))
	n.TokensKnown = len(tokens)
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// Marks of a number against last night.
const (
	Better = "better"
	Same   = "same"
	Worse  = "worse"
)

// Thresholds of "same", as bench compare's verdict per workflow (D-5, D-9):
// the pass rate moves less than one task, tokens less than 10%, wall time
// less than 25%.
const (
	SameTokens = 0.10
	SameWall   = 0.25
)

// Change is one number, last night against the night before, with the
// 7-night trend (oldest first, held nights left out).
type Change struct {
	Now, Before float64
	Mark        string // better | same | worse; "" with no night before
	Trend       []float64
}

// Morning is a benchmark's last night against the one before.
type Morning struct {
	Benchmark string
	// Last is the newest night (may be held); Measured the newest that
	// ran, Before the one before it.
	Last     Night
	Measured *Night
	Before   *Night
	Pass     Change
	Wall     Change
	Tokens   Change
}

// MorningOf compares benchmark's nights; nil with no nights.
func MorningOf(benchmark string, nights []Night) *Morning {
	if len(nights) == 0 {
		return nil
	}
	m := &Morning{Benchmark: benchmark, Last: nights[len(nights)-1]}
	var ran []Night
	for _, n := range nights {
		if n.Ran() {
			ran = append(ran, n)
		}
	}
	if len(ran) == 0 {
		return m
	}
	cur := ran[len(ran)-1]
	m.Measured = &cur
	if len(ran) > 1 {
		b := ran[len(ran)-2]
		m.Before = &b
	}
	week := ran
	if len(week) > 7 {
		week = week[len(week)-7:]
	}
	for _, n := range week {
		m.Pass.Trend = append(m.Pass.Trend, n.PassRate)
		m.Wall.Trend = append(m.Wall.Trend, n.MedianWallSec)
		m.Tokens.Trend = append(m.Tokens.Trend, float64(n.MedianTokens))
	}
	m.Pass.Now, m.Wall.Now, m.Tokens.Now = cur.PassRate, cur.MedianWallSec, float64(cur.MedianTokens)
	if b := m.Before; b != nil {
		m.Pass.Before, m.Wall.Before, m.Tokens.Before = b.PassRate, b.MedianWallSec, float64(b.MedianTokens)
		m.Pass.Mark = passMark(cur, *b)
		m.Wall.Mark = relMark(cur.MedianWallSec, b.MedianWallSec, SameWall)
		if cur.TokensKnown > 0 && b.TokensKnown > 0 {
			m.Tokens.Mark = relMark(float64(cur.MedianTokens), float64(b.MedianTokens), SameTokens)
		}
	}
	return m
}

// passMark: a move of less than one task's worth (1/scored of the
// smaller run) is the same.
func passMark(cur, before Night) string {
	n := cur.Scored
	if before.Scored > n {
		n = before.Scored
	}
	d := cur.PassRate - before.PassRate
	if n == 0 || math.Abs(d) < 1/float64(n)-1e-9 {
		return Same
	}
	if d > 0 {
		return Better
	}
	return Worse
}

// relMark: lower is better; within frac of before is the same.
func relMark(now, before, frac float64) string {
	if before <= 0 {
		if now <= 0 {
			return Same
		}
		return Worse
	}
	d := (now - before) / before
	switch {
	case math.Abs(d) < frac:
		return Same
	case d < 0:
		return Better
	}
	return Worse
}

// MinRegressionNights is how many nights the week before must have before
// a drop raises a Needs you row (Q6).
const MinRegressionNights = 3

// Regression is last night's pass rate under the median of the 7 days
// before it, with at least MinRegressionNights nights there.
type Regression struct {
	Night  Night
	Median float64
	Nights int
}

// RegressionOf is the regression of benchmark's newest measured night, or
// nil.
func RegressionOf(nights []Night) *Regression {
	var ran []Night
	for _, n := range nights {
		if n.Ran() {
			ran = append(ran, n)
		}
	}
	if len(ran) < MinRegressionNights+1 {
		return nil
	}
	last := ran[len(ran)-1]
	from := last.At.AddDate(0, 0, -7)
	var week []float64
	for _, n := range ran[:len(ran)-1] {
		if !n.At.Before(from) && n.Date != last.Date {
			week = append(week, n.PassRate)
		}
	}
	if len(week) < MinRegressionNights {
		return nil
	}
	med := median(week)
	if last.PassRate < med-1e-9 {
		return &Regression{Night: last, Median: med, Nights: len(week)}
	}
	return nil
}

// Duration is seconds as "11m" or "1h05m".
func Duration(sec float64) string {
	d := time.Duration(sec * float64(time.Second)).Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// TokensText is a token count as "14.2M" or "850k".
func TokensText(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// Line is the morning in one line, for status.md and the report comment:
// "pass 7/8 (same) · median 11m (better) · 14.2M tokens (worse) ·
// experiment x: rejected".
func (m *Morning) Line() string {
	if m == nil {
		return ""
	}
	var parts []string
	if m.Last.Held != "" {
		parts = append(parts, m.Last.Date+" held: "+m.Last.Held)
	}
	if n := m.Measured; n != nil {
		if m.Last.Held != "" {
			parts = append(parts, "last measured "+n.Date+":")
		}
		parts = append(parts,
			fmt.Sprintf("pass %d/%d%s", n.Passed, n.Scored, markText(m.Pass.Mark)),
			fmt.Sprintf("median %s%s", Duration(n.MedianWallSec), markText(m.Wall.Mark)),
		)
		if n.TokensKnown > 0 {
			parts = append(parts, fmt.Sprintf("%s tokens%s", TokensText(n.MedianTokens), markText(m.Tokens.Mark)))
		} else {
			parts = append(parts, "tokens unknown")
		}
		if n.Stop != "" && n.Stop != StopDone {
			parts = append(parts, "stopped: "+n.Stop)
		}
	}
	if e := m.Last.Experiment; e != nil {
		parts = append(parts, e.Line())
	}
	return strings.Join(parts, " · ")
}

func markText(mark string) string {
	if mark == "" {
		return " (first night)"
	}
	return " (" + mark + ")"
}

// Line is "experiment <slug>: rejected" or "experiment <slug> skipped: why".
func (e *NightExperiment) Line() string {
	if e == nil {
		return ""
	}
	if e.Skipped {
		return "experiment " + e.Slug + " skipped: " + e.Why
	}
	return "experiment " + e.Slug + ": " + e.Verdict
}

// Markdown is the morning report: the numbers against last night and the
// 7-night trend, the experiment, and the commands to read the runs. The
// holdout shows only as passed n/m.
func (m *Morning) Markdown() string {
	var b strings.Builder
	date := m.Last.Date
	fmt.Fprintf(&b, "# Factory last night: %s, %s\n\n", m.Benchmark, date)
	if m.Last.Held != "" {
		fmt.Fprintf(&b, "Held: %s. Nothing ran.\n\n", m.Last.Held)
	}
	n := m.Measured
	if n == nil {
		b.WriteString("No night has measured anything yet.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Main %s, run %s", short(n.MainSHA), n.Run)
	if n.Stop != "" && n.Stop != StopDone {
		fmt.Fprintf(&b, " (stopped: %s)", n.Stop)
	}
	b.WriteString(".\n\n| | last night | night before | | 7 nights |\n|---|---|---|---|---|\n")
	before := func(f func(Night) string) string {
		if m.Before == nil {
			return "—"
		}
		return f(*m.Before)
	}
	row := func(name, now, prev, mark string, trend []float64, f func(float64) string) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", name, now, prev, valueOrDash(mark), trendText(trend, f))
	}
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }
	row("pass rate", fmt.Sprintf("%d/%d (%s)", n.Passed, n.Scored, pct(n.PassRate)),
		before(func(x Night) string { return fmt.Sprintf("%d/%d (%s)", x.Passed, x.Scored, pct(x.PassRate)) }),
		m.Pass.Mark, m.Pass.Trend, pct)
	row("median time per task", Duration(n.MedianWallSec), before(func(x Night) string { return Duration(x.MedianWallSec) }),
		m.Wall.Mark, m.Wall.Trend, Duration)
	tok := func(v float64) string { return TokensText(int64(v)) }
	row("median tokens per task", fmt.Sprintf("%s (%d known)", TokensText(n.MedianTokens), n.TokensKnown),
		before(func(x Night) string { return TokensText(x.MedianTokens) }), m.Tokens.Mark, m.Tokens.Trend, tok)
	if n.Holdout.Total > 0 {
		fmt.Fprintf(&b, "\nHoldout: passed %d/%d.\n", n.Holdout.Passed, n.Holdout.Total)
	}
	if e := m.Last.Experiment; e != nil {
		fmt.Fprintf(&b, "\nExperiment: %s", e.Line())
		if e.Why != "" && !e.Skipped {
			fmt.Fprintf(&b, " (%s)", e.Why)
		}
		if e.Run != "" {
			fmt.Fprintf(&b, ", run %s", e.Run)
		}
		b.WriteString(". Adopting it is the owner's call.\n")
	} else {
		b.WriteString("\nExperiment: none queued.\n")
	}
	fmt.Fprintf(&b, "\nRead the run: `issue-cli bench report %s/%s`", m.Benchmark, n.Run)
	if p := m.Before; p != nil {
		fmt.Fprintf(&b, " · against the night before: `issue-cli bench compare %s/%s %s/%s`", m.Benchmark, p.Run, m.Benchmark, n.Run)
	}
	b.WriteString("\n")
	return b.String()
}

func valueOrDash(s string) string {
	if s == "" {
		return "first night"
	}
	return s
}

func trendText(v []float64, f func(float64) string) string {
	var parts []string
	for _, x := range v {
		parts = append(parts, f(x))
	}
	return strings.Join(parts, " → ")
}
