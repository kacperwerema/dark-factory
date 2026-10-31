package bench

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Evals: each workflow file's evals: say how a run of a type is scored
// (tests, output, diff); the per-workflow verdict says whether a setup is a
// net positive or negative for each workflow (docs/benchmark.md, Evals).

// TaskEval is the eval that scores t on workflow wf: the task's eval:, else
// the one for its type; nil when the file has none (hidden tests alone).
// An eval: the file doesn't declare is an error.
func TaskEval(t Task, wf *tracker.WorkflowConfig) (*tracker.WorkflowEval, error) {
	if t.Eval != "" {
		if e := wf.EvalByID(t.Eval); e != nil {
			return e, nil
		}
		return nil, fmt.Errorf("the workflow declares no eval %q (add it to evals: — docs/benchmark.md#evals)", t.Eval)
	}
	return wf.EvalFor(t.Type), nil
}

// DepartmentWorkflowFile is the file a task on box key runs on: Product's
// workflow for product, a department's own file for its key; "" with an
// error when the department has no file of its own.
func DepartmentWorkflowFile(p *tracker.Project, key string) (string, error) {
	if key == "" || key == ProductWorkflow {
		return p.WorkflowPath(), nil
	}
	d := p.DepartmentByKey(key)
	if d == nil {
		return "", fmt.Errorf("project %s has no department %q", p.Slug, key)
	}
	if f := p.DepartmentWorkflowPath(d.Name); f != "" {
		return f, nil
	}
	return p.WorkflowPath(), nil
}

// shelfOutput is the newest output on the sandbox's shelf for slug, and
// its text; "" with why when there is none or it can't be read.
func shelfOutput(p *tracker.Project, slug string) (string, string) {
	list, _ := outputs.Load(p)
	var newest *outputs.Output
	for _, o := range list {
		if o.Issue == slug && o.State != outputs.StateRejected && (newest == nil || o.Added.After(newest.Added)) {
			newest = o
		}
	}
	if newest == nil {
		return "", "no output on the shelf for " + slug
	}
	var file string
	switch {
	case newest.File != "":
		file = filepath.Join(outputs.FilesDir(p), filepath.FromSlash(newest.File))
	case newest.Path != "":
		file = filepath.Join(p.Root(), filepath.FromSlash(newest.Path))
	default:
		return "", "output " + newest.ID + " is a link (" + newest.URL + "), nothing to read"
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", "output " + newest.ID + ": " + err.Error()
	}
	return string(data), ""
}

// scoreEval grades an output task with the benchmark's scorer and
// sets the record's eval fields and outcome.
func scoreEval(ctx context.Context, b *Benchmark, t Task, ev *tracker.WorkflowEval, sb *sandbox, rec *TaskRecord) {
	text, why := shelfOutput(sb.proj, t.Slug)
	if why != "" {
		rec.EvalScore = 0
		rec.Outcome, rec.Reason = OutcomeFail, why
		return
	}
	var ref string
	if p := ReferencePath(t); p != "" {
		data, _ := os.ReadFile(p)
		ref = string(data)
	}
	brief, _ := os.ReadFile(SnapshotPath(t))
	started := time.Now()
	res, err := Score(ctx, filepath.Join(sb.dir, "eval"), b.Scorer,
		ScoreInput{Eval: *ev, Output: text, Reference: ref, Brief: string(brief), Grader: b.Grader}, sb.scorerEnv())
	rec.ScoreWallSec = time.Since(started).Seconds()
	rec.EvalSkipped = res.Skipped
	if err != nil {
		rec.Outcome, rec.Reason = OutcomeError, err.Error()
		return
	}
	rec.EvalScore, rec.EvalPassed, rec.EvalTotal, rec.EvalFailed = res.Score, res.Passed, res.Total, res.Failed
	switch {
	case !rec.CheckOK:
		rec.Outcome, rec.Reason = OutcomeFail, fmt.Sprintf("eval %s %.2f, check fails", ev.ID, res.Score)
	case res.Score+1e-9 >= ev.PassBar():
		rec.Outcome = OutcomePass
	default:
		rec.Outcome, rec.Reason = OutcomeFail, fmt.Sprintf("eval %s %.2f < %.2f (%d/%d assertions)", ev.ID, res.Score, ev.PassBar(), res.Passed, res.Total)
	}
}

// Verdicts of a workflow (WorkflowVerdict.Verdict).
const (
	NetPositive   = "net positive"
	NetNegative   = "net negative"
	NoChange      = "no change"
	NotComparable = "not comparable"
)

// ScoreStep is the smallest move of a mean eval score that counts; CostStep
// the relative move of tokens or cost, and WallStep of wall time, that
// decide when quality holds (D-5, D-9).
const (
	ScoreStep = 0.05
	CostStep  = 0.10
	WallStep  = 0.25
)

