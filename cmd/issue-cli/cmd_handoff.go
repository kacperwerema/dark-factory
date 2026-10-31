package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// issue-cli handoff: one box asks another for work as an issue, the relay
// lead forwards or declines it, and the asker is told when it ends. The
// handoff is an ordinary issue (internal/connections/handoff.go); see
// docs/departments.md#handoffs.

const handoffUsage = `usage: issue-cli handoff <to> --title "..." --why "..." [--for <slug>] [--system S] [--type T] [--priority P]
       issue-cli handoff forward <slug> [--note "..."]
       issue-cli handoff decline <slug> --reason "..."`

var handoffCommand = &Command{
	Name:      "handoff",
	ShortHelp: "Ask another department for work as an issue; forward or decline one (handoff <to>|forward|decline)",
	LongHelp: `Hand work to another box of this project (a department, product or hq)
as an issue in its workflow, at its first status:

  handoff <to> --title "..." --why "..." [--for <slug>] [--system S] [--type T]
      Files the issue with requested_by: (your box), handoff_route: (the
      route it takes) and handoff_for: (your issue it serves; a worker's own
      issue by default), plus a ## Handoff section with the why. The route
      follows projects.yaml connections: requests: (off them it is refused,
      naming your routes); without declared routes a department's handoff
      to product goes via hq (engine requests go via the CEO).
  handoff forward <slug> [--note "..."]
      The relay lead (the CEO for via hq, the group's lead for a group)
      lets a handoff through; until then a department lead can't dispatch it.
  handoff decline <slug> --reason "..."
      The relay lead turns it down: the issue is parked with the reason and
      the asker's issue gets a handoff: comment.

When a handoff reaches its workflow's last status, or is declined or
closed, the asker's issue gets a handoff: comment and the output names the
asker's lead to tell (SendMessage). Handoffs never cross projects, and no
handoff command passes a gate: the issue starts before backlog and forward
writes only a comment.

issue-cli handoffs lists them with their state.

Examples:
  issue-cli handoff product --title "Brand fields on the company page" --why "brand.md needs a home in the room" --system Factory
  issue-cli handoff marketing --title "Launch post for 0.60" --why "release notes are ready" --for factory/release-0-60
  issue-cli handoff forward factory/brand-fields-on-the-company-page --note "fits the night plan"
  issue-cli handoff decline factory/brand-fields-on-the-company-page --reason "covered by the brand foundation"`,
	Run:         runHandoff,
	Subcommands: []string{"forward", "decline"},
}

var handoffsCommand = &Command{
	Name:      "handoffs",
	ShortHelp: "List handoffs between departments with their state",
	LongHelp: `Print the handoffs (issues filed with issue-cli handoff) open and
closed in the last 30 days: route, state (waiting on <box> to forward, at
<status>, done, declined: <reason>, parked) and the asker's issue.

  --from <box>   handed off by this box (what you asked for)
  --to <box>     handed to this box (your inbox)
  --waiting      only those waiting for your forward (the CEO's or a
                 group lead's list); --waiting-on <box> for another relay

Examples:
  issue-cli handoffs --from marketing
  issue-cli handoffs --to product
  issue-cli handoffs --waiting
  issue-cli handoffs --json`,
	Run: runHandoffs,
}

func init() {
	registerCommand(handoffCommand)
	registerCommand(handoffsCommand)
}

func runHandoff(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: handoffUsage}
	}
	switch args[0] {
	case "forward":
		return runHandoffForward(ctx, args[1:])
	case "decline":
		return runHandoffDecline(ctx, args[1:])
	}
	return runHandoffNew(ctx, args)
}

