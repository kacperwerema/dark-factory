package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/leaks"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// The lead's tick: what the lead used to run from a scratchpad script every
// 5 minutes under a Monitor (queue next --start, the done branches not on
// main, CI, the sweep, the daily worktree prune), done in one command that prints a line only when the
// lead has to act. Every line it prints wakes the lead for a whole turn, so
// a thing it said is not said again until it goes away and comes back: the
// said keys live in $XDG_STATE_HOME/ai-workflow-engine/lead-tick/<project>.json.

// leadTickMinEvery is the shortest --every.
const leadTickMinEvery = time.Minute

// leadTickSleep waits d between ticks and reports whether to go on; a var
// so tests run a bounded watch with no real sleep.
var leadTickSleep = func(d time.Duration) bool {
	time.Sleep(d)
	return true
}

// leadTickState is what one tick leaves for the next.
type leadTickState struct {
	// Said holds each key the tick said and when it first said it. A key
	// not true at a tick is dropped, so it is said again if it comes back.
	Said map[string]time.Time `json:"said"`
	// Since is when the last tick ran: owner comments after it are new.
	Since time.Time `json:"since"`
	// HeldSince is the first tick of the current run of held starts (no
	// start in between); zero when the last tick started or held nothing.
	HeldSince time.Time `json:"held_since,omitempty"`
	// PrunedOn is the local date (2006-01-02) of the last daily worktree
	// sweep.
	PrunedOn string `json:"pruned_on,omitempty"`
	// LeaksOn is the local date of the last daily doctor leaks look.
	LeaksOn string `json:"leaks_on,omitempty"`
	// ContextOn is the local date the tick last said the lead's context
	// passed leadContextLine.
	ContextOn string `json:"context_on,omitempty"`
	// InstallLagOn is the local date the tick last said the installed
	// issue-cli is behind main (cmd_lead_tick_install.go).
	InstallLagOn string `json:"install_lag_on,omitempty"`
	// LastHold is the key of the last start hold the tick said; a tick
	// where a hold that clears by itself wins quietly keeps it, a start
	// forgets it. The same hold back after such a tick is no news.
	LastHold string `json:"last_hold,omitempty"`
	// LastStart is the last tick that started something; zero before the
	// first (the idle check counts from the first tick then).
	LastStart time.Time `json:"last_start,omitempty"`
	// IdleFrom is when the idle check began counting with no start seen:
	// the first tick after an upgrade or of a new project.
	IdleFrom time.Time `json:"idle_from,omitempty"`
}

// leadTickPath is the project's tick state; "" without a state dir.
func leadTickPath(proj *tracker.Project) string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "lead-tick", proj.Slug+".json")
}

func loadLeadTickState(path string) leadTickState {
	st := leadTickState{}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &st)
		}
	}
	if st.Said == nil {
		st.Said = map[string]time.Time{}
	}
	return st
}

// leadTicker is one tick's bookkeeping: what it says, and the keys still true.
type leadTicker struct {
	w    io.Writer
	prev leadTickState
	seen map[string]time.Time
	now  time.Time
	said int
	// news counts the lines the lead must act on; a held line (a start, a
	// session closed) is said but is no news: --exit-on-news keeps it for
	// the next tick that has some.
	news int
	// watch: a --watch tick, which says nothing of a hold that clears by
	// itself until the start has been held leadstate.HeldLong.
	watch bool
	// heldSince is the next state's HeldSince.
	heldSince time.Time
	// prunedOn is the next state's PrunedOn.
	prunedOn string
	// leaksOn is the next state's LeaksOn.
	leaksOn string
	// contextOn is the next state's ContextOn.
	contextOn string
	// installLagOn is the next state's InstallLagOn.
	installLagOn string
	// lastHold is the next state's LastHold.
	lastHold string
	// lastStart and idleFrom are the next state's LastStart and IdleFrom.
	lastStart, idleFrom time.Time
	// idle is this tick's start hold when it leaves room unused (the
	// queue empty, everything held, idle sessions on the slots); nil
	// when nothing held or the machine or another project did.
	idle *idleReason
}

