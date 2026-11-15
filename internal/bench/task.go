package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// MaxCandidateLines is the biggest branch (lines added plus removed) that
// `bench candidates` proposes: bigger tasks cost a lot per run.
const MaxCandidateLines = 600

// MaxCandidateFiles is the most files a proposed branch may change: past it a
// task is a sprawl to judge and costs like one.
const MaxCandidateFiles = 15

// DefaultCandidateLimit is how many `bench candidates` shows: about twice
// the 10 or so tasks the owner picks.
const DefaultCandidateLimit = 20

// DefaultStart is the status a task's worker starts at when the issue went
// through it: the design is given, the worker builds it.
const DefaultStart = "in progress"

// Candidate is a done issue that could be a golden task.
type Candidate struct {
	Project string `json:"project"`
	Slug    string `json:"slug"`
	Type    string `json:"type,omitempty"`
	Done    string `json:"done,omitempty"`
	Base    string `json:"base"`
	Merged  string `json:"merged"`
	Lines   int    `json:"lines"`
	Files   int    `json:"files"`
	Hidden  int    `json:"hidden_files"`
	Tests   int    `json:"hidden_tests"`
	System  string `json:"system,omitempty"`
	// TestNames are the hidden Go tests that would judge it.
	TestNames []string `json:"tests,omitempty"`
	// Risks name what may make it a poor task though it is proposed (see
	// CandidateRisks).
	Risks []string `json:"risks,omitempty"`
	// Stages name the references per stage task add would give it
	// (candidateStages): "design", "code (n tests)", "docs (n files)" or
	// "docs (rubric only)"; a design it can't have is "no design: <why>".
	Stages []string `json:"stages,omitempty"`
	// Why is set when the issue is not proposed: what it lacks.
	Why string `json:"why,omitempty"`
	// Replay and ReplayWhy are the solvability check's verdict and reason
	// (CheckCandidates), set only by `bench candidates --replay`.
	Replay    string `json:"replay,omitempty"`
	ReplayWhy string `json:"replay_why,omitempty"`
	// Workflow, Output and Eval are set on an output task candidate
	// (OutputCandidates): the department's box key, the approved output
	// that would be the reference and the eval that would score a run.
	Workflow    string `json:"workflow,omitempty"`
	Output      string `json:"output,omitempty"`
	OutputTitle string `json:"output_title,omitempty"`
	Eval        string `json:"eval,omitempty"`
	EvalScore   string `json:"eval_score,omitempty"`

	repo   string
	rng    Range
	hidden []string
}

// BroadTests is the hidden test count over which a candidate is marked
// broad: a branch that only changed tests is judged by every test in its
// files, not by the change's own.
const BroadTests = 20

var (
	riskTmux    = regexp.MustCompile(`(?i)tmux`)
	riskBrowser = regexp.MustCompile(`(?i)chromedp|headless`)
	riskTiming  = regexp.MustCompile(`time\.(Sleep|Since|After|Tick)\(|context\.WithTimeout\(`)
)

// CandidateRisks reads a proposed candidate's hidden test sources (path to
// content at merged), the hidden test names and the issue body: tmux (the
// tests start tmux: slow and flaky under load), browser (a headless
// browser), timing (sleeps or time limits), broad (over BroadTests hidden
// tests), owner steps (an "(owner)" box the worker cannot do). In Go files
// only the hidden tests' own bodies are read, not the rest of the file.
func CandidateRisks(sources map[string]string, tests []string, body string) []string {
	var tmux, browser, timing bool
	for f, src := range sources {
		if strings.HasPrefix(f, "playtest/") {
			browser = true
		}
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		src = testBodies(src, tests)
		browser = browser || riskBrowser.MatchString(src)
		tmux = tmux || riskTmux.MatchString(src)
		timing = timing || riskTiming.MatchString(src)
	}
	var out []string
	if tmux {
		out = append(out, "tmux")
	}
	if browser {
		out = append(out, "browser")
	}
	if timing {
		out = append(out, "timing")
	}
	if len(tests) > BroadTests {
		out = append(out, fmt.Sprintf("broad (%d tests)", len(tests)))
	}
	if hasOwnerBox(body) {
		out = append(out, "owner steps")
	}
	return out
}

// hasOwnerBox reports whether the body has a box the owner marker marks
// (tracker.IsOwnerMarked, the engine's rule).
func hasOwnerBox(body string) bool {
	for _, it := range tracker.ListCheckboxes(body) {
		if tracker.IsOwnerMarked(it.Label()) {
			return true
		}
	}
	return false
}

