package viewer

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// The lead's queue in the viewer (internal/queue): the Report's Queue card
// (next 5, blocked, done today, the refill line, what queue next says) and
// status.md's Queue: line. Read only: the viewer never writes the queue
// (items whose issue shipped are reconciled in memory) and never starts a
// worker from it.

// queueCardRows is how many active items the card lists.
const queueCardRows = 5

// QueueRow is one item on the card.
type QueueRow struct {
	ID      int
	Label   string
	URL     string // the issue page; "" for an idea
	Idea    bool
	Started bool
	Hold    string
	Blocked bool
	SHA     string // done items
}

// QueueView is what the Report and status.md show of the queue.
type QueueView struct {
	Next      []QueueRow // the first queueCardRows active items
	More      int        // active items beyond those
	Active    int
	Blocked   int
	DoneToday []QueueRow
	Verdict   string // "start <slug> (…)" or "hold: …"
	Refill    string
	Empty     bool   // nothing active: only the refill line
	Error     string // the file can't be read
	Cmd       string // the lead's tick command
	// Paused is the owner's pause from the Crew page; PauseLine says it.
	Paused    bool
	PauseLine string
}

// queueView reads the project's queue; nil when it has none.
func queueView(proj *tracker.Project, issues []*tracker.Issue, prefix string, now time.Time) *QueueView {
	if !queue.Exists(proj.IssueDir) {
		return nil
	}
	v := &QueueView{Refill: queue.RefillText, Cmd: tracker.InjectProject(proj, "issue-cli queue next --start")}
	q, err := queue.Load(proj.IssueDir)
	if err != nil {
		log.Printf("queue: %s: %v", proj.Slug, err)
		v.Error = "can't read the queue file (<issues>/.queue/queue.json): " + tracker.InjectProject(proj, "issue-cli queue list") + " says why"
		return v
	}
	agentrun.QueueReconcile(proj, issues, q, now)
	v.Paused, v.PauseLine = q.Paused, q.PauseLine()
	root := ciRootFor(proj)
	auto, _ := autostart.Load(root, proj.Slug)
	last, _ := autostart.LastStart(autostart.SharedDir())
	_, yoloOn, _ := yolo.Active(root, proj.Slug, now)
	verdict := agentrun.QueueEvaluate(agentrun.QueueInput{
		Proj: proj, Issues: issues, Queue: q, Auto: auto, YOLO: yoloOn,
		CIHold: agentrun.QueueCIHold(proj, root), Last: last, WorkRoot: projectRoot(proj), Now: now,
	})
	row := func(it agentrun.QueueItem) QueueRow {
		r := QueueRow{ID: it.ID, Label: it.Label(), Idea: it.Slug == "", Started: it.State == queue.Started, Hold: it.Hold, Blocked: it.Blocked}
		if it.Slug != "" {
			r.URL = prefix + "/issue/" + it.Slug
		}
		return r
	}
	v.Active = len(verdict.Items)
	for i, it := range verdict.Items {
		if it.Blocked {
			v.Blocked++
		}
		if i < queueCardRows {
			v.Next = append(v.Next, row(it))
		}
	}
	v.More = v.Active - len(v.Next)
	for _, it := range q.DoneOn(now) {
		r := QueueRow{ID: it.ID, Label: it.Label(), SHA: tracker.ShortSHA(it.DoneSHA)}
		if it.Slug != "" {
			r.URL = prefix + "/issue/" + it.Slug
		}
		v.DoneToday = append(v.DoneToday, r)
	}
	v.Empty = v.Active == 0
	if verdict.Start && verdict.Item != nil {
		v.Verdict = fmt.Sprintf("start %s (item %d)", verdict.Item.Slug, verdict.Item.ID)
	} else {
		v.Verdict = "hold: " + verdict.Reason
	}
	return v
}

// queueStatusLine is status.md's Queue: line.
func queueStatusLine(v *QueueView) string {
	if v == nil {
		return ""
	}
	if v.Error != "" {
		return "Queue: " + v.Error
	}
	parts := []string{fmt.Sprintf("%d active", v.Active), fmt.Sprintf("%d blocked", v.Blocked), fmt.Sprintf("%d done today", len(v.DoneToday))}
	if len(v.Next) > 0 {
		parts = append(parts, "next "+v.Next[0].Label)
	} else {
		parts = append(parts, "only \"think up more\" left")
	}
	return "Queue: " + strings.Join(parts, " · ") + " · now: " + v.Verdict + " (" + v.Cmd + ")"
}
