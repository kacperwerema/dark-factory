package main

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

var autostartCommand = &Command{
	Name:      "autostart",
	ShortHelp: "Auto-start: the owner's standing start, one worker at a time while memory is low (sub: status|on|pause|resume|off|next)",
	LongHelp: `Auto-start: for a project where the owner turned it on with their words,
the lead starts one more worker while memory is low, picked from the
project's next order (unclaimed backlog, by priority). Off by default.
Nothing runs on a timer: at each check-in the lead runs autostart next,
and when it says start, the dispatch --autostart command it prints. Every
auto-started worker's dispatch: comment quotes the owner's stored words and
the memory reading.

The start budget is shared by every project on this machine: one start per
gap (default 5 min) across all leads, under a lock. Memory comes from
mem-guard (used % and check); mem-guard check stays the hard stop.

Subcommands:
  status
      On, paused or off, the owner's words, the limits, the last start on
      this machine and the verdict now.
  on --quote "<owner's words>" [--how ...] [--below 50] [--max-workers 2]
     [--gap 5]
      Turn auto-start on for this project. Only the owner or the project
      lead (with the owner's words); never a worker.
  pause [--reason "..."]  /  resume
      Hold starts without losing the settings, and lift the hold.
  off [--reason "..."]
      Turn auto-start off; the window is logged.
  next
      Read-only verdict: start <slug> with its dispatch command, or hold
      with the reason. Exit 0 either way.

See docs/agent-dispatch.md, "Auto-start".`,
	Run:         runAutostart,
	Subcommands: []string{"status", "on", "pause", "resume", "off", "next"},
}

func init() {
	registerCommand(autostartCommand)
}

func runAutostart(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("autostart needs a project — pass --project <slug> or run from a project root")
	}
	if len(args) == 0 {
		return runAutostartStatus(ctx, nil)
	}
	switch args[0] {
	case "status":
		return runAutostartStatus(ctx, args[1:])
	case "on":
		return runAutostartOn(ctx, args[1:])
	case "pause":
		return runAutostartPause(ctx, args[1:])
	case "resume":
		return runAutostartResume(ctx, args[1:])
	case "off":
		return runAutostartOff(ctx, args[1:])
	case "next":
		return runAutostartNext(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown autostart subcommand %q (status, on, pause, resume, off, next)", args[0])}
}

// autostartNotWorker refuses a worker: auto-start is the owner's switch,
// relayed by the lead.
func autostartNotWorker(ctx *Context, what string) (who string, err error) {
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return "", err
	}
	me := whoAmI(ctx.Project, issues)
	if me.Role == tracker.RoleWorker {
		return "", fmt.Errorf("workers never %s: auto-start is the owner's switch. Ask your lead on your issue trail with a question: comment", what)
	}
	return valueOr(me.Name, "owner"), nil
}

// autostartVerdict decides for the project now. last is read from the
// shared budget (nil reads it here); only limits the pick to one slug.
func autostartVerdict(ctx *Context, issues []*tracker.Issue, last *autostart.Start, only string) (autostart.Verdict, *autostart.State, error) {
	proj := ctx.Project
	now := ctx.now()
	if last == nil {
		last, _ = autostart.LastStart(autostart.SharedDir())
	}
	_, yoloOn, _ := yolo.Active(yoloRoot(proj), proj.Slug, now)
	return agentrun.AutostartEvaluate(proj, issues, yoloRoot(proj), projectRoot(proj), yoloOn, last, only, now)
}

// autostartLine is the one-line "Auto-start:" summary status, start and
// whoami print; "" when it is off.
func autostartLine(ctx *Context) string {
	st, err := autostart.Load(yoloRoot(ctx.Project), ctx.Project.Slug)
	if err != nil || !st.On {
		return ""
	}
	if st.Paused {
		return fmt.Sprintf("Auto-start: paused (on below %d%%, %s, cap %d). %s", st.Below, loadLimitWords(st), st.MaxWorkers, tracker.InjectProject(ctx.Project, "issue-cli autostart resume"))
	}
	return fmt.Sprintf("Auto-start: on below %d%% memory and %s, cap %d workers (owner: %q). At each check-in: %s", st.Below, loadLimitWords(st), st.MaxWorkers, st.Words, tracker.InjectProject(ctx.Project, "issue-cli autostart next"))
}

