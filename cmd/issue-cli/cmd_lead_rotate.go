package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/leadchat"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// lead rotate: hand a grown lead over to a fresh session of itself. It
// writes the handoff note from the state file and a digest of the old
// transcript into sessions/, then a detached finisher waits for the old
// session's turn to end, closes it and starts the same lead again; the
// fresh session's first prompt carries the state file and the watches to
// restart (docs/patterns/lead-session.md#rotate).

const (
	// rotateWaitEvery and rotateWaitMax: how often and how long the
	// finisher waits for the old session's turn to end.
	rotateWaitEvery = 5 * time.Second
	rotateWaitMax   = 30 * time.Minute
	// rotateDigestLook is how far back a first rotate's digest reaches.
	rotateDigestLook = 2 * 24 * time.Hour
)

// Seams for tests: the finisher's start, its waits, and the pane check.
var (
	leadRotateStartFinisher = startRotateFinisher
	leadRotateSleep         = time.Sleep
	leadRotateBusy          = func(proj *tracker.Project, lead string) bool {
		return agentrun.PaneWorking(agentrun.Target(proj, lead), flushBusyLines)
	}
	leadRotateSpawn = func(ctx *Context, args []string) error { return runLeadSpawn(ctx, args) }
)

func runLeadRotate(ctx *Context, args []string) error {
	fs := newFlagSet("lead rotate", ctx)
	leadFlag := fs.String("lead", "", "the lead to rotate (default: you as a lead, else the CEO)")
	finish := fs.Bool("finish", false, "the detached step: wait for the turn to end, close the session, start it again (rotate starts it)")
	noStart := fs.Bool("no-start", false, "write the note and the digest only; close and start nothing")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	if proj == nil {
		return fmt.Errorf("lead rotate needs a project: pass --project <slug> or run from a project root")
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	me := guardWho(ctx, issues, "lead rotate")
	if me.Role == tracker.RoleWorker {
		guardNever(ctx, "lead rotate")
		return fmt.Errorf("workers can't rotate a lead (you are %s): post a question: comment on your issue instead", valueOr(me.Name, "a worker"))
	}
	lead := leadStateName(proj, me, *leadFlag)
	if leadstate.Dir() == "" {
		return fmt.Errorf("no state dir: set XDG_STATE_HOME or HOME")
	}
	if *finish {
		return leadRotateFinish(ctx, lead)
	}

	state, err := renderLeadState(ctx, lead, issues)
	if err != nil {
		return err
	}
	store, err := leadstate.Load(lead)
	if err != nil {
		return err
	}
	now := ctx.now()
	dir := filepath.Join(projectRoot(proj), "sessions")
	notePath := rotateNotePath(dir, lead, now)
	note := leadstate.HandoffNote(leadStateView(ctx, lead, issues, now), store, tracker.InjectProject(proj, "issue-cli lead state"))
	if err := tracker.CheckHandoffNote(note); err != nil {
		return fmt.Errorf("the note from the state file: %w. Nothing was written", err)
	}
	if what := memory.FindSecret(note); what != "" {
		return fmt.Errorf("the note would hold a secret (%s): take it out of the state file's notes (lead state done <n>). Nothing was written", what)
	}
	since := now.Add(-rotateDigestLook)
	if h := store.Handover; h != nil && h.Since.After(since) {
		since = h.Since
	}
	digestPath := strings.TrimSuffix(notePath, ".md") + ".digest.md"
	digest := rotateDigest(proj, lead, since, now)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := gitfile.WriteAtomic(notePath, []byte(note), 0o644); err != nil {
		return err
	}
	if err := gitfile.WriteAtomic(digestPath, []byte(digest), 0o644); err != nil {
		return err
	}
	store.Handover = &leadstate.Handover{At: now, Note: notePath, Digest: digestPath, State: state, Watches: store.Watches, Since: now}
	if err := store.Save(lead); err != nil {
		return err
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "✓ Wrote %s\n✓ Wrote %s\n", notePath, digestPath)
	history := tracker.InjectProject(proj, "issue-cli lead history <words>")
	if *noStart {
		fmt.Fprintf(w, "--no-start: %s keeps running. Older context: %s\n", lead, history)
		return nil
	}
	if !agentrun.IsLive(proj, lead) {
		fmt.Fprintf(w, "%s is not live: start it with %s; its first prompt carries the state file for %s.\n", lead, tracker.InjectProject(proj, leadSpawnCommand(proj, lead)), leadstate.HandoverFresh)
		return nil
	}
	if err := leadRotateStartFinisher(proj, lead); err != nil {
		return fmt.Errorf("starting the finisher: %w (the note and digest are written; run %s from your shell)", err, tracker.InjectProject(proj, "issue-cli lead rotate --finish --lead "+lead))
	}
	fmt.Fprintf(w, "Rotating %s after this turn: end your turn now. The fresh session reads the state file first (%d watch(es) to restart); older context: %s\n", lead, len(store.Watches), history)
	return nil
}

// rotateNotePath is sessions/<date>-<lead>.md, or <date>-<lead>.<HHMM>.md
// when the day's note is there already (the nightly flush's, or an earlier
// rotate's): a rotate never overwrites a note.
func rotateNotePath(dir, lead string, now time.Time) string {
	p := filepath.Join(dir, tracker.HandoffNoteName(now, lead))
	if _, err := os.Stat(p); err != nil {
		return p
	}
	base := strings.TrimSuffix(p, ".md")
	for i := 0; ; i++ {
		suffix := now.Local().Format("1504")
		if i > 0 {
			suffix += fmt.Sprintf("-%d", i)
		}
		q := base + "." + suffix + ".md"
		if _, err := os.Stat(q); err != nil {
			return q
		}
	}
}

// rotateDigest is the digest of lead's transcripts since since; lines that
// look like a secret are left out.
func rotateDigest(proj *tracker.Project, lead string, since, now time.Time) string {
	var entries []leadchat.Entry
	src := transcripts{}
	for _, top := range src.Sessions(spend.DefaultProjectsDir(), projectRoot(proj)) {
		st, err := os.Stat(top)
		if err != nil || st.ModTime().Before(since) || src.AgentName(top) != lead {
			continue
		}
		entries = append(entries, leadchat.ReadEntries(top, since)...)
	}
	kept := entries[:0]
	for _, e := range entries {
		if memory.FindSecret(e.Text) == "" {
			kept = append(kept, e)
		}
	}
	d := leadchat.Digest(lead, kept, since, now)
	if len(entries) == 0 {
		d += "\nNo Claude Code transcript of " + lead + " on this machine since then (a Codex lead keeps none): the handoff note and the state file are what is left.\n"
	}
	return d
}

// leadSpawnCommand is the spawn command for lead: the CEO's, or a
// department lead's with --department.
func leadSpawnCommand(proj *tracker.Project, lead string) string {
	if d := proj.DepartmentOfLead(lead); d != nil {
		return "issue-cli lead spawn --department " + tracker.DepartmentKey(d.Name)
	}
	return "issue-cli lead spawn"
}

// startRotateFinisher starts `issue-cli lead rotate --finish` in its own
// session, so it outlives the lead's tmux session it closes; its output
// goes to lead/<lead>.rotate.log in the state dir.
func startRotateFinisher(proj *tracker.Project, lead string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(leadstate.Dir(), lead+".rotate.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	c := exec.Command(exe, "--project", proj.Slug, "lead", "rotate", "--finish", "--lead", lead)
	c.Dir = projectRoot(proj)
	c.Env = os.Environ()
	c.Stdout, c.Stderr = log, log
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// leadRotateFinish waits for lead's turn to end, closes its session and
// starts it again. After rotateWaitMax it gives up and leaves it running.
func leadRotateFinish(ctx *Context, lead string) error {
	proj := ctx.Project
	w := ctx.Stdout
	start := ctx.now()
	if agentrun.IsLive(proj, lead) {
		for waited := time.Duration(0); leadRotateBusy(proj, lead); waited += rotateWaitEvery {
			if waited >= rotateWaitMax {
				fmt.Fprintf(w, "%s %s is still in a turn after %s: it stays open, not rotated. Run %s again when it is idle.\n", start.Format(time.RFC3339), lead, rotateWaitMax, tracker.InjectProject(proj, "issue-cli lead rotate"))
				return fmt.Errorf("%s still busy after %s", lead, rotateWaitMax)
			}
			leadRotateSleep(rotateWaitEvery)
		}
		if err := closeLeadSession(proj, lead); err != nil {
			return fmt.Errorf("closing %s: %w", lead, err)
		}
		fmt.Fprintf(w, "%s closed %s\n", ctx.now().Format(time.RFC3339), lead)
	}
	var spawn []string
	if d := proj.DepartmentOfLead(lead); d != nil {
		spawn = append(spawn, "--department", d.Name)
	}
	// The finisher runs with the old lead's environment; the spawn starts
	// the lead under its own name again.
	if err := leadRotateSpawn(ctx, spawn); err != nil {
		return fmt.Errorf("starting %s again: %w", lead, err)
	}
	return nil
}
