package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var jobsCommand = &Command{
	Name:      "jobs",
	ShortHelp: "List the departments' recurring jobs: schedule, next due, last run, standing start",
	LongHelp: `List the recurring jobs of every department (or one): the schedule, when
the next run is due, the last run (issue, status, token estimate, outputs)
and whether the owner's standing start is on for it.

Jobs live in projects.yaml under a department:
  departments:
    - name: Marketing
      jobs:
        - id: weekly-seo-pass
          name: Weekly SEO pass
          every: week        # day | week | month
          on: mon            # week: mon..sun; month: 1..28 or last
          at: "07:00"        # local time
          brief: departments/marketing/jobs/weekly-seo-pass.md
          type: seo-pass     # optional: the run's type of work (default_type)

See docs/departments.md, "Recurring jobs", and issue-cli help job.

Examples:
  issue-cli jobs
  issue-cli jobs marketing --json`,
	Run: runJobs,
}

var jobCommand = &Command{
	Name:      "job",
	ShortHelp: "Recurring jobs: what is due, start a run, the owner's standing start (sub: due|run|auto)",
	LongHelp: `A department's recurring job runs as an issue in the department, created
from the job's brief at its workflow's first status and started like any
dispatch. The issue is the run's record: its worker, gates, timeline and
the outputs it puts on the shelf.

The tool runs nothing on a timer: at each check-in the lead runs job due and
the one command it prints for each job.

Subcommands:
  due
      Each job due now, with its verdict and the command to run: start
      (the job's standing start allows it), waits for the owner (no
      standing start: Needs you shows it), or hold: <reason>. A department
      lead sees its own department's jobs; the CEO those of departments
      whose lead is not live. Logs each due and hold in the run log.
  run <dept>/<job> --quote "<owner's words>" [--how ...] [--again]
      The owner asked for this run: create it and start its worker. Works
      whether or not the job is due; a second run in the same period needs
      --again.
  run <dept>/<job> --scheduled
      Start the due run unattended, on the job's standing start. Checks
      mem-guard, the worker cap and the machine-wide start budget again
      under auto-start's lock; exit 1 with the reason when anything holds.
  auto on <dept>/<job>|--all --quote "<owner's words>" [--how ...]
  auto off <dept>/<job>|--all [--reason ...]
      The owner's standing start: from then on a due run of the job (or of
      every job) may start without the owner naming it. Off by default.
      Only the owner or a lead with the owner's words; never a worker.

Add --dry-run to run to see the issue it would create and the verdict.

Examples:
  issue-cli job due
  issue-cli job run marketing/weekly-seo-pass --quote "run the SEO pass now"
  issue-cli job run marketing/weekly-seo-pass --scheduled
  issue-cli job auto on --all --quote "for now all"`,
	Run:         runJob,
	Subcommands: []string{"due", "run", "auto"},
}

func init() {
	registerCommand(jobsCommand)
	registerCommand(jobCommand)
}

// jobsSnippet is the config to paste when a project has no jobs.
const jobsSnippet = `No recurring jobs. Add them to a department in projects.yaml:
  departments:
    - name: Marketing
      jobs:
        - id: weekly-seo-pass
          name: Weekly SEO pass
          every: week
          on: mon
          at: "07:00"
          brief: departments/marketing/jobs/weekly-seo-pass.md
          type: seo-pass   # optional: the run's type of work
See docs/departments.md, "Recurring jobs".`

