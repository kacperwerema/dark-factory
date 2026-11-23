package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/leadchat"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/ownerstep"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const leadFlushUsage = `usage: issue-cli lead flush <step> --run <lead-flush run slug>

The nightly lead-flush job's steps, run by that job's worker (brief:
builtin:lead-flush). Nothing here sends or decides anything; close starts
only the lead it closed, again.

  pack                 the previous note, the commands that show the state,
                       the last 24 hours of lead chat; reads only
  note --file <f>      write sessions/<date>-lead-<project>.md: 80 lines at
                       most, with ## Next, Rules in force, Released and
                       installed only (the brief renders workers, queue and
                       owner asks live); no secret
  summary --file <f>   write sessions/<date>-lead-<project>.summary.md: 30
                       lines at most; no secret
  close                close the lead's session, once both files exist and
                       the lead isn't in the middle of a turn, stop its old
                       tick and start the lead again under the same name;
                       then rotate each department and group lead that ran
                       20 h or more (as lead rotate --lead <it>)

<date> is the run's created day. The fresh lead's first prompt carries the
newest note, its summary, the memory pins, its state file and the tick to
start again.`

// flushChatLines is how much of the pane a lead with no transcript gives
// the pack; flushChatSince how far back the chat goes.
const (
	flushChatLines = 1500
	flushChatSince = 24 * time.Hour
	// flushBusyLines is how far up from the pane's foot the working line
	// is looked for: a quote of it higher up is chat.
	flushBusyLines = 12
)

// closeLeadSession ends the lead's session; a var for the tests' stand-in.
var closeLeadSession = agentrun.KillTmux

func runLeadFlush(ctx *Context, args []string) error {
	fs := newFlagSet("lead flush", ctx)
	runFlag := fs.String("run", "", "the lead-flush job's run issue")
	file := fs.String("file", "", "note, summary: the file to write from")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	step := fs.Arg(0)
	switch {
	case fs.NArg() == 0:
		return &usageError{msg: leadFlushUsage}
	case fs.NArg() > 1:
		return unexpectedArgError(fs, fs.Arg(1))
	case step != "pack" && step != "note" && step != "summary" && step != "close":
		return &usageError{msg: fmt.Sprintf("unknown lead flush step %q\n\n%s", step, leadFlushUsage)}
	}
	run, err := flushRun(ctx, strings.TrimSpace(*runFlag))
	if err != nil {
		return err
	}
	day := ctx.now()
	if d, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(run.Created), day.Location()); err == nil {
		day = d
	}
	f := flush{ctx: ctx, proj: ctx.Project, lead: tracker.LeadSessionName(ctx.Project), day: day}
	f.dir = filepath.Join(projectRoot(ctx.Project), "sessions")
	switch step {
	case "pack":
		return f.pack(run)
	case "close":
		return f.close()
	case "note":
		return f.write(*file, tracker.HandoffNoteName(day, f.lead), tracker.CheckHandoffNote)
	}
	return f.write(*file, tracker.HandoffSummaryName(day, f.lead), tracker.CheckHandoffSummary)
}

// flushRun is the lead-flush job's run slug names. The flush's steps close a
// lead and write its note, so only that run's worker takes them (or the
// owner's shell); bench nightly's --run asks the same.
func flushRun(ctx *Context, slug string) (*tracker.Issue, error) {
	proj := ctx.Project
	if proj == nil {
		return nil, fmt.Errorf("lead flush needs a project: pass --project <slug> or run from a project root")
	}
	if slug == "" {
		return nil, &usageError{msg: "lead flush needs --run <the lead-flush job's run>\n\n" + leadFlushUsage}
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		return nil, err
	}
	var run *tracker.Issue
	for _, is := range issues {
		if strings.EqualFold(is.Slug, slug) {
			run = is
		}
	}
	if run == nil {
		return nil, fmt.Errorf("no issue %s in %s", slug, proj.Slug)
	}
	if me := whoAmI(proj, issues); me.Role == tracker.RoleWorker && !strings.EqualFold(me.Slug, run.Slug) {
		return nil, fmt.Errorf("%s is not your run: a worker flushes only on its own run (yours: %s)", run.Slug, valueOr(me.Slug, "unknown"))
	}
	if err := checkLeadFlushRun(proj, run); err != nil {
		return nil, err
	}
	return run, nil
}

