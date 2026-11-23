package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// queueVerdict loads the queue (reconciled in memory) and decides now.
// b is the shared budget under its lock (nil: read it to look only).
func queueVerdict(ctx *Context, issues []*tracker.Issue, q *queue.Queue, b *autostart.Budget) (agentrun.QueueVerdict, error) {
	proj := ctx.Project
	now := ctx.now()
	b = budgetOrRead(b)
	auto, err := autostart.Load(yoloRoot(proj), proj.Slug)
	if err != nil {
		return agentrun.QueueVerdict{}, err
	}
	_, yoloOn, _ := yolo.Active(yoloRoot(proj), proj.Slug, now)
	return agentrun.QueueEvaluate(agentrun.QueueInput{
		Proj: proj, Issues: issues, Queue: q, Auto: auto, YOLO: yoloOn,
		CIHold: agentrun.QueueCIHold(proj, ci.RepoRoot(projectRoot(proj))),
		Last:   b.Last, Share: b.Share, WorkRoot: projectRoot(proj), Now: now,
		OnMain: queueOnMain(proj, issues),
	}), nil
}

// queueOnMain answers QueueInput.OnMain: the commit that puts slug's work
// on the project's main branch. In order: the newest merge_sha its stats
// recorded; work/<slug>'s tip once main contains it; an earlier recorded
// branch head main contains (the branch took commits after its merge). A
// commit that is the dispatch's recorded base is never the work: a branch
// cut with no commits of its own is trivially on main. Answers are cached
// for the one verdict.
func queueOnMain(proj *tracker.Project, issues []*tracker.Issue) func(string) (string, bool) {
	type answer struct {
		sha string
		ok  bool
	}
	cache := map[string]answer{}
	root, configured := "", ""
	return func(slug string) (string, bool) {
		key := strings.ToLower(slug)
		if a, seen := cache[key]; seen {
			return a.sha, a.ok
		}
		issue, found := issueExists(issues, slug)
		if !found {
			cache[key] = answer{}
			return "", false
		}
		if root == "" {
			root, configured = projectRoot(proj), queueCIBranch(proj)
		}
		sha, ok := issueWorkOnMain(root, configured, issue)
		cache[key] = answer{sha, ok}
		return sha, ok
	}
}

// issueDispatchBase is the commit issue's first dispatch cut its branch
// from, "" when no stats record one.
func issueDispatchBase(issue *tracker.Issue) string {
	store, _ := tracker.StatsOf(issue.FilePath)
	if d, ok := tracker.DispatchBase(store); ok {
		return d.BaseSHA
	}
	return ""
}

// queueBaseOf is slug's dispatch base among issues ("" unknown).
func queueBaseOf(issues []*tracker.Issue, slug string) string {
	if issue, ok := issueExists(issues, slug); ok {
		return issueDispatchBase(issue)
	}
	return ""
}

func issueWorkOnMain(root, configured string, issue *tracker.Issue) (string, bool) {
	store, _ := tracker.StatsOf(issue.FilePath)
	for i := len(store.Transitions) - 1; i >= 0; i-- {
		if m := store.Transitions[i].MergeSHA; m != "" {
			return m, true
		}
	}
	base := ""
	if d, ok := tracker.DispatchBase(store); ok {
		base = d.BaseSHA
	}
	main := gitmain.Root(root)
	branch, err := gitmain.Branch(main, configured)
	if err != nil {
		return "", false
	}
	contains := gitmain.Contains(main, branch)
	onMain := func(sha string) bool { return gitmain.OnMain(sha, base, contains) }
	if tip, err := gitmain.Git(main, "rev-parse", "--verify", "-q", "refs/heads/work/"+issue.Slug+"^{commit}"); err == nil && onMain(tip) {
		return tip, true
	}
	seen := map[string]bool{}
	for i := len(store.Transitions) - 1; i >= 0; i-- {
		h := store.Transitions[i].BranchHead
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if onMain(h) {
			return h, true
		}
	}
	return "", false
}

