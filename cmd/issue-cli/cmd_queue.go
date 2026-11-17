package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/goalwork"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

var queueCommand = &Command{
	Name:      "queue",
	ShortHelp: "The lead's queue: an ordered plan of items its tick starts while memory and CPU allow (sub: list|add|move|block|unblock|link|quote|drop|done|limits|next)",
	LongHelp: `The lead's queue: the project's ordered plan. Each item is an issue slug
or a one-line idea to file, with what it is blocked on and the owner's
words that authorise it. The queue lives in <issues>/.queue/queue.json.
Every list ends with "Out of work: think up more": when it is the only one
left, refill the queue.

At each check-in (issue-cli lead tick) the lead runs queue next --start:
it takes the first
startable item and starts its worker on the same checks as auto-start
(mem-guard, CPU load under the core count, the worker cap, one start per
gap across all projects), holds while local CI on main is red, and marks
the item started. When the issue reaches its last status the item is
marked done with its merge SHA.

queue next and queue next --start first print "Waits on you": each open
issue's criterion linked to a command only the lead can run (prove <slug>
<AC> --lead "<command>") with no passing run, and the line that runs it:
issue-cli prove <slug> --lead-run.

Starting needs the owner's words: the item's own --quote, or auto-start's
stored words while auto-start is on (issue-cli autostart on). Never an
agent's words. Limits are auto-start's when it is on, else the queue's
(queue limits: memory under 50%, no worker cap by default).

Workers can't change the queue or start from it.

Subcommands:
  list [--all]
      Active items in order with their holds, done today, the refill line.
      --all also lists done and dropped items.
  add <slug> | --idea "<one line>" [--quote "<owner's words>" --how ...]
      [--top | --before N | --after N] [--blocked-on <item|slug> [--merged]]
      [--goal <key>]   the projects.yaml goal it serves (hq/clients); the
      out-of-work line names goals no open issue, item or job serves
  move <item> --top | --bottom | --before <item> | --after <item>
  block <item> --on <item|slug> [--merged]   /   unblock <item> [--on <item|slug>]
      A blocker waits for the blocker's last status; --merged only until
      its work is on main (its merge sha, or its branch head on main):
      "waits for <slug> on main", then "cleared: <slug> on main at <sha>".
  link <item> <slug>              the idea's issue was filed
  quote <item> --quote "<owner's words>" [--how ...]
  drop <item> [--reason ...]      /   done <item> [--sha ...]
  limits [--below N] [--max-workers N]
  next [--start]
      What starts next, or why nothing does. --start starts it.

<item> is the item's number (12 or #12) or its slug.

See docs/agent-dispatch.md, "The lead's queue".`,
	Run:         runQueue,
	Subcommands: []string{"list", "add", "move", "block", "unblock", "link", "quote", "drop", "done", "limits", "next"},
}

func init() {
	registerCommand(queueCommand)
}

