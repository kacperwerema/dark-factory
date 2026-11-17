// Package specflow counts what the spec-flow pieces left on disk: critique
// runs (findings by severity, which blocks held a gate and how each was
// cleared, time), clarify rounds (owner-answered, assumed, open), red-round
// cap questions and post-mortem actions (applied, refused, waiting). It is
// pure over records the caller already loaded, so issue-cli stats
// --spec-flow and the Stats page's Spec flow section count the same way.
// See docs/workflow-stats.md, "Spec flow".
package specflow

import (
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/redrounds"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Outcomes of a run whose blocks held a gate.
const (
	// Fixed: the next run on the same issue and edge was clear.
	Fixed = "fixed"
	// Overridden: the owner let the gate pass past this run.
	Overridden = "overridden"
	// HeldAgain: the next run on the edge held too (a red round).
	HeldAgain = "held again"
	// StillHolds: no later run on the edge yet.
	StillHolds = "still holds"
)

// In is what Gather counts. From and To bound the range ([From, To));
// a zero From or To leaves that end open.
type In struct {
	Runs        []*critique.Run
	Issues      []*tracker.Issue
	PostMortems []postmortem.Record
	From, To    time.Time
}

// Report is the four blocks.
type Report struct {
	From       *time.Time `json:"from,omitempty"`
	To         *time.Time `json:"to,omitempty"`
	Critique   Critique   `json:"critique"`
	Clarify    Clarify    `json:"clarify"`
	RedRounds  RedRounds  `json:"red_rounds"`
	PostMortem PostMortem `json:"post_mortem"`
}

// Empty reports whether no block has a row.
func (r Report) Empty() bool {
	return len(r.Critique.Runs) == 0 && len(r.Clarify.Rows) == 0 && len(r.RedRounds.Rows) == 0 && len(r.PostMortem.Rows) == 0
}

// Critique is the critique runs in range.
type Critique struct {
	Runs   []RunRow       `json:"runs"`
	Totals CritiqueTotals `json:"totals"`
}

// RunRow is one critique run.
type RunRow struct {
	ID      string    `json:"id"`
	Issue   string    `json:"issue"`
	Edge    string    `json:"edge"`
	At      time.Time `json:"at"`
	Agent   string    `json:"agent,omitempty"`
	Verdict string    `json:"verdict"`
	Blocks  int       `json:"blocks"`
	Should  int       `json:"should"`
	Note    int       `json:"note"`
	// TookMS is 0 when the run didn't record its time ("—").
	TookMS int64 `json:"took_ms,omitempty"`
	// Outcome is how a run whose blocks held a gate was cleared (Fixed,
	// Overridden, HeldAgain, StillHolds); "" for any other run.
	Outcome string `json:"outcome,omitempty"`
}

// Took is the run's time in words, "—" when not recorded.
func (r RunRow) Took() string { return (&critique.Run{TookMS: r.TookMS}).TookText() }

// CritiqueTotals adds the runs up.
type CritiqueTotals struct {
	Runs   int `json:"runs"`
	Gated  int `json:"gated"`
	ByHand int `json:"by_hand"`
	// Held is the gated runs whose blocks held the gate (failed runs
	// apart); each has one outcome below.
	Held       int `json:"held"`
	Fixed      int `json:"fixed"`
	Overridden int `json:"overridden"`
	HeldAgain  int `json:"held_again"`
	StillHolds int `json:"still_holds"`
	Failed     int `json:"failed"`
	Blocks     int `json:"blocks"`
	Should     int `json:"should"`
	Note       int `json:"note"`
	// Timed is the runs that recorded their time; MedianTookMS is over them.
	Timed        int   `json:"timed"`
	MedianTookMS int64 `json:"median_took_ms,omitempty"`
}

// MedianTook is the median time in words, "—" when no run was timed.
func (t CritiqueTotals) MedianTook() string {
	return (&critique.Run{TookMS: t.MedianTookMS}).TookText()
}

// Clarify is the clarify rounds in range.
type Clarify struct {
	Rows   []ClarifyRow  `json:"rows"`
	Totals ClarifyTotals `json:"totals"`
}

// ClarifyRow is one issue's round.
type ClarifyRow struct {
	Issue     string `json:"issue"`
	Title     string `json:"title"`
	Date      string `json:"date"`
	Nothing   bool   `json:"nothing_to_ask,omitempty"`
	Questions int    `json:"questions"`
	Owner     int    `json:"owner"`
	Assumed   int    `json:"assumed"`
	Open      int    `json:"open"`
	// Other is an answer in no known form, written by hand.
	Other int `json:"other,omitempty"`
}

// ClarifyTotals adds the rounds up.
type ClarifyTotals struct {
	Designs   int `json:"designs"`
	Nothing   int `json:"nothing_to_ask"`
	Questions int `json:"questions"`
	Owner     int `json:"owner"`
	Assumed   int `json:"assumed"`
	Open      int `json:"open"`
	Other     int `json:"other,omitempty"`
}

// PerDesign is the questions per design, one decimal.
func (t ClarifyTotals) PerDesign() float64 {
	if t.Designs == 0 {
		return 0
	}
	return float64(int(float64(t.Questions)/float64(t.Designs)*10+0.5)) / 10
}

// RedRounds is the red-round cap questions in range.
type RedRounds struct {
	Rows   []RedRow       `json:"rows"`
	Totals RedRoundTotals `json:"totals"`
}

// RedRow is one issue's questions on one check.
type RedRow struct {
	Issue    string `json:"issue"`
	Check    string `json:"check"`
	Raised   int    `json:"raised"`
	Open     int    `json:"open"`
	Resolved int    `json:"resolved"`
}

// RedRoundTotals adds the questions up.
type RedRoundTotals struct {
	Issues   int `json:"issues"`
	Raised   int `json:"raised"`
	Open     int `json:"open"`
	Resolved int `json:"resolved"`
}

// PostMortem is the post-mortems in range, each issue's newest.
type PostMortem struct {
	Rows   []PostMortemRow  `json:"rows"`
	Totals PostMortemTotals `json:"totals"`
}

// PostMortemRow is one issue's newest post-mortem.
type PostMortemRow struct {
	Issue     string    `json:"issue"`
	Title     string    `json:"title"`
	Date      time.Time `json:"date"`
	File      string    `json:"file"`
	Proposed  int       `json:"proposed"`
	Applied   int       `json:"applied"`
	Refused   int       `json:"refused"`
	Overtaken int       `json:"overtaken"`
	Waiting   int       `json:"waiting"`
}

// PostMortemTotals adds the post-mortems up.
type PostMortemTotals struct {
	PostMortems int `json:"post_mortems"`
	Proposed    int `json:"proposed"`
	Applied     int `json:"applied"`
	Refused     int `json:"refused"`
	Overtaken   int `json:"overtaken"`
	Waiting     int `json:"waiting"`
}

// Gather counts in.
func Gather(in In) Report {
	var rep Report
	if !in.From.IsZero() {
		f := in.From
		rep.From = &f
	}
	if !in.To.IsZero() {
		t := in.To
		rep.To = &t
	}
	rep.Critique = gatherCritique(in)
	rep.Clarify = gatherClarify(in)
	rep.RedRounds = gatherRedRounds(in)
	rep.PostMortem = gatherPostMortems(in)
	return rep
}

func (in In) inRange(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	if !in.From.IsZero() && t.Before(in.From) {
		return false
	}
	return in.To.IsZero() || t.Before(in.To)
}

// heldByBlocks is a gated run that held on its findings (or its output's
// problems), whatever the owner did after: a failed run is apart.
func heldByBlocks(r *critique.Run) bool {
	return !r.ByHand() && !r.Failed && (r.Report.Count(critique.Blocks) > 0 || len(r.Report.Problems) > 0)
}

// Outcome is how run r, one of runs, was cleared when its blocks held a
// gate; "" for any other run. The next run is the next on the same issue
// and edge that didn't fail (a failed run judges nothing).
func Outcome(runs []*critique.Run, r *critique.Run) string {
	if !heldByBlocks(r) {
		return ""
	}
	if r.Override != nil {
		return Overridden
	}
	var next *critique.Run
	for _, o := range runs {
		if o == r || o.Slug != r.Slug || o.ByHand() || o.Failed || !strings.EqualFold(o.From, r.From) || !strings.EqualFold(o.To, r.To) || !o.At.After(r.At) {
			continue
		}
		if next == nil || o.At.Before(next.At) {
			next = o
		}
	}
	switch {
	case next == nil:
		return StillHolds
	case heldByBlocks(next):
		return HeldAgain
	default:
		return Fixed
	}
}

func gatherCritique(in In) Critique {
	c := Critique{Runs: []RunRow{}}
	var took []int64
	for _, r := range in.Runs {
		if !in.inRange(r.At) {
			continue
		}
		row := RunRow{ID: r.ID, Issue: r.Slug, Edge: r.Edge(), At: r.At, Agent: r.Agent, Verdict: r.Verdict(),
			Blocks: r.Report.Count(critique.Blocks) + len(r.Report.Problems), Should: r.Report.Count(critique.Should), Note: r.Report.Count(critique.Note),
			TookMS: r.TookMS, Outcome: Outcome(in.Runs, r)}
		c.Runs = append(c.Runs, row)
		t := &c.Totals
		t.Runs++
		if r.ByHand() {
			t.ByHand++
		} else {
			t.Gated++
		}
		if r.Failed {
			t.Failed++
		}
		t.Blocks += row.Blocks
		t.Should += row.Should
		t.Note += row.Note
		if row.Outcome != "" {
			t.Held++
		}
		switch row.Outcome {
		case Fixed:
			t.Fixed++
		case Overridden:
			t.Overridden++
		case HeldAgain:
			t.HeldAgain++
		case StillHolds:
			t.StillHolds++
		}
		if r.TookMS > 0 {
			took = append(took, r.TookMS)
		}
	}
	sort.SliceStable(c.Runs, func(i, j int) bool { return c.Runs[i].At.After(c.Runs[j].At) })
	c.Totals.Timed = len(took)
	if len(took) > 0 {
		sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
		m := len(took) / 2
		if len(took)%2 == 1 {
			c.Totals.MedianTookMS = took[m]
		} else {
			c.Totals.MedianTookMS = (took[m-1] + took[m]) / 2
		}
	}
	return c
}

// clarifyDate dates a round: its newest owner answer, else the issue's
// created date. "" when neither parses.
func clarifyDate(issue *tracker.Issue, c tracker.Clarify) (time.Time, string) {
	var best time.Time
	for _, q := range c.Questions {
		if q.Answer.State != tracker.AnswerOwner {
			continue
		}
		if d, err := time.Parse("2006-01-02", q.Answer.Date); err == nil && d.After(best) {
			best = d
		}
	}
	if best.IsZero() {
		if d, err := time.Parse("2006-01-02", strings.TrimSpace(issue.Created)); err == nil {
			best = d
		}
	}
	if best.IsZero() {
		return best, ""
	}
	return best, best.Format("2006-01-02")
}

// inDayRange is inRange for a calendar date: the day counts when any of it
// falls in range.
func (in In) inDayRange(day time.Time) bool {
	if day.IsZero() {
		return false
	}
	loc := time.Local
	if !in.From.IsZero() {
		loc = in.From.Location()
	} else if !in.To.IsZero() {
		loc = in.To.Location()
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	if !in.From.IsZero() && !end.After(in.From) {
		return false
	}
	return in.To.IsZero() || start.Before(in.To)
}

func gatherClarify(in In) Clarify {
	c := Clarify{Rows: []ClarifyRow{}}
	for _, issue := range in.Issues {
		if !strings.Contains(issue.BodyRaw, tracker.ClarifySection) {
			continue // the cheap test first: most bodies have no round
		}
		cl := tracker.ParseClarify(issue.BodyRaw)
		if !cl.Found {
			continue
		}
		day, date := clarifyDate(issue, cl)
		if !in.inDayRange(day) {
			continue
		}
		row := ClarifyRow{Issue: issue.Slug, Title: issue.Title, Date: date, Nothing: cl.Nothing, Questions: len(cl.Questions)}
		for _, q := range cl.Questions {
			switch q.Answer.State {
			case tracker.AnswerOwner:
				row.Owner++
			case tracker.AnswerAssumed:
				row.Assumed++
			case tracker.AnswerOpen:
				row.Open++
			default:
				row.Other++
			}
		}
		c.Rows = append(c.Rows, row)
		t := &c.Totals
		t.Designs++
		if row.Nothing {
			t.Nothing++
		}
		t.Questions += row.Questions
		t.Owner += row.Owner
		t.Assumed += row.Assumed
		t.Open += row.Open
		t.Other += row.Other
	}
	sort.SliceStable(c.Rows, func(i, j int) bool {
		if c.Rows[i].Date != c.Rows[j].Date {
			return c.Rows[i].Date > c.Rows[j].Date
		}
		return c.Rows[i].Issue < c.Rows[j].Issue
	})
	return c
}

func gatherRedRounds(in In) RedRounds {
	r := RedRounds{Rows: []RedRow{}}
	issues := map[string]bool{}
	for _, issue := range in.Issues {
		comments := issue.Comments()
		if !anyQuestion(comments) {
			continue
		}
		at := map[string]int{}
		for _, q := range redrounds.Questions(comments) {
			if !in.inRange(q.At) {
				continue
			}
			i, ok := at[q.Check]
			if !ok {
				i = len(r.Rows)
				at[q.Check] = i
				r.Rows = append(r.Rows, RedRow{Issue: issue.Slug, Check: q.Check})
			}
			row := &r.Rows[i]
			row.Raised++
			if q.Open {
				row.Open++
			} else {
				row.Resolved++
			}
			issues[issue.Slug] = true
			r.Totals.Raised++
			if q.Open {
				r.Totals.Open++
			} else {
				r.Totals.Resolved++
			}
		}
	}
	r.Totals.Issues = len(issues)
	sort.SliceStable(r.Rows, func(i, j int) bool {
		if r.Rows[i].Issue != r.Rows[j].Issue {
			return r.Rows[i].Issue < r.Rows[j].Issue
		}
		return r.Rows[i].Check < r.Rows[j].Check
	})
	return r
}

// anyQuestion is a cheap test before redrounds.Questions parses each
// comment's check.
func anyQuestion(comments []tracker.Comment) bool {
	for _, c := range comments {
		if _, ok := redrounds.QuestionCheck(c.Text); ok {
			return true
		}
	}
	return false
}

func gatherPostMortems(in In) PostMortem {
	p := PostMortem{Rows: []PostMortemRow{}}
	for _, rec := range in.PostMortems {
		if !in.inRange(rec.Date) {
			continue
		}
		row := PostMortemRow{Issue: rec.Issue, Title: rec.Title, Date: rec.Date, File: rec.Path}
		for _, a := range rec.Actions {
			if a.ID == "" {
				continue
			}
			row.Proposed++
			switch {
			case a.Applied != "" || (a.Done && a.Refused == "" && a.Overtaken == ""):
				row.Applied++
			case a.Refused != "":
				row.Refused++
			case a.Overtaken != "":
				row.Overtaken++
			default:
				row.Waiting++
			}
		}
		p.Rows = append(p.Rows, row)
		t := &p.Totals
		t.PostMortems++
		t.Proposed += row.Proposed
		t.Applied += row.Applied
		t.Refused += row.Refused
		t.Overtaken += row.Overtaken
		t.Waiting += row.Waiting
	}
	sort.SliceStable(p.Rows, func(i, j int) bool { return p.Rows[i].Date.After(p.Rows[j].Date) })
	return p
}

// Load reads the records under issuesDir (the critique log) and retrosDir
// (each issue's newest post-mortem) and counts them with issues, which the
// caller has loaded already, over [from, to).
func Load(issuesDir, retrosDir string, issues []*tracker.Issue, from, to time.Time) (Report, error) {
	runs, err := critique.Load(issuesDir, "")
	if err != nil {
		return Report{}, err
	}
	return Gather(In{Runs: runs, Issues: issues, PostMortems: postmortem.Newest(retrosDir), From: from, To: to}), nil
}