// checkLeadFlushRun is why issue is not a run of a job whose brief is the
// bundled lead flush, or nil.
func checkLeadFlushRun(proj *tracker.Project, issue *tracker.Issue) error {
	key := issue.Field("job")
	if key == "" {
		return fmt.Errorf("%s is not a recurring job's run (no job: field)", issue.Slug)
	}
	r, err := jobs.Find(proj, key)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Job.Brief) != jobs.LeadFlushBrief {
		return fmt.Errorf("%s is a run of %s, whose brief is %s, not %s", issue.Slug, r.Key, valueOr(r.Job.Brief, "none"), jobs.LeadFlushBrief)
	}
	return nil
}

// flush is one run's step.
type flush struct {
	ctx  *Context
	proj *tracker.Project
	lead string
	day  time.Time
	dir  string
}

// write checks the file and puts it in sessions/ whole, or leaves what is
// there alone.
func (f flush) write(from, name string, check func(string) error) error {
	if from == "" {
		return &usageError{msg: "this step needs --file <path>\n\n" + leadFlushUsage}
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	text := string(data)
	if err := check(text); err != nil {
		return fmt.Errorf("%w. Nothing was written", err)
	}
	if what := memory.FindSecret(text); what != "" {
		return fmt.Errorf("%s looks like a secret (%s): a note goes into the next lead's first prompt, so take it out. Nothing was written", name, what)
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(f.dir, name)
	if err := gitfile.WriteAtomic(dest, []byte(strings.TrimSpace(text)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(f.ctx.Stdout, "✓ Wrote %s\n", dest)
	return nil
}

func (f flush) pack(run *tracker.Issue) error {
	w := f.ctx.Stdout
	fmt.Fprintf(w, "== Lead flush %s: %s ==\n", f.day.Format("2006-01-02"), f.lead)
	fmt.Fprintln(w, "Note goes to:    "+filepath.Join(f.dir, tracker.HandoffNoteName(f.day, f.lead)))
	fmt.Fprintln(w, "Summary goes to: "+filepath.Join(f.dir, tracker.HandoffSummaryName(f.day, f.lead)))
	fmt.Fprintln(w, "\n== Previous note ==")
	if note := tracker.NewestHandoffNote(f.dir, f.lead); note != "" {
		fmt.Fprintln(w, "Read "+note)
	} else {
		fmt.Fprintln(w, "None yet.")
	}
	fmt.Fprintln(w, "\n== The state ==")
	for _, c := range []string{"issue-cli status", "issue-cli queue list", "issue-cli ci status", "issue-cli memory list --pinned", "issue-cli memory list --scope lead", "issue-cli decisions --lead ceo"} {
		fmt.Fprintln(w, "  "+tracker.InjectProject(f.proj, c))
	}
	fmt.Fprintln(w, "\n== Open owner steps (the state file has them: don't copy them into the note) ==")
	if steps, err := ownerstep.LoadOpen(f.proj.IssueDir, f.ctx.now()); err != nil {
		fmt.Fprintln(w, "Unreadable: "+err.Error())
	} else if len(steps) == 0 {
		fmt.Fprintln(w, "None open.")
	} else {
		for _, l := range ownerstep.Lines(steps, f.ctx.now()) {
			fmt.Fprintln(w, "- step "+l)
		}
		fmt.Fprintln(w, "Their exact text: "+tracker.InjectProject(f.proj, "issue-cli owner-step list"))
	}
	source, lines := f.chat()
	fmt.Fprintf(w, "\n== Lead chat, last %d hours (%s, %d lines) ==\n", int(flushChatSince.Hours()), source, len(lines))
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	if len(lines) == 0 {
		fmt.Fprintln(w, "Nothing said: the summary is \"No lead chat today.\"")
	}
	return nil
}

// transcripts is leadchat's view of this machine's Claude Code sessions:
// bench finds the files, spend reads the agent name each records.
type transcripts struct{}

func (transcripts) Sessions(projectsDir, cwd string) []string {
	var out []string
	for _, folder := range bench.TranscriptFolders(projectsDir, cwd) {
		for top := range bench.SessionFiles(folder) {
			out = append(out, top)
		}
	}
	return out
}

func (transcripts) AgentName(path string) string { return spend.AgentName(path) }

// chat is the day's chat lines and where they came from: the lead's
// transcript, else (a Codex lead, or none on this machine) its pane.
func (f flush) chat() (string, []string) {
	var lines []string
	msgs := leadchat.FromTranscripts(transcripts{}, spend.DefaultProjectsDir(), projectRoot(f.proj), f.lead, f.ctx.now().Add(-flushChatSince))
	for _, m := range msgs {
		lines = append(lines, fmt.Sprintf("%s %s: %s", m.At.Local().Format("15:04"), m.Who, m.Text))
	}
	if len(lines) > 0 {
		return "transcript", lines
	}
	if !agentrun.IsLive(f.proj, f.lead) {
		return "no transcript, no live lead", nil
	}
	out, err := agentrun.CaptureHistory(agentrun.Target(f.proj, f.lead), flushChatLines)
	if err != nil {
		return "no transcript, pane unreadable", nil
	}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, " "); l != "" {
			lines = append(lines, l)
		}
	}
	return "pane", lines
}

func (f flush) close() error {
	var missing []string
	for _, name := range []string{tracker.HandoffNoteName(f.day, f.lead), tracker.HandoffSummaryName(f.day, f.lead)} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s stays open: no %s in %s yet. Write them first (issue-cli lead flush note|summary)", f.lead, strings.Join(missing, ", no "), f.dir)
	}
	if !agentrun.IsLive(f.proj, f.lead) {
		fmt.Fprintf(f.ctx.Stdout, "No lead live: %s has nothing to close.\n", f.lead)
		f.rotateDepartmentLeads()
		return nil
	}
	if agentrun.PaneWorking(agentrun.Target(f.proj, f.lead), flushBusyLines) {
		return fmt.Errorf("%s is in the middle of a turn: it stays open. Try again in ten minutes", f.lead)
	}
	ticks, err := f.handOver()
	if err != nil {
		return fmt.Errorf("%s stays open: %w", f.lead, err)
	}
	if err := closeLeadSession(f.proj, f.lead); err != nil {
		return fmt.Errorf("closing %s: %w", f.lead, err)
	}
	fmt.Fprintf(f.ctx.Stdout, "✓ Closed %s.\n", f.lead)
	f.stopTicks(ticks)
	// The lead comes back by itself: same name, its first prompt carries
	// the note, the summary, the state file and the tick to restart.
	if err := leadFlushSpawn(f.ctx); err != nil {
		return fmt.Errorf("%s is closed but did not start again: %w. Start it from the owner's shell: %s", f.lead, err, tracker.InjectProject(f.proj, "issue-cli lead spawn"))
	}
	f.rotateDepartmentLeads()
	return nil
}

// leadFlushSpawn starts the lead close just closed; a var for the tests.
var leadFlushSpawn = func(ctx *Context) error { return spawnLead(ctx, "", "", false) }

// stopLeadTick ends an old tick's process; a var for the tests.
var stopLeadTick = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// leadTickProcess reports whether pid still runs a lead tick: a watch's pid
// can be another process's by now. A var for the tests.
var leadTickProcess = func(pid int) bool {
	if !pidAlive(pid) {
		return false
	}
	args, err := agentrun.PaneArgs(pid)
	return err == nil && leadstate.IsTick(args)
}

// handOver records what the fresh lead's first prompt carries after the
// close: the note and summary, the state file as it is now and the watches
// to start again, the tick always among them. It returns the old ticks'
// pids, which the close stops: a background shell can outlive its session,
// and the fresh lead starts its own. Without a state dir there is nothing
// to record.
func (f flush) handOver() ([]int, error) {
	if leadstate.Dir() == "" {
		return nil, nil
	}
	issues, err := f.proj.LoadIssues()
	if err != nil {
		return nil, err
	}
	state, err := renderLeadState(f.ctx, f.lead, issues)
	if err != nil {
		return nil, err
	}
	store, err := leadstate.Load(f.lead)
	if err != nil {
		return nil, err
	}
	var ticks []int
	watches := append([]leadstate.Watch(nil), store.Watches...)
	hasTick := false
	for _, w := range watches {
		if leadstate.IsTick(w.Command) {
			hasTick = true
			ticks = append(ticks, w.PID)
		}
	}
	if !hasTick {
		cmd := "issue-cli lead tick --watch --exit-on-news"
		if store.Tick != nil && store.Tick.Command != "" {
			cmd = store.Tick.Command
		}
		watches = append([]leadstate.Watch{{Command: tracker.InjectProject(f.proj, cmd)}}, watches...)
	}
	now := f.ctx.now()
	store.Handover = &leadstate.Handover{
		At: now, By: leadstate.ByFlush,
		Note:   filepath.Join(f.dir, tracker.HandoffNoteName(f.day, f.lead)),
		Digest: filepath.Join(f.dir, tracker.HandoffSummaryName(f.day, f.lead)),
		State:  state, Watches: watches, Since: now,
	}
	return ticks, store.Save(f.lead)
}

// stopTicks ends the old lead's ticks that still run and drops them from
// its state, so the fresh lead's tick is the only one.
func (f flush) stopTicks(pids []int) {
	for _, pid := range pids {
		if pid == os.Getpid() || !leadTickProcess(pid) {
			continue
		}
		if err := stopLeadTick(pid); err != nil {
			fmt.Fprintf(f.ctx.Stdout, "! The old tick (pid %d) still runs: %s\n", pid, err)
			continue
		}
		fmt.Fprintf(f.ctx.Stdout, "✓ Stopped the old tick (pid %d)\n", pid)
	}
	if store, err := leadstate.Load(f.lead); err == nil && len(pids) > 0 {
		for _, pid := range pids {
			store.Unregister(pid)
		}
		_ = store.Save(f.lead)
	}
}

// flushDeptLeadAge is how long a department or group lead runs before the
// nightly flush rotates it: under a day, so a lead the flush rotated
// yesterday is rotated again tonight.
const flushDeptLeadAge = 20 * time.Hour

// leadSessionStarted is when the live session called name started; false
// when it isn't listed. A var for the tests.
var leadSessionStarted = func(proj *tracker.Project, name string) (time.Time, bool) {
	for _, s := range agentrun.ListSessions() {
		if s.Name != name {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04:05", s.StartTime, time.Local)
		return at, err == nil
	}
	return time.Time{}, false
}

// leadFlushRotate rotates one lead (rotateLead); a var for the tests.
var leadFlushRotate = rotateLead

// rotateDepartmentLeads rotates each live department and group lead of the
// project that has run flushDeptLeadAge or longer, the way lead rotate
// does: a handoff note from its state file, then a detached finisher that
// waits for its turn to end, closes it and starts it again with its tick.
// The flush closed only the CEO until 2026-11-21, and one project's
// marketing lead ran 8 days (factory/no-disk-or-memory-leaks-over-a-247-…). A lead
// that can't be rotated is said and left for the next night; it never
// fails the close.
func (f flush) rotateDepartmentLeads() {
	if f.proj == nil || len(f.proj.Departments) == 0 {
		return
	}
	now := f.ctx.now()
	var issues []*tracker.Issue
	for i := range f.proj.Departments {
		lead := tracker.DepartmentLeadName(f.proj, f.proj.Departments[i].Name)
		if lead == f.lead || !agentrun.IsLive(f.proj, lead) {
			continue
		}
		started, ok := leadSessionStarted(f.proj, lead)
		if ok && now.Sub(started) < flushDeptLeadAge {
			continue
		}
		if issues == nil {
			var err error
			if issues, err = f.proj.LoadIssues(); err != nil {
				fmt.Fprintf(f.ctx.Stdout, "! %s not rotated: %v\n", lead, err)
				return
			}
		}
		if err := leadFlushRotate(f.ctx, lead, issues, false); err != nil {
			fmt.Fprintf(f.ctx.Stdout, "! %s not rotated: %v. The next flush tries again; by hand: %s\n", lead, err, tracker.InjectProject(f.proj, "issue-cli lead rotate --lead "+lead))
		}
	}
}
