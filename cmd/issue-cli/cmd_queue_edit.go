package main

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/goalwork"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

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