func runJob(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("job needs a project — pass --project <slug> or run from a project root")
	}
	if len(args) == 0 {
		return &usageError{msg: "usage: issue-cli job due | run <dept>/<job> | auto on|off <dept>/<job>"}
	}
	switch args[0] {
	case "due":
		return runJobDue(ctx, args[1:])
	case "run":
		return runJobRun(ctx, args[1:])
	case "auto":
		return runJobAuto(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown job subcommand %q (due, run, auto)", args[0])}
}

// jobInput reads what an evaluation needs: the standing starts, mem-guard,
// the project's live workers and the machine's last auto-start.
func jobInput(ctx *Context, last *autostart.Start) (jobs.Input, error) {
	proj := ctx.Project
	st, err := jobs.LoadState(yoloRoot(proj), proj.Slug)
	if err != nil {
		return jobs.Input{}, err
	}
	if last == nil {
		last, _ = autostart.LastStart(autostart.SharedDir())
	}
	return jobs.Input{
		State:   st,
		Memory:  autostart.ReadMemory(),
		Workers: agentrun.LiveWorkers(proj, projectRoot(proj)),
		Last:    last,
		Limits:  jobs.DefaultLimits,
		Now:     ctx.now(),
	}, nil
}

// jobsInScope keeps the jobs this session ticks: a department lead its own
// department's (a group's lead its departments' too), the CEO those of
// departments with no live lead of their own or of their group, anyone
// else every job.
func jobsInScope(proj *tracker.Project, me Identity, all []jobs.Status) []jobs.Status {
	var keep func(dept string) bool
	switch me.Role {
	case tracker.RoleDepartmentLead:
		keep = func(dept string) bool { return proj.InScope(me.Department, dept) }
	case tracker.RoleProjectLead:
		live := sessionLive(proj)
		keep = func(dept string) bool {
			if g := proj.GroupOf(dept); g != nil && live(tracker.DepartmentLeadName(proj, g.Name)) {
				return false
			}
			return !live(tracker.DepartmentLeadName(proj, dept))
		}
	default:
		return all
	}
	var out []jobs.Status
	for _, s := range all {
		if keep(s.Department) {
			out = append(out, s)
		}
	}
	return out
}

type jobsListJSON struct {
	Jobs []jobRow `json:"jobs"`
}

type jobRow struct {
	jobs.Status
	Outputs  int `json:"outputs"`
	ToReview int `json:"to_review"`
}

func runJobs(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("jobs needs a project — pass --project <slug> or run from a project root")
	}
	pos, err := parsePositionals(ctx, "jobs", args, 1)
	if err != nil {
		return err
	}
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return err
	}
	in, err := jobInput(ctx, nil)
	if err != nil {
		return err
	}
	all := jobs.Evaluate(proj, issues, in)
	if len(pos) == 1 {
		d := proj.DepartmentByKey(pos[0])
		if d == nil {
			return fmt.Errorf("no department %q (departments: %s)", pos[0], strings.Join(proj.DepartmentKeys(), ", "))
		}
		var keep []jobs.Status
		for _, s := range all {
			if s.Department == d.Name {
				keep = append(keep, s)
			}
		}
		all = keep
	}
	shelf, _ := outputs.Load(proj)
	rows := make([]jobRow, 0, len(all))
	for _, s := range all {
		row := jobRow{Status: s}
		for _, o := range shelf {
			if s.LastSlug != "" && o.Issue == s.LastSlug {
				row.Outputs++
				if o.State == outputs.StateReview {
					row.ToReview++
				}
			}
		}
		rows = append(rows, row)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, jobsListJSON{Jobs: rows})
	}
	if len(rows) == 0 {
		fmt.Fprintln(ctx.Stdout, jobsSnippet)
		return nil
	}
	printJobRows(ctx.Stdout, proj, rows, in.Now)
	return nil
}

func printJobRows(w io.Writer, proj *tracker.Project, rows []jobRow, now time.Time) {
	dept := ""
	for _, r := range rows {
		if r.Department != dept {
			dept = r.Department
			fmt.Fprintf(w, "%s:\n", dept)
		}
		fmt.Fprintf(w, "  %s — %s\n", r.Key, r.Name)
		if r.Error != "" {
			fmt.Fprintf(w, "    ! %s (fix it in projects.yaml; issue-cli health lint --check departments)\n", r.Error)
			continue
		}
		fmt.Fprintf(w, "    schedule: %s · next %s\n", r.Schedule, whenLabel(r.Next, now))
		if r.LastSlug == "" {
			fmt.Fprintln(w, "    last run: none yet")
		} else {
			fmt.Fprintf(w, "    last run: %s (%s) %s", r.LastSlug, r.LastPeriod, r.LastStatus)
			if r.LastTokens > 0 {
				fmt.Fprintf(w, " · ~%d tokens", r.LastTokens)
			}
			fmt.Fprintf(w, " · %d outputs", r.Outputs)
			if r.ToReview > 0 {
				fmt.Fprintf(w, " (%d to review)", r.ToReview)
			}
			fmt.Fprintf(w, " · %d runs\n", r.Runs)
		}
		if r.Due {
			fmt.Fprintf(w, "    due: %s — %s\n", r.Period, verdictWords(r.Status))
		}
		fmt.Fprintf(w, "    %s\n", autoWords(proj, r.Status))
	}
}

