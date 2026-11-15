package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LeadScopeDepartmentPrefix starts a department lead's scope:
// department:<key>, exported to its session as ISSUE_VIEWER_LEAD_SCOPE.
const LeadScopeDepartmentPrefix = "department:"

// RolePromptMax is how much of a role prompt file a briefing inlines; the
// rest is left to issue-cli department show.
const RolePromptMax = 8 << 10

// DepartmentKey is a department's key in names and commands: its name in
// lower case, spaces as - (goals.DeptKey).
func DepartmentKey(name string) string { return Slugify(name) }

// LeadScopeDepartment is department dept's lead scope.
func LeadScopeDepartment(dept string) string {
	return LeadScopeDepartmentPrefix + DepartmentKey(dept)
}

// DepartmentLeadName is the tmux session (or shared-session window) name of
// department dept's lead: lead-<project-slug>-<dept key>. Like the CEO's,
// the name is the lead's identity.
func DepartmentLeadName(proj *Project, dept string) string {
	return LeadSessionName(proj) + "-" + sessionSafe(DepartmentKey(dept))
}

// DepartmentByKey is the department whose name or key is key (any case), or
// nil. Product and HQ are never departments here: their lead is the CEO.
func (p *Project) DepartmentByKey(key string) *Department {
	if d := p.DepartmentNamed(key); d != nil {
		return d
	}
	if p == nil {
		return nil
	}
	key = DepartmentKey(key)
	for i := range p.Departments {
		if DepartmentKey(p.Departments[i].Name) == key {
			return &p.Departments[i]
		}
	}
	return nil
}

// DepartmentOfLead is the department whose lead session is name, or nil.
func (p *Project) DepartmentOfLead(name string) *Department {
	if p == nil {
		return nil
	}
	for i := range p.Departments {
		if DepartmentLeadName(p, p.Departments[i].Name) == name {
			return &p.Departments[i]
		}
	}
	return nil
}

// DepartmentKeys lists the departments' keys, in projects.yaml order.
func (p *Project) DepartmentKeys() []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, d := range p.Departments {
		out = append(out, DepartmentKey(d.Name))
	}
	return out
}

// RolePrompt reads department dept's role prompt file ("" is Product). path
// is the file ("" when none is configured); text is its content, cut at
// RolePromptMax with a pointer to issue-cli department show.
func RolePrompt(p *Project, dept string) (text, path string, err error) {
	var prompt string
	switch d := p.DepartmentNamed(dept); {
	case d != nil:
		prompt = d.Prompt
	case dept == "" && p != nil && p.Product != nil:
		prompt = p.Product.Prompt
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", nil
	}
	path = prompt
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Root(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", path, err
	}
	text = strings.TrimSpace(string(b))
	if len(text) > RolePromptMax {
		key := DepartmentKey(dept)
		if dept == "" {
			key = DepartmentKey(ProductName)
		}
		text = strings.TrimSpace(text[:RolePromptMax]) + fmt.Sprintf("\n\n[cut at %d KB: the rest is in %s, issue-cli department show %s]", RolePromptMax>>10, path, key)
	}
	return text, path, nil
}

// DepartmentPromptSection is the "## Your department" part of a worker's
// briefing for an issue in a department; "" for Product's issues. It
// carries the department's mission, workflow file, role prompt and the
// goals and shelf commands.
func DepartmentPromptSection(proj *Project, issue *Issue) string {
	if proj == nil || issue == nil {
		return ""
	}
	dept := proj.DepartmentOf(issue)
	if dept == "" {
		return ""
	}
	d := proj.DepartmentNamed(dept)
	key := DepartmentKey(dept)
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n## Your department\n\nYou work for %s.", dept)
	if m := strings.TrimSpace(d.Mission); m != "" {
		fmt.Fprintf(&b, " Mission: %s", m)
	}
	b.WriteString("\n")
	if r := strings.TrimSpace(d.Role); r != "" {
		fmt.Fprintf(&b, "- Role: %s\n", r)
	}
	if path := proj.DepartmentWorkflowPath(dept); path != "" {
		fmt.Fprintf(&b, "- Workflow: %s (the statuses this issue walks; issue-cli process workflow shows them).\n", filepath.Base(path))
	}
	if len(d.Goals) > 0 {
		fmt.Fprintf(&b, "- Goals: issue-cli goals %s.\n", key)
	}
	fmt.Fprintf(&b, "- What you make goes on the department's shelf for the owner's review: issue-cli output add --issue %s --title \"...\" --kind <post|page|design|report> --url|--file|--path ...\n", issue.Slug)
	b.WriteString(ConnectorsBrief(proj, dept, false))
	b.WriteString(PartnerBrief(proj, issue, proj.Root()))
	text, path, err := RolePrompt(proj, dept)
	switch {
	case err != nil:
		fmt.Fprintf(&b, "- Role prompt %s not found; issue-cli department show %s.\n", path, key)
	case text != "":
		if rel, err := filepath.Rel(proj.Root(), path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		fmt.Fprintf(&b, "\n### Role (%s)\n\n%s\n", path, text)
	}
	return b.String()
}
