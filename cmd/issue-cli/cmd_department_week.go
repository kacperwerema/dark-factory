package main

import (
	"fmt"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/weekplan"
)

// runDepartmentWeek prints a department's or group's week (the evidence the
// group's lead plans from) and, with --write, writes the week report under
// the work files. It only reads, and writes that one file: nothing starts
// (docs/departments.md, "The week plan and report").
func runDepartmentWeek(ctx *Context, args []string) error {
	fs := newFlagSet("department week", ctx)
	write := fs.Bool("write", false, "write the week report to <issues>/.reports/<department>/<week>.md (replaces this week's)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli department week <group|department> [--write] [--json]"}
	}
	proj := ctx.Project
	if proj == nil {
		return fmt.Errorf("department week needs a project: pass --project <slug> or run from a project root")
	}
	dept, err := weekplan.Resolve(proj, fs.Arg(0))
	if err != nil {
		return err
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return err
	}
	now := ctx.now()
	var statuses []jobs.Status
	if len(jobs.List(proj)) > 0 {
		st, err := jobs.LoadState(yoloRoot(proj), proj.Slug)
		if err != nil {
			st = &jobs.State{}
		}
		// The viewer's read: never a start, so no budget check.
		statuses = jobs.Evaluate(proj, issues, jobs.Input{State: st, Now: now, SkipBudget: true, Limits: jobs.DefaultLimits})
	}
	w := weekplan.Gather(weekplan.Input{Project: proj, Issues: issues, Jobs: statuses, Now: now}, dept)
	if ctx.JSONOutput && !*write {
		return writeJSON(ctx.Stdout, w)
	}
	if !*write {
		fmt.Fprint(ctx.Stdout, weekplan.Markdown(w))
		fmt.Fprintf(ctx.Stdout, "\nWrite this week's report (shown in the %s room on the company page):\n  %s\n", dept.Name,
			tracker.InjectProject(proj, "issue-cli department week "+w.Box+" --write"))
		return nil
	}
	path, replaced, err := weekplan.Write(proj, w)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/p/%s/reports/%s/%s", viewerBaseURL(proj), proj.Slug, w.Box, w.Key)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"path": path, "replaced": replaced, "url": url, "week": w})
	}
	verb := "Wrote"
	if replaced {
		verb = "Replaced"
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s %s's week report %s: %s\n  %s\n  %s\n", verb, dept.Name, w.Key, w.Counts.Line(), path, url)
	if w.Plan == "" {
		fmt.Fprintf(ctx.Stdout, "No ## %s for %s yet: the run issue of the weekly job writes it (issue-cli jobs %s).\n", weekplan.PlanSection, w.Key, w.Box)
	}
	return nil
}
