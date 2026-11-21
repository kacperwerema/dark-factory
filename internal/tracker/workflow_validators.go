package tracker

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/reellegal"
	"github.com/kacperwerema/dark-factory/internal/tracker/validations"
)

// isStructuredRule reports whether the named rule is implemented by the
// validations sub-package. Legacy colon-string rules (body_not_empty,
// has_assignee, …) keep flowing through checkRule.
func isStructuredRule(name string) bool {
	return validations.Has(name)
}

// checkAction delegates a structured-validate action to the validations
// sub-package, after translating the tracker's native types into the narrow
// view the validators expect.
func (w *WorkflowConfig) checkAction(action WorkflowAction, issue *Issue, _ []Comment) error {
	cfg := validations.Config{
		AllowShell:  w.AllowShell,
		IssuesRoot:  w.IssuesRoot,
		ProjectRoot: w.ProjectRoot,
	}
	if w.LookupIssue != nil {
		cfg.Lookup = func(slug string) *validations.IssueView {
			linked := w.LookupIssue(slug)
			if linked == nil {
				return nil
			}
			return toIssueView(linked)
		}
	}
	if w.ChildrenOf != nil {
		cfg.Children = func(slug string) []*validations.IssueView {
			var out []*validations.IssueView
			for _, c := range w.ChildrenOf(slug) {
				v := toIssueView(c)
				if w.ClosedIssue != nil {
					v.Closed = w.ClosedIssue(c)
				} else {
					v.Closed = IsClosedStatus(w, c.Status)
				}
				out = append(out, v)
			}
			return out
		}
	}
	view := toIssueView(issue)
	view.Department = w.departmentKey(issue)
	return validations.Check(toAction(action), view, cfg)
}

// departmentKey is the key of the department issue's workflow belongs to:
// the department file this config came from, else the issue's department:
// field; "" for Product.
func (w *WorkflowConfig) departmentKey(issue *Issue) string {
	if w.Department != "" {
		return DepartmentKey(w.Department)
	}
	if issue != nil {
		return DepartmentKey(strings.TrimSpace(issue.Field("department")))
	}
	return ""
}

func toAction(a WorkflowAction) validations.Action {
	return validations.Action{
		Rule:           a.Rule,
		Field:          a.Field,
		Pattern:        a.Pattern,
		Section:        a.Section,
		Command:        a.Command,
		Path:           a.Path,
		RefKey:         a.RefKey,
		LinkedStatus:   a.LinkedStatus,
		Hint:           a.Hint,
		Values:         append([]string(nil), a.Values...),
		Min:            a.Min,
		Max:            a.Max,
		TimeoutSeconds: a.TimeoutSeconds,
	}
}

// toIssueView denormalizes the issue's frontmatter into a flat key→value
// map covering both the known Go-typed fields and any custom (extra)
// frontmatter values. List-typed extras are represented by an empty string
// (the key is present so HasKey returns true, but the value is opaque).
func toIssueView(issue *Issue) *validations.IssueView {
	if issue == nil {
		return nil
	}
	fm := map[string]string{}
	if issue.Title != "" {
		fm["title"] = issue.Title
	}
	if issue.Status != "" {
		fm["status"] = issue.Status
	}
	if issue.System != "" {
		fm["system"] = issue.System
	}
	if issue.Version != "" {
		fm["version"] = issue.Version
	}
	if issue.Priority != "" {
		fm["priority"] = issue.Priority
	}
	if issue.Assignee != "" {
		fm["assignee"] = issue.Assignee
	}
	if issue.HumanApproval != "" {
		fm["human_approval"] = issue.HumanApproval
	}
	if issue.StartedAt != "" {
		fm["started_at"] = issue.StartedAt
	}
	if issue.DoneAt != "" {
		fm["done_at"] = issue.DoneAt
	}
	if issue.Created != "" {
		fm["created"] = issue.Created
	}
	if issue.Number != 0 {
		fm["number"] = fmt.Sprintf("%d", issue.Number)
	}
	if issue.Repo != "" {
		fm["repo"] = issue.Repo
	}
	for _, ef := range issue.ExtraFields {
		if ef.IsList {
			fm[ef.Key] = ""
			continue
		}
		fm[ef.Key] = ef.Value
	}
	return &validations.IssueView{
		Slug:        issue.Slug,
		Title:       issue.Title,
		Status:      issue.Status,
		System:      issue.System,
		Priority:    issue.Priority,
		Repo:        issue.Repo,
		Assignee:    issue.Assignee,
		BodyRaw:     issue.BodyRaw,
		Number:      issue.Number,
		Labels:      append([]string(nil), issue.Labels...),
		Frontmatter: fm,
	}
}

