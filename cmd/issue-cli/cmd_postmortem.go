package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/restrace"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const postmortemUsage = `Usage:
  issue-cli postmortem start <slug> --quote "<the owner's words>" [--again] [--agent claude|codex] [--attach]
  issue-cli postmortem brief <slug>
  issue-cli postmortem write <slug> --file <path>
  issue-cli postmortem show <slug> [--json]
  issue-cli postmortem list [--grouped] [--json]
  issue-cli postmortem fixed <retro file>#A<n> --by <slug|sha> [--copies]`

var postmortemCommand = &Command{
	Name:      "postmortem",
	ShortHelp: "Post-mortem at close: a fresh agent's retro whose actions the owner agrees to in one round (sub: start|brief|write|show|list|fixed)",
	LongHelp: `The post-mortem (docs/workflow.md, Post-mortem): when an issue reaches the
workflow's postmortem: at: status (done), a fresh agent that did none of its
work reads the run from paths only and writes a retro with Steps, Worked,
Did not work, Decisions, Known and Proposed actions. Each action passes one
test ("` + postmortem.TheTest + `") and names one
place: memory <kind>, file <path.md>, skill <name>, workflow (an experiment
proposal) or none <doc ref>. The owner agrees or refuses all of them in one
round on /p/<project>/postmortem/<slug>; agreed ones are applied and linked
from the retro.

  start <slug> --quote "…"   start the post-mortem agent by hand (the lead or
                             the owner; workers can't). --again writes a new
                             one when the issue has one already.
  brief <slug>               print the brief the agent gets (paths only)
  write <slug> --file <path> the agent saves its retro; refused, nothing
                             saved, when an action fails the test or names
                             no place
  show <slug>                the newest post-mortem's actions and outcomes
  list                       every post-mortem and how many actions wait
  list --grouped             the owner's queue (/p/<project>/postmortems) as
                             the page has it: one row per group of copies
                             (one answer covers them), the alone ones, the
                             fixed ones, and how many answers are left
  fixed <file>#A<n> --by <slug|sha>
                             link an action to the issue or the commit on
                             main that fixes it: once the issue is done (or
                             at once for a commit) it needs no answer.
                             --copies links every copy of its group too

` + postmortemUsage,
	Run:         runPostmortem,
	Subcommands: []string{"start", "brief", "write", "show", "list", "fixed"},
}

func init() {
	registerCommand(postmortemCommand)
}

func runPostmortem(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("postmortem needs a project — pass --project <slug> or run from a project root")
	}
	if len(args) == 0 {
		fmt.Fprintln(ctx.Stdout, postmortemUsage)
		return nil
	}
	switch args[0] {
	case "start":
		return runPostmortemStart(ctx, args[1:])
	case "brief":
		return runPostmortemBrief(ctx, args[1:])
	case "write":
		return runPostmortemWrite(ctx, args[1:])
	case "show":
		return runPostmortemShow(ctx, args[1:])
	case "list":
		return runPostmortemList(ctx, args[1:])
	case "fixed":
		return runPostmortemFixed(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown postmortem subcommand %q\n\n%s", args[0], postmortemUsage)}
}

// postmortemSession is the post-mortem agent's tmux name for slug.
func postmortemSession(slug string) string {
	return "postmortem-" + strings.TrimPrefix(tracker.AgentSessionName(slug), "agent-")
}

// postmortemRun is where issue's run can be read: paths only.
func postmortemRun(ctx *Context, issue *tracker.Issue) postmortem.Run {
	root := projectRoot(ctx.Project)
	r := postmortem.Run{
		Project: ctx.Project.Slug, Slug: issue.Slug, Title: issue.Title,
		IssueFile: issue.FilePath, Repo: root,
		CLI:      tracker.InjectProject(ctx.Project, "issue-cli"),
		Statuses: ctx.Project.AllStatuses(),
	}
	if p := tracker.SidecarPath(issue.FilePath); pathExists(p) {
		r.DataFile = p
	}
	if p := tracker.StatsSidecarPath(issue.FilePath); pathExists(p) {
		r.StatsFile = p
	}
	if d := filepath.Join(root, ".agent-logs", tracker.AgentSessionName(issue.Slug)); pathExists(d) {
		r.AgentLogs = d
	}
	// Only a branch that exists is named: a merged one is often deleted,
	// and the brief then points at main's commits by slug instead.
	if tracker.BranchHead(root, "work/"+issue.Slug) != "" {
		r.Branch = "work/" + issue.Slug
	}
	if name, err := tracker.MainBranch(root); err == nil {
		r.Main = name
	}
	if st, err := tracker.LoadStats(issue.FilePath); err == nil {
		if d, ok := tracker.DispatchBase(st); ok {
			r.Base = d.BaseSHA
		}
	}
	entries, _ := retro.Load(retrosDir(ctx))
	for _, e := range entries {
		if e.IssueSlug == issue.Slug && e.Kind != postmortem.Kind {
			r.Retros = append(r.Retros, e.FilePath)
		}
	}
	// An epic's post-mortem reads its children's runs too: paths only.
	if issues, err := ctx.Project.LoadIssues(); err == nil && tracker.IsEpic(issue, issues) {
		for _, child := range tracker.EpicChildren(issues, issue.Slug) {
			c := postmortem.Child{Slug: child.Slug, IssueFile: child.FilePath}
			if tracker.BranchHead(root, "work/"+child.Slug) != "" {
				c.Branch = "work/" + child.Slug
			}
			for _, e := range entries {
				if e.IssueSlug != child.Slug {
					continue
				}
				if e.Kind == postmortem.Kind {
					c.Postmortems = append(c.Postmortems, e.FilePath)
				} else {
					c.Retros = append(c.Retros, e.FilePath)
				}
			}
			r.Children = append(r.Children, c)
		}
	}
	return r
}

func runPostmortemBrief(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "postmortem brief")
	if err != nil {
		return err
	}
	fs := newFlagSet("postmortem brief", ctx)
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	fmt.Fprint(ctx.Stdout, postmortem.Brief(postmortemRun(ctx, issue)))
	return nil
}

