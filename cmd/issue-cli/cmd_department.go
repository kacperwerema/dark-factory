package main

import (
	"fmt"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/depttemplate"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// departmentListRow is one row of department list.
type departmentListRow struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // built-in | template | configured
	Description string `json:"description"`
	Workflow    string `json:"workflow,omitempty"`
	Installed   bool   `json:"installed"`
	Add         string `json:"add,omitempty"`
}

// departmentRows are HQ and Product, each bundled template, then the
// project's departments no template made.
func departmentRows(proj *tracker.Project) []departmentListRow {
	rows := []departmentListRow{
		{Key: "hq", Name: "HQ", Kind: "built-in", Description: "the CEO's room: the business goals the departments roll up into", Installed: true},
		{Key: "product", Name: tracker.ProductName, Kind: "built-in", Description: "the software factory: every issue no other department claims", Workflow: "development", Installed: true},
	}
	byTemplate := map[string]bool{}
	for _, t := range depttemplate.List() {
		r := departmentListRow{Key: t.Key, Name: t.Name, Kind: "template", Description: t.Description, Workflow: t.Workflow, Installed: t.Installed(proj)}
		if !r.Installed {
			r.Add = tracker.InjectProject(proj, "issue-cli department add "+t.Key)
		}
		byTemplate[tracker.DepartmentKey(t.Name)] = true
		rows = append(rows, r)
	}
	for _, d := range proj.Departments {
		if key := tracker.DepartmentKey(d.Name); !byTemplate[key] {
			rows = append(rows, departmentListRow{Key: key, Name: d.Name, Kind: "configured", Description: d.Role, Workflow: d.Workflow, Installed: true})
		}
	}
	return rows
}

func runDepartmentList(ctx *Context, args []string) error {
	if _, err := parsePositionals(ctx, "department list", args, 0); err != nil {
		return err
	}
	rows := departmentRows(ctx.Project)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rows)
	}
	w := ctx.Stdout
	section := ""
	for _, r := range rows {
		head := map[string]string{"built-in": "Built in", "template": "Templates", "configured": "Also in projects.yaml"}[r.Kind]
		if head != section {
			if section != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "== %s ==\n", head)
			section = head
		}
		state := ""
		switch {
		case r.Kind == "template" && r.Installed:
			state = " ✓ installed"
		case r.Kind == "template":
			state = " (not installed)"
		}
		fmt.Fprintf(w, "  %-10s %s%s\n", r.Key, r.Name, state)
		desc := r.Description
		if r.Workflow != "" {
			desc += " · workflow: " + r.Workflow
		}
		if desc != "" {
			fmt.Fprintf(w, "             %s\n", desc)
		}
		if r.Add != "" {
			fmt.Fprintf(w, "             add: %s\n", r.Add)
		}
	}
	return nil
}

// refuseWorker stops a worker from editing the owner's projects.yaml.
func refuseWorker(ctx *Context, what string) error {
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	if me := whoAmI(ctx.Project, issues); me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers never %s: it edits the owner's projects.yaml. Ask the CEO on your issue trail (issue-cli comment <slug> \"question: …\")", what)
	}
	return nil
}

