package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/routing"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var leadCommand = &Command{
	Name:      "lead",
	ShortHelp: "The CEO (the project lead): spawn it, see who reports to whom (sub: spawn|whoami|list|message; alias: ceo)",
	LongHelp: `The CEO supervises workers; it is what was called the project lead.
There is one CEO per project (tmux lead-<project>, in the project
checkout); every worker and department reports to it, epic children
included. Only the human decides. issue-cli ceo is the same command.

Subcommands:
  spawn [--department <name>] [--headless] [--agent claude|codex]
      Start the CEO, or with --department a department's lead
      (lead-<project>-<dept>, reporting to the CEO; projects.yaml
      departments:). A live lead is re-attached, never started
      twice. lead_terminal: in projects.yaml decides what opens (unset:
      headless; tmux-window: a tab in lead_window_session); --headless
      opens nothing either way. Workers follow terminal: instead.
  whoami
      Your role, who you report to, and what you may tick.
  list
      The CEO and the department leads (live or not) and the open
      epics, with how many of their children are running.
  message <target> "text"
      Message a Codex session: pastes the text into it, signed
      [from <you>]. target: project (the CEO), department:<name> (its
      lead), a lead-/agent- session name, or an
      issue slug (its worker). A Claude Code session is never pasted into:
      the command refuses and names the address to use with your own
      SendMessage tool (ListAgents lists them). Workers can't message; they
      post question: comments instead.

Examples:
  issue-cli lead spawn
  issue-cli lead spawn --department marketing
  issue-cli lead whoami
  issue-cli lead message ui/workbench-redesign "please rebase on main"`,
	Run:         runLead,
	Subcommands: []string{"spawn", "whoami", "list", "message"},
}

func init() {
	registerCommand(leadCommand)
	registerAlias("ceo", "lead")
}

func runLead(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("lead requires a subcommand\n\nUsage:\n  issue-cli lead spawn [--department <name>] [--headless]\n  issue-cli lead whoami\n  issue-cli lead list\n  issue-cli lead message <target> \"text\"")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "spawn":
		return runLeadSpawn(ctx, rest)
	case "whoami":
		return runLeadWhoami(ctx, rest)
	case "list":
		return runLeadList(ctx, rest)
	case "message":
		return runLeadMessage(ctx, rest)
	default:
		return fmt.Errorf("unknown lead subcommand: %s\n\nValid: spawn, whoami, list, message", sub)
	}
}

// errSubLeadsRemoved answers `lead spawn --epic`, kept so old habits get a
// clear answer instead of an unknown-flag error.
const errSubLeadsRemoved = "sub-leads were removed (one CEO per project); issue-cli lead spawn starts the CEO"