// askerBox is the box a handoff from this session comes from: a department
// lead's department, a worker's issue's box, else --from (the CEO or the
// owner's shell), else hq. own is a worker's issue, for --for.
func askerBox(ctx *Context, me Identity, issues []*tracker.Issue, fromFlag string) (box, own string, err error) {
	p := ctx.Project
	switch me.Role {
	case tracker.RoleDepartmentLead:
		box = connections.ResolveBox(p, me.Department)
	case tracker.RoleWorker:
		if me.Slug == "" {
			return "", "", errors.New("handoff: this worker's issue is unknown (ISSUE_VIEWER_ISSUE_SLUG unset), so the asking department is too")
		}
		x := tracker.FindIssue(issues, me.Slug)
		if x == nil {
			return "", "", fmt.Errorf("handoff: your issue %s is not on the board", me.Slug)
		}
		box, own = connections.BoxOf(p, x), x.Slug
	}
	if fromFlag = strings.TrimSpace(fromFlag); fromFlag != "" {
		if box != "" {
			return "", "", fmt.Errorf("handoff --from: a %s hands off from its own box (%s)", me.Role, connections.BoxName(p, box))
		}
		if box = connections.ResolveBox(p, fromFlag); box == "" {
			return "", "", fmt.Errorf("--from %q: no such box (one of: %s)", fromFlag, strings.Join(connections.Boxes(p), ", "))
		}
	}
	if box == "" {
		box = connections.HQ
	}
	return box, own, nil
}

func runHandoffNew(ctx *Context, args []string) error {
	to := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		to, args = args[0], args[1:]
	}
	fs := newFlagSet("handoff", ctx)
	title := fs.String("title", "", "the work asked for (required)")
	why := fs.String("why", "", "why the asker needs it (required)")
	forFlag := fs.String("for", "", "the asker's issue this serves (a worker's own issue by default)")
	system := fs.String("system", "", "system of the new issue")
	typ := fs.String("type", "", "type of work in the target's workflow")
	priority := fs.String("priority", "", "priority")
	fromFlag := fs.String("from", "", "the asking box, for the CEO or the owner's shell (default hq)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if to == "" || strings.TrimSpace(*title) == "" || strings.TrimSpace(*why) == "" {
		return &usageError{msg: handoffUsage}
	}
	p := ctx.Project
	if len(p.Departments) == 0 {
		return errors.New("handoff: this project has no departments, so there is no one to hand off to (issue-cli department list)")
	}
	issues, _ := tracker.LoadIssues(p.IssueDir)
	me := whoAmI(p, issues)
	from, own, err := askerBox(ctx, me, issues, *fromFlag)
	if err != nil {
		return err
	}
	route, err := connections.PlanRoute(p, from, to)
	if err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	target := route[len(route)-1]
	forSlug := strings.TrimSpace(*forFlag)
	if forSlug == "" {
		forSlug = own
	}
	var forIssue *tracker.Issue
	if forSlug != "" {
		if forIssue = tracker.FindIssue(issues, forSlug); forIssue == nil {
			return fmt.Errorf("handoff --for %q: no such issue", forSlug)
		}
		forSlug = forIssue.Slug
	}

	dept := ""
	if target != connections.Product && target != connections.HQ {
		dept = p.DepartmentByKey(target).Name
	}
	if target == connections.HQ {
		return errors.New("handoff: hq has no workflow of its own; ask the CEO with a question: comment")
	}
	routeKeys := strings.Join(route, connections.RouteSep)
	extra := [][2]string{{connections.HandoffRouteField, routeKeys}}
	if forSlug != "" {
		extra = append(extra, [2]string{connections.HandoffForField, forSlug})
	}
	var body strings.Builder
	body.WriteString("## Handoff\n\n")
	fmt.Fprintf(&body, "- From: %s\n", connections.BoxName(p, from))
	fmt.Fprintf(&body, "- Route: %s\n", connections.RouteNames(p, route))
	if forSlug != "" {
		fmt.Fprintf(&body, "- For: %s\n", forSlug)
	}
	fmt.Fprintf(&body, "- Why: %s\n", strings.TrimSpace(*why))

	made, err := writeNewIssue(ctx, newIssueSpec{
		Title: *title, System: *system, Priority: *priority, Type: *typ, Department: dept,
		RequestedBy: from, Extra: extra, Body: body.String(),
	})
	if err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	slug := made.Slug
	if x, _ := tracker.LoadIssues(p.IssueDir); x != nil {
		if n := findByFile(x, made.File); n != nil {
			slug = n.Slug
		}
	}
	author := commentAuthor(p, forIssue)
	if forIssue != nil {
		_ = tracker.AddCommentBy(forIssue.FilePath, 0, fmt.Sprintf("%s asked %s for %s (%s)", connections.HandoffPrefix, connections.BoxName(p, target), slug, connections.RouteNames(p, route)), "cli", author)
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "✓ Handoff %s: %s\n", connections.RouteNames(p, route), slug)
	if len(route) == 3 {
		lead := connections.RelayLead(p, route[1])
		fmt.Fprintf(w, "  Waiting on %s to forward it (%s runs issue-cli handoff forward %s, or decline … --reason).\n", connections.BoxName(p, route[1]), lead, slug)
		fmt.Fprintf(w, "  Tell %s: SendMessage to %s: \"handoff %s waits for your forward\"\n", lead, lead, slug)
	} else if lead := connections.AskerLead(p, target); lead != "" {
		fmt.Fprintf(w, "  In %s's inbox. Tell %s: SendMessage to %s: \"handoff %s is in your inbox\"\n", connections.BoxName(p, target), lead, lead, slug)
	}
	if forSlug != "" {
		fmt.Fprintf(w, "  %s gets a handoff: comment when it is done or declined.\n", forSlug)
	}
	fmt.Fprintf(w, "  State: %s\n", tracker.InjectProject(p, "issue-cli handoffs --from "+from))
	return nil
}

