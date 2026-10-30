package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
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
	// Why is set when the issue is not proposed: what it lacks.
	Why string `json:"why,omitempty"`
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
		c := Candidate{Project: p.Slug, Slug: is.Slug, Type: is.Type, Done: is.DoneAt}
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
			c.Base, c.Merged, c.Lines, c.Files, c.Hidden = short(r.Base), short(r.Merged), r.Lines, len(r.Files), len(hidden)
			c.Tests = len(HiddenTests(repo, r, hidden))
			switch {
			case len(hidden) == 0:
				c.Why = "no test files in its commits"
			case r.Lines > MaxCandidateLines:
				c.Why = fmt.Sprintf("%d lines (over %d)", r.Lines, MaxCandidateLines)
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