func runLeadSpawn(ctx *Context, args []string) error {
	for _, a := range args {
		if name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); strings.HasPrefix(a, "-") && name == "epic" {
			return fmt.Errorf("%s", tracker.InjectProject(ctx.Project, errSubLeadsRemoved))
		}
	}
	fs := newFlagSet("lead spawn", ctx)
	headlessFlag := fs.Bool("headless", false, "open nothing, even when lead_terminal is set")
	fs.Bool("attach", false, "deprecated, no effect: lead_terminal decides")
	agentFlag := fs.String("agent", "", "agent to run: claude or codex (default: the workflow's routing: rules, else claude)")
	deptFlag := fs.String("department", "", "start this department's lead instead of the CEO")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	if me := whoAmI(proj, issues); me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers can't spawn the CEO (you are %s, detected from %s).\n\nPost a question: comment on your issue instead; the CEO relays it:\n  %s", valueOr(me.Name, "a worker"), me.Signal,
			tracker.InjectProject(proj, fmt.Sprintf("issue-cli comment %s \"question: ...\"", valueOr(me.Slug, "<slug>"))))
	}

	role := "CEO"
	spec := agentrun.LeadSpec(proj, proj.LoadWorkflow(), projectRoot(proj), ctx.now())
	deptKey := ""
	if name := strings.TrimSpace(*deptFlag); name != "" {
		dept, err := spawnDepartment(proj, name)
		if err != nil {
			return err
		}
		wf, err := proj.WorkflowFor(dept.Name)
		if err != nil {
			return fmt.Errorf("%w\n\nFix the department's workflow: file before starting its lead (issue-cli health lint)", err)
		}
		role = dept.Name + " lead"
		spec = agentrun.DepartmentLeadSpec(proj, dept, wf, projectRoot(proj), ctx.now())
		deptKey = tracker.DepartmentKey(dept.Name)
	}
	route := agentrun.RouteAgent(proj, spec.Workflow, projectRoot(proj), routing.Subject{Lead: true, Department: deptKey}, *agentFlag, ctx.now())
	spec.AgentType, spec.Route = route.Agent, &route
	spec.Headless = *headlessFlag

	resp := startAgent(proj, spec)
	if resp.Status == "error" {
		printSteps(ctx, resp.Steps)
		return fmt.Errorf("spawning %s failed (see the steps above)", spec.Session)
	}

	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, resp)
	}
	verb := "Spawned"
	if resp.Status == "reattached" {
		verb = "Already live:"
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s %s %s\n", verb, role, resp.Session)
	printSteps(ctx, resp.Steps)
	if resp.AttachCmd != "" {
		fmt.Fprintf(ctx.Stdout, "\nInside tmux:  %s\nFrom a shell: %s\n", resp.SwitchCmd, resp.AttachCmd)
		if agentrun.LeadHeadless(proj) && !*headlessFlag && os.Getenv("TMUX") != "" {
			fmt.Fprintln(ctx.Stdout, "tip: set lead_terminal: tmux-window and lead_window_session: <your session> in projects.yaml to open the CEO as a tab in your session")
		}
	}
	if *deptFlag == "" && len(proj.Departments) > 0 {
		fmt.Fprintf(ctx.Stdout, "tip: department leads: %s (%s)\n", tracker.InjectProject(proj, "issue-cli lead spawn --department <name>"), strings.Join(proj.DepartmentKeys(), ", "))
	}
	if *deptFlag == "" && len(proj.Departments) == 0 {
		fmt.Fprintf(ctx.Stdout, "tip: add a department (Marketing, …) from a template: %s\n", tracker.InjectProject(proj, "issue-cli department list"))
	}
	return nil
}

// spawnDepartment is the department lead spawn --department names. HQ and
// Product are the CEO's; anything else not in departments: is refused with
// the list.
func spawnDepartment(proj *tracker.Project, name string) (*tracker.Department, error) {
	switch strings.ToLower(name) {
	case "hq", strings.ToLower(tracker.ProductName):
		return nil, fmt.Errorf("%s is the CEO's: %s", name, tracker.InjectProject(proj, "issue-cli lead spawn"))
	}
	if d := proj.DepartmentByKey(name); d != nil {
		return d, nil
	}
	if len(proj.Departments) == 0 {
		return nil, fmt.Errorf("no department %q: this project has no departments: in projects.yaml (docs/departments.md)", name)
	}
	return nil, fmt.Errorf("no department %q (departments: %s)", name, strings.Join(proj.DepartmentKeys(), ", "))
}

func printSteps(ctx *Context, steps []agentrun.Step) {
	for _, s := range steps {
		mark := "✓"
		switch s.Status {
		case "error":
			mark = "✗"
		case "warning":
			mark = "!"
		}
		line := fmt.Sprintf("  %s %s", mark, s.Name)
		if s.Detail != "" {
			line += " — " + s.Detail
		}
		fmt.Fprintln(ctx.Stdout, line)
	}
}

type whoamiJSON struct {
	Role    string `json:"role"`
	Session string `json:"session,omitempty"`
	Scope   string `json:"scope,omitempty"`
	// Department is a department lead's department.
	Department string `json:"department,omitempty"`
	// Group is the group a department lead's department sits under;
	// Departments are a group lead's departments.
	Group       string   `json:"group,omitempty"`
	Departments []string `json:"departments,omitempty"`
	Issue       string   `json:"issue,omitempty"`
	Guards      string   `json:"guards,omitempty"`
	Lead        string   `json:"lead,omitempty"`
	LeadLive    bool     `json:"lead_live"`
	MayTick     string   `json:"may_tick"`
	Reach       string   `json:"reach,omitempty"`
	Signal      string   `json:"signal"`
	LegacyKey   bool     `json:"legacy_umbrella,omitempty"`
	AutoStart   string   `json:"autostart,omitempty"`
	Queue       string   `json:"queue,omitempty"`
	Jobs        string   `json:"jobs,omitempty"`
}