func runDepartmentAdd(ctx *Context, args []string) error {
	fs := newFlagSet("department add", ctx)
	name := fs.String("name", "", "install the template under this department name (default: the template's)")
	dry := fs.Bool("dry-run", false, "print the plan, change nothing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli department add <template> [--name <Name>] [--dry-run]\nTemplates: " + strings.Join(depttemplate.Keys(), ", ")}
	}
	proj := ctx.Project
	tmpl, err := depttemplate.Load(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("%v; templates: %s (issue-cli department list)", err, strings.Join(depttemplate.Keys(), ", "))
	}
	if !*dry {
		if err := refuseWorker(ctx, "add departments"); err != nil {
			return err
		}
	}
	plan, err := depttemplate.NewPlan(proj, tmpl, *name)
	if err != nil {
		return err
	}
	if !*dry {
		if err := plan.Apply(proj); err != nil {
			return err
		}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, struct {
			*depttemplate.Plan
			DryRun bool `json:"dry_run"`
		}{plan, *dry})
	}
	w := ctx.Stdout
	if *dry {
		fmt.Fprintf(w, "Dry run: department add %s (nothing changed)\n", tmpl.Key)
	} else {
		fmt.Fprintf(w, "✓ Added department %s (template %s)\n", plan.Name, tmpl.Key)
	}
	fmt.Fprintf(w, "Files (under %s):\n", plan.Root)
	for _, f := range plan.Files {
		word := map[string]string{
			depttemplate.FileWrite: "write",
			depttemplate.FileSame:  "already there, same as the template",
			depttemplate.FileKeep:  "kept your edited file (differs from the template, not overwritten)",
		}[f.State]
		if f.State == depttemplate.FileWrite && !*dry {
			word = "wrote"
		}
		fmt.Fprintf(w, "  %s: %s\n", f.Path, word)
	}
	verb := "Added to"
	if *dry {
		verb = "Would add to"
	}
	fmt.Fprintf(w, "%s project %s in %s, under departments:\n%s", verb, proj.Slug, plan.Config, indentLines(plan.Entry, "  "))
	if *dry {
		fmt.Fprintf(w, "Apply: %s\n", tracker.InjectProject(proj, "issue-cli department add "+tmpl.Key+nameArg(*name)))
		return nil
	}
	fmt.Fprintln(w, "Next:")
	for _, c := range []string{
		"issue-cli department show " + plan.Key,
		"issue-cli create --department " + plan.Key + " \"<first brief>\"",
		"issue-cli lead spawn --department " + plan.Key,
	} {
		fmt.Fprintf(w, "  %s\n", tracker.InjectProject(proj, c))
	}
	fmt.Fprintf(w, "Edit %s to say what this department should know.\n", plan.Files[len(plan.Files)-1].Path)
	return nil
}

func nameArg(name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(" --name %q", name)
}

func indentLines(s, pad string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(strings.TrimRight(s, "\n")+"\n", "\n") {
		if l != "" {
			b.WriteString(pad + l)
		}
	}
	return b.String()
}

func runDepartmentRemove(ctx *Context, args []string) error {
	fs := newFlagSet("department remove", ctx)
	dry := fs.Bool("dry-run", false, "say what would change, change nothing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli department remove <name> [--dry-run]"}
	}
	proj := ctx.Project
	arg := fs.Arg(0)
	switch tracker.DepartmentKey(arg) {
	case "hq", strings.ToLower(tracker.ProductName):
		return fmt.Errorf("%s is built in and can't be removed", arg)
	}
	d := proj.DepartmentByKey(arg)
	if d == nil {
		return fmt.Errorf("no department %q (departments: %s)", arg, strings.Join(proj.DepartmentKeys(), ", "))
	}
	if !*dry {
		if err := refuseWorker(ctx, "remove departments"); err != nil {
			return err
		}
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	var open []*tracker.Issue
	for _, issue := range issues {
		if proj.DepartmentOf(issue) == d.Name && !proj.IssueDone(issue) {
			open = append(open, issue)
		}
	}
	if len(open) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "department %s has %d open issue(s); move or finish them first:\n", d.Name, len(open))
		for _, issue := range open {
			fmt.Fprintf(&b, "  %s (%s): %s\n", issue.Slug, issue.Status,
				tracker.InjectProject(proj, fmt.Sprintf("issue-cli set-meta %s --key department --value <Department> --status <status>", issue.Slug)))
		}
		return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
	}
	var left []string
	for _, f := range []string{d.Workflow, d.Prompt} {
		if f != "" {
			left = append(left, f)
		}
	}
	if !*dry {
		if err := depttemplate.RemoveEntry(proj.ConfigPath, proj.Slug, d.Name); err != nil {
			return err
		}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"removed": d.Name, "config": proj.ConfigPath, "files_left": left, "dry_run": *dry})
	}
	if *dry {
		fmt.Fprintf(ctx.Stdout, "Dry run: would remove department %s from project %s in %s (nothing changed)\n", d.Name, proj.Slug, proj.ConfigPath)
	} else {
		fmt.Fprintf(ctx.Stdout, "✓ Removed department %s from project %s in %s\n", d.Name, proj.Slug, proj.ConfigPath)
	}
	if len(left) > 0 {
		fmt.Fprintf(ctx.Stdout, "Files left in place (delete them by hand if you don't need them): %s\n", strings.Join(left, ", "))
	}
	return nil
}
