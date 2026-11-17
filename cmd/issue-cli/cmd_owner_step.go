package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/ownerstep"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// issue-cli owner-step: the owner's hand steps (internal/ownerstep), on the
// owner's Waiting on you list until the owner ticks them. Anyone adds one;
// ticking is an owner act (Settled rule "Owner acts are the owner's"): a
// worker is refused, a lead relays the owner's --quote, the owner's shell
// may; in the viewer the owner's signed-in browser.

const ownerStepUsage = `usage: issue-cli owner-step add --title "…" --due <day> --body "<exact text>" [--repeat 7d] [--issue <slug>]
       issue-cli owner-step list [--all] [--json]
       issue-cli owner-step done <id> [--quote "<the owner's words>"]
       issue-cli owner-step drop <id> --why "…"`

var ownerStepCommand = &Command{
	Name:      "owner-step",
	ShortHelp: "Record a step only the owner can do, with a due date and the exact text; it waits on the owner until ticked",
	LongHelp: `A step only the owner can do by hand (reinstall a build whose signing
expires, request indexing of URLs in Search Console, submit to a directory
that refuses scripts) goes on the owner's Waiting on you list with its due
date and the exact text to paste, instead of a handoff note or a report's
"Owner actions". It stays there until the owner ticks it; overdue ones are
marked red.

  add      --title "…" --due <YYYY-MM-DD|today|tomorrow|+Nd|+Nw>
           --body "<exact URLs, text, commands>" (or --body-file <path|->)
           [--repeat Nd|Nw]  opens again N days after each tick, due that day
           [--issue <slug>]  the issue it belongs to (a worker's own by
                             default; a lead's step is on the project)
           The body is kept byte for byte (4000 bytes at most) and must
           hold no secret: name where the owner finds one instead.
  list     the open steps, overdue first; --all adds done and dropped
  done     the owner did it: the owner's act. A worker is refused; a lead
           relays the owner's own words with --quote; the owner's shell may.
           In the viewer: the row's Done, the owner's browser only.
  drop     a step no longer needed or filed by mistake: a lead or the
           owner; a worker only a step it added and the owner hasn't ticked

Examples:
  issue-cli owner-step add --title "Reinstall the iPhone build" --due +7d --repeat 7d --body "Xcode → Product → Run on the phone (free signing lasts 7 days)"
  issue-cli owner-step add --title "Request indexing" --due tomorrow --body-file urls.txt
  issue-cli owner-step list
  issue-cli owner-step done st-3fa9c1 --quote "reinstalled it this morning"
  issue-cli owner-step drop st-3fa9c1 --why "the paid account signs for a year"`,
	Run:         runOwnerStep,
	Subcommands: []string{"add", "list", "done", "drop"},
}

func init() { registerCommand(ownerStepCommand) }

func runOwnerStep(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: ownerStepUsage}
	}
	switch args[0] {
	case "add":
		return runOwnerStepAdd(ctx, args[1:])
	case "list":
		return runOwnerStepList(ctx, args[1:])
	case "done":
		return runOwnerStepDone(ctx, args[1:])
	case "drop":
		return runOwnerStepDrop(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("owner-step: unknown step %q\n\n%s", args[0], ownerStepUsage)}
}

func runOwnerStepAdd(ctx *Context, args []string) error {
	fs := newFlagSet("owner-step add", ctx)
	title := fs.String("title", "", "what the owner does, short")
	due := fs.String("due", "", "the due day: YYYY-MM-DD, today, tomorrow, +Nd or +Nw")
	body := fs.String("body", "", "the exact URLs, text or commands the owner needs")
	bodyFile := fs.String("body-file", "", "read the body from a file (or - for stdin)")
	repeat := fs.String("repeat", "", "open again N days after each tick: Nd or Nw")
	issueFlag := fs.String("issue", "", "the issue the step belongs to")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return unexpectedArgError(fs, fs.Arg(0))
	}
	text, err := resolveBodyInput(ctx, *body, flagWasSet(fs, "body"), *bodyFile, flagWasSet(fs, "body-file"))
	if err != nil {
		return err
	}
	now := ctx.now()
	ev, err := newOwnerStep(*title, text, *due, *repeat, now)
	if err != nil {
		return err
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	me := whoAmI(ctx.Project, issues) // anyone adds: no guard
	ev.By = actorName(me)
	slug := strings.TrimSpace(*issueFlag)
	if slug == "" && me.Role == tracker.RoleWorker {
		slug = me.Slug
	}
	if slug != "" {
		is := tracker.FindIssue(issues, slug)
		if is == nil {
			return fmt.Errorf("owner-step add --issue %s: no such issue on the board", slug)
		}
		ev.Issue = is.Slug
	}
	if err := ownerstep.Append(ctx.Project.IssueDir, ev); err != nil {
		return err
	}
	if ctx.JSONOutput {
		return json.NewEncoder(ctx.Stdout).Encode(map[string]any{"id": ev.ID, "due": ev.Due, "repeat_days": ev.Repeat, "issue": ev.Issue, "url": needsYouURL(ctx.Project)})
	}
	where := "the project"
	if ev.Issue != "" {
		where = ev.Issue
	}
	fmt.Fprintf(ctx.Stdout, "✓ Owner step %s on %s, due %s", ev.ID, where, ev.Due)
	if ev.Repeat > 0 {
		fmt.Fprintf(ctx.Stdout, ", repeats %s after each tick", ownerstep.RepeatLabel(ev.Repeat))
	}
	fmt.Fprintf(ctx.Stdout, "\nThe owner sees it on Waiting on you until they tick it: %s\n", needsYouURL(ctx.Project))
	return nil
}

