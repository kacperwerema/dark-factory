package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/experiment"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var promptCellCommand = &Command{
	Name:      "prompt-cell",
	ShortHelp: "Which workflow.yaml prompt cells (type · status) an experiment proposal edits, and which benchmark tasks walk through them",
	LongHelp: `A type's own status prompt replaces the base one (docs/Workflow/types.md),
so a prompt is a cell: (type, status). An edit to the base "in progress"
prompt never reaches a tweak task when tweak has its own. prompt-cell says,
before any benchmark run, which cells a proposal edits and which of its
tasks walk through them. It only reads.

  prompt-cell <slug> [--ref <branch>] [--cell <status>|<type>:<status>]...
              [--baseline <run>] [--benchmark <name>] [--json]

Cells: the project's workflow file read at main (or at the --baseline
run's commit) and at the proposal's branch (--ref, else work/<slug> when
it exists); a cell is edited when its status prompt, or an inject_prompt
on an edge into it, differs. A change in one systems: overlay is listed
as type · status · system. A proposal with no branch yet names its cells:
--cell "in progress" is the base prompt (every type that shares it),
--cell "tweak:in progress" one type's cell.

Tasks: the benchmark's (--benchmark, the experiment block's, or the only
one), else the slugs listed under the proposal's "## Bench tasks". A task
is reached when an edited cell is on its type's path at or after the
status it starts at; a missed one says why ("tweak has its own in progress
prompt", "tweak's path skips in design", "starts at in progress, after
idea"). The last line is "Reach: N of M tasks".

experiment run --dry-run prints the same Reach line before the spend.`,
	Run: runPromptCell,
}

func init() {
	registerCommand(promptCellCommand)
}

// cellReport is what prompt-cell found: the cells, the tasks and where both
// came from.
type cellReport struct {
	Slug      string                 `json:"slug"`
	File      string                 `json:"file"`
	Base      string                 `json:"base"`
	BaseSHA   string                 `json:"base_sha"`
	Ref       string                 `json:"ref,omitempty"`
	RefSHA    string                 `json:"ref_sha,omitempty"`
	Named     []string               `json:"named,omitempty"`
	Cells     []experiment.Cell      `json:"cells"`
	Shared    map[string][]string    `json:"shared,omitempty"` // base cell status → the types sharing it
	TaskFrom  string                 `json:"tasks_from"`
	Tasks     []experiment.TaskReach `json:"tasks"`
	Reached   int                    `json:"reached"`
	Checked   int                    `json:"checked"`
	TasksNote string                 `json:"tasks_note,omitempty"`

	wf *tracker.WorkflowConfig // the side the tasks are read against: ref, else base
}

func runPromptCell(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("prompt-cell needs a project — pass --project <slug> or run from a project root")
	}
	slug, rest, err := requireSlug(args, "prompt-cell")
	if err != nil {
		return err
	}
	fs := newFlagSet("prompt-cell", ctx)
	ref := fs.String("ref", "", "the proposal's branch (default: work/<slug> when it exists)")
	var cells multiFlag
	fs.Var(&cells, "cell", "a cell the proposal edits: <status> (the base prompt) or <type>:<status>; repeatable")
	baseline := fs.String("baseline", "", "a finished benchmark run: compare against the commit it measured instead of main")
	benchFlag := fs.String("benchmark", "", "the benchmark whose tasks to check (default: the experiment block's, or the only one)")
	jsonOut := fs.Bool("json", false, "print the report as JSON")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	block, _ := experiment.Load(issue.FilePath)
	r := &cellReport{Slug: issue.Slug, Named: cells}
	repo := "."
	r.Base, r.BaseSHA = "main", ""
	if *baseline != "" {
		name, err := experiment.PickBenchmark(*benchFlag, block, experiment.BenchDir())
		if err != nil {
			return err
		}
		run, sha, err := experiment.Pinned(experiment.BenchDir(), name, *baseline)
		if err != nil {
			return err
		}
		r.Base, r.BaseSHA = "run "+run.ID, sha
	} else if r.BaseSHA, err = experiment.SHA(repo, "main"); err != nil {
		return err
	}
	r.Ref = *ref
	if r.Ref == "" {
		if _, err := experiment.SHA(repo, "work/"+issue.Slug); err == nil {
			r.Ref = "work/" + issue.Slug
		}
	}
	if r.Ref == "" && len(cells) == 0 {
		return &usageError{msg: fmt.Sprintf("%s has no branch work/%s: name the cells the proposal edits (issue-cli prompt-cell %s --cell \"in progress\" or --cell tweak:in progress), or pass --ref <branch>", issue.Slug, issue.Slug, issue.Slug)}
	}
	if r.Ref != "" {
		if r.RefSHA, err = experiment.SHA(repo, r.Ref); err != nil {
			return fmt.Errorf("--ref %s: %w", r.Ref, err)
		}
	}
	if err := r.fill(ctx.Project, repo, cells); err != nil {
		return err
	}
	r.tasks(ctx, issue, block, *benchFlag)
	if *jsonOut {
		return writeJSON(ctx.Stdout, r)
	}
	r.print(ctx.Stdout)
	return nil
}

