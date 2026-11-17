package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// exitWaitingOnGate is dispatch's exit code when the issue only waits on the
// owner's approval and nothing was started.
const exitWaitingOnGate = 3

var dispatchCommand = &Command{
	Name:      "dispatch",
	ShortHelp: "Start (or re-attach to) the issue's worker, exactly like ▶ on the board",
	LongHelp: `Start the issue's worker: the same prompt, worktree and tmux session as ▶
on the board (both build it with agentrun.DispatchSpec). Runs here, through
tmux: the viewer does not need to be running. A live worker is re-attached,
never started twice.

When everything the issue's next transition needs is done except the
owner's approval, dispatch refuses and names the gate: a new worker would
only stop there. --force dispatches anyway.

A human starts the work: --quote is required and carries the human's words
that ask for this worker (from the lead's chat, or your own). Without it
nothing starts. Each dispatch (and --reprompt) leaves a comment on the issue:
  dispatch: "<words>" (<how>, <date>)
One slug per call; nothing expands an epic into its children.

--autostart is the one start without new words: on a project where the
owner turned auto-start on (issue-cli autostart on --quote ...), it quotes
the owner's stored words, checks memory (mem-guard), the worker cap and the
machine-wide start budget again under a lock, and starts nothing when any
of them holds. issue-cli autostart next prints the command when it may run.

--takeover is the other start without new words: a worker stopped at its
usage limit hands its branch to the other agent. It quotes the issue's
newest dispatch: words, renames the limited session limited-<slug> (left
running, nothing typed into it) and starts the other agent as agent-<slug> in
the same worktree, its prompt naming HANDOFF.md and the branch. Once per
issue. It holds on mem-guard and the machine-wide start budget. issue-cli
queue next, autostart next and agents print the command when it may run.

--fresh is the third start without new words: the issue entered a status
with session: fresh while its worker was live, and transition marked it
(stage: fresh session at <status>) and told the worker to write HANDOFF.md
and end its turn. It quotes the issue's newest dispatch: words, closes the
old session once its pane is out of a turn, and starts agent-<slug> in the
same worktree on the stage briefing: paths only, trimmed to the stage and
its budget. It holds on mem-guard and the machine-wide start budget. queue
next, autostart next and agents print the command when it may run.

Workers can't dispatch: post a question: comment for your lead instead.

Flags:
  --quote "<words>"      the human's words that ask for this worker (required)
  --how "<how>"          how the words reached you (default: "CEO chat" for
                         the lead, "owner's shell" otherwise)
  --agent claude|codex   agent to run (default claude)
  --force                dispatch even though the issue waits on a gate
  --reprompt             re-send the current briefing into the live worker
                         instead of starting one (the Re-send prompt button)
  --autostart            start on the owner's stored auto-start words, in
                         place of --quote (see issue-cli help autostart)
  --takeover             the other agent takes over from a worker at its
                         usage limit, on the issue's own words, in place of
                         --quote; takes no --agent, it is always the other
  --fresh                start the next stage's fresh session, on the issue's
                         own words, in place of --quote (session: fresh)
  --json                 print the same object POST .../dispatch returns

Exit codes:
  0  started, re-attached, or re-prompted
  1  the launch failed, --reprompt found no live worker, or --autostart,
     --takeover or --fresh held
  2  usage error (also: no --quote)
  3  waiting on the owner's approval; nothing was started

Examples:
  issue-cli dispatch api/my-issue --quote "start the api one"
  issue-cli dispatch api/my-issue --agent codex --quote "try codex on it"
  issue-cli dispatch api/my-issue --reprompt --quote "re-send its prompt"`,
	Run: runDispatch,
}

func init() {
	registerCommand(dispatchCommand)
}