func runQueue(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("queue needs a project — pass --project <slug> or run from a project root")
	}
	if len(args) == 0 {
		return runQueueList(ctx, nil)
	}
	switch args[0] {
	case "list":
		return runQueueList(ctx, args[1:])
	case "add":
		return runQueueAdd(ctx, args[1:])
	case "move":
		return runQueueMove(ctx, args[1:])
	case "block":
		return runQueueBlock(ctx, args[1:], true)
	case "unblock":
		return runQueueBlock(ctx, args[1:], false)
	case "link":
		return runQueueLink(ctx, args[1:])
	case "quote":
		return runQueueQuote(ctx, args[1:])
	case "drop":
		return runQueueDrop(ctx, args[1:])
	case "done":
		return runQueueDone(ctx, args[1:])
	case "limits":
		return runQueueLimits(ctx, args[1:])
	case "next":
		return runQueueNext(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown queue subcommand %q (list, add, move, block, unblock, link, quote, drop, done, limits, next)", args[0])}
}

// queueWriter refuses a worker: the queue is the lead's plan.
func queueWriter(ctx *Context, issues []*tracker.Issue, what string) (string, error) {
	me := guardWho(ctx, issues, "queue")
	if me.Role == tracker.RoleWorker {
		return "", fmt.Errorf("workers never %s: the queue is the lead's plan. Ask your lead on your issue trail with a question: comment", what)
	}
	return valueOr(me.Name, "owner"), nil
}

// queueUpdate loads the issues, refuses a worker and runs fn on the queue
// under its lock, reconciling items whose issue already shipped.
func queueUpdate(ctx *Context, what string, fn func(q *queue.Queue, issues []*tracker.Issue, by string) error) error {
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	by, err := queueWriter(ctx, issues, what)
	if err != nil {
		return err
	}
	return queue.Update(ctx.Project.IssueDir, func(q *queue.Queue) error {
		agentrun.QueueReconcile(ctx.Project, issues, q, ctx.now())
		queueFillSHAs(ctx.Project, q)
		return fn(q, issues, by)
	})
}

// queueRef is the first positional argument after the flags: the item
// reference. The rest are returned too.
func queueRef(ctx *Context, fs *flag.FlagSet, args []string, sub string, extra int) (string, []string, error) {
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return "", nil, err
	}
	pos := fs.Args()
	if len(pos) == 0 {
		return "", nil, &usageError{msg: fmt.Sprintf("queue %s needs an item: its number (12) or its slug", sub)}
	}
	if len(pos) > 1+extra {
		return "", nil, unexpectedArgError(fs, pos[1+extra])
	}
	return pos[0], pos[1:], nil
}

func issueExists(issues []*tracker.Issue, slug string) (*tracker.Issue, bool) {
	for _, issue := range issues {
		if strings.EqualFold(issue.Slug, slug) {
			return issue, true
		}
	}
	return nil, false
}