// postmortemStart is one start of the post-mortem agent: by hand (Quote
// is the owner's words) or by the transition (Auto).
type postmortemStart struct {
	Quote, How string
	Auto       bool
	Again      bool
	Agent      string
	Attach     bool
}

// killAgentSession removes an agent's session; a var so tests stub it.
var killAgentSession = agentrun.KillTmux

// errPostmortemHeld is a start that didn't happen and why; nothing is wrong.
type errPostmortemHeld struct{ why string }

func (e *errPostmortemHeld) Error() string { return e.why }

// startPostmortem starts issue's post-mortem agent. It holds (returns an
// errPostmortemHeld) when the issue has one saved or live already (unless
// Again) and when mem-guard says no.
func startPostmortem(ctx *Context, issue *tracker.Issue, wf *tracker.WorkflowConfig, ps postmortemStart) (agentrun.Response, error) {
	session := postmortemSession(issue.Slug)
	entries, _ := retro.Load(retrosDir(ctx))
	if have := postmortem.Of(entries, issue.Slug); len(have) > 0 && !ps.Again {
		return agentrun.Response{}, &errPostmortemHeld{fmt.Sprintf("%s has a post-mortem already (%s); --again writes a new one", issue.Slug, filepath.Base(have[0].FilePath))}
	}
	target := agentrun.Target(ctx.Project, session)
	if agentrun.HasSession(target) {
		// A pane that is only a shell is a launch whose agent exited or
		// never started: dead, not running. It goes, and this start runs.
		// One whose post-mortem is written since its start is finished:
		// an agent idling at its prompt, not running.
		if !agentrun.BareShell(target) && !postmortemWritten(issue) {
			return agentrun.Response{}, &errPostmortemHeld{session + " is running already"}
		}
		_ = killAgentSession(ctx.Project, session)
	}
	if m := autostart.ReadMemory(); m.Found && !m.CanStart {
		return agentrun.Response{}, &errPostmortemHeld{"mem-guard says no (" + strings.TrimSpace(m.Note) + ")"}
	}
	agent := ps.Agent
	if agent == "" && wf != nil && wf.Postmortem != nil {
		agent = wf.Postmortem.Agent
	}
	// postmortem: agent: (or --agent) names it; else routing: picks it, as
	// for the issue's own worker. Never an empty agent: the pane would run
	// an empty command.
	route := agentrun.RouteAgent(ctx.Project, wf, projectRoot(ctx.Project), routing.IssueSubject(ctx.Project, issue), agent, ctx.now())
	if route.Hold != "" {
		return agentrun.Response{}, &errPostmortemHeld{route.Hold}
	}
	startedBy := fmt.Sprintf("Started on the owner's words: %q (%s, %s).", ps.Quote, ps.How, ctx.now().Format("2006-01-02"))
	if ps.Auto {
		startedBy = fmt.Sprintf("Started by the transition into %s (workflow postmortem: start: auto, %s).", issue.Status, ctx.now().Format("2006-01-02"))
	}
	resp := startAgent(ctx.Project, agentrun.Spec{
		Session:   session,
		Prompt:    postmortem.Brief(postmortemRun(ctx, issue)) + "\n" + startedBy + "\n",
		AgentType: route.Agent,
		Route:     &route,
		IssueSlug: issue.Slug,
		WorkDir:   projectRoot(ctx.Project),
		Workflow:  wf,
		Headless:  !ps.Attach,
	})
	// Start removes a launch that dies at once (not on PATH, an empty
	// command): no session left and no "started" comment.
	if resp.Status == "error" {
		return resp, fmt.Errorf("starting %s failed%s", session, agentrun.FailureDetail(resp))
	}
	author := commentAuthor(ctx.Project, issue)
	_ = tracker.AddCommentBy(issue.FilePath, 0, "postmortem: started "+session+". "+startedBy, "cli", author)
	return resp, nil
}