// whenLabel is "Mon 07:00 (in 3d)" for a time in the next week, else the date.
func whenLabel(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := t.Sub(now)
	switch {
	case d < 0:
		return t.Format("2006-01-02 15:04")
	case d < time.Hour:
		return fmt.Sprintf("%s (in %d min)", t.Format("Mon 15:04"), int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%s (in %dh)", t.Format("Mon 15:04"), int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%s (in %dd)", t.Format("Mon 15:04"), int(d.Hours()/24))
	}
	return t.Format("2006-01-02 15:04")
}

// verdictWords is a due job's verdict in words.
func verdictWords(s jobs.Status) string {
	missed := ""
	if s.Missed > 0 {
		missed = fmt.Sprintf(" (%d missed before it)", s.Missed)
	}
	switch s.Verdict {
	case jobs.VerdictStart:
		return "start it now" + missed
	case jobs.VerdictOwner:
		return "waits for the owner" + missed
	case jobs.VerdictHold:
		return "hold: " + s.Reason + missed
	}
	return s.Verdict
}

func autoWords(proj *tracker.Project, s jobs.Status) string {
	if s.Auto == nil {
		return "standing start: off — the owner turns it on: " + tracker.InjectProject(proj, fmt.Sprintf("issue-cli job auto on %s --quote \"<owner's words>\"", s.Key))
	}
	return fmt.Sprintf("standing start: on (owner: %q, turned on by %s %s %s)", s.Auto.Words, s.Auto.By, s.Auto.How, s.Auto.Since.Format("2006-01-02"))
}

func runJobDue(ctx *Context, args []string) error {
	fs := newFlagSet("job due", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return err
	}
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers don't tick recurring jobs: your lead does. Ask it with a question: comment on your issue")
	}
	in, err := jobInput(ctx, nil)
	if err != nil {
		return err
	}
	all := jobs.Evaluate(proj, issues, in)
	scoped := jobsInScope(proj, me, all)
	var due []jobs.Status
	for _, s := range scoped {
		if s.Due || s.Verdict == jobs.VerdictError {
			due = append(due, s)
		}
	}
	noteDue(ctx, proj, due)
	if ctx.JSONOutput {
		if due == nil {
			due = []jobs.Status{}
		}
		return writeJSON(ctx.Stdout, due)
	}
	w := ctx.Stdout
	if len(all) == 0 {
		fmt.Fprintln(w, jobsSnippet)
		return nil
	}
	if me.Role == tracker.RoleProjectLead {
		defer printOutOfScope(w, proj, all, scoped)
	}
	if len(due) == 0 {
		fmt.Fprintf(w, "No job due (%d in your scope).", len(scoped))
		if next := nextJob(scoped); next != nil {
			fmt.Fprintf(w, " Next: %s %s.", next.Key, whenLabel(next.Next, in.Now))
		}
		fmt.Fprintln(w)
		return nil
	}
	for _, s := range due {
		if s.Verdict == jobs.VerdictError {
			fmt.Fprintf(w, "! %s: %s (skipped; fix it in projects.yaml)\n", s.Key, s.Error)
			continue
		}
		fmt.Fprintf(w, "%s · %s (due %s): %s\n", s.Key, s.Period, s.DueAt.Format("Mon 2006-01-02 15:04"), verdictWords(s))
		switch s.Verdict {
		case jobs.VerdictStart:
			fmt.Fprintln(w, "  "+tracker.InjectProject(proj, "issue-cli job run "+s.Key+" --scheduled"))
		case jobs.VerdictOwner:
			fmt.Fprintf(w, "  in Needs you: %s\n", needsYouURL(proj))
			fmt.Fprintln(w, "  when the owner asks: "+tracker.InjectProject(proj, fmt.Sprintf("issue-cli job run %s --quote \"<owner's words>\"", s.Key)))
		}
	}
	return nil
}

// printOutOfScope names the live department leads that tick the jobs left
// out of this session's scope.
func printOutOfScope(w io.Writer, proj *tracker.Project, all, scoped []jobs.Status) {
	n := len(all) - len(scoped)
	if n == 0 {
		return
	}
	in := map[string]bool{}
	for _, s := range scoped {
		in[s.Key] = true
	}
	var leads []string
	seen := map[string]bool{}
	for _, s := range all {
		if lead := tracker.DepartmentLeadName(proj, s.Department); !in[s.Key] && !seen[lead] {
			seen[lead] = true
			leads = append(leads, lead)
		}
	}
	noun := "jobs belong"
	if n == 1 {
		noun = "job belongs"
	}
	fmt.Fprintf(w, "(%d more %s to a live department lead: %s)\n", n, noun, strings.Join(leads, ", "))
}

