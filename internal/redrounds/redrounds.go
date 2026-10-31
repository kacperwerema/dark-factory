// Package redrounds counts fix rounds per check on an issue and turns a
// check still red after the cap (workflow.yaml max_red_rounds, 4 by default)
// into a question for the owner. Results come from the issue's stats sidecar
// (rounds:, written by issue-cli transition and `rounds --cannot-fix`), from
// local CI runs that name the issue and from its critique runs (FromCritique);
// Count folds them into streaks.
// While a cap question is open, issue-cli transition refuses before running
// anything again. See docs/workflow.md, "Red-round cap".
package redrounds

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/critique"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Result is one outcome of one check (tracker.RoundResult).
type Result = tracker.RoundResult

// Streak is one check's red rounds since its last green (or since the
// owner resolved its last cap question).
type Streak struct {
	Check  string `json:"check"`
	Rounds int    `json:"rounds"`
	// CannotFix is set when the worker said it cannot fix the check: the
	// streak is at the cap whatever Rounds says.
	CannotFix bool `json:"cannot_fix,omitempty"`
	// Last is the newest red result of the streak.
	Last Result `json:"last"`
}

// AtCap reports whether the streak has reached max rounds, or the worker
// said it cannot fix it.
func (s Streak) AtCap(max int) bool { return s.CannotFix || s.Rounds >= max }

// Label is "red 3 of 4", or "red 2 of 4 · cannot fix".
func (s Streak) Label(max int) string {
	l := fmt.Sprintf("red %d of %d", s.Rounds, max)
	if s.CannotFix {
		l += " · cannot fix"
	}
	return l
}