func runLeadWhoami(ctx *Context, args []string) error {
	fs := newFlagSet("lead whoami", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	live := sessionLive(proj)
	me := whoAmI(proj, issues)
	projectLead := tracker.LeadSessionName(proj)
	out := whoamiJSON{Role: string(me.Role), Session: me.Name, Scope: me.Scope, Issue: me.Slug, Signal: me.Signal}

	switch me.Role {
	case tracker.RoleProjectLead:
		out.Guards = "main (the project checkout)"
		out.MayTick = "gates the human approved in your chat, with issue-cli approve --quote; never Human Testing"
		out.AutoStart = autostartLine(ctx)
		out.Queue = queueLine(ctx, issues)
		out.Jobs = jobsLine(ctx, me, issues)
	case tracker.RoleDepartmentLead:
		out.Jobs = jobsLine(ctx, me, issues)
		out.Department = me.Department
		out.Guards = "main (the project checkout)"
		out.Lead, out.LeadLive = projectLead, live(projectLead)
		out.MayTick = me.Department + "'s gates the human approved in your chat, with issue-cli approve --quote; never Human Testing, never another department's"
		if g := proj.GroupOf(me.Department); g != nil {
			out.Group = g.Name
			// The nearest live group lead up the chain, else the CEO.
			for _, a := range proj.Ancestors(me.Department) {
				if gl := tracker.DepartmentLeadName(proj, a.Name); live(gl) {
					out.Lead, out.LeadLive = gl, true
					break
				}
			}
		}
		for _, c := range proj.Children(me.Department) {
			out.Departments = append(out.Departments, c.Name)
		}
		if len(out.Departments) > 0 {
			var all []string
			for _, c := range proj.Descendants(me.Department) {
				all = append(all, c.Name)
			}
			out.MayTick = me.Department + "'s and its departments' (" + strings.Join(all, ", ") + ") gates the human approved in your chat, with issue-cli approve --quote; never Human Testing, never another group's or Product's"
		}
	case tracker.RoleWorker:
		issue := tracker.FindIssue(issues, me.Slug)
		route := tracker.LeadFor(proj, issue, issues, live)
		out.Lead, out.LeadLive = route.Lead, route.Live
		if issue != nil {
			out.Guards = tracker.AgentSessionName(issue.Slug) + "'s branch only"
			out.Reach = fmt.Sprintf("issue-cli comment %s \"question: ...\" — %s follows your issue trail", issue.Slug, route.Lead)
			out.LegacyKey = tracker.UsesLegacyUmbrella(issue)
		} else {
			out.Reach = "post question: comments on your issue — the CEO follows your issue trail"
		}
		out.MayTick = "nothing: workers never tick gates"
	default:
		out.Lead, out.LeadLive = projectLead, live(projectLead)
		out.MayTick = "as the human, anything; an agent started by hand should be spawned instead"
	}

	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "Role: %s", out.Role)
	if out.Session != "" {
		fmt.Fprintf(w, " (%s)", out.Session)
	}
	fmt.Fprintf(w, "  [detected from %s]\n", out.Signal)
	if out.Scope != "" {
		fmt.Fprintf(w, "Scope: %s\n", out.Scope)
	}
	if out.Issue != "" {
		fmt.Fprintf(w, "Issue: %s\n", out.Issue)
	}
	if out.Guards != "" {
		fmt.Fprintf(w, "Guards: %s\n", out.Guards)
	}
	if out.Group != "" {
		if up := proj.Ancestors(out.Department); len(up) > 1 {
			// A nested group: each lead up the chain, nearest first.
			var leads, names []string
			for _, g := range up {
				names = append(names, g.Name)
				leads = append(leads, tracker.DepartmentLeadName(proj, g.Name))
			}
			fmt.Fprintf(w, "Group: %s (their leads %s come first while live, nearest first, else the CEO %s)\n", strings.Join(names, " in "), strings.Join(leads, ", then "), projectLead)
		} else {
			fmt.Fprintf(w, "Group: %s (its lead %s comes first while live, else the CEO %s)\n", out.Group, tracker.DepartmentLeadName(proj, out.Group), projectLead)
		}
	}
	if len(out.Departments) > 0 {
		fmt.Fprintf(w, "Your departments: %s\n", strings.Join(out.Departments, ", "))
		if out.Department != "" {
			fmt.Fprintf(w, "Week: %s (the week report: --write)\n", tracker.InjectProject(proj, "issue-cli department week "+tracker.DepartmentKey(out.Department)))
		}
	}
	if out.Lead != "" {
		fmt.Fprintf(w, "Reports to: %s (%s)\n", out.Lead, liveWord(out.LeadLive))
	} else {
		fmt.Fprintln(w, "Reports to: the human")
	}
	if out.Reach != "" {
		fmt.Fprintf(w, "Reach your lead: %s\n", tracker.InjectProject(proj, out.Reach))
	}
	fmt.Fprintf(w, "May tick: %s\n", out.MayTick)
	if out.AutoStart != "" {
		fmt.Fprintln(w, out.AutoStart)
	}
	if out.Queue != "" {
		fmt.Fprintln(w, out.Queue)
	}
	if out.Jobs != "" {
		fmt.Fprintln(w, out.Jobs)
	}
	if out.LegacyKey {
		fmt.Fprintf(w, "Hint: your issue links its epic with umbrella: (legacy alias). Rename it: issue-cli set-meta %s --key parent --value %s\n", out.Issue, tracker.ParentSlug(tracker.FindIssue(issues, out.Issue)))
	}
	if me.Role == tracker.RoleUnmanaged {
		fmt.Fprintf(w, "\nNot a spawned CEO or worker. If you are acting as the CEO, run: %s\n", tracker.InjectProject(proj, "issue-cli lead spawn"))
	}
	return nil
}