// say prints line unless key was already said and is still true.
func (t *leadTicker) say(key, line string) {
	if _, ok := t.seen[key]; ok {
		return
	}
	if at, ok := t.prev.Said[key]; ok {
		t.seen[key] = at
		return
	}
	t.seen[key] = t.now
	fmt.Fprintln(t.w, line)
	t.said++
	t.news++
}

// note prints line every time (a leaks finding, the context nudge): news
// by itself.
func (t *leadTicker) note(line string) {
	fmt.Fprintln(t.w, line)
	t.said++
	t.news++
}

// held prints line every time but it is no news: a start, a session
// closed, a status move nobody waits on. The lead has nothing to do about
// it, so --exit-on-news doesn't wake it for one; the line goes out with
// the next news.
func (t *leadTicker) held(line string) {
	fmt.Fprintln(t.w, line)
	t.said++
}

// tickCount is what one tick printed: every line, and the news among them.
type tickCount struct{ said, news int }

func (t *leadTicker) count() tickCount { return tickCount{said: t.said, news: t.news} }

// fail says a step's error once.
func (t *leadTicker) fail(step string, err error) {
	t.say("err:"+step+":"+tickKey(err.Error()), fmt.Sprintf("tick error: %s: %s", step, firstLineOf(err.Error())))
}

var tickDigits = regexp.MustCompile(`[0-9]+(\.[0-9]+)?`)