func runQueueNext(ctx *Context, args []string) error {
	fs := newFlagSet("queue next", ctx)
	start := fs.Bool("start", false, "start the first startable item")
	agentFlag := fs.String("agent", "", "agent to run: claude or codex (default: the workflow's routing: rules)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	defer agentrun.KeepReadings()()
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	waits := leadWaits(ctx.Project, whoAmI(ctx.Project, issues), issues)
	printStalled(ctx)
	printTakeovers(ctx)
	if *start {
		if !ctx.JSONOutput {
			printLeadWaits(ctx.Stdout, ctx.Project, waits)
		}
		return queueStart(ctx, issues, *agentFlag)
	}
	printDueJobs(ctx, issues)
	q, err := loadQueue(ctx, issues)
	if err != nil {
		return err
	}
	var v agentrun.QueueVerdict
	err = askBudget(func(b *autostart.Budget) error {
		var err error
		v, err = queueVerdict(ctx, issues, q, b)
		if err == nil {
			b.Decided(ctx.Project.Slug, ctx.Project.Weight(), v.Verdict, ctx.now())
		}
		return err
	})
	if err != nil {
		return err
	}
	var free []agentrun.Reclaim
	if !v.Start && isMemoryHold(v.Kind) {
		free = reclaimableSessions(ctx.Project, issues, ctx.now())
	}
	if ctx.JSONOutput {
		if waits == nil {
			waits = []issueLeadWait{}
		}
		return writeJSON(ctx.Stdout, struct {
			agentrun.QueueVerdict
			WaitsOnLead []issueLeadWait    `json:"waits_on_lead"`
			CouldFree   []agentrun.Reclaim `json:"could_free,omitempty"`
		}{v, waits, free})
	}
	printLeadWaits(ctx.Stdout, ctx.Project, waits)
	printQueueNext(ctx.Stdout, ctx, v)
	printCouldFree(ctx.Stdout, ctx.Project, free)
	if v.Start {
		fmt.Fprintln(ctx.Stdout, "  "+tracker.InjectProject(ctx.Project, "issue-cli queue next --start"))
	}
	return nil
}

// printQueueNext prints the verdict, each held item before the first ready
// one, and the refill hint when the queue ran dry.
func printQueueNext(w io.Writer, ctx *Context, v agentrun.QueueVerdict) {
	fmt.Fprintln(w, queueVerdictLine(v))
	for _, it := range v.Items {
		if v.Item != nil && it.ID == v.Item.ID {
			break
		}
		if it.Hold != "" {
			fmt.Fprintf(w, "  %d. %s: %s\n", it.ID, it.Label(), it.Hold)
		}
	}
	if v.Refill || queueOnlyIdeasOrStarted(v) {
		fmt.Fprintln(w, "∞ "+queue.RefillText)
		if look := goalGaps(ctx, nil).LookFirst(ctx.Project, lookFirstMax); look != "" {
			fmt.Fprintln(w, "  "+look)
		}
		fmt.Fprintln(w, "  add items: "+tracker.InjectProject(ctx.Project, "issue-cli queue add <slug> --quote \"<owner's words>\"")+"  (or --idea \"…\")")
	}
}

// queueOnlyIdeasOrStarted: nothing open is an issue to start.
func queueOnlyIdeasOrStarted(v agentrun.QueueVerdict) bool {
	for _, it := range v.Items {
		if it.State == queue.Open && it.Slug != "" {
			return false
		}
	}
	return true
}

// queueStart is queue next --start: under the machine-wide start budget it
// decides again, starts the first startable item's worker with the
// owner's words, marks the item started and says what is next.
func queueStart(ctx *Context, issues []*tracker.Issue, agentType string) error {
	defer agentrun.KeepReadings()() // lead tick's call too; nested under queue next it is a no-op
	proj := ctx.Project
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		guardNever(ctx, "queue next --start")
		return fmt.Errorf("workers can't start from the queue (you are %s). Post a question: comment on your issue instead", valueOr(me.Name, "a worker"))
	}
	if agentType != "" && agentType != "claude" && agentType != "codex" {
		return &usageError{msg: fmt.Sprintf("--agent must be claude or codex, got %q", agentType)}
	}
	if ok, err := startDueJob(ctx, me, issues, agentType); ok || err != nil {
		return err
	}
	var started *agentrun.QueueItem
	err := autostart.WithShare(autostart.SharedDir(), func(b *autostart.Budget) (*autostart.Start, error) {
		recent.Forget() // decide on what the machine is now, under the lock
		q, err := loadQueue(ctx, issues)
		if err != nil {
			return nil, err
		}
		v, err := queueVerdict(ctx, issues, q, b)
		if err != nil {
			return nil, err
		}
		b.Decided(proj.Slug, proj.Weight(), v.Verdict, ctx.now())
		if !v.Start || v.Item == nil {
			var b strings.Builder
			printQueueNext(&b, ctx, v)
			return nil, &exitCodeError{Code: 1, Hold: true, Clears: v.Clears, HoldKind: v.Kind, Msg: "✗ Nothing started from the queue.\n" + strings.TrimRight(b.String(), "\n")}
		}
		item := v.Item
		issue, _ := issueExists(issues, item.Slug)
		if refusal := outOfScope(proj, me, issue, "dispatches"); refusal != "" {
			return nil, fmt.Errorf("%s", refusal)
		}
		if refusal := relayRefusal(proj, me, issue); refusal != "" {
			return nil, fmt.Errorf("%s", refusal)
		}
		now := ctx.now()
		_, yoloOn, _ := yolo.Active(yoloRoot(proj), proj.Slug, now)
		spec, refused := agentrun.DispatchSpec(proj, issue, agentType, projectRoot(proj), yoloOn)
		if refused != nil {
			return nil, &exitCodeError{Code: exitWaitingOnGate, Msg: fmt.Sprintf("✗ Not started: %s waits on the owner's approval for %q. Nothing was started.", issue.Slug, refused.Gate)}
		}
		if spec.Route != nil && spec.Route.Hold != "" {
			return nil, &exitCodeError{Code: 1, Hold: true, Clears: true, Msg: fmt.Sprintf("✗ Not started: hold: %s. Nothing was started.", spec.Route.Hold)}
		}
		comment := queueDispatchComment(item, v, now)
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
		if err := queue.Update(proj.IssueDir, func(q *queue.Queue) error {
			if it := q.Find(strconv.Itoa(item.ID)); it != nil && it.State == queue.Open {
				it.State, it.StartedAt, it.StartedBy = queue.Started, now, valueOr(me.Name, "owner")
			}
			return nil
		}); err != nil {
			fmt.Fprintf(ctx.Stderr, "warning: marking queue item %d started: %v\n", item.ID, err)
		}
		started = item
		b.Started(proj.Slug, issue.Slug, v.Verdict, now)
		return &autostart.Start{Project: proj.Slug, Slug: issue.Slug, At: now, Memory: v.Memory, By: me.Name}, rerr
	})
	if errors.Is(err, autostart.ErrBusy) {
		return &exitCodeError{Code: 1, Hold: true, Clears: true, Msg: "✗ Nothing started from the queue: hold: " + err.Error() + "."}
	}
	var held *exitCodeError
	if errors.As(err, &held) && held.Hold && isMemoryHold(held.HoldKind) {
		var b strings.Builder
		printCouldFree(&b, proj, reclaimableSessions(proj, issues, ctx.now()))
		if b.Len() > 0 {
			held.Msg += "\n" + strings.TrimRight(b.String(), "\n")
		}
	}
	if err != nil || started == nil || ctx.JSONOutput {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Started queue item %d: %s (marked [>])\n", started.ID, started.Slug)
	if q, lerr := loadQueue(ctx, issues); lerr == nil {
		for _, it := range q.ActiveItems() {
			if it.State == queue.Open {
				fmt.Fprintf(ctx.Stdout, "  next in the queue: %d. %s\n", it.ID, it.Label())
				return nil
			}
		}
		fmt.Fprintln(ctx.Stdout, "  the queue has nothing else open: ∞ think up more and add items")
		if look := goalGaps(ctx, issues).LookFirst(ctx.Project, lookFirstMax); look != "" {
			fmt.Fprintln(ctx.Stdout, "  "+look)
		}
	}
	return nil
}

// queueDispatchComment is the started worker's dispatch: line: the item's
// own words, or auto-start's stored ones, and the reading.
func queueDispatchComment(item *agentrun.QueueItem, v agentrun.QueueVerdict, now time.Time) string {
	how := fmt.Sprintf("queue item %d, %s; memory %d%% < %d%%, %s, %d workers, %s",
		item.ID, valueOr(item.How, "the owner"), v.Memory, v.Below, v.LoadPart(), v.Workers, queueCapWords(v.Cap))
	return tracker.DispatchComment(item.Words, how, now.Format("2006-01-02"))
}