func runDispatch(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "dispatch")
	if err != nil {
		return err
	}
	fs := newFlagSet("dispatch", ctx)
	agentFlag := fs.String("agent", "", "agent to run: claude or codex (default: the workflow's routing: rules, else claude)")
	force := fs.Bool("force", false, "dispatch even though the issue waits on the owner's approval")
	reprompt := fs.Bool("reprompt", false, "re-send the current briefing into the live worker")
	quoteFlag := fs.String("quote", "", "the human's words that ask for this worker (required)")
	howFlag := fs.String("how", "", "how the words reached you")
	auto := fs.Bool("autostart", false, "start it on the owner's standing words (issue-cli autostart), when autostart next allows")
	takeover := fs.Bool("takeover", false, "the other agent takes over from a worker at its usage limit, on the issue's own words")
	fresh := fs.Bool("fresh", false, "start the fresh session a session: fresh status asks for, on the issue's own words")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if *auto && (quote != "" || *reprompt || *force) {
		return &usageError{msg: "--autostart quotes the owner's stored words and starts a new worker: --quote, --reprompt and --force don't apply to it"}
	}
	if *takeover && (quote != "" || *reprompt || *force || *auto || *agentFlag != "") {
		return &usageError{msg: "--takeover quotes the issue's own dispatch words and picks the other agent: --quote, --reprompt, --force, --autostart and --agent don't apply to it"}
	}
	if *fresh && (quote != "" || *reprompt || *force || *auto || *takeover) {
		return &usageError{msg: "--fresh quotes the issue's own dispatch words and starts the stage's new session: --quote, --reprompt, --force, --autostart and --takeover don't apply to it"}
	}
	if quote == "" && !*auto && !*takeover && !*fresh {
		msg := fmt.Sprintf("dispatch needs --quote: the human's words that ask for this worker. Nothing was started.\n\nA human starts the work; never dispatch on your own (after planning, or the next issue).\n\nExample:\n  issue-cli dispatch %s --quote \"<the human's words>\"", slug)
		if line := autostartLine(ctx); line != "" && !strings.HasPrefix(line, "Auto-start: paused") {
			msg += "\n\nAuto-start is on for this project: " + tracker.InjectProject(ctx.Project, "issue-cli autostart next")
		}
		if queue.Exists(ctx.Project.IssueDir) {
			msg += "\n\nThe project has a queue; items carry the owner's words: " + tracker.InjectProject(ctx.Project, "issue-cli queue next")
		}
		return &usageError{msg: msg}
	}
	if *agentFlag != "" && *agentFlag != "claude" && *agentFlag != "codex" {
		return &usageError{msg: fmt.Sprintf("--agent must be claude or codex, got %q", *agentFlag)}
	}
	if *reprompt && (*force || flagSet(fs, "agent")) {
		return &usageError{msg: "--reprompt re-sends the briefing into the live worker; --agent and --force don't apply to it"}
	}

	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	proj := ctx.Project
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		guardNever(ctx, "dispatch")
		return fmt.Errorf("workers can't dispatch (you are %s, detected from %s).\n\nPost a question: comment on your issue instead; the CEO relays it:\n  %s%s", valueOr(me.Name, "a worker"), me.Signal,
			tracker.InjectProject(proj, fmt.Sprintf("issue-cli comment %s \"question: ...\"", valueOr(me.Slug, "<slug>"))), leadLinkHint(proj, me.Slug))
	}

	if refusal := outOfScope(proj, me, issue, "dispatches"); refusal != "" {
		return fmt.Errorf("%s", refusal)
	}
	if refusal := relayRefusal(proj, me, issue); refusal != "" {
		return fmt.Errorf("%s", refusal)
	}
	if *auto {
		return dispatchAutostart(ctx, issue, issues, me, *agentFlag)
	}
	if *takeover {
		return dispatchTakeover(ctx, issue, me)
	}
	if *fresh {
		return dispatchFresh(ctx, issue, me, *agentFlag)
	}

	how := strings.TrimSpace(*howFlag)
	if how == "" {
		how = "owner's shell"
		switch me.Role {
		case tracker.RoleProjectLead:
			how = "CEO chat"
		case tracker.RoleDepartmentLead:
			how = me.Department + " lead chat"
		}
	}
	comment := tracker.DispatchComment(quote, how, ctx.now().Format("2006-01-02"))
	if me.Name != "" {
		comment += " — recorded by " + me.Name
	}

	if *reprompt {
		resp := repromptWorker(proj, issue, projectRoot(proj))
		return reportDispatch(ctx, issue, resp, comment)
	}

	spec, refused := agentrun.DispatchSpec(proj, issue, *agentFlag, projectRoot(proj), *force)
	if refused != nil {
		if ctx.JSONOutput {
			if err := writeJSON(ctx.Stdout, refused); err != nil {
				return err
			}
			return &exitCodeError{Code: exitWaitingOnGate}
		}
		return &exitCodeError{Code: exitWaitingOnGate, Msg: fmt.Sprintf(
			"✗ Not dispatched: %s waits on the owner's approval for %q.\nEverything else %s → %s needs is done, so a new worker would only stop at the gate.\n\nA human approves at: %s\nSeveral waiting?     %s\nDispatch anyway:     %s",
			issue.Slug, refused.Gate, issue.Status, refused.Gate,
			approvalURL(proj, issue.Slug, refused.Gate), needsYouURL(proj),
			tracker.InjectProject(proj, fmt.Sprintf("issue-cli dispatch %s --force --quote %s", issue.Slug, shell.Quote(quote))))}
	}
	resp := startAgent(proj, spec)
	if resp.AttachCmd == "" && resp.Status != "error" {
		resp.AttachCmd, resp.SwitchCmd = agentrun.AttachCmd(proj, spec.Session), agentrun.SwitchCmd(proj, spec.Session)
	}
	return reportDispatch(ctx, issue, resp, comment)
}