// testBodies is the source of the named Go test functions in src, each from
// its func line to the next top-level func.
func testBodies(src string, tests []string) string {
	want := map[string]bool{}
	for _, t := range tests {
		want[t] = true
	}
	var b strings.Builder
	keep := false
	for _, l := range strings.SplitAfter(src, "\n") {
		if strings.HasPrefix(l, "func ") {
			m := goTestFunc.FindStringSubmatch(l)
			keep = m != nil && want[m[1]]
		}
		if keep {
			b.WriteString(l)
		}
	}
	return b.String()
}

// Candidates looks at every done issue of p: proposed ones (Why empty) have
// one branch range, test files in it and at most MaxCandidateLines; the rest
// say why not. Nothing is written.
func Candidates(p *tracker.Project, issues []*tracker.Issue, in *Benchmark) []Candidate {
	repo, err := git(p.Root(), "rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	repo = strings.TrimSpace(repo)
	// The issues to look at, then every range from one read of the history.
	type row struct {
		c     Candidate
		issue *tracker.Issue
		rec   recorded
		rng   Range
		err   error
		stats tracker.StatsStore
	}
	var rows []*row
	var tips []string
	for _, is := range issues {
		if !p.IssueDone(is) {
			continue
		}
		if in != nil {
			if _, t := in.Task(p.Slug + "/" + is.Slug); t != nil {
				continue
			}
		}
		x := &row{c: Candidate{Project: p.Slug, Slug: is.Slug, Type: is.Type, Done: is.DoneAt, System: is.System}, issue: is}
		stats, _ := tracker.LoadStats(is.FilePath)
		x.stats = stats
		if x.rec, x.err = recordedOf(stats); x.err == nil {
			tips = append(tips, x.rec.walk())
		}
		rows = append(rows, x)
	}
	h, herr := loadHistory(repo, tips)
	var ranges []*Range
	for _, x := range rows {
		switch {
		case x.err != nil:
		case herr != nil:
			x.err = herr
		default:
			if x.rng, x.err = h.rangeOf(x.rec); x.err == nil {
				ranges = append(ranges, &x.rng)
			}
		}
	}
	ferr := fillRanges(repo, h, ranges)

	o := openObjects(repo)
	defer o.close()
	wf := p.LoadWorkflow()
	issueDir := ""
	if rel, err := filepath.Rel(repo, p.IssueDir); err == nil && !strings.HasPrefix(rel, "..") {
		issueDir = rel
	}
	var fits []*row // the rows that pass every rule, for main's files below
	for _, x := range rows {
		c, r := &x.c, x.rng
		switch {
		case x.err != nil:
			c.Why = x.err.Error()
		case ferr != nil:
			c.Why = ferr.Error()
		case r.Commits == 0:
			c.Why = "no commits of its own"
		default:
			hidden := o.hiddenFiles(r)
			c.repo, c.rng, c.hidden = repo, r, hidden
			c.Base, c.Merged, c.Lines, c.Files, c.Hidden = gitdir.ShortSHA(r.Base), gitdir.ShortSHA(r.Merged), r.Lines, len(r.Files), len(hidden)
			c.TestNames = o.hiddenTests(r, hidden)
			c.Tests = len(c.TestNames)
			switch {
			case len(hidden) == 0:
				c.Why = "no test files in its commits"
			case c.Tests == 0:
				c.Why = "no Go tests in its test files (the scorer runs Go tests only, so just --check would judge it)"
			case r.Lines > MaxCandidateLines:
				c.Why = fmt.Sprintf("%d lines (over %d)", r.Lines, MaxCandidateLines)
			case len(r.Files) > MaxCandidateFiles:
				c.Why = fmt.Sprintf("%d files (over %d)", len(r.Files), MaxCandidateFiles)
			default:
				sources := map[string]string{}
				for _, f := range hidden {
					sources[f], _ = o.read(r.Merged, f)
				}
				c.Risks = CandidateRisks(sources, c.TestNames, x.issue.BodyRaw)
				c.Stages = candidateStages(wf, x.stats, x.issue.BodyRaw, c.Tests, len(DocsFiles(r, issueDir)))
				fits = append(fits, x)
			}
		}
	}
	diffs := make([]Range, len(fits))
	for i, x := range fits {
		diffs[i] = x.rng
	}
	for i, foreign := range foreignFiles(repo, diffs) {
		if n := len(foreign); n > 0 {
			fits[i].c.Risks = append(fits[i].c.Risks, fmt.Sprintf("main merged in (%d files not its own)", n))
		}
	}
	var out []Candidate
	for _, x := range rows {
		out = append(out, x.c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Why == "") != (out[j].Why == "") {
			return out[i].Why == ""
		}
		return out[i].Done > out[j].Done
	})
	return out
}