// findByFile is the issue loaded from path.
func findByFile(issues []*tracker.Issue, path string) *tracker.Issue {
	for _, x := range issues {
		if x.FilePath == path {
			return x
		}
	}
	return nil
}

// loadHandoff is the handoff issue slug names, with its comments.
func loadHandoff(ctx *Context, slug string) (*tracker.Issue, []*tracker.Issue, connections.Handoff, error) {
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return nil, nil, connections.Handoff{}, err
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	h, ok := connections.HandoffOf(ctx.Project, issue, comments)
	if !ok {
		return nil, nil, h, fmt.Errorf("%s is not a handoff (no handoff_route:; issue-cli handoff files one)", issue.Slug)
	}
	return issue, issues, h, nil
}

// relayAllowed: only the relay box's lead, or the owner's shell, forwards or
// declines. author is what its comment is signed with.
func relayAllowed(ctx *Context, h connections.Handoff, verb string) (author string, err error) {
	p := ctx.Project
	if h.Via == "" {
		return "", fmt.Errorf("handoff %s: %s goes straight to %s, there is nothing to %s; the receiving lead parks it (issue-cli transition --to parked) if it won't do it", verb, h.Slug, connections.BoxName(p, h.To), verb)
	}
	lead := connections.RelayLead(p, h.Via)
	me := whoAmI(p, nil)
	switch {
	case me.Role == tracker.RoleUnmanaged:
		return tracker.OwnerAuthor, nil
	case me.Name == lead:
		return lead, nil
	}
	who := me.Name
	if who == "" {
		who = "a " + string(me.Role)
	}
	return "", fmt.Errorf("handoff %s: only %s (%s's lead) or the owner may %s %s; you are %s.\nAsk %s: issue-cli comment %s \"question: %s this?\"", verb, lead, connections.BoxName(p, h.Via), verb, h.Slug, who, lead, h.Slug, verb)
}

func runHandoffForward(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "handoff forward")
	if err != nil {
		return err
	}
	fs := newFlagSet("handoff forward", ctx)
	note := fs.String("note", "", "a few words for the receiving lead")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	issue, _, h, err := loadHandoff(ctx, slug)
	if err != nil {
		return err
	}
	author, err := relayAllowed(ctx, h, "forward")
	if err != nil {
		return err
	}
	if h.State != connections.StateWaiting {
		return fmt.Errorf("handoff forward %s changed nothing: it is %s", h.Slug, h.Word)
	}
	text := fmt.Sprintf("%s forwarded by %s to %s", connections.HandoffPrefix, author, connections.BoxName(ctx.Project, h.To))
	if n := strings.TrimSpace(*note); n != "" {
		text += ": " + n
	}
	if err := tracker.AddCommentBy(issue.FilePath, 0, text, "cli", author); err != nil {
		return fmt.Errorf("handoff forward %s: %w", h.Slug, err)
	}
	p := ctx.Project
	fmt.Fprintf(ctx.Stdout, "✓ Forwarded %s to %s (it stays at %s; no gate passed)\n", h.Slug, connections.BoxName(p, h.To), issue.Status)
	if lead := connections.AskerLead(p, h.To); lead != "" {
		fmt.Fprintf(ctx.Stdout, "  Tell %s: SendMessage to %s: \"handoff %s is forwarded to you\"\n", lead, lead, h.Slug)
	}
	return nil
}