func runQueueAdd(ctx *Context, args []string) error {
	slug := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug, args = args[0], args[1:]
	}
	fs := newFlagSet("queue add", ctx)
	idea := fs.String("idea", "", "a one-line idea to file, instead of a slug")
	quoteFlag := fs.String("quote", "", "the owner's words that authorise this item")
	howFlag := fs.String("how", "", "how the words reached you (default: \"in the lead chat\")")
	top := fs.Bool("top", false, "put it first")
	before := fs.String("before", "", "put it before this item")
	after := fs.String("after", "", "put it after this item")
	blockedOn := fs.String("blocked-on", "", "an item or slug it waits for")
	merged := fs.Bool("merged", false, "with --blocked-on: wait only until the blocker's work is on main, not for its last status")
	note := fs.String("note", "", "a short note shown with it")
	goalFlag := fs.String("goal", "", "the goal it serves (a projects.yaml goal key: hq/clients)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	goalKey := ""
	if flagWasSet(fs, "goal") {
		key, err := goalwork.Check(ctx.Project, *goalFlag, ctx.now())
		if err != nil {
			return fmt.Errorf("queue add: %w", err)
		}
		goalKey = key
	}
	if *merged && strings.TrimSpace(*blockedOn) == "" {
		return &usageError{msg: "queue add: --merged goes with --blocked-on <item|slug>"}
	}
	if (slug == "") == (strings.TrimSpace(*idea) == "") {
		return &usageError{msg: "queue add needs an issue slug or --idea \"<one line>\", one of them\n\nExamples:\n  issue-cli queue add api/my-issue --quote \"<owner's words>\"\n  issue-cli queue add --idea \"bulk YOLO review page\" --quote \"<owner's words>\""}
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	how := strings.TrimSpace(*howFlag)
	if quote != "" && how == "" {
		how = "in the lead chat"
	}
	var id int
	err := queueUpdate(ctx, "change the queue", func(q *queue.Queue, issues []*tracker.Issue, by string) error {
		if slug != "" {
			issue, ok := issueExists(issues, slug)
			if !ok {
				return fmt.Errorf("no issue %s on the board. File it first, or queue it as an idea: --idea \"…\"", slug)
			}
			slug = issue.Slug
		}
		var err error
		id, err = q.Add(queue.Item{Slug: slug, Idea: *idea, Goal: goalKey, Quote: quote, How: how, Note: strings.TrimSpace(*note), AddedBy: by},
			queue.Where{Top: *top, Before: *before, After: *after}, ctx.now())
		if err != nil {
			return err
		}
		if *blockedOn != "" {
			return queueBlockOn(q, issues, strconv.Itoa(id), *blockedOn, *merged)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Queued item %d: %s\n", id, valueOr(slug, strings.TrimSpace(*idea)))
	if quote == "" {
		fmt.Fprintf(ctx.Stdout, "  no owner's words on it: it starts only while auto-start is on, or after %s\n", tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli queue quote %d --quote \"<owner's words>\"", id)))
	}
	fmt.Fprintln(ctx.Stdout, "  next: "+tracker.InjectProject(ctx.Project, "issue-cli queue next"))
	return nil
}

// queueBlockOn blocks ref on on, checking a slug is on the board; merged:
// until on's work is on main.
func queueBlockOn(q *queue.Queue, issues []*tracker.Issue, ref, on string, merged bool) error {
	on = strings.TrimSpace(on)
	if _, err := strconv.Atoi(strings.TrimPrefix(on, "#")); err != nil {
		issue, ok := issueExists(issues, on)
		if !ok {
			return fmt.Errorf("no issue %s on the board to block on", on)
		}
		on = issue.Slug
	}
	return q.BlockOn(ref, on, merged)
}

func runQueueMove(ctx *Context, args []string) error {
	fs := newFlagSet("queue move", ctx)
	top := fs.Bool("top", false, "move it first")
	bottom := fs.Bool("bottom", false, "move it last (above the refill line)")
	before := fs.String("before", "", "move it before this item")
	after := fs.String("after", "", "move it after this item")
	ref, _, err := queueRef(ctx, fs, args, "move", 0)
	if err != nil {
		return err
	}
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, issues []*tracker.Issue, _ string) error {
		return q.Move(ref, queue.Where{Top: *top, Bottom: *bottom, Before: *before, After: *after})
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Moved item %s\n", ref)
	return nil
}

func runQueueBlock(ctx *Context, args []string, block bool) error {
	sub := "block"
	if !block {
		sub = "unblock"
	}
	fs := newFlagSet("queue "+sub, ctx)
	on := fs.String("on", "", "the item number or issue slug")
	var merged *bool
	if block {
		merged = fs.Bool("merged", false, "wait only until the blocker's work is on main (its merge sha, or its branch head on main), not for its last status")
	}
	ref, _, err := queueRef(ctx, fs, args, sub, 0)
	if err != nil {
		return err
	}
	if block && strings.TrimSpace(*on) == "" {
		return &usageError{msg: "queue block needs --on <item or slug>"}
	}
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, issues []*tracker.Issue, _ string) error {
		if block {
			return queueBlockOn(q, issues, ref, *on, *merged)
		}
		return q.Unblock(ref, *on)
	})
	if err != nil {
		return err
	}
	switch {
	case block && *merged:
		fmt.Fprintf(ctx.Stdout, "✓ Item %s waits for %s on main (its merge, not its last status)\n", ref, *on)
	case block:
		fmt.Fprintf(ctx.Stdout, "✓ Item %s is blocked on %s\n", ref, *on)
	default:
		fmt.Fprintf(ctx.Stdout, "✓ Item %s unblocked\n", ref)
	}
	return nil
}