func liveWord(live bool) string {
	if live {
		return "live"
	}
	return "not running"
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

type leadRow struct {
	Session string `json:"session"`
	Live    bool   `json:"live"`
}

type epicRow struct {
	Slug            string `json:"slug"`
	Title           string `json:"title"`
	Status          string `json:"status"`
	ChildrenRunning int    `json:"children_running"`
	ChildrenTotal   int    `json:"children_total"`
}

type leadListJSON struct {
	Lead leadRow `json:"lead"`
	// Departments are the department leads, in projects.yaml order.
	Departments []deptLeadRow `json:"departments"`
	Epics       []epicRow     `json:"epics"`
}

type deptLeadRow struct {
	Department string `json:"department"`
	Session    string `json:"session"`
	Live       bool   `json:"live"`
	Open       int    `json:"open"`
	// Group is the group the department sits under ("" when none);
	// IsGroup marks a group's own row.
	Group   string `json:"group,omitempty"`
	IsGroup bool   `json:"is_group,omitempty"`
	// Depth is how many groups sit above the department (0 at the top).
	Depth int `json:"depth,omitempty"`
}

// leadList builds the project lead's row, one row per department lead (a
// group's departments right after it) and one row per open epic.
func leadList(proj *tracker.Project, issues []*tracker.Issue, live map[string]bool) leadListJSON {
	projectLead := tracker.LeadSessionName(proj)
	out := leadListJSON{Lead: leadRow{Session: projectLead, Live: live[projectLead]}, Departments: []deptLeadRow{}, Epics: []epicRow{}}
	var depts []tracker.Department
	for _, d := range proj.Departments {
		if proj.GroupOf(d.Name) != nil {
			continue
		}
		depts = append(depts, d)
		depts = append(depts, proj.Descendants(d.Name)...)
	}
	for _, d := range depts {
		name := tracker.DepartmentLeadName(proj, d.Name)
		row := deptLeadRow{Department: d.Name, Session: name, Live: live[name], IsGroup: proj.IsGroup(d.Name)}
		if g := proj.GroupOf(d.Name); g != nil {
			row.Group, row.Depth = g.Name, len(proj.Ancestors(d.Name))
		}
		for _, issue := range issues {
			if issue.Status != "done" && proj.DepartmentOf(issue) == d.Name {
				row.Open++
			}
		}
		out.Departments = append(out.Departments, row)
	}
	for _, issue := range issues {
		if issue.Status == "done" || !tracker.IsEpic(issue, issues) {
			continue
		}
		row := epicRow{Slug: issue.Slug, Title: issue.Title, Status: issue.Status}
		for _, child := range tracker.EpicChildren(issues, issue.Slug) {
			row.ChildrenTotal++
			if live[tracker.AgentSessionName(child.Slug)] {
				row.ChildrenRunning++
			}
		}
		out.Epics = append(out.Epics, row)
	}
	return out
}

func runLeadList(ctx *Context, args []string) error {
	fs := newFlagSet("lead list", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	list := leadList(proj, issues, liveSessionNames(proj))
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, list)
	}
	w := ctx.Stdout
	fmt.Fprintln(w, "== CEO ==")
	fmt.Fprintf(w, "  %-40s CEO  %s\n", list.Lead.Session, liveWord(list.Lead.Live))
	if len(list.Departments) > 0 {
		fmt.Fprintln(w, "\n== Department leads ==")
		for _, r := range list.Departments {
			switch {
			case r.Group != "":
				group := ""
				if r.IsGroup {
					group = " (group)"
				}
				pad := strings.Repeat("  ", r.Depth)
				fmt.Fprintf(w, "  %s└ %-*s %s%s  %s · %d open (reports to %s)\n", pad, 38-2*r.Depth, r.Session, r.Department, group, liveWord(r.Live), r.Open, r.Group)
			case r.IsGroup:
				fmt.Fprintf(w, "  %-40s %s (group)  %s · %d open\n", r.Session, r.Department, liveWord(r.Live), r.Open)
			default:
				fmt.Fprintf(w, "  %-40s %s  %s · %d open\n", r.Session, r.Department, liveWord(r.Live), r.Open)
			}
		}
	}
	if len(list.Epics) > 0 {
		fmt.Fprintln(w, "\n== Epics ==")
		for _, r := range list.Epics {
			fmt.Fprintf(w, "  %s (%s) · children running %d/%d\n", r.Slug, r.Status, r.ChildrenRunning, r.ChildrenTotal)
		}
	}
	if !list.Lead.Live {
		fmt.Fprintf(w, "\nThe CEO is not running. Start it: %s\n", tracker.InjectProject(proj, "issue-cli lead spawn"))
	}
	return nil
}