func runPostmortemStart(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "postmortem start")
	if err != nil {
		return err
	}
	fs := newFlagSet("postmortem start", ctx)
	quote := fs.String("quote", "", "the owner's words that ask for it")
	how := fs.String("how", "", "how the words reached you (default: CEO chat or owner's shell)")
	again := fs.Bool("again", false, "write a new post-mortem when the issue has one")
	agent := fs.String("agent", "", "claude or codex (default: postmortem: agent:, else routing)")
	attach := fs.Bool("attach", false, "open a terminal on it (default: headless)")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	me := whoAmI(ctx.Project, issues)
	if me.Role == tracker.RoleWorker {
		guardNever(ctx, "postmortem start")
		return fmt.Errorf("workers can't start agents (you are %s, detected from %s). The transition into done starts the post-mortem when the workflow says start: auto; else ask on your issue trail:\n  %s", valueOr(me.Name, "a worker"), me.Signal,
			tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: ...\"", valueOr(me.Slug, "<slug>"))))
	}
	if strings.TrimSpace(*quote) == "" {
		return &usageError{msg: "postmortem start needs --quote: the owner's words that ask for it. Nothing was started.\n\n" + postmortemUsage}
	}
	h := strings.TrimSpace(*how)
	if h == "" {
		h = "owner's shell"
		if me.Role == tracker.RoleProjectLead {
			h = "CEO chat"
		}
	}
	wf, _ := issueWorkflow(ctx, issue)
	resp, err := startPostmortem(ctx, issue, wf, postmortemStart{Quote: *quote, How: h, Again: *again, Agent: *agent, Attach: *attach})
	var held *errPostmortemHeld
	if errors.As(err, &held) {
		return fmt.Errorf("nothing started: %s", held.why)
	}
	if err != nil {
		printSteps(ctx, resp.Steps)
		return err
	}
	printPostmortemStarted(ctx.Stdout, ctx, resp)
	return nil
}

func printPostmortemStarted(w io.Writer, ctx *Context, resp agentrun.Response) {
	fmt.Fprintf(w, "✓ Post-mortem started: %s (headless, reads the run from paths only)\n", resp.Session)
	if resp.AttachCmd != "" {
		fmt.Fprintf(w, "  Inside tmux: %s · From a shell: %s\n", resp.SwitchCmd, resp.AttachCmd)
	}
}

// postmortemAtTransition runs after a transition into to: the post-mortem
// starts (start: auto) or its command is printed (start: ask).
func postmortemAtTransition(ctx *Context, w io.Writer, wf *tracker.WorkflowConfig, issue *tracker.Issue, to string) {
	at := wf.PostmortemAt()
	if at == "" || !strings.EqualFold(at, to) {
		return
	}
	if issue.Type == tracker.ChoreType && !wf.PostmortemListsType(issue.Type) {
		// A night's housekeeping: a fresh agent each time is spend with
		// nothing to learn.
		return
	}
	cmd := tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli postmortem start %s --quote \"<the owner's words>\"", issue.Slug))
	fmt.Fprintln(w, "\n== Post-mortem ==")
	if !wf.PostmortemAutoFor(issue.Type) {
		if wf.PostmortemAutoStart() {
			// start: auto with auto_types: this type gets one only when asked.
			fmt.Fprintf(w, "Not started by itself: postmortem: auto_types: %s starts it, and this is %s.\n", strings.Join(wf.Postmortem.AutoTypes, ", "), postmortemTypeWords(wf, issue.Type))
		}
		fmt.Fprintf(w, "A fresh agent writes this issue's post-mortem when the lead starts it on the owner's words:\n  %s\n", cmd)
		return
	}
	resp, err := startPostmortem(ctx, issue, wf, postmortemStart{Auto: true})
	var held *errPostmortemHeld
	switch {
	case errors.As(err, &held):
		fmt.Fprintf(w, "Post-mortem not started: %s. Later: %s\n", held.why, cmd)
	case err != nil:
		fmt.Fprintf(w, "Post-mortem not started: %v. Later: %s\n", err, cmd)
	default:
		printPostmortemStarted(w, ctx, resp)
		fmt.Fprintln(w, "  You are done: don't wait for it. The owner agrees to its actions in one round.")
	}
}

