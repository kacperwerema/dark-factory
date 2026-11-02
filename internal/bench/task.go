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

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// MaxCandidateLines is the biggest branch (lines added plus removed) that
// `bench candidates` proposes: bigger tasks cost a lot per run.
const MaxCandidateLines = 600

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
	var out []Candidate
	for _, is := range issues {
		if !p.IssueDone(is) {
			continue
		}
		c := Candidate{Project: p.Slug, Slug: is.Slug, Type: is.Type, Done: is.DoneAt, System: is.System}
		if in != nil {
			if _, t := in.Task(p.Slug + "/" + is.Slug); t != nil {
				continue
			}
		}
		stats, _ := tracker.LoadStats(is.FilePath)
		r, err := FindRange(repo, stats)
		switch {
		case err != nil:
			c.Why = err.Error()
		case r.Commits == 0:
			c.Why = "no commits of its own"
		default:
			hidden := HiddenFiles(repo, r)
			c.repo, c.rng, c.hidden = repo, r, hidden
			c.Base, c.Merged, c.Lines, c.Files, c.Hidden = short(r.Base), short(r.Merged), r.Lines, len(r.Files), len(hidden)
			c.TestNames = HiddenTests(repo, r, hidden)
			c.Tests = len(c.TestNames)
			switch {
			case len(hidden) == 0:
				c.Why = "no test files in its commits"
			case c.Tests == 0:
				c.Why = "no Go tests in its test files (the scorer runs Go tests only, so just --check would judge it)"
			case r.Lines > MaxCandidateLines:
				c.Why = fmt.Sprintf("%d lines (over %d)", r.Lines, MaxCandidateLines)
			default:
				sources := map[string]string{}
				for _, f := range hidden {
					sources[f], _ = git(repo, "show", r.Merged+":"+f)
				}
				c.Risks = CandidateRisks(sources, c.TestNames, is.BodyRaw)
				if n := len(ForeignFiles(repo, r)); n > 0 {
					c.Risks = append(c.Risks, fmt.Sprintf("main merged in (%d files not its own)", n))
				}
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Why == "") != (out[j].Why == "") {
			return out[i].Why == ""
		}
		return out[i].Done > out[j].Done
	})
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
		return t, nil, fmt.Errorf("%s has no test files in %s..%s and no --check: nothing could tell a good result", issue.Slug, short(r.Base), short(r.Merged))
	}
	t.Start = opt.Start
	if t.Start == "" {
		t.Start = DefaultStart
		if !wentThrough(stats, DefaultStart) && len(stats.Transitions) > 0 {
			t.Start = stats.Transitions[0].To
		}
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
	return t, warnings, os.WriteFile(path, snap, 0o644)
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
	log("  checking the merged commit %s", short(t.Merged))
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
	log("  checking the base %s", short(t.Base))
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
