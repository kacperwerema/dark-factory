package main

import (
	"flag"
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/goalwork"
	"io"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
