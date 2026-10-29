package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// issueWorkflow is the workflow issue walks: its department's file, or
// Product's. A department file that doesn't load is an error naming it, so
// the command stops instead of walking another department's gates.
func issueWorkflow(ctx *Context, issue *tracker.Issue) (*tracker.WorkflowConfig, error) {
	wf := ctx.Project.LoadWorkflowForIssue(issue)
	if wf.LoadErr != nil {
		return nil, fmt.Errorf("%w\nFix the file (or remove workflow: from the department in projects.yaml); issue-cli health lint --check department lists every problem", wf.LoadErr)
	}
	return wf, nil
}

// issueWorkflowFileLabel is the "Workflow file:" value for an issue: its
// department's file, or Product's (workflowFileLabel).
func issueWorkflowFileLabel(p *tracker.Project, wf *tracker.WorkflowConfig) string {
	if wf != nil && wf.File != "" {
		if abs, err := filepath.Abs(wf.File); err == nil {
			return abs
		}
		return wf.File
	}
	return workflowFileLabel(p)
}

// printDepartmentLine says which department an issue is in, when the
// project has departments, and why its file didn't load.
func printDepartmentLine(w io.Writer, p *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig) {
	if p == nil || len(p.Departments) == 0 {
		return
	}
	dept := p.DepartmentOf(issue)
	if dept == "" {
		dept = "Product"
	}
	fmt.Fprintf(w, "Department: %s\n", dept)
	if wf != nil && wf.LoadErr != nil {
		fmt.Fprintf(w, "Workflow error: %v\n", wf.LoadErr)
	}
}

// departmentFlag resolves a --department value to the department's name:
// "" and "product" are Product (""), anything else must be in projects.yaml.
func departmentFlag(p *tracker.Project, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "product") {
		return "", nil
	}
	if d := p.DepartmentNamed(value); d != nil {
		return d.Name, nil
	}
	var names []string
	for _, d := range p.Departments {
		names = append(names, d.Name)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("--department %q: this project has no departments (add departments: to it in projects.yaml)", value)
	}
	return "", fmt.Errorf("--department %q: unknown department (departments: product, %s)", value, strings.Join(names, ", "))
}

// printWorkflowDepartments lists the departments on their own workflow
// file, so process output on Product's says the project has others.
func printWorkflowDepartments(w io.Writer, p *tracker.Project, shown, cmd string) {
	depts := p.WorkflowDepartments()
	if len(depts) == 0 {
		return
	}
	if shown != "" {
		fmt.Fprintf(w, "Department: %s\n", shown)
		return
	}
	fmt.Fprintln(w, "Departments on their own workflow file (this is Product's):")
	for _, d := range depts {
		fmt.Fprintf(w, "  %s: %s  (issue-cli %s --department %s)\n", d.Name, d.Workflow, cmd, d.Name)
	}
}