// workflowFile is the project's workflow file relative to its root, the
// path git reads at each revision.
func workflowFile(proj *tracker.Project) string {
	if p := proj.WorkflowPath(); p != "" {
		if rel, err := filepath.Rel(projectRoot(proj), p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
		return filepath.Base(p)
	}
	return "workflow.yaml"
}

// workflowAt parses the workflow file at rev.
func workflowAt(repo, rev, file string) (*tracker.WorkflowConfig, error) {
	data, err := experiment.FileAt(repo, rev, file)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("%s has no %s", gitdir.ShortSHA(rev), file)
	}
	wf, err := experiment.ParseWorkflow(data)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", file, gitdir.ShortSHA(rev), err)
	}
	return wf, nil
}

// fill reads the workflow at base (and ref) and works out the cells.
func (r *cellReport) fill(proj *tracker.Project, repo string, named []string) error {
	r.File = workflowFile(proj)
	base, err := workflowAt(repo, r.BaseSHA, r.File)
	if err != nil {
		return err
	}
	wf := base
	if r.RefSHA != "" {
		ref, err := workflowAt(repo, r.RefSHA, r.File)
		if err != nil {
			return err
		}
		wf = ref
		r.Cells = experiment.EditedCells(base, ref)
	}
	if len(named) > 0 {
		cs, err := experiment.NamedCells(wf, named)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if !containsCell(r.Cells, c) {
				r.Cells = append(r.Cells, c)
			}
		}
	}
	r.Shared = map[string][]string{}
	for _, c := range r.Cells {
		if c.Owner == experiment.OwnerBase && c.Type != "" {
			r.Shared[c.Status] = experiment.SharedBy(wf, c.Status)
		}
	}
	r.wf = wf
	return nil
}

func containsCell(cs []experiment.Cell, c experiment.Cell) bool {
	for _, x := range cs {
		if x.Type == c.Type && x.Status == c.Status && x.System == c.System {
			return true
		}
	}
	return false
}

// tasks finds the tasks to check: the benchmark's, else the proposal's
// "## Bench tasks" list, and reaches each.
func (r *cellReport) tasks(ctx *Context, issue *tracker.Issue, block *experiment.Block, benchFlag string) {
	var refs []experiment.TaskRef
	name, err := experiment.PickBenchmark(benchFlag, block, experiment.BenchDir())
	if err == nil {
		b, lerr := bench.Load(name)
		if lerr == nil {
			r.TaskFrom = "benchmark " + name
			refs = benchTaskRefs(ctx, b.Tasks)
		} else {
			r.TasksNote = lerr.Error()
		}
	} else {
		r.TasksNote = err.Error()
	}
	if r.TaskFrom == "" {
		if slugs := benchTaskSlugs(issue.BodyRaw); len(slugs) > 0 {
			r.TaskFrom = "the proposal's ## Bench tasks"
			for _, s := range slugs {
				refs = append(refs, boardTaskRef(ctx, s, "", ""))
			}
		}
	}
	r.Tasks = experiment.Reach(r.wf, r.Cells, refs)
	r.Reached, r.Checked = experiment.ReachCount(r.Tasks)
}

// benchTaskRefs are b's tasks as reach reads them: type and start from the
// task, the system (and a missing type) from its issue on this board.
func benchTaskRefs(ctx *Context, tasks []bench.Task) []experiment.TaskRef {
	var out []experiment.TaskRef
	for _, t := range tasks {
		ref := boardTaskRef(ctx, t.Slug, t.Type, t.Start)
		if t.Project != "" && t.Project != ctx.Project.Slug {
			ref = experiment.TaskRef{Slug: t.Project + "/" + t.Slug, Type: t.Type, Start: t.Start, Unread: "from project " + t.Project + ": its workflow is not read"}
		} else if t.Workflow != "" && t.Workflow != bench.ProductWorkflow {
			ref.Unread = "runs on the " + t.Workflow + " department's workflow, not read"
		}
		out = append(out, ref)
	}
	return out
}