// repromptWorker re-sends the issue's briefing into its live worker. A var
// so tests need no tmux.
var repromptWorker = agentrun.Reprompt

// dispatchMainHolds is what main did to the brief's named tests since the
// issue was created; dispatch says it to the lead and still starts the
// worker (D-1 of factory/dispatch-says-already-holds-on-main-…). A var so
// tests need no git history.
var dispatchMainHolds = tracker.MainHolds

// reportDispatch prints a dispatch or reprompt response: JSON, or the
// outcome line, the steps and how to reach the session. A worker started or
// re-prompted gets comment (the dispatch: line) on its issue. A failed launch
// or a reprompt with no live worker exits 1.
func reportDispatch(ctx *Context, issue *tracker.Issue, resp agentrun.Response, comment string) error {
	failed := resp.Status == "error" || resp.Status == "no-session"
	commented := false
	if resp.Status == "dispatched" || resp.Status == "reprompted" {
		if err := tracker.AddCommentBy(issue.FilePath, 0, comment, "cli", commentAuthor(ctx.Project, issue)); err != nil {
			fmt.Fprintf(ctx.Stderr, "warning: writing the dispatch comment: %v\n", err)
		} else {
			commented = true
		}
	}
	if ctx.JSONOutput {
		if err := writeJSON(ctx.Stdout, resp); err != nil {
			return err
		}
		if failed {
			return &exitCodeError{Code: 1}
		}
		return nil
	}
	switch resp.Status {
	case "dispatched":
		fmt.Fprintf(ctx.Stdout, "✓ Dispatched %s for %s\n", resp.Session, issue.Slug)
	case "reattached":
		fmt.Fprintf(ctx.Stdout, "✓ Already live: %s (re-attached, prompt not re-sent)\n", resp.Session)
	case "reprompted":
		fmt.Fprintf(ctx.Stdout, "✓ Re-sent the briefing to %s\n", resp.Session)
	}
	if commented {
		fmt.Fprintf(ctx.Stdout, "  comment: %s\n", comment)
	}
	if resp.Status == "dispatched" {
		if line := dispatchMainHolds(projectRoot(ctx.Project), issue).Summary(); line != "" {
			fmt.Fprintf(ctx.Stdout, "  %s; the briefing says so\n", line)
		}
	}
	printSteps(ctx, resp.Steps)
	if resp.Status == "no-session" {
		return fmt.Errorf("no live worker for %s; nothing was re-sent (see the step above)", issue.Slug)
	}
	if failed {
		return fmt.Errorf("dispatching %s failed (see the steps above)", issue.Slug)
	}
	if resp.AttachCmd != "" {
		fmt.Fprintf(ctx.Stdout, "\nInside tmux:  %s\nFrom a shell: %s\n", resp.SwitchCmd, resp.AttachCmd)
	}
	return nil
}