// tickKey is s with its numbers dropped: "memory 62% ≥ 60%" and
// "memory 63% ≥ 60%" are the same hold.
func tickKey(s string) string { return tickDigits.ReplaceAllString(firstLineOf(s), "#") }

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func runLeadTick(ctx *Context, args []string) error {
	fs := newFlagSet("lead tick", ctx)
	watch := fs.Bool("watch", false, "tick again every --every until stopped; quiet ticks print nothing")
	every := fs.Duration("every", 5*time.Minute, "time between ticks with --watch (at least 1m)")
	exitOnNews := fs.Bool("exit-on-news", false, "with --watch: exit after the first tick with news the lead must act on, printing the starts and closes held since above it (for a background shell that wakes the lead once)")
	verbose := fs.Bool("verbose", false, "with --exit-on-news: exit on any line, a start or a session closed too")
	observe := fs.Bool("observe", false, "read-only tick for a lead that doesn't start work: never starts or closes anything; says its scope's due jobs, status moves, shelf items, comments and ended workers")
	dept := fs.String("department", "", "with --observe: the department to watch (default: your own as a department lead, else the whole project)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if *dept != "" && !*observe {
		return &usageError{msg: "--department goes with --observe"}
	}
	proj := ctx.Project
	if proj == nil {
		return fmt.Errorf("lead tick needs a project — pass --project <slug> or run from a project root")
	}
	if *every < leadTickMinEvery {
		return &usageError{msg: fmt.Sprintf("--every must be at least %s, got %s", leadTickMinEvery, *every)}
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	if me := guardWho(ctx, issues, "lead tick"); me.Role == tracker.RoleWorker {
		guardNever(ctx, "lead tick")
		return fmt.Errorf("workers don't run the lead's tick (you are %s): post a question: comment on your issue instead", valueOr(me.Name, "a worker"))
	}
	var lw *leadShell
	if *watch {
		lw = openLeadWatch(ctx, whoAmI(proj, issues))
		defer lw.done()
	}
	once := func(ctx *Context, watch bool) (tickCount, error) { return leadTickOnce(ctx, watch) }
	if *observe {
		me := whoAmI(proj, issues)
		scope := *dept
		if scope == "" && me.Role == tracker.RoleDepartmentLead {
			scope = me.Department
		}
		if scope != "" {
			d := proj.DepartmentByKey(scope)
			if d == nil {
				return &usageError{msg: fmt.Sprintf("no department %q in this project (%s)", scope, strings.Join(proj.DepartmentKeys(), ", "))}
			}
			scope = d.Name
		}
		once = func(ctx *Context, _ bool) (tickCount, error) { return leadObserveOnce(ctx, scope, me.Name) }
	}
	if !*watch {
		_, err := once(ctx, false)
		return err
	}
	// Under --exit-on-news the ticks write into held: a tick with no news
	// adds its lines there, and the first tick with news prints them all,
	// oldest first, and exits. --verbose exits on any line.
	// A tick an earlier one held and never said (it was killed) goes out
	// first. A signal (cmd_lead_tick_signal.go) says what was held and
	// the stop, then exits.
	var held, tickOut bytes.Buffer
	var mu sync.Mutex
	sub := *ctx
	if *exitOnNews {
		sub.Stdout = &tickOut
		held.WriteString(lw.takeHeld())
	}
	sigs, stopSigs := leadTickSignals()
	defer stopSigs()
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		select {
		case sig := <-sigs:
			mu.Lock()
			defer mu.Unlock()
			if !lw.stopped(signalName(sig)) {
				return
			}
			ctx.Stdout.Write(held.Bytes())
			held.Reset()
			fmt.Fprint(ctx.Stdout, leadTickStopLine(signalName(sig), os.Getpid(), lw.cmd))
			leadTickExit(signalExitCode(sig))
		case <-quit:
		}
	}()
	flush := func() error {
		mu.Lock()
		defer mu.Unlock()
		_, werr := ctx.Stdout.Write(held.Bytes())
		held.Reset()
		return werr
	}
	for {
		tickOut.Reset()
		n, err := once(&sub, true)
		if err != nil {
			fmt.Fprintf(sub.Stdout, "tick error: %s\n", firstLineOf(err.Error()))
			n.said++
			n.news++
		}
		if *exitOnNews {
			mu.Lock()
			holdOnce(&held, tickOut.String())
			mu.Unlock()
		}
		if *exitOnNews && (n.news > 0 || (*verbose && n.said > 0)) {
			return flush()
		}
		if *exitOnNews {
			mu.Lock()
			lw.hold(held.String())
			mu.Unlock()
		}
		if !leadTickSleep(*every) {
			// Stopped with no news: what was held still goes out.
			return flush()
		}
	}
}

// holdOnce adds a tick's lines to held, but a "hold: " line held already
// only once: a hold that comes back on every other tick goes out with the
// next news one time.
func holdOnce(held *bytes.Buffer, out string) {
	for _, line := range strings.SplitAfter(out, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "hold: ") && bytes.Contains(held.Bytes(), []byte(line)) {
			continue
		}
		held.WriteString(line)
	}
}