// postmortemTypeWords names an issue's type for the not-started line: "a
// tweak", or "an issue with no type" when nothing resolves.
func postmortemTypeWords(wf *tracker.WorkflowConfig, issueType string) string {
	t := strings.TrimSpace(issueType)
	if resolved, _ := wf.ResolveType(t); resolved != "" {
		t = resolved
	}
	if t == "" {
		return "an issue with no type"
	}
	if strings.ContainsRune("aeiou", rune(strings.ToLower(t)[0])) {
		return "an " + t
	}
	return "a " + t
}

func runPostmortemWrite(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "postmortem write")
	if err != nil {
		return err
	}
	fs := newFlagSet("postmortem write", ctx)
	file := fs.String("file", "", "the post-mortem markdown file")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	if *file == "" {
		return &usageError{msg: "postmortem write needs --file <path>\n\n" + postmortemUsage}
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	_, body := retro.Parse(string(data))
	acts, err := postmortem.Check(body, projectRoot(ctx.Project), ctx.Project.AllStatuses())
	if err != nil {
		return err
	}
	path, err := postmortem.Save(retrosDir(ctx), postmortem.Meta{Issue: issue.Slug, Title: issue.Title, Status: issue.Status, System: issue.System}, body, ctx.now())
	if err != nil {
		return err
	}
	n := len(postmortem.Pending(acts))
	link := postmortemURL(ctx.Project, issue.Slug)
	note := "no actions"
	if n > 0 {
		note = fmt.Sprintf("%d action(s) wait", n) + " for the owner's one round: " + link
	}
	author := commentAuthor(ctx.Project, issue)
	_ = tracker.AddCommentBy(issue.FilePath, 0, fmt.Sprintf("postmortem: saved retros/%s — %s", filepath.Base(path), note), "cli", author)
	fmt.Fprintf(ctx.Stdout, "✓ Post-mortem saved for %s\nfile: %s\n", issue.Slug, path)
	fmt.Fprintf(ctx.Stdout, "%s. You are done: stop here.\n", capitalize(note))
	if closeOwnPostmortemSession(ctx.Project, issue.Slug) {
		fmt.Fprintf(ctx.Stdout, "This session (%s) closes now.\n", postmortemSession(issue.Slug))
	}
	return nil
}

// postmortemWritten reports whether issue's last post-mortem comment is a
// saved one: the agent its last start launched has written its post-mortem.
func postmortemWritten(issue *tracker.Issue) bool {
	comments, _ := tracker.LoadComments(issue.FilePath)
	for i := len(comments) - 1; i >= 0; i-- {
		if text := comments[i].Text; strings.HasPrefix(text, "postmortem: ") {
			return strings.HasPrefix(text, "postmortem: saved ")
		}
	}
	return false
}

// closeOwnPostmortemSession closes slug's post-mortem session when this
// process runs inside it, and reports whether it does. The kill runs
// detached in its own process session, a moment later, so this command's
// output reaches the agent first and the kill outlives the pane's hangup.
// It names the session (or, in shared mode, the window) exactly; never
// kill-server.
func closeOwnPostmortemSession(proj *tracker.Project, slug string) bool {
	name := postmortemSession(slug)
	if !inOwnPane(proj, name) {
		return false
	}
	verb := "kill-session"
	if agentrun.SharedSession(proj) != "" {
		verb = "kill-window"
	}
	cmd := exec.Command("sh", "-c", `sleep 1; exec tmux "$0" -t "$1"`, verb, agentrun.Target(proj, name))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return false
	}
	restrace.Start(restrace.KindChild, cmd.Process.Pid, "", "post-mortem session close (detached)")
	go func() { _ = cmd.Wait() }()
	return true
}