// flagSet reports whether the flag called name was given on the command line.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// dispatchAutostart starts issue's worker on the owner's standing words.
// Under the machine-wide lock it decides again for this issue (a stale
// autostart next can't overshoot), starts it, and stamps the last start, so
// a lead in another project ticking at the same minute holds.
func dispatchAutostart(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue, me Identity, agentType string) error {
	proj := ctx.Project
	err := autostart.WithShare(autostart.SharedDir(), func(b *autostart.Budget) (*autostart.Start, error) {
		v, st, err := autostartVerdict(ctx, issues, b, issue.Slug)
		if err != nil {
			return nil, err
		}
		b.Decided(proj.Slug, proj.Weight(), v, ctx.now())
		if !v.Start {
			return nil, &exitCodeError{Code: 1, Hold: true, HoldKind: v.Kind, Msg: fmt.Sprintf("✗ Not auto-started: hold: %s. Nothing was started.", v.Reason)}
		}
		now := ctx.now()
		_, yoloOn, _ := yolo.Active(yoloRoot(proj), proj.Slug, now)
		spec, refused := agentrun.DispatchSpec(proj, issue, agentType, projectRoot(proj), yoloOn)
		if refused != nil {
			return nil, &exitCodeError{Code: exitWaitingOnGate, Msg: fmt.Sprintf("✗ Not auto-started: %s waits on the owner's approval for %q. Nothing was started.", issue.Slug, refused.Gate)}
		}
		if spec.Route != nil && spec.Route.Hold != "" {
			return nil, &exitCodeError{Code: 1, Hold: true, Msg: fmt.Sprintf("✗ Not auto-started: hold: %s. Nothing was started.\nA wrong rest: %s", spec.Route.Hold, tracker.InjectProject(proj, "issue-cli agents clear <agent>"))}
		}
		comment := autostartComment(st, v, now)
		if me.Name != "" {
			comment += " — recorded by " + me.Name
		}
		resp := startAgent(proj, spec)
		if resp.AttachCmd == "" && resp.Status != "error" {
			resp.AttachCmd, resp.SwitchCmd = agentrun.AttachCmd(proj, spec.Session), agentrun.SwitchCmd(proj, spec.Session)
		}
		rerr := reportDispatch(ctx, issue, resp, comment)
		if resp.Status != "dispatched" {
			return nil, rerr
		}
		b.Started(proj.Slug, issue.Slug, v, now)
		return &autostart.Start{Project: proj.Slug, Slug: issue.Slug, At: now, Memory: v.Memory, By: me.Name}, rerr
	})
	if errors.Is(err, autostart.ErrBusy) {
		return &exitCodeError{Code: 1, Hold: true, Msg: "✗ Not auto-started: hold: " + err.Error() + ". Nothing was started."}
	}
	return err
}

// dispatchTakeover starts the other agent on issue's branch. Under the
// machine-wide lock it decides again, so two ticks can't both start one, and
// a whole subscription stopping at once becomes one start per gap. The worker
// cap doesn't apply: the limited session leaves the agent-* names as this one
// joins them.
func dispatchTakeover(ctx *Context, issue *tracker.Issue, me Identity) error {
	proj := ctx.Project
	root := projectRoot(proj)
	err := autostart.WithBudget(autostart.SharedDir(), func(last *autostart.Start) (*autostart.Start, error) {
		now := ctx.now()
		hold := func(why string) (*autostart.Start, error) {
			return nil, &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not taken over: hold: %s. Nothing was started.", why)}
		}
		_, yoloOn, _ := yolo.Active(yoloRoot(proj), proj.Slug, now)
		t, ok := agentrun.TakeoverOf(proj, root, issue, yoloOn, now)
		if !ok {
			return hold(issue.Slug + "'s worker shows no usage limit")
		}
		if t.Hold != "" {
			return hold(t.Hold)
		}
		if allowed, why := autostart.MemGuardAllows(); !allowed {
			return hold(why)
		}
		if gap := time.Duration(autostart.DefaultGapMinutes) * time.Minute; last != nil && now.Sub(last.At) < gap {
			return hold(fmt.Sprintf("%s started %s %s ago (gap %dm)", last.Project, last.Slug, now.Sub(last.At).Round(time.Second), autostart.DefaultGapMinutes))
		}
		spec := agentrun.TakeoverSpec(proj, issue, t, root)
		limited := tracker.LimitedSessionName(issue.Slug)
		if err := agentrun.RenameSession(proj, t.Session, limited); err != nil {
			return nil, fmt.Errorf("renaming %s to %s: %w; nothing was started", t.Session, limited, err)
		}
		comment := tracker.TakeoverComment(t.Words, t.From, now.Format("2006-01-02"))
		if me.Name != "" {
			comment += " — recorded by " + me.Name
		}
		resp := startAgent(proj, spec)
		if resp.Status != "dispatched" && !agentrun.IsLive(proj, t.Session) {
			if err := agentrun.RenameSession(proj, limited, t.Session); err != nil {
				fmt.Fprintf(ctx.Stderr, "warning: renaming %s back: %v\n", limited, err)
			}
		}
		if resp.AttachCmd == "" && resp.Status != "error" {
			resp.AttachCmd, resp.SwitchCmd = agentrun.AttachCmd(proj, spec.Session), agentrun.SwitchCmd(proj, spec.Session)
		}
		rerr := reportDispatch(ctx, issue, resp, comment)
		if resp.Status != "dispatched" {
			return nil, rerr
		}
		return &autostart.Start{Project: proj.Slug, Slug: issue.Slug, At: now, By: me.Name}, rerr
	})
	if errors.Is(err, autostart.ErrBusy) {
		return &exitCodeError{Code: 1, Msg: "✗ Not taken over: hold: " + err.Error() + ". Nothing was started."}
	}
	return err
}