// leadTickOnce runs one tick and returns how many lines it printed; watch
// is a --watch tick.
func leadTickOnce(ctx *Context, watch bool) (tickCount, error) {
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return tickCount{}, fmt.Errorf("cannot load issues: %w", err)
	}
	path := leadTickPath(proj)
	t := &leadTicker{w: ctx.Stdout, prev: loadLeadTickState(path), seen: map[string]time.Time{}, now: ctx.now(), watch: watch}
	t.lastHold = t.prev.LastHold
	t.lastStart, t.idleFrom = t.prev.LastStart, t.prev.IdleFrom
	if t.idleFrom.IsZero() {
		t.idleFrom = t.now
	}
	first := path == "" || t.prev.Since.IsZero()

	unlanded := leadTickBranches(ctx, t, issues)
	leadTickGreen(ctx, t, issues)
	leadTickSweep(ctx, t, issues, unlanded)
	leadTickYoloMoves(ctx, t, issues)
	leadTickWorktrees(ctx, t, issues, unlanded)
	leadTickCI(ctx, t)
	leadTickLeaks(ctx, t)
	leadTickClean(t)
	leadTickLong(ctx, t)
	leadTickInstallLag(ctx, t)
	leadTickAttention(ctx, t, issues)
	leadTickReports(ctx, t, issues)
	leadTickQuestions(ctx, t, issues)
	leadTickStart(ctx, t, issues)
	if fresh, err := tracker.LoadIssues(proj.IssueDir); err == nil {
		issues = fresh
	}
	leadTickQueueLow(ctx, t, issues)
	leadTickIdle(ctx, t)
	if !first {
		leadTickOwner(ctx, t, issues)
	}
	t.contextOn = leadTickContext(ctx, t, tracker.LeadSessionName(proj), t.prev.ContextOn)
	if _, err := renderLeadState(ctx, tracker.LeadSessionName(proj), issues); err != nil {
		t.fail("state", err)
	}

	if path != "" {
		next := leadTickState{Said: t.seen, Since: t.now, HeldSince: t.heldSince, PrunedOn: t.prunedOn, LeaksOn: t.leaksOn, ContextOn: t.contextOn, InstallLagOn: t.installLagOn, LastHold: t.lastHold, LastStart: t.lastStart, IdleFrom: t.idleFrom}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			if err := jsonfile.Write(path, next, 0o644); err != nil {
				fmt.Fprintf(ctx.Stderr, "warning: saving the tick state: %v\n", err)
			}
		}
	}
	return t.count(), nil
}

// leadTickStart is queue next --start (a due job on its standing start
// first) with its output captured: a start is said, a hold only when its
// reason is new. Under --watch a hold that clears by itself (the machine,
// the gap, the worker cap, another project's turn) is said only once the
// start has been held leadstate.HeldLong: until then the lead has nothing
// to do, and every line wakes it for a whole turn.
func leadTickStart(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	var out, errOut bytes.Buffer
	sub := *ctx
	sub.Stdout, sub.Stderr, sub.JSONOutput = &out, &errOut, false
	err := queueStart(&sub, issues, "")
	// A memory hold or the worker cap: close the idle sessions holding
	// memory or a slot for nothing (issue done, or waiting on the owner),
	// say so in one line and ask the queue once more.
	if h := (*exitCodeError)(nil); errors.As(err, &h) && h.Hold && (isMemoryHold(h.HoldKind) || h.HoldKind == autostart.HoldWorkers) {
		under := "the memory hold"
		if h.HoldKind == autostart.HoldWorkers {
			under = "the worker cap"
		}
		if line, closed := leadTickFree(ctx, issues, under); line != "" {
			if strings.Contains(line, "not closed ") {
				t.note(line) // a session that won't close is the lead's
			} else {
				t.held(line)
			}
			if closed {
				leadTickPause(leadTickSettle)
				if fresh, lerr := tracker.LoadIssues(ctx.Project.IssueDir); lerr == nil {
					issues = fresh
				}
				out.Reset()
				errOut.Reset()
				err = queueStart(&sub, issues, "")
			}
		}
	}
	// A queue item's start says "✓ Started queue item N"; a due job's run
	// only "✓ Dispatched <session>".
	var started, dispatched []string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "✓ Started"):
			started = append(started, line)
		case strings.HasPrefix(line, "✓ Dispatched"):
			dispatched = append(dispatched, line)
		}
	}
	if len(started) == 0 {
		started = dispatched
	}
	if len(started) > 0 {
		t.held("start: " + strings.Join(started, "; "))
	}
	var held *exitCodeError
	isHold := err != nil && errors.As(err, &held) && held.Hold
	if len(started) > 0 {
		t.lastStart = t.now
	} else if isHold {
		t.idle = leadTickIdleReason(ctx, issues, held)
	}
	if isHold && len(started) == 0 {
		t.heldSince = t.prev.HeldSince
		if t.heldSince.IsZero() {
			t.heldSince = t.now
		}
		// Said once per run of held starts, whatever holds in between.
		if at, ok := t.prev.Said["held-long"]; ok {
			t.seen["held-long"] = at
		}
	}
	if err == nil || len(started) > 0 {
		t.lastHold = ""
	}
	if err == nil {
		return
	}
	msg := err.Error()
	if isHold {
		reason := leadTickHoldReason(held.Msg)
		if t.watch && held.Clears {
			if t.idle != nil {
				return // the idle line says it (cmd_lead_tick_idle.go)
			}
			if d := t.now.Sub(t.heldSince); d >= leadstate.HeldLong && reason != "" {
				t.say("held-long", fmt.Sprintf("hold: nothing started for %s (since %s), now: %s", roundHours(d), t.heldSince.Local().Format("15:04"), reason))
			}
			return
		}
		// The gap between starts holds right after every start, and an
		// empty queue is said by the refill line: neither needs the lead.
		if reason == "" || strings.Contains(reason, "gap ") || strings.Contains(reason, "nothing open in the queue") {
			return
		}
		key := "hold:" + tickKey(reason)
		_, said := t.prev.Said[key]
		if key == t.lastHold && !said {
			// The hold said last, back after a tick where one that
			// clears by itself won: no news, it goes out with the next.
			t.seen[key] = t.now
			t.held("hold: " + reason)
			return
		}
		t.say(key, "hold: "+reason)
		t.lastHold = key
		return
	}
	t.say("start-error:"+tickKey(msg), "start: "+firstLineOf(msg))
}

