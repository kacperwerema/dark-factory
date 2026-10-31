package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Output tasks: golden tasks for workflows whose result is an output on the
// shelf (marketing, content). The known good result is an output the owner
// approved (every review carries the owner's words and workers are refused,
// docs/departments.md: Nothing goes out without the owner's stamp); its file
// becomes the task's reference, and the eval scores the run's own output.

// approvedOutput is the newest output of slug the owner approved or
// published, with a file to read.
func approvedOutput(list []*outputs.Output, slug string) (*outputs.Output, string) {
	var best *outputs.Output
	why := "no output on the shelf"
	for _, o := range list {
		if o.Issue != slug {
			continue
		}
		switch {
		case o.State != outputs.StateApproved && o.State != outputs.StatePublished:
			if best == nil {
				why = "its output " + o.ID + " is " + o.State + ", not approved by the owner"
			}
			continue
		case o.File == "" && o.Path == "":
			if best == nil {
				why = "its output " + o.ID + " is a link only, no file to compare with"
			}
			continue
		case o.Missing:
			if best == nil {
				why = "its output " + o.ID + "'s file is gone"
			}
			continue
		}
		if best == nil || o.Reviewed.After(best.Reviewed) {
			best = o
		}
	}
	if best == nil {
		return nil, why
	}
	return best, ""
}

// NewOutputTask builds an output task for issue from its approved output
// (outputID, or the newest approved one when ""), writes the snapshot and
// copies the output's file as the reference. Base is opt.Base, else the
// issue's branch range's base, else the repository's HEAD (with a warning).
func NewOutputTask(ctx context.Context, p *tracker.Project, issue *tracker.Issue, outputID string, opt AddOptions, now time.Time) (Task, []string, error) {
	dept := tracker.DepartmentKey(p.DepartmentOf(issue))
	t := Task{Project: p.Slug, Slug: issue.Slug, Type: issue.Type, Check: opt.Check, Holdout: opt.Holdout, Added: now.Format("2006-01-02"), Workflow: dept}
	if dept == "" || dept == ProductWorkflow {
		return t, nil, fmt.Errorf("%s is Product's: output tasks are for a department's issues (Product's development tasks come from hidden tests)", issue.Slug)
	}
	list, _ := outputs.Load(p)
	var o *outputs.Output
	if outputID != "" {
		found, err := outputs.Find(list, outputID)
		if err != nil {
			return t, nil, err
		}
		if found.Issue != issue.Slug {
			return t, nil, fmt.Errorf("output %s is %s's, not %s's", found.ID, orNone(found.Issue), issue.Slug)
		}
		var why string
		if o, why = approvedOutput([]*outputs.Output{found}, issue.Slug); o == nil {
			return t, nil, fmt.Errorf("%s: %s", issue.Slug, why)
		}
	} else {
		var why string
		if o, why = approvedOutput(list, issue.Slug); o == nil {
			return t, nil, fmt.Errorf("%s: %s (only an output the owner approved makes a golden task)", issue.Slug, why)
		}
	}
	t.Output = o.ID
	wf, err := p.WorkflowFor(p.DepartmentOf(issue))
	if err != nil {
		return t, nil, err
	}
	if wf.EvalFor(issue.Type) == nil {
		return t, nil, fmt.Errorf("the %s workflow has no eval for type %q: add one to evals: in %s (docs/benchmark.md#evals)", dept, orNone(issue.Type), orNone(wf.File))
	}
	repo, err := git(p.Root(), "rev-parse", "--show-toplevel")
	if err != nil {
		return t, nil, err
	}
	repo = strings.TrimSpace(repo)
	var warnings []string
	base := opt.Base
	stats, _ := tracker.LoadStats(issue.FilePath)
	if base == "" {
		if r, err := FindRange(repo, stats); err == nil {
			base = r.Base
		} else {
			base = "HEAD"
			warnings = append(warnings, "no branch range in the issue's stats, so the task starts at HEAD: pass --base <the commit the work started from>")
		}
	}
	full, err := git(repo, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return t, warnings, fmt.Errorf("--base %s: %w", base, err)
	}
	t.Base = strings.TrimSpace(full)
	t.Merged = t.Base
	if o.Path != "" {
		if _, err := git(repo, "cat-file", "-e", t.Base+":"+o.Path); err == nil {
			warnings = append(warnings, fmt.Sprintf("the approved file %s is in the checkout at base: the worker can read it; pass an earlier --base", o.Path))
		}
	}
	t.Start = opt.Start
	if t.Start == "" && len(stats.Transitions) > 0 {
		t.Start = stats.Transitions[0].To
	}
	if t.Start == "" {
		return t, warnings, fmt.Errorf("%s: no status history; pass --start <status> and --snapshot <file>", issue.Slug)
	}
	var snap []byte
	if opt.Snapshot != "" {
		data, err := os.ReadFile(opt.Snapshot)
		if err != nil {
			return t, warnings, err
		}
		snap = SetStatus(data, t.Start)
	} else if snap, err = Snapshot(issue, stats, t.Start); err != nil {
		return t, warnings, fmt.Errorf("%w (pass --snapshot <file> with the issue as it stood at %q)", err, t.Start)
	}
	src := filepath.Join(outputs.FilesDir(p), filepath.FromSlash(o.File))
	if o.File == "" {
		src = filepath.Join(p.Root(), filepath.FromSlash(o.Path))
	}
	ref, err := os.ReadFile(src)
	if err != nil {
		return t, warnings, fmt.Errorf("output %s: %w", o.ID, err)
	}
	t.Reference = o.ID + "-" + filepath.Base(src)
	if strings.HasPrefix(filepath.Base(src), o.ID+"-") {
		t.Reference = filepath.Base(src)
	}
	if err := os.MkdirAll(TaskDir(t.Project, t.Slug), 0o755); err != nil {
		return t, warnings, err
	}
	if err := os.WriteFile(ReferencePath(t), ref, 0o644); err != nil {
		return t, warnings, err
	}
	return t, warnings, os.WriteFile(SnapshotPath(t), snap, 0o644)
}