func runLeadMessage(ctx *Context, args []string) error {
	fs := newFlagSet("lead message", ctx)
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	args = fs.Args()
	if len(args) < 2 {
		return fmt.Errorf("lead message requires <target> and \"text\"\n\nExample:\n  issue-cli lead message <slug> \"please rebase on main\"")
	}
	target := args[0]
	text := normalizeEscapedText(strings.Join(args[1:], " "))
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("lead message: text is empty")
	}
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		slug := valueOr(me.Slug, "<slug>")
		return fmt.Errorf("workers can't message sessions. Ask the CEO on your issue trail instead:\n  %s", tracker.InjectProject(proj, fmt.Sprintf("issue-cli comment %s \"question: ...\"", slug)))
	}
	session := resolveMessageTarget(proj, issues, target)
	signed := fmt.Sprintf("[from %s] %s", me.sender(), text)
	if !sessionLive(proj)(session) {
		return messageNotRunning(proj, session)
	}
	if sessionAgent(proj, session) != "codex" {
		return fmt.Errorf("not sent: %s runs Claude Code, and issue-cli never pastes into a Claude session. Send it with your own SendMessage tool:\n  SendMessage({to: %q, message: %q})\n\nListAgents lists the live names. issue-cli lead message pastes only into Codex sessions", session, session, signed)
	}
	textFile := filepath.Join(projectRoot(proj), ".agent-logs", session, "messages", ctx.now().Format("20060102-150405")+".txt")
	resp := pasteIntoSession(proj, session, signed, textFile, "")
	switch resp.Status {
	case "no-session":
		return messageNotRunning(proj, resp.Session)
	case "error":
		printSteps(ctx, resp.Steps)
		return fmt.Errorf("sending to %s failed (see the steps above)", resp.Session)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, resp)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Sent to %s\n", resp.Session)
	printSteps(ctx, resp.Steps)
	return nil
}

