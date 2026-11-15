package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
// project has departments, and why its file didn't load. A department
// with a role prompt or goals gets the fuller departmentLine further down
// instead, so the name isn't printed twice.
func printDepartmentLine(w io.Writer, p *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig) {
	if p == nil || len(p.Departments) == 0 {
		return
	}
	dept := p.DepartmentOf(issue)
	if dept == "" {
		dept = "Product"
	}
	if departmentLine(p, issue) == "" {
		fmt.Fprintf(w, "Department: %s\n", dept)
	}
	if wf != nil && wf.LoadErr != nil {
		fmt.Fprintf(w, "Workflow error: %v\n", wf.LoadErr)
	}
	if cfg, _ := p.PartnerOf(issue); cfg != nil {
		st, err := partner.Read(*cfg, p.Root(), issue)
		var bridge partner.Bridge
		if err == nil {
			bridge = partner.BridgeOf(*cfg, p.Root(), issue, st)
		}
		line := "Partner: " + partner.Line(*cfg, st, err)
		if a := bridge.Answered; a != nil {
			line = "Partner: " + partner.AnsweredLine(*cfg, st.Stage, *a)
		}
		if err == nil && st.Project != "" {
			line += " (project " + st.Project + " at " + cfg.Dir(p.Root()) + ")"
		}
		if err == nil && st.State == partner.StateNone {
			line += "; another id: issue-cli set-meta " + issue.Slug + " " + partner.ProjectField + " <id>"
		}
		switch a := bridge.Answered; {
		case a != nil && a.Approved:
			line += fmt.Sprintf("\n  The owner approved %s (%s). Now %s.", st.Stage, a.Ref, partner.ApprovalHow(*cfg, st.Stage, a.Ref))
		case a != nil:
			line += fmt.Sprintf("\n  The owner sent %s back (%s)%s: revise it at %s and ask again; never approve it yourself.", st.Stage, a.Ref, quoted(a.Quote), cfg.Label())
		case err == nil && st.Awaiting():
			line += "\n  Waits for the owner on Needs you; write the partner's approval only after their D-n."
		}
		for _, u := range bridge.Unpaired {
			line += "\n  ⚠ " + u + ": the owner answers on Needs you, or re-write the checkpoint without the approval."
		}
		fmt.Fprintln(w, line)
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
	// The key works too ("growth-team" for Growth Team): workflow hints
	// fill {{department}} with it.
	if d := p.DepartmentByKey(value); d != nil {
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

// partnerConfigLine is department show's line on the department's
// partner: its name, adapter, checkout and pin, or what is wrong with it.
func partnerConfigLine(cfg tracker.Partner, root string) string {
	line := "Partner: " + cfg.Label() + " (adapter " + cfg.Adapter
	if _, err := partner.For(cfg); err != nil {
		line += ": " + err.Error()
	}
	line += ")"
	switch dir := cfg.Dir(root); {
	case dir == "":
		line += ", no path: set"
	default:
		line += " at " + dir
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			line += " (missing)"
		}
	}
	if c := strings.TrimSpace(cfg.Commit); c != "" {
		line += ", pinned " + c
	}
	return line + ". Read only: the stage of each issue shows in issue-cli show and on the Station."
}

// quoted is ": \"words\"" or "" for no words.
func quoted(words string) string {
	if words == "" {
		return ""
	}
	return fmt.Sprintf(": %q", words)
}
