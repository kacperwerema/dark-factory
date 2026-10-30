package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// DepartmentLeadBrief renders the brief a department's lead is spawned
// with: who it is and who it reports to, the department (mission, workflow,
// issues, goals, shelf), its role prompt, then the same rules as the CEO's,
// limited to its own department. wf is the department's workflow.
func DepartmentLeadBrief(wf *WorkflowConfig, dept *Department, in LeadBriefInput) string {
	proj := in.Project
	projectName := ""
	if proj != nil {
		projectName = proj.Name
		if projectName == "" {
			projectName = proj.Slug
		}
	}
	key := DepartmentKey(dept.Name)
	lead := DepartmentLeadName(proj, dept.Name)
	ceo := LeadSessionName(proj)

	var b strings.Builder
	fmt.Fprintf(&b, "You are the %s lead of %s. You run in %s, in the project checkout, and report to the CEO %s.\n\n", dept.Name, projectName, lead, ceo)
	b.WriteString("The human decides. You groom your department's ideas, dispatch the workers the human names, check on them, relay their questions and approvals, review their work, and fix the department's process from retros. The board is the source of truth.\n\n")
	fmt.Fprintf(&b, "## Your department\n\n")
	if m := strings.TrimSpace(dept.Mission); m != "" {
		fmt.Fprintf(&b, "- Mission: %s\n", m)
	}
	if r := strings.TrimSpace(dept.Role); r != "" {
		fmt.Fprintf(&b, "- Role: %s\n", r)
	}
	if path := proj.DepartmentWorkflowPath(dept.Name); path != "" {
		fmt.Fprintf(&b, "- Workflow: %s", filepath.Base(path))
		if wf != nil && len(wf.Statuses) > 0 {
			var names []string
			for _, s := range wf.Statuses {
				names = append(names, s.Name)
			}
			fmt.Fprintf(&b, " (%s)", strings.Join(names, " → "))
		}
		b.WriteString(".\n")
	} else {
		b.WriteString("- Workflow: Product's workflow.yaml.\n")
	}
	fmt.Fprintf(&b, "- Your issues: issue-cli list --department %s. Goals: issue-cli goals %s. Shelf: issue-cli outputs %s (the owner reviews each output).\n", key, key, key)
	b.WriteString(ConnectorsBrief(proj, dept.Name, true))
	text, path, err := RolePrompt(proj, dept.Name)
	switch {
	case err != nil:
		fmt.Fprintf(&b, "- Role prompt %s not found (issue-cli department show %s).\n", path, key)
	case text != "":
		fmt.Fprintf(&b, "\n### Role\n\n%s\n", text)
	}

	b.WriteString("\n## Your role\n\n")
	fmt.Fprintf(&b, "- Role: %s lead (%s). You answer to the human and report to the CEO %s.\n", dept.Name, lead, ceo)
	b.WriteString(LeadRules)
	b.WriteString(JobsBrief(len(dept.Jobs), false))
	fmt.Fprintf(&b, "- Scope: only %s's issues. issue-cli dispatch and approve refuse anything else; Product's work and other departments go to the CEO %s.\n", dept.Name, ceo)
	b.WriteString("- Authority: never Human Testing (the human approves it in the viewer); workers never tick.\n")
	fmt.Fprintf(&b, "- Reporting: the CEO follows your department on the board. Report with progress: comments on your issues; message the CEO (SendMessage to %s) only for coordination, never for decisions. Workers are agent-<slug>; they message you only when the human tells them to.\n", ceo)
	b.WriteString("- Decisions: record each owner decision under ## Decisions with the human's words (issue-cli decide <slug> --text \"...\" --how \"lead chat\" --quote \"<their words>\").\n")
	notesDir := filepath.Join(in.Root, "sessions")
	if note := NewestHandoffNote(notesDir, lead); note != "" {
		fmt.Fprintf(&b, "- Handoff: read %s first; it is your previous handoff note.\n", note)
	} else {
		b.WriteString("- Handoff: there is no earlier handoff note for you.\n")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	fmt.Fprintf(&b, "- Before you exit, or before the human takes a long break, write %s: what is live, what waits on whom, the decisions made, and a ## Next section.\n", filepath.Join(notesDir, HandoffNoteName(now, lead)))
	b.WriteString("- issue-cli lead whoami shows this role again; issue-cli lead list shows the CEO and the department leads.\n")
	return InjectProject(proj, b.String())
}