// OutputCandidates lists the done issues of department key that could be
// output tasks: proposed (Why empty) when the issue has an output the owner
// approved with a file and the department's workflow has an eval for its
// type. It reads the shelf and the workflow file only; tasks already in
// in are left out. Newest done first, proposed before the rest.
func OutputCandidates(p *tracker.Project, issues []*tracker.Issue, key string, in *Benchmark) ([]Candidate, error) {
	d := p.DepartmentByKey(key)
	if d == nil {
		return nil, fmt.Errorf("project %s has no department %q", p.Slug, key)
	}
	wf, err := p.WorkflowFor(d.Name)
	if err != nil {
		return nil, err
	}
	list, _ := outputs.Load(p)
	var out []Candidate
	for _, is := range issues {
		if tracker.DepartmentKey(p.DepartmentOf(is)) != key || !p.IssueDone(is) {
			continue
		}
		if in != nil {
			if _, t := in.Task(p.Slug + "/" + is.Slug); t != nil {
				continue
			}
		}
		c := Candidate{Project: p.Slug, Slug: is.Slug, Type: is.Type, Done: is.DoneAt, System: is.System, Workflow: key}
		o, why := approvedOutput(list, is.Slug)
		if o != nil {
			c.Output, c.OutputTitle = o.ID, o.Title
		}
		ev := wf.EvalFor(is.Type)
		if ev != nil {
			c.Eval, c.EvalScore = ev.ID, ev.ScoreKind()
		}
		switch {
		case o == nil:
			c.Why = why
		case ev == nil:
			c.Why = fmt.Sprintf("the %s workflow has no eval for type %q (add one to evals: in %s)", key, orNone(is.Type), orNone(wf.File))
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Why == "") != (out[j].Why == "") {
			return out[i].Why == ""
		}
		return out[i].Done > out[j].Done
	})
	return out, nil
}

// EvalDepartments are the keys of the project's departments whose workflow
// file of its own declares at least one eval, in projects.yaml order.
func EvalDepartments(p *tracker.Project) []string {
	var keys []string
	for _, d := range p.Departments {
		if p.DepartmentWorkflowPath(d.Name) == "" {
			continue
		}
		wf, err := p.WorkflowFor(d.Name)
		if err == nil && wf != nil && len(wf.Evals) > 0 {
			keys = append(keys, tracker.DepartmentKey(d.Name))
		}
	}
	return keys
}