// roundHours is d as "2h" or "2h30m", to the minute.
func roundHours(d time.Duration) string {
	s := strings.TrimSuffix(d.Truncate(time.Minute).String(), "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// leadTickHoldReason is the reason line of a queue hold: the verdict's
// "hold: …" line, else the first line without its ✗.
func leadTickHoldReason(msg string) string {
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if r, ok := strings.CutPrefix(line, "hold: "); ok {
			return r
		}
		if _, r, ok := strings.Cut(line, ": hold: "); ok {
			return strings.TrimSuffix(strings.TrimSuffix(r, " Nothing was started."), ".")
		}
	}
	return strings.TrimSpace(strings.TrimPrefix(firstLineOf(msg), "✗"))
}

// leadTickQueueLow says when the queue is down to its refill line.
func leadTickQueueLow(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	proj := ctx.Project
	if !queue.Exists(proj.IssueDir) {
		return
	}
	q, err := queue.Load(proj.IssueDir)
	if err != nil {
		t.fail("queue", err)
		return
	}
	open := 0
	for _, it := range q.ActiveItems() {
		if it.State == queue.Open && it.Slug != "" {
			if issue, ok := issueExists(issues, it.Slug); ok && !proj.IssueClosed(issue) {
				open++
			}
		}
	}
	if open > leadstate.RefillLine {
		return
	}
	add := tracker.InjectProject(proj, `issue-cli queue add <slug> --quote "<owner's words>"`)
	// Under YOLO with auto-start on the tick queues a sweep itself
	// (cmd_lead_tick_sweep.go); the line says which, once per sweep.
	sweep, sweepKey := leadTickQueueSweep(ctx, t, issues, open)
	if sweep != "" {
		sweep, sweepKey = "; "+sweep, ":"+sweepKey
	}
	if open == 0 {
		line := "queue: nothing open left: ∞ think up more, then " + add + sweep
		if look := goalGaps(ctx, issues).LookFirst(proj, lookFirstMax); look != "" {
			line += "; " + look
		}
		t.say("queue-low:0"+sweepKey, line)
		return
	}
	t.say("queue-low"+sweepKey, fmt.Sprintf("queue: %d open item(s) left (refill at %d): %s%s", open, leadstate.RefillLine, add, sweep))
}

// leadTickBranches says each issue at its last status whose branch has
// commits not on main, once, and returns those slugs. The branch is the
// one checks.IssueBranch finds (a branch: field, work/<slug>, a worktree
// that names the slug), asked only for issues one of the unmerged
// branches could belong to.
func leadTickBranches(ctx *Context, t *leadTicker, issues []*tracker.Issue) map[string]bool {
	proj := ctx.Project
	out := map[string]bool{}
	root := gitmain.Root(projectRoot(proj))
	if root == "" {
		return out
	}
	branch, err := gitmain.Branch(root, queueCIBranch(proj))
	if err != nil {
		return out
	}
	refs, err := gitmain.Git(root, "for-each-ref", "--no-merged=refs/heads/"+branch, "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		t.fail("branches", err)
		return out
	}
	unmerged := strings.Fields(refs)
	if len(unmerged) == 0 {
		return out
	}
	isUnmerged := map[string]bool{}
	for _, ref := range unmerged {
		isUnmerged[ref] = true
	}
	for _, issue := range issues {
		if !leadTickMayOwn(issue, unmerged, isUnmerged) || !proj.LoadWorkflowForIssue(issue).IsLastStatus(issue.Status) {
			continue
		}
		b := checks.IssueBranch(root, issue)
		if !b.Found() || !isUnmerged[b.Name] {
			continue
		}
		slug := issue.Slug
		out[slug] = true
		n, _ := gitmain.Git(root, "rev-list", "--count", "refs/heads/"+branch+"..refs/heads/"+b.Name)
		if c, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			n = strconv.Itoa(c)
		} else {
			n = "some"
		}
		t.say("land:"+slug, fmt.Sprintf("land: %s is %s but %s has %s commit(s) not on %s", slug, issue.Status, b.Name, n, branch))
	}
	return out
}

