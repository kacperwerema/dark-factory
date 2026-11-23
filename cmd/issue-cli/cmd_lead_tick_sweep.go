package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// The queue-low sweep: at the refill line, with YOLO and auto-start on, the
// tick files a retro sweep and queues it itself, so a lead that misses the
// queue: line doesn't leave the queue dry for hours (11-16: about 10). The
// item has no owner's words: it starts only under auto-start's, which are
// on. No job's standing start is turned on
// (factory/the-lead-tick-at-5-ready-items-left-queues-a-sweep-itself-under-yolo-with-auto-start-on-so-the-queue-never-runs-dry#D-4).

// queueSweepNote is the queued item's note.
const queueSweepNote = "queue-low sweep by the lead tick: file its proposals"

// queueSweepsPerDay is how many queue-low sweeps the tick runs in a local
// day: the first at the refill line, more only while the queue stays empty
// and the last one closed, so a night with no lead turn still gets new
// work and a sweep that files nothing can't repeat all night.
const queueSweepsPerDay = 4

// leadTickQueueSweep files and queues the queue-low sweep when YOLO and
// auto-start are on, no sweep of the same job is open or ran today, and
// retros/ has files since the last sweep's mark (none: it says so). It
// returns the phrase the queue-low line adds ("" when it did nothing) and
// the said key's suffix (the sweep's slug, so a new sweep is said again).
func leadTickQueueSweep(ctx *Context, t *leadTicker, issues []*tracker.Issue, open int) (phrase, key string) {
	proj := ctx.Project
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		return "", "" // the queue is the lead's plan
	}
	root := yoloRoot(proj)
	if _, on, _ := yolo.Active(root, proj.Slug, t.now); !on {
		return "", ""
	}
	auto, err := autostart.Load(root, proj.Slug)
	if err != nil || auto == nil || !auto.On || auto.Paused {
		return "", ""
	}
	plan, ref := jobs.QueueSweepRef(proj)
	runs := jobs.Runs(plan, issues, ref.Key)
	for _, run := range runs {
		if run.Open {
			return "sweep " + run.Issue.Slug + " is still open: file its proposals when its report: lands", run.Issue.Slug
		}
	}
	period := t.now.Local().Format("2006-01-02")
	n, err := jobs.PlanRun(plan, ref, runs, period, false)
	var exists *jobs.ExistsError
	if errors.As(err, &exists) {
		// Today's sweep ran. With the queue empty another may, up to
		// queueSweepsPerDay; otherwise the lead's call until tomorrow.
		today := 0
		for _, run := range runs {
			if run.Period == period {
				today++
			}
		}
		if open > 0 || today >= queueSweepsPerDay {
			return "", ""
		}
		n, err = jobs.PlanRun(plan, ref, runs, period, true)
	}
	if err != nil {
		t.fail("queue-sweep", err)
		return "", ""
	}
	// A sweep with nothing to read files nothing: say so once per mark
	// instead of queueing a run that can only report "no retros".
	picked, mark, err := retrosSinceMark(ctx, t.now.Truncate(time.Second))
	if err != nil {
		t.fail("queue-sweep", err)
		return "", ""
	}
	if len(picked) == 0 {
		since := "the start (no sweep recorded yet)"
		if !mark.IsZero() {
			since = mark.Format(time.RFC3339)
		}
		return "queue low: no retros since " + since + ", no sweep queued", "no-retros:" + since
	}
	brief, err := jobs.Brief(plan, ref, n, t.now)
	if err == nil {
		err = jobs.WriteRun(plan, ref, n, brief, t.now)
	}
	if err != nil {
		t.fail("queue-sweep", err)
		return "", ""
	}
	// The board's slug for the file just written (its system folder in
	// front of the slug: pin).
	slug := strings.ToLower(n.System) + "/" + n.Slug
	if fresh, lerr := tracker.LoadIssues(proj.IssueDir); lerr == nil {
		for _, is := range fresh {
			if is.FilePath == n.File {
				slug = is.Slug
				break
			}
		}
	}
	by := valueOr(me.Name, "lead tick")
	var id int
	err = queue.Update(proj.IssueDir, func(q *queue.Queue) error {
		var aerr error
		id, aerr = q.Add(queue.Item{Slug: slug, Note: queueSweepNote, AddedBy: by}, queue.Where{}, t.now)
		return aerr
	})
	if err != nil {
		t.fail("queue-sweep", err)
		return "", ""
	}
	return fmt.Sprintf("queued sweep %s as item %d (YOLO and auto-start on; it starts on auto-start's words): file its proposals when its report: lands", slug, id), slug
}