// newOwnerStep checks an add's fields and makes its event (no By, no
// Issue).
func newOwnerStep(title, body, due, repeat string, now time.Time) (ownerstep.Event, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return ownerstep.Event{}, errors.New("owner-step add needs --title: what the owner does, short")
	}
	if strings.ContainsAny(title, "\n\r") {
		return ownerstep.Event{}, errors.New("owner-step add --title is one line; the exact text goes in --body")
	}
	if strings.TrimSpace(body) == "" {
		return ownerstep.Event{}, errors.New("owner-step add needs --body (or --body-file): the exact URLs, text or commands the owner needs, so nobody has to find them again")
	}
	if len(body) > ownerstep.MaxBody {
		return ownerstep.Event{}, fmt.Errorf("owner-step add --body is %d bytes, the cap is %d: put the long list in a file in the repo and name its path", len(body), ownerstep.MaxBody)
	}
	if kind := memory.FindSecret(title + "\n" + body); kind != "" {
		return ownerstep.Event{}, fmt.Errorf("owner-step add: the step seems to hold a secret (%s); steps are on the board for anyone to read. Name where the owner finds it instead (\"the token in Keychain item x\")", kind)
	}
	day, err := ownerstep.ParseDue(due, now)
	if err != nil {
		return ownerstep.Event{}, err
	}
	n, err := ownerstep.ParseRepeat(repeat)
	if err != nil {
		return ownerstep.Event{}, err
	}
	return ownerstep.Event{Op: ownerstep.OpAdd, ID: ownerstep.NewID(), At: now.Format(time.RFC3339), Title: title, Body: body, Due: day, Repeat: n}, nil
}

// actorName is who a log line names: the session, or the owner.
func actorName(me Identity) string {
	if me.Role == tracker.RoleUnmanaged || me.Name == "" {
		return "owner"
	}
	return me.Name
}

type ownerStepJSON struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Due     string `json:"due"`
	Overdue bool   `json:"overdue"`
	Repeat  int    `json:"repeat_days,omitempty"`
	Issue   string `json:"issue,omitempty"`
	By      string `json:"by,omitempty"`
	State   string `json:"state"`
	Done    string `json:"done,omitempty"`
}

func ownerStepState(s ownerstep.Step, now time.Time) string {
	switch {
	case !s.Dropped.IsZero():
		return "dropped"
	case s.IsOpen(now):
		return "open"
	case s.Repeat > 0:
		return "waiting to reopen"
	}
	return "done"
}

func runOwnerStepList(ctx *Context, args []string) error {
	fs := newFlagSet("owner-step list", ctx)
	all := fs.Bool("all", false, "also done and dropped steps")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return unexpectedArgError(fs, fs.Arg(0))
	}
	evs, err := ownerstep.Read(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	now := ctx.now()
	steps := ownerstep.Fold(evs)
	shown := ownerstep.Open(steps, now)
	if *all {
		shown = steps
	}
	if ctx.JSONOutput {
		out := []ownerStepJSON{}
		for _, s := range shown {
			j := ownerStepJSON{ID: s.ID, Title: s.Title, Body: s.Body, Due: s.Due, Overdue: s.IsOpen(now) && s.Overdue(now), Repeat: s.Repeat, Issue: s.Issue, By: s.By, State: ownerStepState(s, now)}
			if !s.Done.IsZero() {
				j.Done = s.Done.Format(time.RFC3339)
			}
			out = append(out, j)
		}
		return json.NewEncoder(ctx.Stdout).Encode(out)
	}
	w := ctx.Stdout
	if len(shown) == 0 {
		fmt.Fprintln(w, "No open owner step. Add one: issue-cli owner-step add --title \"…\" --due <day> --body \"<exact text>\"")
		return nil
	}
	fmt.Fprintf(w, "== Owner steps (%d) ==\n", len(shown))
	for _, s := range shown {
		fmt.Fprintf(w, "%s · %s · %s", s.ID, ownerStepState(s, now), s.DueLine(now))
		if s.Repeat > 0 {
			fmt.Fprintf(w, " · repeats %s", ownerstep.RepeatLabel(s.Repeat))
		}
		if s.Issue != "" {
			fmt.Fprintf(w, " · %s", s.Issue)
		}
		fmt.Fprintf(w, "\n  %s\n", s.Title)
		for _, l := range strings.Split(strings.TrimRight(s.Body, "\n"), "\n") {
			fmt.Fprintf(w, "    %s\n", l)
		}
	}
	fmt.Fprintf(w, "The owner ticks them on Waiting on you: %s\n", needsYouURL(ctx.Project))
	return nil
}

