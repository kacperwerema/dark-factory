package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/queue"
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

At each check-in the lead runs queue next --start: it takes the first
startable item and starts its worker on the same checks as auto-start
(mem-guard, CPU load under the core count, the worker cap, one start per
gap across all projects), holds while local CI on main is red, and marks
the item started. When the issue reaches its last status the item is
marked done with its merge SHA.

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
      [--top | --before N | --after N] [--blocked-on <item|slug>]
  move <item> --top | --bottom | --before <item> | --after <item>
  block <item> --on <item|slug>   /   unblock <item> [--on <item|slug>]
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
	me := whoAmI(ctx.Project, issues)
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
	note := fs.String("note", "", "a short note shown with it")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
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
		id, err = q.Add(queue.Item{Slug: slug, Idea: *idea, Quote: quote, How: how, Note: strings.TrimSpace(*note), AddedBy: by},
			queue.Where{Top: *top, Before: *before, After: *after}, ctx.now())
		if err != nil {
			return err
		}
		if *blockedOn != "" {
			return queueBlockOn(q, issues, strconv.Itoa(id), *blockedOn)
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

// queueBlockOn blocks ref on on, checking a slug is on the board.
func queueBlockOn(q *queue.Queue, issues []*tracker.Issue, ref, on string) error {
	on = strings.TrimSpace(on)
	if _, err := strconv.Atoi(strings.TrimPrefix(on, "#")); err != nil {
		issue, ok := issueExists(issues, on)
		if !ok {
			return fmt.Errorf("no issue %s on the board to block on", on)
		}
		on = issue.Slug
	}
	return q.Block(ref, on)
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
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, _ []*tracker.Issue, _ string) error {
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
	ref, _, err := queueRef(ctx, fs, args, sub, 0)
	if err != nil {
		return err
	}
	if block && strings.TrimSpace(*on) == "" {
		return &usageError{msg: "queue block needs --on <item or slug>"}
	}
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, issues []*tracker.Issue, _ string) error {
		if block {
			return queueBlockOn(q, issues, ref, *on)
		}
		return q.Unblock(ref, *on)
	})
	if err != nil {
		return err
	}
	if block {
		fmt.Fprintf(ctx.Stdout, "✓ Item %s is blocked on %s\n", ref, *on)
	} else {
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
	sha := fs.String("sha", "", "the commit it shipped as")
	note := fs.String("note", "", "what it came to (no code, a report, …)")
	ref, _, err := queueRef(ctx, fs, args, "done", 0)
	if err != nil {
		return err
	}
	err = queueUpdate(ctx, "change the queue", func(q *queue.Queue, _ []*tracker.Issue, _ string) error {
		it := q.Find(ref)
		if it == nil {
			return fmt.Errorf("no queue item %s", ref)
		}
		it.State, it.DoneAt, it.DoneSHA = queue.Done, ctx.now(), strings.TrimSpace(*sha)
		if n := strings.TrimSpace(*note); n != "" {
			it.Note = n
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Item %s done\n", ref)
	return nil
}

func runQueueLimits(ctx *Context, args []string) error {
	fs := newFlagSet("queue limits", ctx)
	below := fs.Int("below", 0, "start only while memory used is under this % (default 50)")
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
func queueVerdict(ctx *Context, issues []*tracker.Issue, q *queue.Queue, last *autostart.Start) (agentrun.QueueVerdict, error) {
	proj := ctx.Project
	now := ctx.now()
	if last == nil {
		last, _ = autostart.LastStart(autostart.SharedDir())
	}
	auto, err := autostart.Load(yoloRoot(proj), proj.Slug)
	if err != nil {
		return agentrun.QueueVerdict{}, err
	}
	_, yoloOn, _ := yolo.Active(yoloRoot(proj), proj.Slug, now)
	return agentrun.QueueEvaluate(agentrun.QueueInput{
		Proj: proj, Issues: issues, Queue: q, Auto: auto, YOLO: yoloOn,
		CIHold: agentrun.QueueCIHold(proj, ci.RepoRoot(projectRoot(proj))),
		Last:   last, WorkRoot: projectRoot(proj), Now: now,
	}), nil
}

// loadQueue reads the queue and reconciles shipped items in memory.
func loadQueue(ctx *Context, issues []*tracker.Issue) (*queue.Queue, error) {
	q, err := queue.Load(ctx.Project.IssueDir)
	if err != nil {
		return nil, err
	}
	agentrun.QueueReconcile(ctx.Project, issues, q, ctx.now())
	return q, nil
}

type queueListJSON struct {
	Items     []agentrun.QueueItem  `json:"items"`
	DoneToday []queue.Item          `json:"done_today"`
	All       []queue.Item          `json:"all,omitempty"`
	Refill    string                `json:"refill"`
	Verdict   agentrun.QueueVerdict `json:"verdict"`
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
		out := queueListJSON{Items: v.Items, DoneToday: done, Refill: queue.RefillText, Verdict: v}
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
	if len(done) > 0 {
		fmt.Fprintln(w, "\nDone today:")
		for _, it := range done {
			fmt.Fprintf(w, "  %d. [x] %s %s\n", it.ID, valueOr(tracker.ShortSHA(it.DoneSHA), "(no sha)"), it.Label())
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

func queueDoneSuffix(it queue.Item) string {
	switch {
	case it.State == queue.Done && it.DoneSHA != "":
		return " · " + tracker.ShortSHA(it.DoneSHA)
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
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	if *start {
		return queueStart(ctx, issues, *agentFlag)
	}
	q, err := loadQueue(ctx, issues)
	if err != nil {
		return err
	}
	v, err := queueVerdict(ctx, issues, q, nil)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, v)
	}
	printQueueNext(ctx.Stdout, ctx, v)
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
	proj := ctx.Project
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers can't start from the queue (you are %s). Post a question: comment on your issue instead", valueOr(me.Name, "a worker"))
	}
	if agentType != "" && agentType != "claude" && agentType != "codex" {
		return &usageError{msg: fmt.Sprintf("--agent must be claude or codex, got %q", agentType)}
	}
	var started *agentrun.QueueItem
	err := autostart.WithBudget(autostart.SharedDir(), func(last *autostart.Start) (*autostart.Start, error) {
		q, err := loadQueue(ctx, issues)
		if err != nil {
			return nil, err
		}
		v, err := queueVerdict(ctx, issues, q, last)
		if err != nil {
			return nil, err
		}
		if !v.Start || v.Item == nil {
			var b strings.Builder
			printQueueNext(&b, ctx, v)
			return nil, &exitCodeError{Code: 1, Msg: "✗ Nothing started from the queue.\n" + strings.TrimRight(b.String(), "\n")}
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
			return nil, &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ Not started: hold: %s. Nothing was started.", spec.Route.Hold)}
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
		return &autostart.Start{Project: proj.Slug, Slug: issue.Slug, At: now, Memory: v.Memory, By: me.Name}, rerr
	})
	if errors.Is(err, autostart.ErrBusy) {
		return &exitCodeError{Code: 1, Msg: "✗ Nothing started from the queue: hold: " + err.Error() + "."}
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
// when main contains it (the worker fast-forwarded main), else main's head.
func queueShipSHA(root, slug string) string {
	tip, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "refs/heads/work/"+slug).Output()
	if err == nil {
		sha := strings.TrimSpace(string(tip))
		for _, branch := range []string{"main", "master"} {
			if exec.Command("git", "-C", root, "merge-base", "--is-ancestor", sha, "refs/heads/"+branch).Run() == nil {
				return sha
			}
		}
	}
	for _, branch := range []string{"main", "master"} {
		if head, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Output(); err == nil {
			return strings.TrimSpace(string(head))
		}
	}
	return ""
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
	sha := queueShipSHA(projectRoot(ctx.Project), slug)
	var ids []int
	err = queue.Update(ctx.Project.IssueDir, func(q *queue.Queue) error {
		ids = q.MarkDone(slug, sha, ctx.now())
		return nil
	})
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: marking the queue item done: %v\n", err)
		return
	}
	for _, id := range ids {
		fmt.Fprintf(w, "✓ Queue item %d marked done at %s\n", id, valueOr(tracker.ShortSHA(sha), "(no sha)"))
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
		return fmt.Sprintf("Queue: %d active, nothing open: ∞ think up more and add items (%s)", len(active), tracker.InjectProject(ctx.Project, "issue-cli queue add"))
	}
	return fmt.Sprintf("Queue: %d active, next %s. At each check-in: %s", len(active), next, tracker.InjectProject(ctx.Project, "issue-cli queue next --start"))
}