// loadLimitWords is "load under 10.5" or "load under the core count (14)".
func loadLimitWords(st *autostart.State) string {
	if st.MaxLoad > 0 {
		return "load under " + autostart.FormatLimit(st.MaxLoad)
	}
	return fmt.Sprintf("load under the core count (%d)", runtime.NumCPU())
}

type autostartStatusJSON struct {
	State   *autostart.State  `json:"state"`
	Last    *autostart.Start  `json:"last,omitempty"`
	Verdict autostart.Verdict `json:"verdict"`
}

func runAutostartStatus(ctx *Context, args []string) error {
	fs := newFlagSet("autostart status", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	last, _ := autostart.LastStart(autostart.SharedDir())
	v, st, err := autostartVerdict(ctx, issues, last, "")
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, autostartStatusJSON{State: st, Last: last, Verdict: v})
	}
	w := ctx.Stdout
	switch {
	case !st.On:
		fmt.Fprintln(w, "Auto-start: off")
		if st.EndReason != "" {
			fmt.Fprintf(w, "  last window: %s → %s (%s)\n", st.Since.Format("Mon 15:04"), st.Ended.Format("Mon 15:04"), st.EndReason)
		}
		fmt.Fprintln(w, "  the owner turns it on: "+tracker.InjectProject(ctx.Project, `issue-cli autostart on --quote "<owner's words>"`))
		return nil
	case st.Paused:
		fmt.Fprintf(w, "Auto-start: paused since %s", st.PausedAt.Format("Mon 15:04"))
		if st.PauseReason != "" {
			fmt.Fprintf(w, " (%s)", st.PauseReason)
		}
		fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "Auto-start: on since %s\n", st.Since.Format("Mon 15:04"))
	}
	fmt.Fprintf(w, "  turned on by %s (%s): %q\n", st.By, st.How, st.Words)
	fmt.Fprintf(w, "  limits: memory under %d%% · %s · at most %d workers · one start per %d min across all projects\n", st.Below, loadLimitWords(st), st.MaxWorkers, st.GapMinutes)
	if last != nil {
		fmt.Fprintf(w, "  last start on this machine: %s %s at %s (memory %d%%)\n", last.Project, last.Slug, last.At.Format("Mon 15:04"), last.Memory)
	}
	fmt.Fprintln(w, "  now: "+verdictLine(v))
	return nil
}

// verdictLine is "start <slug> (memory 31% < 50%, load 4.3 < 14 on 14
// cores, 1/2 workers)" or "hold: <reason>".
func verdictLine(v autostart.Verdict) string {
	if v.Start {
		return fmt.Sprintf("start %s (memory %d%% < %d%%, %s, %d/%d workers)", v.Slug, v.Memory, v.Below, v.LoadPart(), v.Workers, v.Cap)
	}
	return "hold: " + v.Reason
}