// noteDue logs each due job's due and hold in the run log, once.
func noteDue(ctx *Context, proj *tracker.Project, due []jobs.Status) {
	log := jobs.ReadLog(proj)
	date := ctx.now().Format(time.RFC3339)
	for _, s := range due {
		if s.Verdict == jobs.VerdictError {
			continue
		}
		events := []jobs.Event{{Op: jobs.OpDue, Job: s.Key, Run: s.Period, Date: date, Missed: s.Missed}}
		if s.Verdict == jobs.VerdictHold {
			events = append(events, jobs.Event{Op: jobs.OpHold, Job: s.Key, Run: s.Period, Date: date, Reason: s.Reason})
		}
		for _, e := range events {
			if err := jobs.Note(proj, log, e); err != nil {
				fmt.Fprintf(ctx.Stderr, "warning: run log: %v\n", err)
			}
			log = append(log, e)
		}
	}
}

func nextJob(all []jobs.Status) *jobs.Status {
	var next *jobs.Status
	for i := range all {
		s := &all[i]
		if s.Next.IsZero() {
			continue
		}
		if next == nil || s.Next.Before(next.Next) {
			next = s
		}
	}
	return next
}

func runJobRun(ctx *Context, args []string) error {
	fs := newFlagSet("job run", ctx)
	quoteFlag := fs.String("quote", "", "the owner's words asking for this run")
	howFlag := fs.String("how", "", "how the words reached you")
	scheduled := fs.Bool("scheduled", false, "start the due run on the job's standing start (unattended)")
	again := fs.Bool("again", false, "make another run in a period that already has one")
	dryRun := fs.Bool("dry-run", false, "print the run it would create; create and start nothing")
	agentFlag := fs.String("agent", "", "agent to run: claude or codex (default: the workflow's routing: rules, else claude)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli job run <dept>/<job> --quote \"<owner's words>\" | --scheduled"}
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if (quote == "") == !*scheduled {
		return &usageError{msg: "job run takes --quote \"<owner's words>\" (the owner asked for this run) or --scheduled (the job's standing start), not both\n\nExample:\n  issue-cli job run " + fs.Arg(0) + " --quote \"run the SEO pass now\""}
	}
	if *scheduled && *again {
		return &usageError{msg: "--again is for a run the owner asks for (--quote); a scheduled run is one per period"}
	}
	if *agentFlag != "" && *agentFlag != "claude" && *agentFlag != "codex" {
		return &usageError{msg: fmt.Sprintf("--agent must be claude or codex, got %q", *agentFlag)}
	}
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return err
	}
	r, err := jobs.Find(proj, fs.Arg(0))
	if err != nil {
		return err
	}
	if r.Err != nil {
		return fmt.Errorf("job %s: %v (fix it in projects.yaml)", r.Key, r.Err)
	}
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers never start runs: a recurring job is started by its lead or the owner. Ask your lead with a question: comment on your issue")
	}
	if me.Role == tracker.RoleDepartmentLead && !proj.InScope(me.Department, r.Department) {
		return fmt.Errorf("%s is %s's job, not %s's: a department lead starts only its own department's jobs (a group's lead, its departments' too). Ask the CEO %s", r.Key, r.Department, me.Department, tracker.LeadSessionName(proj))
	}
	by := valueOr(me.Name, "owner")
	rs := agentrun.RunStart{Words: quote, By: by, Agent: *agentFlag, Again: *again}
	rs.How = strings.TrimSpace(*howFlag)
	if rs.How == "" {
		rs.How = "owner's shell"
		switch me.Role {
		case tracker.RoleProjectLead:
			rs.How = "CEO chat"
		case tracker.RoleDepartmentLead:
			rs.How = me.Department + " lead chat"
		}
	}
	if !*scheduled {
		in, err := jobInput(ctx, nil)
		if err != nil {
			return err
		}
		s := jobs.Evaluate1(proj, r, jobs.Runs(proj, issues, r.Key), in)
		return startJobRun(ctx, r, issues, s.Period, rs, *dryRun)
	}
	return startScheduledRun(ctx, r, issues, rs, *dryRun)
}