// leadTickMayOwn reports whether one of the unmerged branches could be
// issue's by a rule checks.IssueBranch follows: its branch: field,
// work/<slug>, or a branch that names the slug.
func leadTickMayOwn(issue *tracker.Issue, unmerged []string, isUnmerged map[string]bool) bool {
	if f := issue.Field(checks.BranchField); f != "" && isUnmerged[f] {
		return true
	}
	if isUnmerged["work/"+issue.Slug] {
		return true
	}
	for _, ref := range unmerged {
		if checks.BranchNamesSlug(ref, issue.Slug) {
			return true
		}
	}
	return false
}

// leadTickSweep closes the finished worker sessions as agents sweep does,
// except those whose issue's branch is not on main yet (the lead lands it
// first). Quiet: closing needs nothing from the lead. The idle session of
// an issue whose report waits on the owner on the shelf is the exception:
// it was still at work as far as the board knew, so its close prints a held
// line naming the issue and the output, and posts a session: closed comment
// the Agents tab and a later re-dispatch read.
func leadTickSweep(ctx *Context, t *leadTicker, issues []*tracker.Issue, unlanded map[string]bool) {
	proj := ctx.Project
	keep := map[string]bool{}
	if session, window := currentTmuxNames(); session != "" {
		keep[session], keep[window] = true, true
	}
	for _, f := range sweepFinished(proj, issues, keep) {
		if f.Slug != "" && unlanded[f.Slug] {
			continue
		}
		if err := closeWorkerSession(proj, f.Name); err != nil {
			t.fail("sweep "+f.Name, err)
			continue
		}
		if f.Shelved == "" {
			continue
		}
		issue := tracker.FindIssue(issues, f.Slug)
		status := ""
		if issue != nil {
			status = issue.Status
			_ = tracker.AddCommentBy(issue.FilePath, 0, tracker.SessionClosedComment(f.Name, status, f.Shelved), "cli", commentAuthor(proj, issue))
		}
		t.held(fmt.Sprintf("closed: %s (%s waits on the owner's review at %s; its output %s is on the shelf, so it holds no worker slot; re-dispatch resumes it)", f.Name, f.Slug, status, f.Shelved))
	}
}