// messageNotRunning is lead message's answer when the target session is not
// live.
func messageNotRunning(proj *tracker.Project, session string) error {
	hint := ""
	if session == tracker.LeadSessionName(proj) {
		hint = "\n\nStart it: " + tracker.InjectProject(proj, "issue-cli lead spawn")
	} else if d := proj.DepartmentOfLead(session); d != nil {
		hint = "\n\nStart it: " + tracker.InjectProject(proj, "issue-cli lead spawn --department "+tracker.DepartmentKey(d.Name))
	}
	return fmt.Errorf("%s is not running; nothing was sent.%s", session, hint)
}

// resolveMessageTarget turns a message target into a session name: "project"
// is the project lead; lead-/agent- names are taken as is; an issue slug is
// its worker.
func resolveMessageTarget(proj *tracker.Project, issues []*tracker.Issue, target string) string {
	switch {
	case target == tracker.LeadScopeProject:
		return tracker.LeadSessionName(proj)
	case strings.HasPrefix(target, tracker.LeadScopeDepartmentPrefix):
		key := strings.TrimPrefix(target, tracker.LeadScopeDepartmentPrefix)
		if d := proj.DepartmentByKey(key); d != nil {
			key = d.Name
		}
		return tracker.DepartmentLeadName(proj, key)
	case strings.HasPrefix(target, "lead-"), strings.HasPrefix(target, "agent-"):
		return target
	}
	slug := target
	if issue := tracker.FindIssue(issues, target); issue != nil {
		slug = issue.Slug
	}
	return tracker.AgentSessionName(slug)
}

// leadHeaderLine is the "CEO:" line show and start print for an issue.
func leadHeaderLine(proj *tracker.Project, issue *tracker.Issue, issues []*tracker.Issue) string {
	live := sessionLive(proj)
	route := tracker.LeadFor(proj, issue, issues, live)
	if route.Department == "" {
		return fmt.Sprintf("CEO: %s (%s)", route.Lead, liveWord(route.Live))
	}
	line := fmt.Sprintf("Lead: %s (%s)", route.DepartmentLead, liveWord(!route.Covering))
	groupLive := route.GroupLead != "" && live(route.GroupLead)
	if route.GroupLead != "" {
		line += fmt.Sprintf(" · %s lead: %s (%s)", route.Group, route.GroupLead, liveWord(groupLive))
	}
	line += fmt.Sprintf(" · CEO: %s (%s)", route.CEO, liveWord(route.CEOLive))
	if route.Covering {
		who := "the CEO"
		if groupLive {
			who = "the " + route.Group + " lead"
		}
		line += " — " + who + " covers until the " + route.Department + " lead starts: " + tracker.InjectProject(proj, "issue-cli lead spawn --department "+tracker.DepartmentKey(route.Department))
	}
	return line
}

// umbrellaHint nudges issues still on the legacy umbrella: key to parent:.
func umbrellaHint(proj *tracker.Project, issue *tracker.Issue) string {
	if !tracker.UsesLegacyUmbrella(issue) {
		return ""
	}
	return tracker.InjectProject(proj, fmt.Sprintf("Hint: umbrella: is a legacy alias for parent:. Rename it: issue-cli set-meta %s --key parent --value %s && issue-cli set-meta %s --key umbrella --clear",
		issue.Slug, tracker.ParentSlug(issue), issue.Slug))
}

// printLeadLines prints who the issue reports to, the department line
// (role prompt, goals), the outputs and connectors lines and the umbrella: rename hint when
// they apply. show and start print it under the status line.
func printLeadLines(ctx *Context, issue *tracker.Issue) {
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return
	}
	fmt.Fprintln(ctx.Stdout, leadHeaderLine(ctx.Project, issue, issues))
	if line := yoloHeaderLine(ctx.Project, ctx.now()); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
	if line := experimentHeaderLine(ctx.Project, issue); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
	if line := departmentLine(ctx.Project, issue); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
	if line := outputsLine(ctx.Project, issue, ctx.Project.LoadWorkflowForIssue(issue)); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
	if line := tracker.ConnectorsLine(ctx.Project, ctx.Project.DepartmentOf(issue)); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
	if hint := umbrellaHint(ctx.Project, issue); hint != "" {
		fmt.Fprintln(ctx.Stdout, hint)
	}
}
