package main

import (
	"fmt"
	"io"
	"path/filepath"
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
	// Group lists a group's departments (a group template's children, or
	// a configured group's); Parent is the group a department is under.
	Group  []string `json:"group,omitempty"`
	Parent string   `json:"parent,omitempty"`
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
		if t.Group {
			r.Group = append([]string{}, t.Children...)
		}
		if g := proj.GroupOf(t.Name); g != nil {
			r.Parent = g.Name
		}
		if !r.Installed {
			r.Add = tracker.InjectProject(proj, "issue-cli department add "+t.Key)
		}
		byTemplate[tracker.DepartmentKey(t.Name)] = true
		if d := t.InstalledAs(proj); d != nil {
			byTemplate[tracker.DepartmentKey(d.Name)] = true
		}
		rows = append(rows, r)
	}
	for _, d := range proj.Departments {
		if key := tracker.DepartmentKey(d.Name); !byTemplate[key] {
			r := departmentListRow{Key: key, Name: d.Name, Kind: "configured", Description: d.Role, Workflow: d.Workflow, Installed: true}
			for _, c := range proj.Children(d.Name) {
				r.Group = append(r.Group, tracker.DepartmentKey(c.Name))
			}
			if g := proj.GroupOf(d.Name); g != nil {
				r.Parent = g.Name
			}
			rows = append(rows, r)
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
		if len(r.Group) > 0 {
			state = " (group: " + strings.Join(r.Group, ", ") + ")" + state
		}
		if r.Parent != "" {
			state += " · in group " + r.Parent
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
	parent := fs.String("parent", "", "put the department under this group (a department of the project)")
	dry := fs.Bool("dry-run", false, "print the plan, change nothing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli department add <template> [--parent <group>] [--dry-run]\nTemplates: " + strings.Join(depttemplate.Keys(), ", ")}
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
	plan, err := depttemplate.NewPlanUnder(proj, tmpl, *parent)
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
	verb := "Added to"
	if *dry {
		verb = "Would add to"
	}
	printPlan(w, plan, proj, verb, *dry)
	for _, c := range plan.Children {
		fmt.Fprintf(w, "\nIn group %s: department %s (template %s)\n", plan.Name, c.Name, c.Template.Key)
		printPlan(w, c, proj, verb, *dry)
	}
	for _, n := range plan.Reparent {
		if *dry {
			fmt.Fprintf(w, "Would move %s under %s (parent: %s in projects.yaml; its files stay as they are)\n", n, plan.Name, plan.Key)
		} else {
			fmt.Fprintf(w, "Moved %s under %s (parent: %s in projects.yaml; its files stay as they are)\n", n, plan.Name, plan.Key)
		}
	}
	for _, n := range plan.Notes {
		fmt.Fprintln(w, n)
	}
	if *dry {
		args := tmpl.Key
		if *parent != "" {
			args += " --parent " + *parent
		}
		fmt.Fprintf(w, "Apply: %s\n", tracker.InjectProject(proj, "issue-cli department add "+args))
		return nil
	}
	fmt.Fprintln(w, "Next:")
	for _, c := range []string{
		"issue-cli department show " + plan.Key,
		"issue-cli create --department " + plan.Key + " --title \"<first brief>\"",
		"issue-cli lead spawn --department " + plan.Key,
	} {
		fmt.Fprintf(w, "  %s\n", tracker.InjectProject(proj, c))
	}
	if tmpl.Group {
		fmt.Fprintf(w, "%s is a group: its lead is the lead of its departments' leads. Add another department under it: %s\n", plan.Name,
			tracker.InjectProject(proj, "issue-cli department add <template> --parent "+plan.Key))
	}
	var roles, briefs []string
	files := append([]depttemplate.FileAction{}, plan.Files...)
	for _, c := range plan.Children {
		files = append(files, c.Files...)
	}
	for _, f := range files {
		switch {
		case strings.HasSuffix(f.Path, "/role.md"):
			roles = append(roles, f.Path)
		case strings.Contains(f.Path, "/jobs/"):
			briefs = append(briefs, f.Path)
		case strings.HasPrefix(f.Path, "workflows/"):
			departmentWorkflowNotes(w, proj, plan, tmpl.Workflow, f)
		}
	}
	fmt.Fprintf(w, "Edit %s to say what this department should know", strings.Join(roles, " and "))
	if len(briefs) > 0 {
		fmt.Fprintf(w, ", and each job's brief (%s)", strings.Join(briefs, ", "))
	}
	fmt.Fprintln(w, ".")
	fmt.Fprintf(w, "Templates carry no connectors: %s prints the config to add the outside tools it uses.\n",
		tracker.InjectProject(proj, "issue-cli connectors "+plan.Key))
	return nil
}

// printPlan prints a plan's files and the projects.yaml entry it adds.
func printPlan(w io.Writer, plan *depttemplate.Plan, proj *tracker.Project, verb string, dry bool) {
	fmt.Fprintf(w, "Files (under %s):\n", plan.Root)
	for _, f := range plan.Files {
		word := map[string]string{
			depttemplate.FileWrite: "write",
			depttemplate.FileSame:  "already there, same as the template",
			depttemplate.FileKeep:  "kept your edited file (differs from the template, not overwritten)",
		}[f.State]
		if f.State == depttemplate.FileWrite && !dry {
			word = "wrote"
		}
		fmt.Fprintf(w, "  %s: %s\n", f.Path, word)
	}
	fmt.Fprintf(w, "%s project %s in %s, under departments:\n%s", verb, proj.Slug, plan.Config, indentLines(plan.Entry, "  "))
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
	if kids := proj.Children(d.Name); len(kids) > 0 {
		var names []string
		for _, c := range kids {
			names = append(names, tracker.DepartmentKey(c.Name))
		}
		return fmt.Errorf("%s is a group with departments under it (%s); remove them first (issue-cli department remove <name>), or drop their parent: %s in projects.yaml", d.Name, strings.Join(names, ", "), tracker.DepartmentKey(d.Name))
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

// departmentWorkflowNotes names the types of work the department's workflow
// file has (a channel each, in the marketing template) and, when add kept an
// edited file, how to put the bundled template next to it to compare.
func departmentWorkflowNotes(w io.Writer, proj *tracker.Project, plan *depttemplate.Plan, template string, f depttemplate.FileAction) {
	if f.State == depttemplate.FileKeep {
		side := strings.TrimSuffix(f.Path, ".yaml") + ".template.yaml"
		fmt.Fprintf(w, "Your %s was kept. To compare it with the %s template, write the template next to it: %s\n", f.Path, template,
			tracker.InjectProject(proj, "issue-cli workflow init --template "+template+" --out "+side))
	}
	wf, err := tracker.LoadWorkflow(filepath.Join(plan.Root, filepath.FromSlash(f.Path)))
	if err != nil || !wf.HasTypes() {
		return
	}
	names := wf.TypeNames()
	names[0] += " (default)"
	fmt.Fprintf(w, "Types of work: %s. Pick one with %s\n", strings.Join(names, ", "),
		tracker.InjectProject(proj, "issue-cli create --department "+plan.Key+" --type <type> --title \"...\""))
}