func runQueueLink(ctx *Context, args []string) error {
	fs := newFlagSet("queue link", ctx)
	ref, rest, err := queueRef(ctx, fs, args, "link", 1)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return &usageError{msg: "queue link needs the item and the slug of the issue filed for it: issue-cli queue link 12 api/my-issue"}
	}
	slug := rest[0]
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, issues []*tracker.Issue, _ string) error {
		it := q.Find(ref)
		if it == nil {
			return fmt.Errorf("no queue item %s", ref)
		}
		issue, ok := issueExists(issues, slug)
		if !ok {
			return fmt.Errorf("no issue %s on the board", slug)
		}
		if dup := q.Find(issue.Slug); dup != nil && dup.Active() && dup.ID != it.ID {
			return fmt.Errorf("%s is already item %d", issue.Slug, dup.ID)
		}
		it.Slug = issue.Slug
		slug = issue.Slug
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Item %s is %s\n", ref, slug)
	return nil
}

func runQueueQuote(ctx *Context, args []string) error {
	fs := newFlagSet("queue quote", ctx)
	quoteFlag := fs.String("quote", "", "the owner's words that authorise this item (required)")
	howFlag := fs.String("how", "", "how the words reached you (default: \"in the lead chat\")")
	ref, _, err := queueRef(ctx, fs, args, "quote", 0)
	if err != nil {
		return err
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if quote == "" {
		return &usageError{msg: "queue quote needs --quote: the owner's words that authorise the item"}
	}
	how := valueOr(strings.TrimSpace(*howFlag), "in the lead chat")
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, _ []*tracker.Issue, _ string) error {
		it := q.Find(ref)
		if it == nil {
			return fmt.Errorf("no queue item %s", ref)
		}
		it.Quote, it.How = quote, how
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Item %s carries the owner's words: %q (%s)\n", ref, quote, how)
	return nil
}

func runQueueDrop(ctx *Context, args []string) error {
	fs := newFlagSet("queue drop", ctx)
	reason := fs.String("reason", "", "why")
	ref, _, err := queueRef(ctx, fs, args, "drop", 0)
	if err != nil {
		return err
	}
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, _ []*tracker.Issue, by string) error {
		it := q.Find(ref)
		if it == nil {
			return fmt.Errorf("no queue item %s", ref)
		}
		it.State = queue.Dropped
		it.Note = strings.TrimSpace(valueOr(strings.TrimSpace(*reason), "dropped") + " (" + by + ")")
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Dropped item %s\n", ref)
	return nil
}

func runQueueDone(ctx *Context, args []string) error {
	fs := newFlagSet("queue done", ctx)
	sha := fs.String("sha", "", "the commit it shipped as (default: work/<slug>'s tip once main contains it)")
	note := fs.String("note", "", "what it came to (no code, a report, …)")
	ref, _, err := queueRef(ctx, fs, args, "done", 0)
	if err != nil {
		return err
	}
	var got queue.Item
	where := branchUnknown
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, issues []*tracker.Issue, _ string) error {
		it := q.Find(ref)
		if it == nil {
			return fmt.Errorf("no queue item %s", ref)
		}
		if it.State != queue.Done || it.DoneAt.IsZero() {
			it.State, it.DoneAt = queue.Done, ctx.now()
		}
		if s := strings.TrimSpace(*sha); s != "" {
			it.DoneSHA = s
		} else if it.Slug != "" {
			var tip string
			tip, where = queueBranchSHA(projectRoot(ctx.Project), it.Slug, queueCIBranch(ctx.Project), queueBaseOf(issues, it.Slug))
			switch {
			case it.DoneSHA != "":
				// already filled (queueUpdate's fill, or an earlier SHA)
			case where == branchOnMain:
				it.DoneSHA = tip
				if it.Note == queue.NotOnMain {
					it.Note = ""
				}
			case where == branchNotOnMain:
				it.Note = queue.NotOnMain
			}
		}
		if n := strings.TrimSpace(*note); n != "" {
			it.Note = n
		}
		got = *it
		return nil
	})
	if err != nil {
		return err
	}
	switch {
	case where == branchNotOnMain && got.DoneSHA == "":
		fmt.Fprintf(ctx.Stdout, "Item %s marked done, but work/%s isn't on main yet: no SHA. It gets the branch's SHA by itself once the branch lands (queue list, queue next).\n", ref, got.Slug)
	case where == branchOnMain && got.DoneSHA != "":
		fmt.Fprintf(ctx.Stdout, "✓ Item %s done at %s: work/%s is on main\n", ref, gitdir.ShortSHA(got.DoneSHA), got.Slug)
	default:
		fmt.Fprintf(ctx.Stdout, "✓ Item %s done\n", ref)
	}
	return nil
}