// leadTickWorktrees removes the worktree of each closed issue (done,
// parked or a side exit) whose branch is on main or has no commits of its
// own (the worker's own pane was still in it at its done, the issue was
// parked, or the lead landed the branch since), quietly; a tree that stays
// for a reason the lead can act on (dirty) is said once. Once a local day
// it sweeps every merged, clean, idle worktree (worktrees prune) and says
// how many went.
func leadTickWorktrees(ctx *Context, t *leadTicker, issues []*tracker.Issue, unlanded map[string]bool) {
	proj := ctx.Project
	t.prunedOn = t.prev.PrunedOn
	var done, open []*tracker.Issue
	for _, issue := range issues {
		if unlanded[issue.Slug] {
			continue
		}
		wt := tracker.IssueWorktreeOnDisk(projectRoot(proj), issue, proj.LoadWorkflowForIssue(issue))
		if wt.Path == "" || !dirExists(wt.Path) {
			continue
		}
		switch {
		case proj.IssueClosed(issue):
			done = append(done, issue)
		case !issueSessionLive(proj, issue):
			open = append(open, issue)
		}
	}
	today := t.now.Local().Format("2006-01-02")
	daily := t.prev.PrunedOn != today
	if len(done) == 0 && len(open) == 0 && !daily {
		return
	}
	in, err := pruneInput(ctx)
	if err != nil {
		return // no main checkout with a branch: leadTickBranches is quiet too
	}
	for _, issue := range done {
		wt := tracker.IssueWorktreeOnDisk(projectRoot(proj), issue, proj.LoadWorkflowForIssue(issue))
		v := agentrun.RemoveIssueTree(in, wt.Path, wt.Branch)
		if v.Kind == agentrun.TreeDirty {
			t.say("worktree-dirty:"+issue.Slug, fmt.Sprintf("worktree: %s is %s but %s stays: %s", issue.Slug, issue.Status, wt.Path, v.Why))
		}
	}
	// An open issue whose branch already landed, with no session working
	// on it: its tree goes now, not at the daily sweep's 3-hour idle mark.
	// Kept quietly when anything says it is still in use.
	for _, issue := range open {
		wt := tracker.IssueWorktreeOnDisk(projectRoot(proj), issue, proj.LoadWorkflowForIssue(issue))
		agentrun.RemoveLandedTree(in, wt.Path, wt.Branch)
	}
	if !daily {
		return
	}
	verdicts, err := agentrun.Sweep(in, false)
	if err != nil {
		t.fail("worktrees prune", err)
		return
	}
	t.prunedOn = today
	if removed, kept := sweepCounts(verdicts); removed > 0 {
		line := fmt.Sprintf("worktrees: removed %s merged, clean and idle", countOf(removed, "worktree", "worktrees"))
		if len(kept) > 0 {
			line += "; kept " + keptWords(kept)
		}
		t.held(line + "  (" + tracker.InjectProject(proj, "issue-cli worktrees prune --dry-run") + ")")
	}
}

// issueSessionLive reports whether the issue's agent session is live; a
// var so a test can say.
var issueSessionLive = func(proj *tracker.Project, issue *tracker.Issue) bool {
	return agentrun.IsLive(proj, tracker.AgentSessionName(issue.Slug))
}

