package main

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/goalwork"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

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
