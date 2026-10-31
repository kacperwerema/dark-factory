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
	live := func(name string) bool { return in.Live != nil && in.Live(name) }
	// boss is who this lead reports to: its group's lead while live, else
	// the CEO.
	boss := "the CEO " + ceo
	group := proj.GroupOf(dept.Name)
	children := proj.Children(dept.Name)
	if group != nil {
		// Up the chain: each group's lead while it is live, nearest first,
		// else the CEO (factory/nested-groups-…#D-2).
		var parts []string
		for _, g := range proj.Ancestors(dept.Name) {
			gl := DepartmentLeadName(proj, g.Name)
			parts = append(parts, fmt.Sprintf("the %s lead %s while it is live (now %s)", g.Name, gl, liveText(live(gl))))
		}
		boss = strings.Join(parts, ", else ") + ", else the CEO " + ceo
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are the %s lead of %s. You run in %s, in the project checkout, and report to %s.\n\n", dept.Name, projectName, lead, boss)
	if len(children) > 0 {
		fmt.Fprintf(&b, "%s is a group: you are the lead of its departments' leads. They report to you while you are live; while one is not running, you cover its department.\n\n", dept.Name)
	}
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
	fmt.Fprintf(&b, "- Asking another department: hand it off, issue-cli handoff <department> --title \"...\" --why \"...\" [--for <your issue>]. requested_by: %s and the route are filled for you; engine work goes to Product through the CEO and waits for the CEO's forward. issue-cli handoffs --from %s shows each one's state (your inbox: --to %s), and the issue you name gets a handoff: comment when it is done or declined. When you use another department's output, issue-cli output use <id> --by %s. issue-cli connections --from %s and --to %s list what passes between you and the others.\n", dept.Name, key, key, key, key, key)
	b.WriteString(routesBrief(proj, key))
	b.WriteString(ConnectorsBrief(proj, dept.Name, true))
	if len(children) > 0 {
		b.WriteString("\n## Your departments\n\n")
		for _, c := range children {
			cl := DepartmentLeadName(proj, c.Name)
			fmt.Fprintf(&b, "- %s: lead %s (%s)", c.Name, cl, liveText(live(cl)))
			if sub := proj.Children(c.Name); len(sub) > 0 {
				var names []string
				for _, s := range sub {
					names = append(names, s.Name)
				}
				fmt.Fprintf(&b, ". A group itself: %s report to its lead", strings.Join(names, ", "))
			}
			if m := strings.TrimSpace(c.Mission); m != "" {
				fmt.Fprintf(&b, ". Mission: %s", m)
			}
			fmt.Fprintf(&b, ". Issues: issue-cli list --department %s; goals: issue-cli goals %s.\n", DepartmentKey(c.Name), DepartmentKey(c.Name))
		}
		fmt.Fprintf(&b, "- Start a department's lead only when the human says so: issue-cli lead spawn --department <key>. Talk to a live one with SendMessage (ListAgents lists them), for coordination only.\n")
		fmt.Fprintf(&b, "- The week: issue-cli department week %s reads your departments' goals, outputs, jobs, handoffs and what is blocked on whom; --write writes the week report your room on the company page links. The weekly plan is a job (issue-cli jobs %s); its run writes ## Week plan and files work as issues at their first status, never starting any.\n", key, key)
		b.WriteString("- Handoffs routed via you wait for your forward: issue-cli handoffs --waiting, then issue-cli handoff forward <slug> or issue-cli handoff decline <slug> --reason \"...\". A forward writes a comment only: it starts no worker and passes no gate.\n")
	}
	text, path, err := RolePrompt(proj, dept.Name)
	switch {
	case err != nil:
		fmt.Fprintf(&b, "- Role prompt %s not found (issue-cli department show %s).\n", path, key)
	case text != "":
		fmt.Fprintf(&b, "\n### Role\n\n%s\n", text)
	}

	b.WriteString("\n## Your role\n\n")
	fmt.Fprintf(&b, "- Role: %s lead (%s). You answer to the human and report to %s.\n", dept.Name, lead, boss)
	b.WriteString(LeadRules)
	b.WriteString(JobsBrief(len(dept.Jobs), false))
	if len(children) > 0 {
		var names []string
		for _, c := range proj.Descendants(dept.Name) {
			names = append(names, c.Name)
		}
		fmt.Fprintf(&b, "- Scope: %s's issues and its departments' (%s). issue-cli dispatch and approve refuse anything else; Product's work and other departments go to the CEO %s.\n", dept.Name, strings.Join(names, ", "), ceo)
	} else {
		fmt.Fprintf(&b, "- Scope: only %s's issues. issue-cli dispatch and approve refuse anything else; Product's work and other departments go to the CEO %s.\n", dept.Name, ceo)
	}
	b.WriteString("- Authority: never Human Testing (the human approves it in the viewer); workers never tick.\n")
	reportTo := ceo
	if group != nil {
		reportTo = DepartmentLeadName(proj, group.Name) + " (or the CEO " + ceo + " while it is not running)"
	}
	fmt.Fprintf(&b, "- Reporting: the CEO follows your department on the board. Report with progress: comments on your issues; message %s (SendMessage) only for coordination, never for decisions. Workers are agent-<slug>; they message you only when the human tells them to.\n", reportTo)
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

func liveText(live bool) string {
	if live {
		return "live"
	}
	return "not running"
}

// routesBrief is the briefing line naming the request routes projects.yaml
// declares from department key (connections: requests:); "" when the
// project declares none. A request off them is refused by create.
func routesBrief(proj *Project, key string) string {
	if proj.Connections == nil || len(proj.Connections.Requests) == 0 {
		return ""
	}
	var routes []string
	for _, r := range proj.Connections.Requests {
		if DepartmentKey(strings.TrimSpace(r.From)) != key {
			continue
		}
		s := strings.TrimSpace(r.To)
		if v := strings.TrimSpace(r.Via); v != "" {
			s += " via " + v
		}
		routes = append(routes, s)
	}
	if len(routes) == 0 {
		return "- Your routes: projects.yaml declares none from you, so issue-cli create refuses your requests to another department; ask the CEO with a question: comment.\n"
	}
	return fmt.Sprintf("- Your routes (projects.yaml connections:): you may ask %s. A request off them is refused; one via another box waits until that box's lead forwards it.\n", strings.Join(routes, "; "))
}