// WorkflowVerdict is one workflow's part of a comparison.
type WorkflowVerdict struct {
	Workflow  string   `json:"workflow"`
	Tasks     int      `json:"tasks"`
	Verdict   string   `json:"verdict"`
	Why       []string `json:"why"`
	PassRateA float64  `json:"pass_rate_a"`
	PassRateB float64  `json:"pass_rate_b"`
	// ScoreA and ScoreB are the mean eval score (judge/5 for tasks without
	// one); -1 when no task has either.
	ScoreA      float64  `json:"score_a"`
	ScoreB      float64  `json:"score_b"`
	TokensA     int64    `json:"tokens_a"`
	TokensB     int64    `json:"tokens_b"`
	USDA        float64  `json:"usd_a"`
	USDB        float64  `json:"usd_b"`
	WallA       float64  `json:"wall_a_sec"`
	WallB       float64  `json:"wall_b_sec"`
	Regressions []string `json:"regressions"`
	Improved    []string `json:"improved"`
}

// taskScore is a record's quality score 0-1: its eval score, else its judge
// score over 5; ok false when it has neither.
func taskScore(r TaskRecord) (float64, bool) {
	if r.Eval != "" && r.EvalScore >= 0 {
		return r.EvalScore, true
	}
	if r.Judge >= 0 {
		return float64(r.Judge) / 5, true
	}
	return 0, false
}

// recordWorkflow is a record's workflow key; records from before evals
// count as product.
func recordWorkflow(r TaskRecord) string {
	if r.Workflow != "" {
		return r.Workflow
	}
	return ProductWorkflow
}

type side struct {
	tasks              map[string]TaskRecord
	scored, passed     int
	scoreSum           float64
	scoreN             int
	tokens             int64
	usd, wall          float64
	models, ccVersions map[string]bool
}

func collect(r *Run) map[string]*side {
	out := map[string]*side{}
	for _, t := range r.Tasks {
		w := recordWorkflow(t)
		s := out[w]
		if s == nil {
			s = &side{tasks: map[string]TaskRecord{}, models: map[string]bool{}, ccVersions: map[string]bool{}}
			out[w] = s
		}
		s.tasks[t.Task] = t
		if t.Outcome != OutcomeBroken {
			s.scored++
			if t.Outcome == OutcomePass {
				s.passed++
			}
		}
		if v, ok := taskScore(t); ok {
			s.scoreSum += v
			s.scoreN++
		}
		s.tokens += t.Usage.Tokens()
		s.usd += t.Usage.USD
		s.wall += t.WallSec + t.ScoreWallSec
		if t.Usage.Model != "" {
			s.models[t.Usage.Model] = true
		}
		if t.Usage.ToolVersion != "" {
			s.ccVersions[t.Usage.ToolVersion] = true
		}
	}
	return out
}

func (s *side) rate() float64 {
	if s == nil || s.scored == 0 {
		return 0
	}
	return float64(s.passed) / float64(s.scored)
}

func (s *side) score() float64 {
	if s == nil || s.scoreN == 0 {
		return -1
	}
	return s.scoreSum / float64(s.scoreN)
}

// CompareByWorkflow gives each workflow with tasks in a or b its verdict on
// b against a (D-5): not comparable when a run didn't finish, the
// workflow's tasks differ, or the models or Claude Code versions differ;
// net negative on a task that passed and now fails, a pass-rate drop of at
// least one task, a score drop of ScoreStep, or the same quality at
// CostStep more tokens or cost; net positive on the same gains with no
// regression, or the same quality at CostStep less; else no change. The
// holdout is not split by workflow: it stays in the run's overall numbers.
func CompareByWorkflow(a, b *Run) []WorkflowVerdict {
	sa, sb := collect(a), collect(b)
	keys := map[string]bool{}
	for k := range sa {
		keys[k] = true
	}
	for k := range sb {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == ProductWorkflow) != (names[j] == ProductWorkflow) {
			return names[i] == ProductWorkflow
		}
		return names[i] < names[j]
	})
	var out []WorkflowVerdict
	for _, w := range names {
		x, y := sa[w], sb[w]
		v := WorkflowVerdict{Workflow: w, Why: []string{}, Regressions: []string{}, Improved: []string{}, ScoreA: x.score(), ScoreB: y.score(), PassRateA: x.rate(), PassRateB: y.rate()}
		if x != nil {
			v.TokensA, v.USDA, v.WallA = x.tokens, x.usd, x.wall
		}
		if y != nil {
			v.TokensB, v.USDB, v.WallB = y.tokens, y.usd, y.wall
		}
		var broken []string
		for _, r := range []*Run{a, b} {
			if !r.Finished() || r.Stop != StopDone {
				broken = append(broken, fmt.Sprintf("run %s did not finish (stop: %s)", r.ID, orNone(r.Stop)))
			}
		}
		switch {
		case x == nil:
			broken = append(broken, "no "+w+" tasks in "+a.ID)
		case y == nil:
			broken = append(broken, "no "+w+" tasks in "+b.ID)
		default:
			v.Tasks = len(y.tasks)
			for id := range y.tasks {
				if _, ok := x.tasks[id]; !ok {
					broken = append(broken, id+" is only in "+b.ID)
				}
			}
			for id := range x.tasks {
				if _, ok := y.tasks[id]; !ok {
					broken = append(broken, id+" is only in "+a.ID)
				}
			}
			if m := union(x.models, y.models); len(m) > 1 {
				broken = append(broken, "other models: "+strings.Join(m, ", "))
			}
			if m := union(x.ccVersions, y.ccVersions); len(m) > 1 {
				broken = append(broken, "other Claude Code versions: "+strings.Join(m, ", "))
			}
		}
		sort.Strings(broken)
		if len(broken) > 0 {
			v.Verdict, v.Why = NotComparable, broken
			out = append(out, v)
			continue
		}
		var ids []string
		for id := range y.tasks {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			ra, rb := x.tasks[id], y.tasks[id]
			switch {
			case ra.Outcome == OutcomePass && rb.Outcome != OutcomePass && rb.Outcome != OutcomeBroken:
				v.Regressions = append(v.Regressions, id)
			case ra.Outcome != OutcomePass && rb.Outcome == OutcomePass:
				v.Improved = append(v.Improved, id)
			}
		}
		v.Verdict, v.Why = judgeWorkflow(v, max(x.scored, y.scored))
		out = append(out, v)
	}
	return out
}