// Count folds results into one streak per check that is red now, sorted by
// check. Results are taken in time order. A green result ends the check's
// streak; reds at the same non-empty Head are one round (retrying without a
// new commit is not a fix round); a red with no Head is its own round.
// since holds, per check, the time before which nothing counts (the owner
// resolved a cap question then).
func Count(results []Result, since map[string]time.Time) []Streak {
	sorted := append([]Result(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	type acc struct {
		Streak
		heads map[string]bool
	}
	by := map[string]*acc{}
	for _, r := range sorted {
		if r.Check == "" {
			continue
		}
		if t, ok := since[r.Check]; ok && !r.At.After(t) {
			continue
		}
		a := by[r.Check]
		if !r.Red {
			delete(by, r.Check)
			continue
		}
		if a == nil {
			a = &acc{Streak: Streak{Check: r.Check}, heads: map[string]bool{}}
			by[r.Check] = a
		}
		if r.Head == "" || !a.heads[r.Head] {
			a.Rounds++
			if r.Head != "" {
				a.heads[r.Head] = true
			}
		}
		if r.CannotFix {
			a.CannotFix = true
		}
		a.Last = r
	}
	out := make([]Streak, 0, len(by))
	for _, a := range by {
		out = append(out, a.Streak)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Check < out[j].Check })
	return out
}

// FromCI turns local CI runs that merged slug into results: each run red
// on a command is a red ci:<command> result at that run's sha, a green run
// is green for every command it ran.
func FromCI(runs []ci.Run, slug string) []Result {
	var out []Result
	for _, run := range runs {
		if !mergedSlug(run, slug) {
			continue
		}
		at := run.Finished
		if at.IsZero() {
			at = run.Started
		}
		switch run.State {
		case ci.Red:
			why := strings.Join(run.Failures, ", ")
			out = append(out, Result{Check: "ci:" + run.Failed, Red: true, At: at, Head: run.Sha, Why: why})
		case ci.Green:
			for _, c := range run.Commands {
				out = append(out, Result{Check: "ci:" + c.Cmd, At: at, Head: run.Sha})
			}
		}
	}
	return out
}

// CritiqueCheck is the check critique runs on the gate from → to count
// under ("critique in design → backlog"): each gate has its own streak.
func CritiqueCheck(from, to string) string {
	return "critique " + from + " → " + to
}

// FromCritique turns slug's critique runs into results of their gate's
// critique check: a run with a blocks finding is one red round of its own
// (keyed by run, since a design stage's branch head does not move between
// runs); a clear run is green, an overridden one green from the override.
// A failed run (non-zero exit, timeout, usage limit) and a run holding
// only on output problems (no Coverage line) are the critic's fault, not
// the work's, and are not rounds.
func FromCritique(runs []*critique.Run, slug string) []Result {
	var out []Result
	for _, run := range runs {
		if run.Slug != slug {
			continue
		}
		check := CritiqueCheck(run.From, run.To)
		switch {
		case run.Override != nil:
			out = append(out, Result{Check: check, At: run.Override.At, Head: "critique:" + run.ID})
		case run.Failed:
		case run.Report.Count(critique.Blocks) > 0:
			out = append(out, Result{Check: check, Red: true, At: run.At, Head: "critique:" + run.ID, Why: critiqueWhy(run.Report)})
		case run.Holds():
		default:
			out = append(out, Result{Check: check, At: run.At, Head: "critique:" + run.ID})
		}
	}
	return out
}

// critiqueWhy is a holding run in a line: its blocks count and the first.
func critiqueWhy(r critique.Report) string {
	b := r.Of(critique.Blocks)
	return fmt.Sprintf("%d blocks: %s", len(b), b[0].String())
}

func mergedSlug(run ci.Run, slug string) bool {
	for _, m := range run.Merged {
		if m.Slug == slug {
			return true
		}
	}
	return false
}

// QuestionPrefix starts every cap question comment; the check follows it up
// to " — ".
const QuestionPrefix = "question: red rounds: "

// QuestionText is the cap question for a streak: the recommendation first,
// then the options, the clarify round's order (the clarify sibling owns the
// format; this is the plain question: form until it lands, D-5).
func QuestionText(s Streak, max int) string {
	why := firstLine(s.Last.Why)
	if why == "" {
		why = "no failure text recorded"
	}
	reason := fmt.Sprintf("still red after %d fix rounds", s.Rounds)
	if s.CannotFix {
		reason = "the worker says it cannot fix it"
	}
	return fmt.Sprintf("%s%s — %s (%s, cap %d): %s. "+
		"Recommendation: tell the worker what the check should accept or what to change, then resolve this question; the count starts over. "+
		"Options: (a) a fix direction for the worker; (b) change or drop the check; (c) park the issue. "+
		"The worker stops working on this check and the transition is refused until this is resolved.",
		QuestionPrefix, s.Check, s.Label(max), reason, max, why)
}

// QuestionCheck reads the check a cap question names; ok is false for any
// other comment.
func QuestionCheck(text string) (check string, ok bool) {
	rest, ok := strings.CutPrefix(text, QuestionPrefix)
	if !ok {
		return "", false
	}
	check, _, found := strings.Cut(rest, " — ")
	if !found || check == "" {
		return "", false
	}
	return check, true
}

// Question is one cap question comment on the issue.
type Question struct {
	Check     string
	CommentID int
	Open      bool
	At        time.Time
}

// Questions lists the cap questions among comments, oldest first.
func Questions(comments []tracker.Comment) []Question {
	var out []Question
	for _, c := range comments {
		check, ok := QuestionCheck(c.Text)
		if !ok {
			continue
		}
		at, _ := tracker.CommentTime(c.Date)
		out = append(out, Question{Check: check, CommentID: c.ID, Open: c.Status != "done", At: at})
	}
	return out
}

// Since is, per check, the time of its newest resolved cap question: rounds
// before it don't count any more.
func Since(qs []Question) map[string]time.Time {
	since := map[string]time.Time{}
	for _, q := range qs {
		// Comment dates keep whole seconds: the red that raised the
		// question can be later in the same second.
		at := q.At.Add(time.Second)
		if !q.Open && at.After(since[q.Check]) {
			since[q.Check] = at
		}
	}
	return since
}

// Open is the open cap questions, by check.
func Open(qs []Question) map[string]Question {
	open := map[string]Question{}
	for _, q := range qs {
		if q.Open {
			open[q.Check] = q
		}
	}
	return open
}

// State is an issue's red rounds: every streak, the cap and the open
// questions.
type State struct {
	Max     int
	Streaks []Streak
	Open    map[string]Question
}

// Load reads an issue's results (sidecar, CI runs from ciRoot and critique
// runs from issuesDir, "" for none) and comments into its State.
func Load(issuePath, slug, ciRoot, issuesDir string, max int) (State, error) {
	store, err := tracker.LoadStats(issuePath)
	if err != nil {
		return State{}, err
	}
	comments, err := tracker.LoadComments(issuePath)
	if err != nil {
		return State{}, err
	}
	results := append([]Result(nil), store.Rounds...)
	if ciRoot != "" {
		results = append(results, FromCI(ci.RecentRuns(ciRoot, 0), slug)...)
	}
	if issuesDir != "" {
		runs, err := critique.Load(issuesDir, slug)
		if err != nil {
			return State{}, err
		}
		results = append(results, FromCritique(runs, slug)...)
	}
	return Build(results, comments, max), nil
}

// Build is Load's pure half: the State of results and comments.
func Build(results []Result, comments []tracker.Comment, max int) State {
	qs := Questions(comments)
	return State{Max: max, Streaks: Count(results, Since(qs)), Open: Open(qs)}
}

// Streak returns check's streak (zero when it is not red).
func (s State) Streak(check string) Streak {
	for _, st := range s.Streaks {
		if st.Check == check {
			return st
		}
	}
	return Streak{Check: check}
}

// NeedQuestion is the streaks at the cap with no open question yet.
func (s State) NeedQuestion() []Streak {
	var out []Streak
	for _, st := range s.Streaks {
		if _, open := s.Open[st.Check]; !open && st.AtCap(s.Max) {
			out = append(out, st)
		}
	}
	return out
}

// HoldError is the refusal while a cap question is open: the check is not
// run again until the owner resolves it.
type HoldError struct {
	Slug     string
	Question Question
	Streak   Streak
	Max      int
}

func (e *HoldError) Error() string {
	return fmt.Sprintf("%s is %s: it is a question for the owner now (comment #%d), so it is not re-run.\n"+
		"Stop working on this check. The owner resolving the question starts the count over.\n"+
		"See: issue-cli rounds %s",
		e.Question.Check, e.Streak.Label(e.Max), e.Question.CommentID, e.Slug)
}

// Hold returns the HoldError for the first open cap question (by check), or
// nil when none is open.
func (s State) Hold(slug string) error {
	if len(s.Open) == 0 {
		return nil
	}
	checks := make([]string, 0, len(s.Open))
	for c := range s.Open {
		checks = append(checks, c)
	}
	sort.Strings(checks)
	q := s.Open[checks[0]]
	st := s.Streak(q.Check)
	if st.Rounds == 0 {
		st.Rounds = s.Max
	}
	return &HoldError{Slug: slug, Question: q, Streak: st, Max: s.Max}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
