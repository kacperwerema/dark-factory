package briefing

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// DepartmentLeadBrief renders the brief a department's lead is spawned
// with: who it is and who it reports to, the department (mission, workflow,
// issues, goals, shelf), its role prompt, then the same rules as the CEO's,
// limited to its own department. wf is the department's workflow.
func DepartmentLeadBrief(wf *tracker.WorkflowConfig, dept *tracker.Department, in LeadBriefInput) string {
	proj := in.Project
	projectName := ""
	if proj != nil {
		projectName = proj.Name
		if projectName == "" {
			projectName = proj.Slug
		}
	}
	key := tracker.DepartmentKey(dept.Name)
	lead := tracker.DepartmentLeadName(proj, dept.Name)
	ceo := tracker.LeadSessionName(proj)
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
			gl := tracker.DepartmentLeadName(proj, g.Name)
			parts = append(parts, fmt.Sprintf("the %s lead %s while it is live (now %s)", g.Name, gl, liveText(live(gl))))
		}
		boss = strings.Join(parts, ", else ") + ", else the CEO " + ceo
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are the %s lead of %s. You run in %s, in the project checkout, and report to %s.\n\n", dept.Name, projectName, lead, boss)
	if len(children) > 0 {
		fmt.Fprintf(&b, "%s is a group: you are the lead of its departments' leads. They report to you while you are live; while one is not running, you cover its department.\n\n", dept.Name)
	}
	b.WriteString(tracker.LeadDuties("your department's ideas", "the department's process") + "\n\n")
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
	b.WriteString(tracker.ConnectorsBrief(proj, dept.Name, true))
	if len(children) > 0 {
		b.WriteString("\n## Your departments\n\n")
		for _, c := range children {
			cl := tracker.DepartmentLeadName(proj, c.Name)
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
			fmt.Fprintf(&b, ". Issues: issue-cli list --department %s; goals: issue-cli goals %s.\n", tracker.DepartmentKey(c.Name), tracker.DepartmentKey(c.Name))
		}
		fmt.Fprintf(&b, "- Start a department's lead only when the human says so: issue-cli lead spawn --department <key>. Talk to a live one with SendMessage (ListAgents lists them), for coordination only.\n")
		fmt.Fprintf(&b, "- The week: issue-cli department week %s reads your departments' goals, outputs, jobs, handoffs and what is blocked on whom; --write writes the week report your room on the company page links. The weekly plan is a job (issue-cli jobs %s); its run writes ## Week plan and files work as issues at their first status, never starting any.\n", key, key)
		b.WriteString("- Handoffs routed via you wait for your forward: issue-cli handoffs --waiting, then issue-cli handoff forward <slug> or issue-cli handoff decline <slug> --reason \"...\". A forward writes a comment only: it starts no worker and passes no gate.\n")
	}
	text, path, err := tracker.RolePrompt(proj, dept.Name)
	switch {
	case err != nil:
		fmt.Fprintf(&b, "- Role prompt %s not found (issue-cli department show %s).\n", path, key)
	case text != "":
		fmt.Fprintf(&b, "\n### Role\n\n%s\n", text)
	}

	b.WriteString("\n## Your role\n\n")
	fmt.Fprintf(&b, "- Role: %s lead (%s). You answer to the human and report to %s.\n", dept.Name, lead, boss)
	b.WriteString(tracker.LeadRules)
	b.WriteString("- Partners: " + tracker.PartnerLicenceRule + "\n")
	b.WriteString(tracker.JobsBrief(len(dept.Jobs), false))
	b.WriteString(RulesLeadLines(wf))
	b.WriteString(MemoryLeadLine(proj, lead, dept.Name))
	b.WriteString(tracker.DecisionsLeadLines(tracker.LeadDecisionBlock(proj, dept.Name, wf, in.today()), dept.Name))
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
		reportTo = tracker.DepartmentLeadName(proj, group.Name) + " (or the CEO " + ceo + " while it is not running)"
	}
	fmt.Fprintf(&b, "- Reporting: the CEO follows your department on the board. Report with progress: comments on your issues; message %s (SendMessage) only for coordination, never for decisions. Workers are agent-<slug>; they message you only when the human tells them to.\n", reportTo)
	b.WriteString("- Decisions: record each owner decision under ## Decisions with the human's words (issue-cli decide <slug> --text \"...\" --how \"lead chat\" --quote \"<their words>\").\n")
	notesDir := filepath.Join(in.Root, "sessions")
	if note := tracker.NewestHandoffNote(notesDir, lead); note != "" {
		fmt.Fprintf(&b, "- Handoff: read %s first; it is your previous handoff note.\n", note)
	} else {
		b.WriteString("- Handoff: there is no earlier handoff note for you.\n")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	fmt.Fprintf(&b, "- Before you exit, or before the human takes a long break, write %s: what is live, what waits on whom, the decisions made, and a ## Next section.\n", filepath.Join(notesDir, tracker.HandoffNoteName(now, lead)))
	b.WriteString("- issue-cli lead whoami shows this role again; issue-cli lead list shows the CEO and the department leads.\n")
	return tracker.InjectProject(proj, b.String())
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
func routesBrief(proj *tracker.Project, key string) string {
	if proj.Connections == nil || len(proj.Connections.Requests) == 0 {
		return ""
	}
	var routes []string
	for _, r := range proj.Connections.Requests {
		if tracker.DepartmentKey(strings.TrimSpace(r.From)) != key {
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