func judgeWorkflow(v WorkflowVerdict, n int) (string, []string) {
	floor := 1.0
	if n > 0 {
		floor = 1/float64(n) - 1e-9
	}
	dRate := v.PassRateB - v.PassRateA
	dScore := 0.0
	if v.ScoreA >= 0 && v.ScoreB >= 0 {
		dScore = v.ScoreB - v.ScoreA
	}
	var bad, good []string
	if len(v.Regressions) > 0 {
		bad = append(bad, fmt.Sprintf("%d task(s) regressed: %s", len(v.Regressions), strings.Join(v.Regressions, ", ")))
	}
	if dRate <= -floor {
		bad = append(bad, fmt.Sprintf("pass rate %s → %s", pct(v.PassRateA), pct(v.PassRateB)))
	} else if dRate >= floor {
		good = append(good, fmt.Sprintf("pass rate %s → %s", pct(v.PassRateA), pct(v.PassRateB)))
	}
	if dScore <= -ScoreStep+1e-9 {
		bad = append(bad, fmt.Sprintf("score %.2f → %.2f", v.ScoreA, v.ScoreB))
	} else if dScore >= ScoreStep-1e-9 {
		good = append(good, fmt.Sprintf("score %.2f → %.2f", v.ScoreA, v.ScoreB))
	}
	if len(bad) > 0 {
		return NetNegative, bad
	}
	if len(good) > 0 {
		return NetPositive, good
	}
	// Quality held: cost and time decide. More of either is a net negative
	// even when the other fell.
	tok := relative(float64(v.TokensA), float64(v.TokensB))
	usd := relative(v.USDA, v.USDB)
	wall := relative(v.WallA, v.WallB)
	line := "same quality, " + costText(tok, usd, wall)
	switch {
	case tok >= CostStep-1e-9 || usd >= CostStep-1e-9 || wall >= WallStep-1e-9:
		return NetNegative, []string{line}
	case tok <= -CostStep+1e-9 || usd <= -CostStep+1e-9 || wall <= -WallStep+1e-9:
		return NetPositive, []string{line}
	}
	return NoChange, []string{line}
}

// relative is b's change on a as a fraction; 0 when a is 0.
func relative(a, b float64) float64 {
	if a == 0 {
		return 0
	}
	return (b - a) / math.Abs(a)
}

func costText(tok, usd, wall float64) string {
	return fmt.Sprintf("tokens %+.0f%%, cost %+.0f%%, wall %+.0f%%", tok*100, usd*100, wall*100)
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func union(a, b map[string]bool) []string {
	m := map[string]bool{}
	for k := range a {
		m[k] = true
	}
	for k := range b {
		m[k] = true
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Estimate is what a run of b will likely take, from the newest finished
// run's mean per task (worker and scoring time, tokens, cost) times b's
// task count; "no previous run" when there is none.
func Estimate(b *Benchmark) string {
	runs := Runs(b.Name)
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if !r.Finished() || len(r.Tasks) == 0 {
			continue
		}
		var wall, usd float64
		var tokens int64
		for _, t := range r.Tasks {
			wall += t.WallSec + t.ScoreWallSec
			usd += t.Usage.USD
			tokens += t.Usage.Tokens()
		}
		n := float64(len(r.Tasks))
		k := float64(len(b.Tasks))
		return fmt.Sprintf("about %s, %s tokens, $%.2f for %d tasks (run %s's mean per task: %s, $%.2f)",
			(time.Duration(wall/n*k) * time.Second).Round(time.Minute), compact(int64(float64(tokens)/n*k)), usd/n*k, len(b.Tasks), r.ID,
			(time.Duration(wall/n) * time.Second).Round(time.Second), usd/n)
	}
	return "no previous run, so no estimate yet"
}