func runHandoffDecline(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "handoff decline")
	if err != nil {
		return err
	}
	fs := newFlagSet("handoff decline", ctx)
	reason := fs.String("reason", "", "why it is declined (required)")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("handoff decline needs --reason \"...\": the asker reads it\n\nExample:\n  issue-cli handoff decline %s --reason \"covered by …\"", slug)
	}
	issue, issues, h, err := loadHandoff(ctx, slug)
	if err != nil {
		return err
	}
	author, err := relayAllowed(ctx, h, "decline")
	if err != nil {
		return err
	}
	if !h.Open() {
		return fmt.Errorf("handoff decline %s changed nothing: it is %s", h.Slug, h.Word)
	}
	wf, err := issueWorkflow(ctx, issue)
	if err != nil {
		return err
	}
	park := parkStatus(wf, issue.Status)
	if park == "" {
		return fmt.Errorf("handoff decline %s: the workflow has no status every status can move to (e.g. parked)", h.Slug)
	}
	r := strings.TrimSpace(*reason)
	fields := map[string]string{}
	if t := wf.ResolveTransition(issue.Status, park); t != nil {
		for _, f := range t.Fields {
			if f.Required {
				fields[f.Name] = "Handoff declined: " + r
			}
		}
	}
	gate, err := passYOLOGate(ctx, wf, issue, issues, park, fields)
	if err != nil {
		return err
	}
	from, _, err := wf.ApplyTransitionToFileWithFields(issue.FilePath, park, fields)
	if err != nil {
		return yoloRefusal(ctx.Stderr, gate, issue.Status, park, ctx.now(), fmt.Errorf("handoff decline %s: %w", h.Slug, err))
	}
	text := fmt.Sprintf("%s declined by %s: %s", connections.HandoffPrefix, author, r)
	if err := tracker.AddCommentBy(issue.FilePath, 0, text, "cli", author); err != nil {
		return fmt.Errorf("handoff decline %s: parked, but the comment failed: %w", h.Slug, err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Declined %s: %s → %s (%s)\n", h.Slug, from, park, r)
	printYOLOGate(ctx.Stdout, gate, from, park, ctx.now())
	h.State, h.Reason, h.Word = connections.StateDeclined, r, "declined: "+r
	tellAsker(ctx, ctx.Stdout, h, author)
	fmt.Fprintf(ctx.Stdout, "Undo: %s\n", tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli transition %s --to %q", h.Slug, from)))
	return nil
}

// tellAsker writes the end of a handoff on the asker's issue (handoff_for)
// and prints who to tell. It never pastes into a session (docs/agent-
// dispatch.md): the caller sends the message.
func tellAsker(ctx *Context, w io.Writer, h connections.Handoff, author string) {
	p := ctx.Project
	text := fmt.Sprintf("%s %s (asked of %s) is %s", connections.HandoffPrefix, h.Slug, connections.BoxName(p, h.To), h.Word)
	if h.For != "" {
		if x, _, err := findIssueOrErr(ctx, h.For); err == nil {
			if err := tracker.AddCommentBy(x.FilePath, 0, text, "cli", author); err == nil {
				fmt.Fprintf(w, "✓ Told the asker on %s: %s\n", x.Slug, text)
			}
		}
	}
	if lead := connections.AskerLead(p, h.From); lead != "" {
		fmt.Fprintf(w, "  Tell %s (%s asked for it): SendMessage to %s: %q\n", lead, connections.BoxName(p, h.From), lead, strings.TrimPrefix(text, connections.HandoffPrefix+" "))
	}
}

// handoffEnded tells the asker when a transition or close ended a handoff:
// it reached its workflow's last status, or a side status (parked). Once per
// handoff: a second end writes nothing.
func handoffEnded(ctx *Context, w io.Writer, wf *tracker.WorkflowConfig, slug, to string) {
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil || !connections.IsHandoff(issue) {
		return
	}
	if to != lastStatus(wf) && !wf.IsSideStatus(to) {
		return
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	h, _ := connections.HandoffOf(ctx.Project, issue, comments)
	if h.Open() || h.State == connections.StateDeclined {
		return
	}
	end := connections.HandoffPrefix + " ended: " + h.Word
	for _, c := range comments {
		if strings.HasPrefix(strings.TrimSpace(c.Text), connections.HandoffPrefix+" ended") {
			return
		}
	}
	author := commentAuthor(ctx.Project, issue)
	_ = tracker.AddCommentBy(issue.FilePath, 0, end, "cli", author)
	tellAsker(ctx, w, h, author)
}

// lastStatus is the workflow's last status on the main line (side statuses
// such as parked are off it).
func lastStatus(wf *tracker.WorkflowConfig) string {
	last := ""
	for _, s := range wf.Statuses {
		if s.Name != "none" && !wf.IsSideStatus(s.Name) {
			last = s.Name
		}
	}
	return last
}

func runHandoffs(ctx *Context, args []string) error {
	fs := newFlagSet("handoffs", ctx)
	from := fs.String("from", "", "handed off by this box")
	to := fs.String("to", "", "handed to this box")
	waiting := fs.Bool("waiting", false, "only those waiting for your forward")
	waitingOn := fs.String("waiting-on", "", "only those waiting for this box's forward (hq or a group)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	p := ctx.Project
	var f connections.HandoffFilter
	for _, b := range []struct {
		flag, name string
		dst        *string
	}{{*from, "--from", &f.From}, {*to, "--to", &f.To}, {*waitingOn, "--waiting-on", &f.WaitingOn}} {
		if b.flag == "" {
			continue
		}
		if *b.dst = connections.ResolveBox(p, b.flag); *b.dst == "" {
			return fmt.Errorf("%s %q: no such box (one of: %s)", b.name, b.flag, strings.Join(connections.Boxes(p), ", "))
		}
	}
	if *waiting && f.WaitingOn == "" {
		f.WaitingOn = myRelayBox(p, whoAmI(p, nil))
		if f.WaitingOn == "" {
			return errors.New("handoffs --waiting: you forward nothing (only the CEO and group leads do); use --waiting-on <hq|group>")
		}
	}
	issues, _ := tracker.LoadIssues(p.IssueDir)
	list := connections.ListHandoffs(p, issues, func(i *tracker.Issue) []tracker.Comment {
		c, _ := tracker.LoadComments(i.FilePath)
		return c
	}, f, ctx.now())
	if ctx.JSONOutput {
		if list == nil {
			list = []connections.Handoff{}
		}
		return writeJSON(ctx.Stdout, list)
	}
	w := ctx.Stdout
	if len(list) == 0 {
		fmt.Fprintln(w, "No handoffs here. A department lead or worker hands work to another with:")
		fmt.Fprintln(w, "  "+tracker.InjectProject(p, `issue-cli handoff <to> --title "..." --why "..."`))
		return nil
	}
	open := 0
	for _, h := range list {
		if h.Open() {
			open++
		}
	}
	fmt.Fprintf(w, "== Handoffs · %d open ==\n", open)
	for _, h := range list {
		route := []string{h.From}
		if h.Via != "" {
			route = append(route, h.Via)
		}
		route = append(route, h.To)
		fmt.Fprintf(w, "%s · %s · %s\n", h.Slug, connections.RouteNames(p, route), h.Word)
		if h.For != "" {
			fmt.Fprintf(w, "  for %s\n", h.For)
		}
		if h.State == connections.StateWaiting {
			fmt.Fprintf(w, "  %s: issue-cli handoff forward %s  |  issue-cli handoff decline %s --reason \"...\"\n", connections.RelayLead(p, h.Via), h.Slug, h.Slug)
		}
	}
	return nil
}

// myRelayBox is the box this session forwards for: hq for the CEO, its
// group for a group's lead; "" otherwise.
func myRelayBox(p *tracker.Project, me Identity) string {
	switch me.Role {
	case tracker.RoleProjectLead:
		return connections.HQ
	case tracker.RoleDepartmentLead:
		if d := p.DepartmentByKey(me.Department); d != nil && p.IsGroup(d.Name) {
			return tracker.DepartmentKey(d.Name)
		}
	}
	return ""
}