// dispatchFresh starts the fresh session issue's status asks for: under the
// machine-wide lock it decides again, closes the old session (its pane out
// of a turn) and starts agent-<slug> in the same worktree on the stage
// briefing, quoting the issue's own words.
func dispatchFresh(ctx *Context, issue *tracker.Issue, me Identity, agent string) error {
	proj := ctx.Project
	root := projectRoot(proj)
	err := autostart.WithBudget(autostart.SharedDir(), func(last *autostart.Start) (*autostart.Start, error) {
		now := ctx.now()
		hold := func(why string) (*autostart.Start, error) {
			return nil, &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ No fresh session: hold: %s. Nothing was started.", why)}
		}
		_, yoloOn, _ := yolo.Active(yoloRoot(proj), proj.Slug, now)
		f, ok := agentrun.FreshStartOf(proj, issue, yoloOn)
		if !ok {
			return hold(issue.Slug + " waits for no fresh session: no stage: mark at its status, or a dispatch answered it")
		}
		if f.Hold != "" {
			return hold(f.Hold)
		}
		if allowed, why := autostart.MemGuardAllows(); !allowed {
			return hold(why)
		}
		if gap := time.Duration(autostart.DefaultGapMinutes) * time.Minute; last != nil && now.Sub(last.At) < gap {
			return hold(fmt.Sprintf("%s started %s %s ago (gap %dm)", last.Project, last.Slug, now.Sub(last.At).Round(time.Second), autostart.DefaultGapMinutes))
		}
		spec, _ := agentrun.DispatchSpec(proj, issue, agent, root, true)
		if f.Live {
			if err := agentrun.CloseSession(proj, f.Session); err != nil {
				return nil, fmt.Errorf("closing %s: %w; nothing was started", f.Session, err)
			}
		}
		comment := tracker.DispatchComment(f.Words, tracker.FreshDispatchHow(issue.Status), now.Format("2006-01-02"))
		if me.Name != "" {
			comment += " — recorded by " + me.Name
		}
		resp := startAgent(proj, spec)
		if resp.AttachCmd == "" && resp.Status != "error" {
			resp.AttachCmd, resp.SwitchCmd = agentrun.AttachCmd(proj, spec.Session), agentrun.SwitchCmd(proj, spec.Session)
		}
		rerr := reportDispatch(ctx, issue, resp, comment)
		if resp.Status != "dispatched" {
			return nil, rerr
		}
		return &autostart.Start{Project: proj.Slug, Slug: issue.Slug, At: now, By: me.Name}, rerr
	})
	if errors.Is(err, autostart.ErrBusy) {
		return &exitCodeError{Code: 1, Msg: "✗ No fresh session: hold: " + err.Error() + ". Nothing was started."}
	}
	return err
}
