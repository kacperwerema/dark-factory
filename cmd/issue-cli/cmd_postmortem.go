package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const postmortemUsage = `Usage:
  issue-cli postmortem start <slug> --quote "<the owner's words>" [--again] [--agent claude|codex] [--attach]
  issue-cli postmortem brief <slug>
  issue-cli postmortem write <slug> --file <path>
  issue-cli postmortem show <slug> [--json]
  issue-cli postmortem list [--json]`

var postmortemCommand = &Command{
	Name:      "postmortem",
	ShortHelp: "Post-mortem at close: a fresh agent's retro whose actions the owner agrees to in one round (sub: start|brief|write|show|list)",
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

` + postmortemUsage,
	Run:         runPostmortem,
	Subcommands: []string{"start", "brief", "write", "show", "list"},
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
		CLI: tracker.InjectProject(ctx.Project, "issue-cli"),
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
	r.Branch = "work/" + issue.Slug
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
	if agentrun.HasSession(agentrun.Target(ctx.Project, session)) {
		return agentrun.Response{}, &errPostmortemHeld{session + " is running already"}
	}
	if m := autostart.ReadMemory(); m.Found && !m.CanStart {
		return agentrun.Response{}, &errPostmortemHeld{"mem-guard says no (" + strings.TrimSpace(m.Note) + ")"}
	}
	agent := ps.Agent
	if agent == "" && wf != nil && wf.Postmortem != nil {
		agent = wf.Postmortem.Agent
	}
	startedBy := fmt.Sprintf("Started on the owner's words: %q (%s, %s).", ps.Quote, ps.How, ctx.now().Format("2006-01-02"))
	if ps.Auto {
		startedBy = fmt.Sprintf("Started by the transition into %s (workflow postmortem: start: auto, %s).", issue.Status, ctx.now().Format("2006-01-02"))
	}
	resp := startAgent(ctx.Project, agentrun.Spec{
		Session:   session,
		Prompt:    postmortem.Brief(postmortemRun(ctx, issue)) + "\n" + startedBy + "\n",
		AgentType: agent,
		IssueSlug: issue.Slug,
		WorkDir:   projectRoot(ctx.Project),
		Workflow:  wf,
		Headless:  !ps.Attach,
	})
	if resp.Status == "error" {
		return resp, fmt.Errorf("starting %s failed", session)
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
	cmd := tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli postmortem start %s --quote \"<the owner's words>\"", issue.Slug))
	fmt.Fprintln(w, "\n== Post-mortem ==")
	if !wf.PostmortemAutoStart() {
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
	acts, err := postmortem.Check(body, projectRoot(ctx.Project))
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
	return nil
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
	var out []postmortemView
	for _, e := range postmortem.Of(entries, slug) {
		acts := postmortem.Actions(e.Body)
		out = append(out, postmortemView{Issue: e.IssueSlug, File: e.FilePath, Pending: len(postmortem.Pending(acts)), Actions: acts})
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
	issue, _, err := findIssueOrErr(ctx, slug)
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
	fmt.Fprintf(ctx.Stdout, "== Post-mortem of %s ==\nfile: %s\n", issue.Slug, v.File)
	for _, a := range v.Actions {
		state := "waits for the owner"
		switch {
		case a.Applied != "":
			state = "applied: " + a.Applied
		case a.Refused != "":
			state = "refused: " + a.Refused
		}
		fmt.Fprintf(ctx.Stdout, "  %s [%s] %s\n      %s\n", a.ID, a.PlaceString(), a.Title, state)
	}
	if v.Pending > 0 {
		fmt.Fprintf(ctx.Stdout, "The owner agrees or refuses them in one round: %s\n", postmortemURL(ctx.Project, issue.Slug))
	}
	return nil
}

func runPostmortemList(ctx *Context, args []string) error {
	fs := newFlagSet("postmortem list", ctx)
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
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
	for _, v := range views {
		fmt.Fprintf(ctx.Stdout, "  %s  %d action(s), %d waiting  %s\n", v.Issue, len(v.Actions), v.Pending, filepath.Base(v.File))
	}
	return nil
}