func runAutostartOn(ctx *Context, args []string) error {
	fs := newFlagSet("autostart on", ctx)
	quoteFlag := fs.String("quote", "", "the owner's words turning auto-start on (required)")
	howFlag := fs.String("how", "", "how the words reached you (default: \"in the lead chat\")")
	below := fs.Int("below", autostart.DefaultBelow, "start only while memory used is under this %")
	maxWorkers := fs.Int("max-workers", autostart.DefaultMaxWorkers, "at most this many live workers in the project")
	gap := fs.Int("gap", autostart.DefaultGapMinutes, "minutes between two starts, across all projects")
	maxLoad := fs.Float64("max-load", 0, "start only while the 1-minute load average is under this (default: the core count)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if quote == "" {
		return &usageError{msg: "autostart on needs --quote: the owner's words turning it on\n\nExample:\n  issue-cli autostart on --quote \"start a new job when memory is under 50%\""}
	}
	if *below <= 0 || *below > 100 || *maxWorkers <= 0 || *gap <= 0 || *maxLoad < 0 {
		return &usageError{msg: "autostart on: --below is 1–100, --max-workers and --gap are at least 1, --max-load is not negative"}
	}
	by, err := autostartNotWorker(ctx, "turn auto-start on")
	if err != nil {
		return err
	}
	how := strings.TrimSpace(*howFlag)
	if how == "" {
		how = "in the lead chat"
	}
	st := autostart.State{By: by, Words: quote, How: how, Below: *below, MaxWorkers: *maxWorkers, GapMinutes: *gap, MaxLoad: *maxLoad}
	if err := autostart.TurnOn(yoloRoot(ctx.Project), ctx.Project.Slug, st, ctx.now()); err != nil {
		if strings.Contains(err.Error(), "already on") {
			return fmt.Errorf("auto-start is already on. Turn it off first to change it:\n  %s", tracker.InjectProject(ctx.Project, "issue-cli autostart off"))
		}
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Auto-start on: memory under %d%%, %s, at most %d workers, one start per %d min across all projects\n", *below, loadLimitWords(&st), *maxWorkers, *gap)
	fmt.Fprintf(ctx.Stdout, "  owner's words: %q (%s)\n", quote, how)
	fmt.Fprintln(ctx.Stdout, "  at each check-in: "+tracker.InjectProject(ctx.Project, "issue-cli autostart next"))
	return nil
}

func runAutostartPause(ctx *Context, args []string) error {
	fs := newFlagSet("autostart pause", ctx)
	reason := fs.String("reason", "", "why")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	by, err := autostartNotWorker(ctx, "pause auto-start")
	if err != nil {
		return err
	}
	r := strings.TrimSpace(*reason)
	if r == "" {
		r = "paused by " + by
	}
	if _, err := autostart.Pause(yoloRoot(ctx.Project), ctx.Project.Slug, r, ctx.now()); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Auto-start paused (%s). Resume: %s\n", r, tracker.InjectProject(ctx.Project, "issue-cli autostart resume"))
	return nil
}

func runAutostartResume(ctx *Context, args []string) error {
	fs := newFlagSet("autostart resume", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if _, err := autostartNotWorker(ctx, "resume auto-start"); err != nil {
		return err
	}
	if _, err := autostart.Resume(yoloRoot(ctx.Project), ctx.Project.Slug); err != nil {
		return err
	}
	fmt.Fprintln(ctx.Stdout, "✓ Auto-start resumed")
	return nil
}

func runAutostartOff(ctx *Context, args []string) error {
	fs := newFlagSet("autostart off", ctx)
	reasonFlag := fs.String("reason", "", "why (default: \"turned off\")")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	by, err := autostartNotWorker(ctx, "turn auto-start off")
	if err != nil {
		return err
	}
	root := yoloRoot(ctx.Project)
	st, err := autostart.Load(root, ctx.Project.Slug)
	if err != nil {
		return err
	}
	if !st.On {
		fmt.Fprintln(ctx.Stdout, "Auto-start is already off")
		return nil
	}
	reason := strings.TrimSpace(*reasonFlag)
	if reason == "" {
		reason = "turned off"
	}
	reason += " by " + by
	if err := autostart.TurnOff(root, ctx.Project.Slug, st, ctx.now(), reason); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Auto-start off (%s). Workers start only when the owner names them again.\n", reason)
	return nil
}

func runAutostartNext(ctx *Context, args []string) error {
	fs := newFlagSet("autostart next", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	v, _, err := autostartVerdict(ctx, issues, nil, "")
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, v)
	}
	fmt.Fprintln(ctx.Stdout, verdictLine(v))
	if v.Start {
		fmt.Fprintln(ctx.Stdout, "  "+tracker.InjectProject(ctx.Project, "issue-cli dispatch "+v.Slug+" --autostart"))
	}
	return nil
}

// autostartComment is the dispatch: line of an auto-started worker: the
// owner's stored words, who turned it on and when, and the reading.
func autostartComment(st *autostart.State, v autostart.Verdict, now time.Time) string {
	how := fmt.Sprintf("auto-start, turned on by %s %s %s; memory %d%% < %d%%, %s, %d/%d workers",
		st.By, st.How, st.Since.Format("2006-01-02"), v.Memory, v.Below, v.LoadPart(), v.Workers, v.Cap)
	return tracker.DispatchComment(st.Words, how, now.Format("2006-01-02"))
}