// startScheduledRun starts r's due run on its standing start, deciding
// again under auto-start's machine-wide lock so two leads ticking in the
// same minute start one worker between them.
func startScheduledRun(ctx *Context, r *jobs.Ref, issues []*tracker.Issue, rs agentrun.RunStart, dryRun bool) error {
	proj := ctx.Project
	decideNow := func(last *autostart.Start) (jobs.Status, error) {
		in, err := jobInput(ctx, last)
		if err != nil {
			return jobs.Status{}, err
		}
		return jobs.Evaluate1(proj, r, jobs.Runs(proj, issues, r.Key), in), nil
	}
	refuse := func(s jobs.Status) error {
		if s.Due && s.Verdict == jobs.VerdictHold {
			noteDue(ctx, proj, []jobs.Status{s})
		}
		switch {
		case !s.Due:
			return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not started: %s is not due (last run %s, next %s). Nothing was started.", r.Key, valueOr(s.LastPeriod, "none"), whenLabel(s.Next, ctx.now()))}
		case s.Verdict == jobs.VerdictOwner:
			return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not started: %s has no standing start, so its run waits for the owner (Needs you: %s). Nothing was started.\n\nThe owner may turn one on:\n  %s",
				r.Key, needsYouURL(proj), tracker.InjectProject(proj, fmt.Sprintf("issue-cli job auto on %s --quote \"<owner's words>\"", r.Key)))}
		}
		return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not started: hold: %s. Nothing was started.", s.Reason)}
	}
	if dryRun {
		s, err := decideNow(nil)
		if err != nil {
			return err
		}
		if s.Verdict != jobs.VerdictStart {
			return refuse(s)
		}
		auto := *s.Auto
		rs.Words, rs.Auto, rs.Memory = auto.Words, &auto, s.Memory
		return startJobRun(ctx, r, issues, s.Period, rs, true)
	}
	err := autostart.WithBudget(autostart.SharedDir(), func(last *autostart.Start) (*autostart.Start, error) {
		s, err := decideNow(last)
		if err != nil {
			return nil, err
		}
		if s.Verdict != jobs.VerdictStart {
			return nil, refuse(s)
		}
		auto := *s.Auto
		rs.Words, rs.Auto, rs.Memory = auto.Words, &auto, s.Memory
		res, err := runAndReport(ctx, r, issues, s.Period, rs)
		if res.Issue == nil || res.Response.Status != "dispatched" {
			return nil, err
		}
		return &autostart.Start{Project: proj.Slug, Slug: res.Issue.Slug, At: ctx.now(), Memory: s.Memory, By: rs.By}, err
	})
	if errors.Is(err, autostart.ErrBusy) {
		return &exitCodeError{Code: 1, Msg: "✗ Not started: hold: " + err.Error() + ". Nothing was started."}
	}
	return err
}

// startJobRun creates and starts r's run for period, or with dryRun prints
// what it would create.
func startJobRun(ctx *Context, r *jobs.Ref, issues []*tracker.Issue, period string, rs agentrun.RunStart, dryRun bool) error {
	proj := ctx.Project
	if dryRun {
		n, err := jobs.PlanRun(proj, r, jobs.Runs(proj, issues, r.Key), period, rs.Again)
		if err != nil {
			return runExists(ctx, r, err)
		}
		if _, err := jobs.Brief(proj, r, n, ctx.now()); err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "Would create %s/%s (%q) at %s in %s, and dispatch it.\n", strings.ToLower(n.System), n.Slug, n.Title, n.Status, r.Department)
		fmt.Fprintf(ctx.Stdout, "  file: %s\n", n.File)
		fmt.Fprintf(ctx.Stdout, "  comment: %s\n", agentrun.RunComment(r.Key, period, rs, ctx.now()))
		fmt.Fprintln(ctx.Stdout, "Dry run: nothing was created or started.")
		return nil
	}
	_, err := runAndReport(ctx, r, issues, period, rs)
	return err
}

func runExists(ctx *Context, r *jobs.Ref, err error) error {
	var exists *jobs.ExistsError
	if errors.As(err, &exists) {
		return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not started: %s. Nothing was created.\n\nIts worker:      %s\nAnother run now: %s",
			exists.Error(),
			tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli dispatch %s --quote \"<owner's words>\"", exists.Slug)),
			tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli job run %s --again --quote \"<owner's words>\"", r.Key)))}
	}
	return err
}