// PickCandidates is at most n of cands' proposed rows (Why empty), the ones
// with the fewest risks first and within those one of each type in turn, so
// a table by type has data; each type stays newest first.
func PickCandidates(cands []Candidate, n int) []Candidate {
	byRisk := map[int][]Candidate{}
	var risks []int
	for _, c := range cands {
		if c.Why != "" {
			continue
		}
		k := len(c.Risks)
		if _, ok := byRisk[k]; !ok {
			risks = append(risks, k)
		}
		byRisk[k] = append(byRisk[k], c)
	}
	sort.Ints(risks)
	var out []Candidate
	for _, k := range risks {
		out = append(out, acrossTypes(byRisk[k])...)
	}
	return out[:max(min(n, len(out)), 0)]
}

// acrossTypes takes one row of each type in turn, types in first-seen order.
func acrossTypes(cands []Candidate) []Candidate {
	byType := map[string][]Candidate{}
	var types []string
	for _, c := range cands {
		if _, ok := byType[c.Type]; !ok {
			types = append(types, c.Type)
		}
		byType[c.Type] = append(byType[c.Type], c)
	}
	var out []Candidate
	for len(out) < len(cands) {
		for _, t := range types {
			if rows := byType[t]; len(rows) > 0 {
				out = append(out, rows[0])
				byType[t] = rows[1:]
			}
		}
	}
	return out
}

// CheckCandidates replays each proposed candidate (Why empty) whose slug is
// in only (all when only is empty), one at a time, and sets its Replay and
// ReplayWhy (see Replay). progress, when set, is told before each one.
// Nothing is written but the throwaway worktrees, which are removed.
func CheckCandidates(ctx context.Context, cands []Candidate, only []string, env ReplayEnv, progress func(i, n int, c Candidate)) {
	want := map[string]bool{}
	for _, s := range only {
		want[s] = true
	}
	var todo []int
	for i, c := range cands {
		if c.Why == "" && c.repo != "" && (len(want) == 0 || want[c.Slug]) {
			todo = append(todo, i)
		}
	}
	for k, i := range todo {
		if ctx.Err() != nil {
			cands[i].Replay, cands[i].ReplayWhy = ReplayNotChecked, "interrupted"
			continue
		}
		if progress != nil {
			progress(k+1, len(todo), cands[i])
		}
		c := &cands[i]
		res := Replay(ctx, c.repo, c.rng, c.hidden, c.TestNames, env)
		c.Replay, c.ReplayWhy = res.Verdict, res.Why
	}
}

// AddOptions are `bench task add`'s choices; empty fields are found.
type AddOptions struct {
	Base, Merged, Start, Check string
	Holdout                    bool
	// Snapshot is a file to use as the issue at its start instead of the
	// tracker's git history.
	Snapshot string
	// Verify runs the base and merged checks (see Verify).
	Verify bool
	Log    func(format string, args ...any)
	// DocsRubric are rubric lines for the docs stage beside
	// DefaultDocsRubric (--docs-rubric).
	DocsRubric []string
}

// NewTask builds the task for issue and writes its snapshot. It does not add
// it to a benchmark.
func NewTask(ctx context.Context, p *tracker.Project, issue *tracker.Issue, opt AddOptions, now time.Time) (Task, []string, error) {
	t := Task{Project: p.Slug, Slug: issue.Slug, Type: issue.Type, Check: opt.Check, Holdout: opt.Holdout, Added: now.Format("2006-01-02")}
	repo, err := git(p.Root(), "rev-parse", "--show-toplevel")
	if err != nil {
		return t, nil, err
	}
	repo = strings.TrimSpace(repo)
	stats, _ := tracker.LoadStats(issue.FilePath)
	var r Range
	if opt.Base != "" && opt.Merged != "" {
		r, err = RangeOf(repo, opt.Base, opt.Merged)
	} else {
		r, err = FindRange(repo, stats)
		if err == nil && opt.Base != "" {
			r, err = RangeOf(repo, opt.Base, r.Merged)
		}
		if err == nil && opt.Merged != "" {
			r, err = RangeOf(repo, r.Base, opt.Merged)
		}
	}
	if err != nil {
		return t, nil, fmt.Errorf("%s: %w (pass --base and --merged)", issue.Slug, err)
	}
	for _, ref := range []*string{&r.Base, &r.Merged} {
		full, err := git(repo, "rev-parse", "--verify", *ref+"^{commit}")
		if err != nil {
			return t, nil, err
		}
		*ref = strings.TrimSpace(full)
	}
	t.Base, t.Merged = r.Base, r.Merged
	t.Hidden = HiddenFiles(repo, r)
	t.HiddenTests = HiddenTests(repo, r, t.Hidden)
	if len(t.Hidden) == 0 && t.Check == "" {
		return t, nil, fmt.Errorf("%s has no test files in %s..%s and no --check: nothing could tell a good result", issue.Slug, gitdir.ShortSHA(r.Base), gitdir.ShortSHA(r.Merged))
	}
	t.Start = opt.Start
	if t.Start == "" {
		t.Start = defaultStart(p.LoadWorkflow(), stats)
	}
	var snap []byte
	if opt.Snapshot != "" {
		data, err := os.ReadFile(opt.Snapshot)
		if err != nil {
			return t, nil, err
		}
		snap = SetStatus(data, t.Start)
	} else if snap, err = Snapshot(issue, stats, t.Start); err != nil {
		return t, nil, fmt.Errorf("%w (pass --snapshot <file> with the issue as it stood at %q)", err, t.Start)
	}
	var warnings []string
	if opt.Verify {
		if warnings, err = Verify(ctx, repo, &t, opt.Log); err != nil {
			return t, warnings, err
		}
	}
	path := SnapshotPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return t, warnings, err
	}
	if err := os.WriteFile(path, snap, 0o644); err != nil {
		return t, warnings, err
	}
	return t, warnings, StageReferences(repo, p, issue, stats, r, &t, opt.DocsRubric)
}