// leadTickLeaks runs doctor leaks once a day (the first tick of the local
// day) and says what it found in one line: what the factory left running
// (a playtest Chrome, a detached CLI daemon, a killed test's tmux server,
// a per_run: tool no run holds). It stops nothing: the owner's command is
// doctor leaks --stop. The traced still-running ones aren't counted: they
// may be in use.
func leadTickLeaks(ctx *Context, t *leadTicker) {
	t.leaksOn = t.prev.LeaksOn
	today := t.now.Local().Format("2006-01-02")
	if t.prev.LeaksOn == today {
		return
	}
	t.leaksOn = today
	var projects []*tracker.Project
	for i := range ctx.AllProjects {
		projects = append(projects, &ctx.AllProjects[i])
	}
	if len(projects) == 0 {
		projects = []*tracker.Project{ctx.Project}
	}
	rep := findLeaks(leaks.Options{Now: t.now, Projects: projects})
	if len(rep.Leaks) == 0 {
		return
	}
	kinds := map[string]int{}
	var order []string
	for _, f := range rep.Leaks {
		if kinds[f.Kind] == 0 {
			order = append(order, f.Kind)
		}
		kinds[f.Kind]++
	}
	var parts []string
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%d %s", kinds[k], k))
	}
	t.note(fmt.Sprintf("leaks: %s left running (%s); see %s, stop with --stop", countOf(len(rep.Leaks), "thing", "things"), strings.Join(parts, ", "), "issue-cli doctor leaks"))
}

// leadTickCI says local CI red on main once per red streak, and a CI that
// can't run once per error.
func leadTickCI(ctx *Context, t *leadTicker) {
	proj := ctx.Project
	wf := proj.LoadWorkflow()
	if wf == nil || !wf.CI.Enabled() {
		return
	}
	root := ci.RepoRoot(projectRoot(proj))
	st, err := ci.ReadState(root)
	if err != nil || st == nil {
		return
	}
	branch, _ := ci.ResolveBranch(root, wf.CI)
	switch st.State {
	case ci.Red:
		t.say("ci-red:"+valueOr(st.RedSince, st.LastSha), "ci: "+ci.OneLine(st, nil, false, branch, t.now)+"  ("+tracker.InjectProject(proj, "issue-cli ci status")+")")
	case ci.Error:
		t.say("ci-error:"+tickKey(st.Error), "ci: "+ci.OneLine(st, nil, false, branch, t.now))
	}
}

// leadTickAttention says what queue next prints before its verdict, once
// each: criteria only the lead can prove, stalled agents, takeovers.
func leadTickAttention(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	proj := ctx.Project
	for _, lw := range leadWaits(proj, whoAmI(proj, issues), issues) {
		t.say("wait:"+lw.Slug+":"+lw.ID, fmt.Sprintf("⚠ waits on you: %s %s: %s → %s", lw.Slug, lw.ID, lw.Command,
			tracker.InjectProject(proj, "issue-cli prove "+lw.Slug+" --lead-run")))
	}
	for _, s := range scanStalls(proj, projectRoot(proj), t.now) {
		t.say("stall:"+s.Session+":"+tickKey(s.Words()), fmt.Sprintf("⚠ %s %s: %s", s.Session, s.Words(), s.Ask()))
	}
	for _, line := range takeoverLines(ctx) {
		t.say("takeover:"+tickKey(line), "⚠ "+line)
	}
}

// leadTickOwner says each comment the owner wrote since the last tick.
// Comment dates are whole seconds, so the window starts a second before
// the last tick and each comment is said once by its key.
func leadTickOwner(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	type reply struct {
		at        time.Time
		key, line string
	}
	from := t.prev.Since.Truncate(time.Second).Add(-time.Second)
	var replies []reply
	for _, issue := range issues {
		for _, c := range issue.Comments() {
			if tracker.CommentAuthor(c) != "owner" {
				continue
			}
			at, ok := tracker.CommentTime(c.Date)
			if !ok || at.Before(from) {
				continue
			}
			text := strings.Join(strings.Fields(c.Text), " ")
			if r := []rune(text); len(r) > 100 {
				text = string(r[:100]) + "…"
			}
			replies = append(replies, reply{at, fmt.Sprintf("owner:%s:%d", issue.Slug, c.ID), fmt.Sprintf("owner: %s: %s", issue.Slug, text)})
		}
	}
	sort.SliceStable(replies, func(i, j int) bool { return replies[i].at.Before(replies[j].at) })
	for _, r := range replies {
		t.say(r.key, r.line)
	}
}