func postmortemURL(proj *tracker.Project, slug string) string {
	return fmt.Sprintf("%s/p/%s/postmortem/%s", viewerBaseURL(proj), proj.Slug, slug)
}

// postmortemView is a post-mortem as show and list print it.
type postmortemView struct {
	Issue   string              `json:"issue"`
	File    string              `json:"file"`
	Pending int                 `json:"pending"`
	Actions []postmortem.Action `json:"actions"`
}

func loadPostmortems(ctx *Context, slug string) ([]postmortemView, error) {
	entries, err := retro.Load(retrosDir(ctx))
	if err != nil {
		return nil, err
	}
	issues, _ := ctx.Project.LoadIssues()
	fixes := postmortemFixes(ctx.Project, issues, ctx.now())
	var out []postmortemView
	for _, e := range postmortem.Of(entries, slug) {
		acts := postmortem.Actions(e.Body)
		pending := 0
		for _, a := range postmortem.Pending(acts) {
			if !fixes.Of(a).Done {
				pending++ // a fixed action waits on nobody
			}
		}
		out = append(out, postmortemView{Issue: e.IssueSlug, File: e.FilePath, Pending: pending, Actions: acts})
	}
	return out, nil
}

func runPostmortemShow(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "postmortem show")
	if err != nil {
		return err
	}
	fs := newFlagSet("postmortem show", ctx)
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	views, err := loadPostmortems(ctx, issue.Slug)
	if err != nil {
		return err
	}
	if len(views) == 0 {
		return fmt.Errorf("%s has no post-mortem yet. Start one: %s", issue.Slug,
			tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli postmortem start %s --quote \"…\"", issue.Slug)))
	}
	v := views[0]
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, v)
	}
	fixes := postmortemFixes(ctx.Project, issues, ctx.now())
	fmt.Fprintf(ctx.Stdout, "== Post-mortem of %s ==\nfile: %s\n", issue.Slug, v.File)
	for _, a := range v.Actions {
		state := "waits for the owner"
		switch {
		case a.Applied != "":
			state = "applied: " + a.Applied
		case a.Refused != "":
			state = "refused: " + a.Refused
		case a.Overtaken != "":
			state = "overtaken: " + a.Overtaken
		}
		fmt.Fprintf(ctx.Stdout, "  %s [%s] %s\n      %s\n", a.ID, a.PlaceString(), a.Title, state)
		for _, line := range a.Reopened {
			fmt.Fprintf(ctx.Stdout, "      reopened: %s\n", line)
		}
		if f := fixes.Of(a); f.By != "" && !a.Decided() {
			fmt.Fprintf(ctx.Stdout, "      %s\n", f.Words())
		}
	}
	if v.Pending > 0 {
		fmt.Fprintf(ctx.Stdout, "The owner agrees or refuses them in one round: %s\n", postmortemURL(ctx.Project, issue.Slug))
	}
	return nil
}

func runPostmortemList(ctx *Context, args []string) error {
	fs := newFlagSet("postmortem list", ctx)
	grouped := fs.Bool("grouped", false, "the owner's queue: one row per group of copies, and the fixed ones")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if *grouped {
		return runPostmortemListGrouped(ctx)
	}
	views, err := loadPostmortems(ctx, "")
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		if views == nil {
			views = []postmortemView{}
		}
		return writeJSON(ctx.Stdout, views)
	}
	if len(views) == 0 {
		fmt.Fprintln(ctx.Stdout, "No post-mortems yet. One starts when an issue reaches the workflow's postmortem: at: status, or by hand: issue-cli postmortem start <slug> --quote \"…\"")
		return nil
	}
	waiting := 0
	for _, v := range views {
		fmt.Fprintf(ctx.Stdout, "  %s  %d action(s), %d waiting  %s\n", v.Issue, len(v.Actions), v.Pending, filepath.Base(v.File))
		waiting += v.Pending
	}
	if waiting > 0 {
		fmt.Fprintf(ctx.Stdout, "The owner answers them together, one answer per group of copies, on %s/p/%s/postmortems\n", viewerBaseURL(ctx.Project), ctx.Project.Slug)
		fmt.Fprintln(ctx.Stdout, "The same rows, and the ones fixed already: "+tracker.InjectProject(ctx.Project, "issue-cli postmortem list --grouped"))
	}
	return nil
}