// defaultStart is the status a task's worker starts at with no --start: the
// workflow's work status, or the issue's first step when it never went there.
func defaultStart(wf *tracker.WorkflowConfig, stats tracker.StatsStore) string {
	start := wf.RoleStatus(tracker.StatusRoleWork)
	if start == "" {
		start = DefaultStart
	}
	if !wentThrough(stats, start) && len(stats.Transitions) > 0 {
		return stats.Transitions[0].To
	}
	return start
}

func wentThrough(stats tracker.StatsStore, status string) bool {
	for _, tr := range stats.Transitions {
		if strings.EqualFold(tr.To, status) {
			return true
		}
	}
	return false
}

// Verify checks a task in a throwaway clone: at merged the hidden tests
// pass (a failing one is dropped, with a warning) and the check is green; at
// base, without the hidden files, the check is green. Either check failing
// refuses the task.
func Verify(ctx context.Context, repo string, t *Task, log func(string, ...any)) ([]string, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	dir, err := os.MkdirTemp("", "awe-bench-verify-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	clone := filepath.Join(dir, "repo")
	if _, err := git(dir, "clone", "--quiet", "--no-checkout", "--shared", repo, clone); err != nil {
		return nil, err
	}
	var warnings []string
	log("  checking the merged commit %s", gitdir.ShortSHA(t.Merged))
	if _, err := git(clone, "checkout", "--quiet", "--detach", t.Merged); err != nil {
		return nil, err
	}
	if len(t.HiddenTests) > 0 {
		var kept []string
		passed := goHidden(ctx, clone, *t)
		for _, name := range t.HiddenTests {
			if passed[name] {
				kept = append(kept, name)
			} else {
				warnings = append(warnings, name+" fails on the merged commit itself; left out")
			}
		}
		t.HiddenTests = kept
		if len(kept) == 0 && t.Check == "" {
			return warnings, fmt.Errorf("no hidden test passes on the merged commit and there is no check")
		}
	}
	if t.Check != "" {
		if ok, out := shell(ctx, clone, t.Check, nil, 30*time.Minute); !ok {
			return warnings, fmt.Errorf("the check fails on the merged commit: %s", lastLine(out))
		}
	}
	if t.Check == "" {
		return warnings, nil
	}
	log("  checking the base %s", gitdir.ShortSHA(t.Base))
	if _, err := git(clone, "checkout", "--quiet", "--force", "--detach", t.Base); err != nil {
		return warnings, err
	}
	if _, err := git(clone, "clean", "-fdq"); err != nil {
		return warnings, err
	}
	for _, h := range t.Hidden {
		_ = os.Remove(filepath.Join(clone, filepath.FromSlash(h)))
	}
	if ok, out := shell(ctx, clone, t.Check, nil, 30*time.Minute); !ok {
		return warnings, fmt.Errorf("the check fails at the base: %s", lastLine(out))
	}
	return warnings, nil
}

// candidateStages is what task add would give a candidate per stage, read
// from its stats and body (no git call): a design when it left a design
// status with a ## Design section, the code's tests, the docs it changed.
func candidateStages(wf *tracker.WorkflowConfig, stats tracker.StatsStore, body string, tests, docs int) []string {
	var out []string
	went := false
	for _, tr := range stats.Transitions {
		if wf.HasRole(tr.From, "design") {
			went = true
			break
		}
	}
	switch text, ok := tracker.SectionText(body, "Design"); {
	case !went:
		out = append(out, "no design: never went through a design status")
	case !ok || strings.TrimSpace(text) == "":
		out = append(out, "no design: no ## Design section")
	default:
		out = append(out, StageDesign)
	}
	out = append(out, fmt.Sprintf("code (%s)", plural(tests, "test")))
	if docs > 0 {
		out = append(out, fmt.Sprintf("docs (%s)", plural(docs, "file")))
	} else {
		out = append(out, "docs (rubric only)")
	}
	return out
}