// structuredSummary returns a short one-liner for a structured validate
// action, used by DescribeAction in transition previews.
func structuredSummary(action WorkflowAction) string {
	rule := strings.TrimSpace(action.Rule)
	switch rule {
	case "field_present":
		return fmt.Sprintf("Validate frontmatter %q is set", action.Field)
	case "field_not_empty":
		return fmt.Sprintf("Validate frontmatter %q is not empty", action.Field)
	case "field_in":
		return fmt.Sprintf("Validate frontmatter %q ∈ [%s]", action.Field, strings.Join(action.Values, ", "))
	case "field_matches":
		return fmt.Sprintf("Validate frontmatter %q matches /%s/", action.Field, action.Pattern)
	case "has_label":
		return fmt.Sprintf("Validate label %q is set", action.Field)
	case "has_any_label":
		return "Validate at least one label is set"
	case "has_pr_url":
		return "Validate frontmatter \"pr\" is a github PR url"
	case "linked_issue_in_status":
		return fmt.Sprintf("Validate linked issue (frontmatter %q) is in status %q", action.RefKey, action.LinkedStatus)
	case "has_section":
		return fmt.Sprintf("Validate section %q exists", action.Section)
	case "children_closed":
		if len(action.Values) == 0 {
			return "Validate every child issue is done, parked or obsolete"
		}
		return fmt.Sprintf("Validate every child issue is in [%s]", strings.Join(action.Values, ", "))
	case "section_min_length":
		return fmt.Sprintf("Validate section %q has ≥%d chars", action.Section, action.Min)
	case "section_min_links":
		return fmt.Sprintf("Validate section %q has ≥%d distinct http(s) links", action.Section, action.Min)
	case "section_max_length":
		return fmt.Sprintf("Validate section %q has ≤%d chars", action.Section, action.Max)
	case "no_todo_markers":
		return "Validate body has no TODO/FIXME markers"
	case "file_exists":
		return fmt.Sprintf("Validate file %s exists", action.Path)
	case "reel_legal_check":
		sec := action.Section
		if sec == "" {
			sec = "Script"
		}
		return fmt.Sprintf("Validate the reel's ## %s meets the legal check (issue-cli reel-legal)", sec)
	case "command_succeeds":
		cmd := action.Command
		if len(cmd) > 60 {
			cmd = cmd[:60] + "…"
		}
		return fmt.Sprintf("Validate shell command succeeds: %s", cmd)
	}
	return fmt.Sprintf("Validate %s", rule)
}

// ReelLegalAction is the reel_legal_check action the gate runs: the first
// one on a transition out of status (else out of any status), or the
// default action when the workflow has none, so issue-cli reel-legal
// checks with the same section and rules file the gate does.
func (w *WorkflowConfig) ReelLegalAction(status string) WorkflowAction {
	var any *WorkflowAction
	for i := range w.Transitions {
		t := &w.Transitions[i]
		for j := range t.Actions {
			a := &t.Actions[j]
			if a.Type != "validate" || strings.TrimSpace(a.Rule) != "reel_legal_check" {
				continue
			}
			if t.From == status {
				return *a
			}
			if any == nil {
				any = a
			}
		}
	}
	if any != nil {
		return *any
	}
	return WorkflowAction{Type: "validate", Rule: "reel_legal_check"}
}

// ReelLegalFindings runs action (a reel_legal_check) on issue the way the
// gate does and returns each finding; an error is a rules file or a
// section it can't read.
func (w *WorkflowConfig) ReelLegalFindings(action WorkflowAction, issue *Issue) ([]reellegal.Finding, error) {
	cfg := validations.Config{IssuesRoot: w.IssuesRoot, ProjectRoot: w.ProjectRoot}
	view := toIssueView(issue)
	view.Department = w.departmentKey(issue)
	return validations.ReelLegalFindings(toAction(action), view, cfg)
}