// runAndReport creates the run, dispatches it, prints the outcome as
// dispatch does (the dispatch: comment included) and logs the start.
func runAndReport(ctx *Context, r *jobs.Ref, issues []*tracker.Issue, period string, rs agentrun.RunStart) (agentrun.RunResult, error) {
	proj := ctx.Project
	now := ctx.now()
	res, err := agentrun.StartRun(proj, r, issues, period, rs, projectRoot(proj), now, startAgent)
	if err != nil {
		return res, runExists(ctx, r, err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Created run %s (%s) at %s\n", res.Issue.Slug, period, res.Issue.Status)
	fmt.Fprintf(ctx.Stdout, "file: %s\n", res.Issue.FilePath)
	if res.Response.Status == "waiting-on-gate" {
		return res, &exitCodeError{Code: exitWaitingOnGate, Msg: fmt.Sprintf("✗ Not dispatched: the run waits on the owner's approval for %q", res.Response.Gate)}
	}
	rerr := reportDispatch(ctx, res.Issue, res.Response, res.Comment)
	if res.Response.Status == "dispatched" {
		if err := agentrun.RecordRun(proj, r.Key, res, rs, now); err != nil {
			fmt.Fprintf(ctx.Stderr, "warning: run log: %v\n", err)
		}
	}
	return res, rerr
}

// jobAutoNotWorker refuses a worker: the standing start is the owner's.
func jobAutoNotWorker(ctx *Context, what string) (Identity, error) {
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return Identity{}, err
	}
	me := whoAmI(ctx.Project, issues)
	if me.Role == tracker.RoleWorker {
		return me, fmt.Errorf("workers never %s: a job's standing start is the owner's switch. Ask your lead on your issue trail with a question: comment", what)
	}
	return me, nil
}

func runJobAuto(ctx *Context, args []string) error {
	usage := &usageError{msg: "usage: issue-cli job auto on <dept>/<job>|--all --quote \"<owner's words>\" | auto off <dept>/<job>|--all"}
	if len(args) == 0 || (args[0] != "on" && args[0] != "off") {
		if _, err := parsePositionals(ctx, "job auto", args, -1); err != nil {
			return err
		}
		return usage
	}
	on := args[0] == "on"
	fs := newFlagSet("job auto "+args[0], ctx)
	all := fs.Bool("all", false, "every job of the project")
	var quoteFlag, howFlag, reasonFlag *string
	if on {
		quoteFlag = fs.String("quote", "", "the owner's words turning the standing start on (required)")
		howFlag = fs.String("how", "", "how the words reached you (default: \"in the lead chat\")")
	} else {
		reasonFlag = fs.String("reason", "", "why (default: \"turned off\")")
	}
	if err := parseFlagsArgs(ctx, fs, args[1:]); err != nil {
		return err
	}
	if *all == (fs.NArg() == 1) || fs.NArg() > 1 {
		return &usageError{msg: "job auto " + args[0] + " takes one <dept>/<job> or --all"}
	}
	proj := ctx.Project
	key, label := jobs.AllJobs, "every job"
	if !*all {
		r, err := jobs.Find(proj, fs.Arg(0))
		if err != nil {
			return err
		}
		key, label = strings.ToLower(r.Key), r.Key
	}
	verb := "turn a job's standing start off"
	if on {
		verb = "turn a job's standing start on"
	}
	me, err := jobAutoNotWorker(ctx, verb)
	if err != nil {
		return err
	}
	by := valueOr(me.Name, "owner")
	root := yoloRoot(proj)
	if !on {
		reason := strings.TrimSpace(*reasonFlag)
		if reason == "" {
			reason = "turned off"
		}
		reason += " by " + by
		was, err := jobs.TurnOff(root, proj.Slug, key, ctx.now(), reason)
		if err != nil {
			return err
		}
		if !was {
			fmt.Fprintf(ctx.Stdout, "No standing start for %s: nothing to turn off.\n", label)
			return nil
		}
		fmt.Fprintf(ctx.Stdout, "✓ Standing start off for %s (%s). Its due runs wait for the owner again.\n", label, reason)
		return nil
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if quote == "" {
		return &usageError{msg: "job auto on needs --quote: the owner's words turning it on\n\nExample:\n  issue-cli job auto on --all --quote \"for now all\""}
	}
	how := strings.TrimSpace(*howFlag)
	if how == "" {
		how = "in the lead chat"
	}
	if err := jobs.TurnOn(root, proj.Slug, key, jobs.Auto{Words: quote, By: by, How: how, Since: ctx.now()}); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Standing start on for %s: a due run may start without the owner naming it\n", label)
	fmt.Fprintf(ctx.Stdout, "  owner's words: %q (%s)\n", quote, how)
	fmt.Fprintf(ctx.Stdout, "  limits: memory under %d%% · at most %d workers · one start per %d min across all projects (auto-start's)\n",
		jobs.DefaultLimits.Below, jobs.DefaultLimits.MaxWorkers, int(jobs.DefaultLimits.Gap.Minutes()))
	fmt.Fprintln(ctx.Stdout, "  at each check-in: "+tracker.InjectProject(proj, "issue-cli job due"))
	return nil
}

// jobsLine is the one-line "Jobs:" summary lead whoami and status print for
// the jobs in the session's scope; "" when there are none.
func jobsLine(ctx *Context, me Identity, issues []*tracker.Issue) string {
	proj := ctx.Project
	if len(jobs.List(proj)) == 0 {
		return ""
	}
	in, err := jobInput(ctx, nil)
	if err != nil {
		return ""
	}
	scoped := jobsInScope(proj, me, jobs.Evaluate(proj, issues, in))
	if len(scoped) == 0 {
		return ""
	}
	return "Jobs: " + jobsSummary(scoped) + ". At each check-in: " + tracker.InjectProject(proj, "issue-cli job due")
}

// jobsSummary is "3 jobs, 1 due (1 waits for the owner), 1 held".
func jobsSummary(all []jobs.Status) string {
	var start, owner, hold, broken int
	for _, s := range all {
		switch s.Verdict {
		case jobs.VerdictStart:
			start++
		case jobs.VerdictOwner:
			owner++
		case jobs.VerdictHold:
			hold++
		case jobs.VerdictError:
			broken++
		}
	}
	parts := []string{fmt.Sprintf("%d jobs", len(all))}
	if n := start + owner + hold; n > 0 {
		due := fmt.Sprintf("%d due", n)
		var why []string
		if start > 0 {
			why = append(why, fmt.Sprintf("%d to start", start))
		}
		if owner > 0 {
			why = append(why, fmt.Sprintf("%d waits for the owner", owner))
		}
		if hold > 0 {
			why = append(why, fmt.Sprintf("%d held", hold))
		}
		parts = append(parts, due+" ("+strings.Join(why, ", ")+")")
	} else {
		parts = append(parts, "none due")
	}
	if broken > 0 {
		parts = append(parts, fmt.Sprintf("%d broken", broken))
	}
	return strings.Join(parts, ", ")
}

// printDepartmentJobs is department show's Jobs block.
func printDepartmentJobs(ctx *Context, w io.Writer, dept string) {
	proj := ctx.Project
	d := proj.DepartmentByKey(dept)
	if d == nil {
		return
	}
	if len(d.Jobs) == 0 {
		fmt.Fprintf(w, "Jobs: none (add jobs: to %s in projects.yaml; issue-cli help jobs)\n", d.Name)
		return
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return
	}
	in, err := jobInput(ctx, nil)
	if err != nil {
		return
	}
	var rows []jobRow
	for _, s := range jobs.Evaluate(proj, issues, in) {
		if s.Department == d.Name {
			rows = append(rows, jobRow{Status: s})
		}
	}
	fmt.Fprintln(w, "Jobs:")
	for _, r := range rows {
		if r.Error != "" {
			fmt.Fprintf(w, "  %s — ! %s\n", r.Key, r.Error)
			continue
		}
		last := "no run yet"
		if r.LastSlug != "" {
			last = fmt.Sprintf("last %s %s", r.LastPeriod, r.LastStatus)
		}
		auto := "standing start off"
		if r.Auto != nil {
			auto = "standing start on"
		}
		fmt.Fprintf(w, "  %s — %s · next %s · %s · %s\n", r.Key, r.Schedule, whenLabel(r.Next, in.Now), last, auto)
	}
	fmt.Fprintln(w, "  "+tracker.InjectProject(proj, "issue-cli jobs "+tracker.DepartmentKey(d.Name)))
}
