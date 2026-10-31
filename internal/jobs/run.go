package jobs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// NewRun is the issue a run creates.
type NewRun struct {
	Slug   string
	File   string
	Title  string
	Period string
	Status string
	System string
	// Type is the run's type of work; empty when the workflow has no types.
	Type string
	// Previous is the slug of the job's last run before this one.
	Previous string
}

// RunSlug is the base slug of job id's run for period: weekly-seo-pass-2026-w44.
func RunSlug(id, period string) string { return tracker.Slugify(id + "-" + period) }

// PlanRun works out the run issue for r's period: its system folder (the
// department's first system, else its name), slug (with -2, -3 … when again
// asks for another run in the same period), title, type (the job's type:,
// else default_type) and that type's first status.
func PlanRun(p *tracker.Project, r *Ref, runs []Run, period string, again bool) (*NewRun, error) {
	dept := p.DepartmentByKey(r.Department)
	if dept == nil {
		return nil, fmt.Errorf("department %q is not in projects.yaml", r.Department)
	}
	wf, err := p.WorkflowFor(dept.Name)
	if err != nil {
		return nil, err
	}
	typ, err := RunType(wf, r.Job)
	if err != nil {
		return nil, fmt.Errorf("job %s: %w", r.Key, err)
	}
	n := &NewRun{Period: period, Status: wf.FirstStatus(typ), System: dept.Name, Type: typ}
	if len(dept.Systems) > 0 && strings.TrimSpace(dept.Systems[0]) != "" {
		n.System = strings.TrimSpace(dept.Systems[0])
	}
	if n.Status == "" {
		return nil, fmt.Errorf("the %s workflow has no statuses", dept.Name)
	}
	base := RunSlug(r.Job.ID, period)
	n.Slug = base
	seq := 1
	for _, run := range runs {
		if run.Period != period {
			continue
		}
		if !again {
			return nil, &ExistsError{Slug: run.Issue.Slug, Status: run.Issue.Status}
		}
		seq++
	}
	if seq > 1 {
		n.Slug = fmt.Sprintf("%s-%d", base, seq)
	}
	n.Title = r.Name() + " · " + period
	if seq > 1 {
		n.Title += fmt.Sprintf(" (%d)", seq)
	}
	if len(runs) > 0 {
		n.Previous = runs[len(runs)-1].Issue.Slug
	}
	n.File = filepath.Join(p.IssueDir, n.System, n.Slug+".md")
	return n, nil
}

// RunType is the type of work job j's runs get in wf: its type:, else
// default_type; empty when wf has no types. A type wf doesn't define is an
// error naming the types.
func RunType(wf *tracker.WorkflowConfig, j tracker.Job) (string, error) {
	want := strings.TrimSpace(j.Type)
	if want != "" && !wf.HasTypes() {
		return "", fmt.Errorf("type %q: the workflow defines no types", want)
	}
	typ, known := wf.ResolveType(want)
	if !known {
		return "", fmt.Errorf("type %q is not in the workflow (types: %s)", want, strings.Join(wf.TypeNames(), ", "))
	}
	return typ, nil
}

// ExistsError is a run for the period that is already there.
type ExistsError struct {
	Slug   string
	Status string
}

func (e *ExistsError) Error() string {
	return fmt.Sprintf("this period's run is already there: %s (%s)", e.Slug, e.Status)
}

// Brief is the job's brief file with {{period}}, {{date}}, {{last_run}} and
// {{department}} (the department's key) filled in.
func Brief(p *tracker.Project, r *Ref, n *NewRun, now time.Time) (string, error) {
	data, err := BriefText(p, r.Job)
	if err != nil {
		return "", fmt.Errorf("job %s: %w", r.Key, err)
	}
	last := n.Previous
	if last == "" {
		last = "none (first run)"
	}
	return strings.NewReplacer(
		"{{period}}", n.Period,
		"{{date}}", now.Format("2006-01-02"),
		"{{last_run}}", last,
		"{{department}}", tracker.DepartmentKey(r.Department),
	).Replace(strings.TrimSpace(data)), nil
}

// WriteRun writes n's issue file: type, department, job, run, the job's priority,
// the label job, the brief and a ## Run line. It never overwrites a file.
func WriteRun(p *tracker.Project, r *Ref, n *NewRun, brief string, now time.Time) error {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %q\n", n.Title)
	fmt.Fprintf(&b, "status: %q\n", n.Status)
	b.WriteString(tracker.CreatedLine(now))
	fmt.Fprintf(&b, "system: %q\n", n.System)
	if n.Type != "" {
		fmt.Fprintf(&b, "type: %q\n", n.Type)
	}
	if pr := strings.TrimSpace(r.Job.Priority); pr != "" {
		fmt.Fprintf(&b, "priority: %q\n", pr)
	}
	b.WriteString("labels:\n  - job\n")
	fmt.Fprintf(&b, "slug: %q\n", n.Slug)
	fmt.Fprintf(&b, "department: %q\n", r.Department)
	fmt.Fprintf(&b, "job: %q\n", r.Key)
	fmt.Fprintf(&b, "run: %q\n", n.Period)
	b.WriteString("---\n")
	if err := tracker.CheckIssueFrontmatter(b.String()); err != nil {
		return fmt.Errorf("run issue: %w", err)
	}
	b.WriteString("\n" + brief + "\n\n## Run\n\n")
	fmt.Fprintf(&b, "Recurring job `%s` (%s), run %s.", r.Key, r.Schedule.Label(), n.Period)
	if n.Previous != "" {
		fmt.Fprintf(&b, " Previous run: %s.", n.Previous)
	}
	b.WriteString("\n")
	if err := os.MkdirAll(filepath.Dir(n.File), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(n.File, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("run issue %s already exists", n.File)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