// ownerStepArg is the one id a done or drop names, and the steps.
func ownerStepArg(ctx *Context, fs interface {
	NArg() int
	Arg(int) string
}, verb string) (*ownerstep.Step, []ownerstep.Step, error) {
	if fs.NArg() != 1 {
		return nil, nil, &usageError{msg: fmt.Sprintf("owner-step %s needs one step id (issue-cli owner-step list)\n\n%s", verb, ownerStepUsage)}
	}
	evs, err := ownerstep.Read(ctx.Project.IssueDir)
	if err != nil {
		return nil, nil, err
	}
	steps := ownerstep.Fold(evs)
	s := ownerstep.Find(steps, fs.Arg(0))
	if s == nil {
		var ids []string
		for _, o := range ownerstep.Open(steps, ctx.now()) {
			ids = append(ids, o.ID)
		}
		open := "none open"
		if len(ids) > 0 {
			open = "open: " + strings.Join(ids, ", ")
		}
		return nil, nil, fmt.Errorf("owner-step %s: no step %s (%s)", verb, fs.Arg(0), open)
	}
	return s, steps, nil
}

func runOwnerStepDone(ctx *Context, args []string) error {
	fs := newFlagSet("owner-step done", ctx)
	quote := fs.String("quote", "", "a lead: the owner's own words saying it is done")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	s, _, err := ownerStepArg(ctx, fs, "done")
	if err != nil {
		return err
	}
	now := ctx.now()
	if !s.Dropped.IsZero() {
		return fmt.Errorf("owner-step done: %s was dropped on %s (%s)", s.ID, ownerstep.DayOf(s.Dropped), s.DropWhy)
	}
	if !s.IsOpen(now) {
		return fmt.Errorf("owner-step done: %s was ticked on %s by %s; it is not open (%s)", s.ID, ownerstep.DayOf(s.Done), s.DoneBy, ownerStepState(*s, now))
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	me := guardWho(ctx, issues, "owner-step done")
	ev := ownerstep.Event{Op: ownerstep.OpDone, ID: s.ID, At: now.Format(time.RFC3339)}
	switch me.Role {
	case tracker.RoleWorker:
		return fmt.Errorf("ticking an owner step is the owner's act: a worker can't say the owner did it. The owner ticks it on Waiting on you (%s); to ask, comment on your issue trail:\n  %s",
			needsYouURL(ctx.Project), tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: did you do owner step %s?\"", valueOrDefault(me.Slug, "<slug>"), s.ID)))
	case tracker.RoleProjectLead, tracker.RoleDepartmentLead:
		if s.Issue != "" {
			if is := tracker.FindIssue(issues, s.Issue); is != nil {
				if refusal := outOfScope(ctx.Project, me, is, "ticks owner steps of"); refusal != "" {
					return errors.New(refusal)
				}
			}
		}
		if strings.TrimSpace(*quote) == "" {
			return errors.New("ticking an owner step is the owner's act: relay the owner's own words with --quote \"…\" (a lead never ticks one on its own)")
		}
		ev.Role, ev.By, ev.Words = ownerstep.RoleLeadRelay, me.Name, strings.TrimSpace(*quote)
	default:
		ev.Role, ev.By, ev.Words = ownerstep.RoleOwnerShell, "owner", strings.TrimSpace(*quote)
	}
	if err := ownerstep.Append(ctx.Project.IssueDir, ev); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s done (%s)\n", s.ID, s.Title)
	if s.Repeat > 0 {
		fmt.Fprintf(ctx.Stdout, "It opens again on %s.\n", ownerstep.AddDays(ownerstep.DayOf(now), s.Repeat))
	}
	return nil
}

func runOwnerStepDrop(ctx *Context, args []string) error {
	fs := newFlagSet("owner-step drop", ctx)
	why := fs.String("why", "", "why the step is no longer needed")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	s, _, err := ownerStepArg(ctx, fs, "drop")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*why) == "" {
		return errors.New("owner-step drop needs --why: the owner reads it in the log")
	}
	if !s.Dropped.IsZero() {
		return fmt.Errorf("owner-step drop: %s was already dropped on %s", s.ID, ownerstep.DayOf(s.Dropped))
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	me := guardWho(ctx, issues, "owner-step drop")
	if me.Role == tracker.RoleWorker && (s.By != me.Name || !s.Done.IsZero()) {
		return fmt.Errorf("owner-step drop: a worker drops only a step it added that the owner hasn't ticked; %s is %s's. Ask your lead on your issue trail:\n  %s",
			s.ID, s.By, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: drop owner step %s? …\"", valueOrDefault(me.Slug, "<slug>"), s.ID)))
	}
	ev := ownerstep.Event{Op: ownerstep.OpDrop, ID: s.ID, At: ctx.now().Format(time.RFC3339), By: actorName(me), Why: strings.TrimSpace(*why)}
	if err := ownerstep.Append(ctx.Project.IssueDir, ev); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s dropped (%s)\n", s.ID, s.Title)
	return nil
}

func valueOrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