// boardTaskRef is a task with its type and system filled from its issue on
// this board; a slug the board doesn't have is unread.
func boardTaskRef(ctx *Context, slug, typ, start string) experiment.TaskRef {
	ref := experiment.TaskRef{Slug: slug, Type: typ, Start: start}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil || issue == nil {
		if typ == "" {
			ref.Unread = "no issue " + slug + " on this board"
		}
		return ref
	}
	if ref.Type == "" {
		ref.Type = issue.Type
	}
	ref.System = issue.System
	return ref
}

// benchTaskSlugs are the slugs listed under body's "## Bench tasks": each
// list item's first word, backticks dropped.
func benchTaskSlugs(body string) []string {
	text, ok := tracker.SectionText(body, "Bench tasks")
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		item, ok := strings.CutPrefix(line, "- ")
		if !ok {
			continue
		}
		fields := strings.Fields(strings.Trim(strings.TrimSpace(item), "`"))
		if len(fields) == 0 {
			continue
		}
		s := strings.Trim(fields[0], "`,;:.")
		if strings.Contains(s, "/") {
			out = append(out, s)
		}
	}
	return out
}

func (r *cellReport) print(w io.Writer) {
	fmt.Fprintf(w, "== Prompt cells: %s ==\n", r.Slug)
	base := r.Base + " " + gitdir.ShortSHA(r.BaseSHA)
	if r.RefSHA != "" {
		fmt.Fprintf(w, "Change: %s at %s → %s %s\n", r.File, base, r.Ref, gitdir.ShortSHA(r.RefSHA))
	}
	if len(r.Named) > 0 {
		fmt.Fprintf(w, "Named: --cell %s, read from %s at %s\n", strings.Join(r.Named, ", --cell "), r.File, base)
	}
	if len(r.Cells) == 0 {
		fmt.Fprintf(w, "Cells it edits: none (%s's prompts are the same on both sides)\n", r.File)
	} else {
		fmt.Fprintf(w, "Cells it edits (%d):\n", len(r.Cells))
		width := 0
		for _, c := range r.Cells {
			width = max(width, len([]rune(c.Label())))
		}
		for _, c := range r.Cells {
			fmt.Fprintf(w, "  %-*s  %s\n", width, c.Label(), r.ownerText(c))
		}
	}
	switch {
	case r.TaskFrom == "" && r.TasksNote != "":
		fmt.Fprintf(w, "Tasks: none to check (%s; the proposal has no ## Bench tasks list)\n", r.TasksNote)
	case r.TaskFrom == "":
		fmt.Fprintln(w, "Tasks: none to check (no benchmark, and the proposal has no ## Bench tasks list)")
	default:
		fmt.Fprintf(w, "Tasks (%s, %d):\n", r.TaskFrom, len(r.Tasks))
		for _, t := range r.Tasks {
			typ := t.Type
			if t.Task.Type != "" && t.Task.Type != t.Type {
				typ = t.Task.Type + " → " + t.Type
			}
			if typ != "" {
				typ = " (" + typ + ")"
			}
			switch {
			case !t.Checked:
				fmt.Fprintf(w, "  – %s: %s\n", t.Task.Slug, strings.Join(t.Why, "; "))
			case t.Reached():
				var ls []string
				for _, c := range t.Cells {
					ls = append(ls, c.Label())
				}
				fmt.Fprintf(w, "  ✓ %s%s: %s\n", t.Task.Slug, typ, strings.Join(ls, ", "))
			default:
				fmt.Fprintf(w, "  ✗ %s%s: %s\n", t.Task.Slug, typ, strings.Join(t.Why, "; "))
			}
		}
	}
	fmt.Fprintln(w, r.reachLine())
}

func (r *cellReport) ownerText(c experiment.Cell) string {
	if c.Owner != experiment.OwnerBase {
		return c.Owner + "'s own prompt"
	}
	if shared := r.Shared[c.Status]; len(shared) > 0 {
		return "base prompt, shared by " + strings.Join(shared, ", ")
	}
	return "base prompt"
}

// reachLine is the last line: how many checked tasks walk through an
// edited cell, and what to do when none does.
func (r *cellReport) reachLine() string {
	unread := len(r.Tasks) - r.Checked
	tail := ""
	if unread > 0 {
		tail = fmt.Sprintf(" (%d not read)", unread)
	}
	switch {
	case len(r.Tasks) == 0:
		return "Reach: no tasks to check"
	case len(r.Cells) == 0:
		return fmt.Sprintf("Reach: 0 of %d tasks%s: no prompt cell edited", r.Checked, tail)
	case r.Reached == 0 && r.Checked > 0:
		return fmt.Sprintf("Reach: 0 of %d tasks%s: the runs would measure nothing this change does; put the line in the cells the tasks walk through, or pick tasks of the types it edits", r.Checked, tail)
	}
	return fmt.Sprintf("Reach: %d of %d tasks%s", r.Reached, r.Checked, tail)
}