func runQueueLimits(ctx *Context, args []string) error {
	fs := newFlagSet("queue limits", ctx)
	below := fs.Int("below", 0, fmt.Sprintf("start only while memory used is under this %% (default %d)", queue.DefaultBelow))
	maxWorkers := fs.Int("max-workers", -1, "at most this many live workers (0: no cap, memory is the limit)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if *below < 0 || *below > 100 || *maxWorkers < -1 {
		return &usageError{msg: "queue limits: --below is 1–100, --max-workers is 0 (no cap) or more"}
	}
	var q2 queue.Queue
	err := queueUpdate(ctx, "change the queue's limits", func(q *queue.Queue, _ []*tracker.Issue, _ string) error {
		if *below > 0 {
			q.Below = *below
		}
		if *maxWorkers >= 0 {
			q.MaxWorkers = *maxWorkers
		}
		q2 = *q
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "Queue limits (used while auto-start is off): memory under %d%%, %s, load under the core count, one start per %d min across all projects\n",
		q2.BelowOr(), queueCapWords(q2.MaxWorkers), autostart.DefaultGapMinutes)
	return nil
}

func queueCapWords(n int) string {
	if n <= 0 || n >= agentrun.QueueNoCap {
		return "no worker cap"
	}
	return fmt.Sprintf("at most %d workers", n)
}

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

// loadQueue reads the queue and reconciles shipped items in memory.
func loadQueue(ctx *Context, issues []*tracker.Issue) (*queue.Queue, error) {
	q, err := queue.Load(ctx.Project.IssueDir)
	if err != nil {
		return nil, err
	}
	agentrun.QueueReconcile(ctx.Project, issues, q, ctx.now())
	if len(queueFillSHAs(ctx.Project, q)) > 0 {
		// Save what the branches now say; a failed save only means the
		// next read fills it again.
		_ = queue.Update(ctx.Project.IssueDir, func(sq *queue.Queue) error {
			queueFillSHAs(ctx.Project, sq)
			return nil
		})
	}
	return q, nil
}

type queueListJSON struct {
	Items     []agentrun.QueueItem `json:"items"`
	DoneToday []queue.Item         `json:"done_today"`
	All       []queue.Item         `json:"all,omitempty"`
	Refill    string               `json:"refill"`
	// GoalsWithoutWork are the goals and open epics nothing serves: the
	// places to look first when the queue runs dry (internal/goalwork).
	GoalsWithoutWork *goalwork.Report      `json:"goals_without_work"`
	Verdict          agentrun.QueueVerdict `json:"verdict"`
}

func runQueueList(ctx *Context, args []string) error {
	fs := newFlagSet("queue list", ctx)
	all := fs.Bool("all", false, "also list done and dropped items")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	q, err := loadQueue(ctx, issues)
	if err != nil {
		return err
	}
	v, err := queueVerdict(ctx, issues, q, nil)
	if err != nil {
		return err
	}
	done := q.DoneOn(ctx.now())
	if ctx.JSONOutput {
		out := queueListJSON{Items: v.Items, DoneToday: done, Refill: queue.RefillText, Verdict: v, GoalsWithoutWork: goalGaps(ctx, issues)}
		if out.Items == nil {
			out.Items = []agentrun.QueueItem{}
		}
		if *all {
			out.All = q.Items
		}
		return writeJSON(ctx.Stdout, out)
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "Queue (%d active, %d done today)\n", len(v.Items), len(done))
	for _, it := range v.Items {
		fmt.Fprintln(w, "  "+queueItemLine(it))
	}
	fmt.Fprintln(w, "  ∞ "+queue.RefillText)
	if look := goalGaps(ctx, issues).LookFirst(ctx.Project, lookFirstMax); look != "" {
		fmt.Fprintln(w, "    "+look)
	}
	if len(done) > 0 {
		fmt.Fprintln(w, "\nDone today:")
		for _, it := range done {
			fmt.Fprintf(w, "  %d. [x] %s %s\n", it.ID, queueDoneMark(it), it.Label())
		}
	}
	if *all {
		fmt.Fprintln(w, "\nAll items:")
		for _, it := range q.Items {
			fmt.Fprintf(w, "  %d. %-7s %s%s\n", it.ID, it.State, it.Label(), queueDoneSuffix(it))
		}
	}
	fmt.Fprintln(w, "\nNow: "+queueVerdictLine(v))
	fmt.Fprintln(w, "Tick: "+tracker.InjectProject(ctx.Project, "issue-cli queue next --start"))
	return nil
}

// queueDoneMark is a done item's short SHA, "done (not on main)" when it
// shipped none, else "(no sha)".
func queueDoneMark(it queue.Item) string {
	if it.DoneSHA == "" && it.Note == queue.NotOnMain {
		return "done (" + queue.NotOnMain + ")"
	}
	return valueOr(gitdir.ShortSHA(it.DoneSHA), "(no sha)")
}

func queueDoneSuffix(it queue.Item) string {
	switch {
	case it.State == queue.Done && (it.DoneSHA != "" || it.Note == queue.NotOnMain):
		return " · " + queueDoneMark(it)
	case it.Note != "":
		return " · " + it.Note
	}
	return ""
}

// queueItemLine is "12. [ ] api/x · blocked on #11" or "[>] started".
func queueItemLine(it agentrun.QueueItem) string {
	mark := "[ ]"
	if it.State == queue.Started {
		mark = "[>]"
	}
	s := fmt.Sprintf("%d. %s %s", it.ID, mark, it.Label())
	if it.Slug == "" {
		s = fmt.Sprintf("%d. %s idea: %s", it.ID, mark, it.Idea)
	}
	if it.Note != "" {
		s += " (" + it.Note + ")"
	}
	for _, c := range it.Cleared {
		s += " · cleared: " + c
	}
	if it.Hold != "" {
		s += " · " + it.Hold
	} else if it.Standing {
		s += " · ready (auto-start's words)"
	} else {
		s += " · ready"
	}
	return s
}

// queueVerdictLine is "start <slug> (item N, memory …)" or "hold: …".
func queueVerdictLine(v agentrun.QueueVerdict) string {
	if v.Start && v.Item != nil {
		return fmt.Sprintf("start %s (item %d; memory %d%% < %d%%, %s, %d workers, %s)", v.Item.Slug, v.Item.ID, v.Memory, v.Below, v.LoadPart(), v.Workers, queueCapWords(v.Cap))
	}
	return "hold: " + v.Reason
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

// queueShipSHA is the commit an issue shipped as: the tip of work/<slug>
// when the project's main branch (ci.branch, else the main checkout's
// branch: gitmain.Branch) contains it. A tip not on it ships nothing yet:
// no SHA and the note queue.NotOnMain, never main's head, which has
// nothing to do with the issue. With no work branch at all (work done on
// main) it is main's head; "" when the main branch isn't known.
func queueShipSHA(root, slug, configured, base string) (sha, note string) {
	tip, where := queueBranchSHA(root, slug, configured, base)
	switch where {
	case branchOnMain:
		return tip, ""
	case branchNotOnMain:
		return "", queue.NotOnMain
	case branchNone:
		main := gitmain.Root(root)
		if branch, err := gitmain.Branch(main, configured); err == nil {
			head, _ := gitmain.Head(main, branch)
			return head, ""
		}
	}
	return "", ""
}

// Where work/<slug>'s tip stands against the project's main branch.
const (
	branchUnknown   = iota // no repo, or the main branch isn't known
	branchNone             // no work/<slug> branch
	branchOnMain           // main contains the tip
	branchNotOnMain        // the tip is ahead of main
	branchEmpty            // the tip is the dispatch base: nothing of its own
)

// queueBranchSHA is work/<slug>'s tip and where it stands (gitmain.WorkAt
// against base, the issue's dispatch base): the commit the issue shipped
// as once the main branch contains it. A branch still at its base shipped
// nothing, wherever main is.
func queueBranchSHA(root, slug, configured, base string) (string, int) {
	main := gitmain.Root(root)
	branch, err := gitmain.Branch(main, configured)
	if err != nil {
		return "", branchUnknown
	}
	tip, err := gitmain.Git(main, "rev-parse", "--verify", "-q", "refs/heads/work/"+slug+"^{commit}")
	if err != nil || tip == "" {
		return "", branchNone
	}
	switch gitmain.WorkAt(tip, base, gitmain.Contains(main, branch)) {
	case gitmain.WorkEmpty:
		return tip, branchEmpty
	case gitmain.WorkOnMain:
		return tip, branchOnMain
	}
	return tip, branchNotOnMain
}

// queueCIBranch is the workflow's ci.branch, "" when unset.
func queueCIBranch(proj *tracker.Project) string {
	if wf := proj.LoadWorkflow(); wf != nil && wf.CI != nil {
		return wf.CI.Branch
	}
	return ""
}

// queueFillSHAs gives each done item with no SHA (marked not on main, or
// done by reconcile) its work branch's tip once the main branch contains
// it, and drops the not-on-main note. An item with no work branch keeps
// no SHA: main's head says nothing about it. Returns the numbers filled.
func queueFillSHAs(proj *tracker.Project, q *queue.Queue) []int {
	var ids []int
	root, configured := "", ""
	var issues []*tracker.Issue
	for i := range q.Items {
		it := &q.Items[i]
		if it.State != queue.Done || it.Slug == "" || it.DoneSHA != "" || (it.Note != "" && it.Note != queue.NotOnMain) {
			continue
		}
		if root == "" {
			root, configured = projectRoot(proj), queueCIBranch(proj)
			issues, _ = tracker.LoadIssues(proj.IssueDir)
		}
		tip, where := queueBranchSHA(root, it.Slug, configured, queueBaseOf(issues, it.Slug))
		if where == branchUnknown {
			return ids
		}
		if where != branchOnMain {
			continue
		}
		it.DoneSHA, it.Note = tip, ""
		ids = append(ids, it.ID)
	}
	return ids
}

// queueMarkShipped marks the issue's queue items done with its SHA when it
// reaches its workflow's last status. Quiet when it isn't queued.
func queueMarkShipped(ctx *Context, w io.Writer, wf *tracker.WorkflowConfig, slug, to string) {
	if !wf.IsLastStatus(to) || !queue.Exists(ctx.Project.IssueDir) {
		return
	}
	q, err := queue.Load(ctx.Project.IssueDir)
	if err != nil || !q.HasActive(slug) {
		return
	}
	configured := ""
	if wf.CI != nil {
		configured = wf.CI.Branch
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	sha, note := queueShipSHA(projectRoot(ctx.Project), slug, configured, queueBaseOf(issues, slug))
	var ids []int
	err = queue.Update(ctx.Project.IssueDir, func(q *queue.Queue) error {
		ids = q.MarkDone(slug, sha, ctx.now())
		for _, id := range ids {
			if note != "" {
				q.Find(strconv.Itoa(id)).Note = note
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: marking the queue item done: %v\n", err)
		return
	}
	for _, id := range ids {
		if note != "" {
			fmt.Fprintf(w, "✓ Queue item %d marked done (%s): work/%s isn't on the main branch, so it has no SHA\n", id, note, slug)
			continue
		}
		fmt.Fprintf(w, "✓ Queue item %d marked done at %s\n", id, valueOr(gitdir.ShortSHA(sha), "(no sha)"))
	}
}

// queueLine is the one-line "Queue:" summary status, start and whoami
// print for a lead; "" without a queue or for a worker.
func queueLine(ctx *Context, issues []*tracker.Issue) string {
	if !queue.Exists(ctx.Project.IssueDir) {
		return ""
	}
	q, err := loadQueue(ctx, issues)
	if err != nil {
		return "Queue: can't read it: " + err.Error()
	}
	active := q.ActiveItems()
	next := ""
	for _, it := range active {
		if it.State == queue.Open {
			next = it.Label()
			break
		}
	}
	if next == "" {
		line := fmt.Sprintf("Queue: %d active, nothing open: ∞ think up more and add items (%s)", len(active), tracker.InjectProject(ctx.Project, "issue-cli queue add"))
		if look := goalGaps(ctx, issues).LookFirst(ctx.Project, lookFirstMax); look != "" {
			line += "; " + look
		}
		return line
	}
	return fmt.Sprintf("Queue: %d active, next %s. At each check-in: %s (it runs queue next --start)", len(active), next, tracker.InjectProject(ctx.Project, "issue-cli lead tick"))
}

// issueLeadWait is a criterion of an open issue waiting on the lead's
// command, for the lead's tick.
type issueLeadWait struct {
	Slug string `json:"slug"`
	proof.LeadWait
}

// leadWaits lists, from the data sidecars alone (no git), every open issue
// in me's scope whose unticked criterion has a lead link with no passing
// run since it was linked. The tick shows them before its verdict.
func leadWaits(proj *tracker.Project, me Identity, issues []*tracker.Issue) []issueLeadWait {
	var out []issueLeadWait
	closed := tracker.ClosedIssueFunc(proj)
	for _, issue := range issues {
		if outOfScope(proj, me, issue, "runs lead commands for") != "" {
			continue
		}
		for _, w := range proof.IssueLeadWaits(issue, closed) {
			out = append(out, issueLeadWait{Slug: issue.Slug, LeadWait: w})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// printLeadWaits prints the tick's "Waits on you" block: each command and
// the one line that runs it.
func printLeadWaits(w io.Writer, proj *tracker.Project, waits []issueLeadWait) {
	if len(waits) == 0 {
		return
	}
	fmt.Fprintf(w, "Waits on you (%d):\n", len(waits))
	for _, lw := range waits {
		fmt.Fprintf(w, "  %s %s: %s (%s)  → %s\n", lw.Slug, lw.ID, lw.Command, lw.State,
			tracker.InjectProject(proj, "issue-cli prove "+lw.Slug+" --lead-run"))
	}
}

// lookFirstMax is how many goals and epics the out-of-work line names
// before "+N more".
const lookFirstMax = 5

// goalGaps is the coverage of the project's goals and epics for the
// out-of-work line: issues as loaded (nil: load them), the queue's items.
// A board that can't be read gives an empty report: the line then says
// nothing more.
func goalGaps(ctx *Context, issues []*tracker.Issue) *goalwork.Report {
	proj := ctx.Project
	if issues == nil {
		var err error
		if issues, err = proj.LoadIssues(); err != nil {
			return &goalwork.Report{}
		}
	}
	var items []queue.Item
	if q, err := queue.Load(proj.IssueDir); err == nil {
		items = q.Items
	}
	return goalwork.Find(proj, issues, items, ctx.now())
}
