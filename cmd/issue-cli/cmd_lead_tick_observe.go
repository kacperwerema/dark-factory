package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// leadContextLine: above this many tokens of context in the lead's last
// turn, the tick says once a day to rotate. Every wake re-reads
// the whole context, so a big lead costs on each tick
// (docs/patterns/lead-session.md#the-nightly-flush).
const leadContextLine = 300_000

// leadContextLook is how far back a lead's transcript may have changed to
// count: an older one is a lead that is gone.
const leadContextLook = 2 * 24 * time.Hour

// leadContextDir is where the tick looks for transcripts; a var for tests.
var leadContextDir = spend.DefaultProjectsDir

// leadTickContext says, once a local day, when lead's last turn read more
// than leadContextLine tokens, and returns the next state's date. A lead
// with no Claude Code transcript (a Codex lead) says nothing.
func leadTickContext(ctx *Context, t *leadTicker, lead, on string) string {
	today := t.now.Local().Format("2006-01-02")
	if on == today {
		return on
	}
	n, _, ok := spend.LastContext(leadContextDir(), projectRoot(ctx.Project), lead, t.now.Add(-leadContextLook))
	if !ok || n <= leadContextLine {
		return on
	}
	t.note(fmt.Sprintf("lead context %dk: rotate (%s): %s (docs/patterns/lead-session.md#rotate)", n/1000, lead, tracker.InjectProject(ctx.Project, "issue-cli lead rotate")))
	return today
}

// observeStatusNews says why an issue's new status needs someone ("" when
// it is a plain move the lead holds for the next news): it is ready for a
// gate's approval and waits on nothing else (the owner's, or the lead's to
// pass under YOLO), it is
// at the owner's own testing, or it was parked.
func observeStatusNews(proj *tracker.Project, issue *tracker.Issue) string {
	wf := proj.LoadWorkflowForIssue(issue)
	switch {
	case wf == nil:
		return ""
	case wf.HasRole(issue.Status, tracker.StatusRoleOwnerTest):
		return "the owner's to test"
	case tracker.IsParkedStatus(issue.Status):
		return "parked"
	}
	if gate := tracker.WaitingOnGate(wf, issue, issue.Comments()); gate != "" {
		return "waits on the " + gate + " gate"
	}
	return ""
}

// leadObserveState is what one --observe tick leaves for the next.
type leadObserveState struct {
	Said  map[string]time.Time `json:"said"`
	Since time.Time            `json:"since"`
	// Statuses are the scope's issues' statuses at the last tick.
	Statuses map[string]string `json:"statuses"`
	// Shelf are the scope's output ids; Threads each shelf comment
	// thread's message count.
	Shelf   map[string]bool `json:"shelf"`
	Threads map[string]int  `json:"threads"`
	// Live are the scope's worker sessions live at the last tick.
	Live      map[string]bool `json:"live"`
	ContextOn string          `json:"context_on,omitempty"`
}

// leadObservePath is the observe tick's own state: it never shares said
// keys with the CEO's tick. "" without a state dir.
func leadObservePath(proj *tracker.Project, dept string) string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	scope := "all"
	if dept != "" {
		scope = tracker.DepartmentKey(dept)
	}
	return filepath.Join(d, "lead-tick", proj.Slug+".observe-"+scope+".json")
}

// leadObserveOnce is one read-only tick for dept ("" the whole project):
// it starts nothing, closes nothing and runs no prune; it says the scope's
// due jobs, status moves, new shelf items and comment messages, new issue
// comments by anyone but me, and worker sessions that ended. The first
// tick takes the baseline and says only due jobs.
func leadObserveOnce(ctx *Context, dept, me string) (tickCount, error) {
	proj := ctx.Project
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return tickCount{}, fmt.Errorf("cannot load issues: %w", err)
	}
	path := leadObservePath(proj, dept)
	prev := leadObserveState{}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &prev)
		}
	}
	if prev.Said == nil {
		prev.Said = map[string]time.Time{}
	}
	first := prev.Since.IsZero()
	t := &leadTicker{w: ctx.Stdout, prev: leadTickState{Said: prev.Said}, seen: map[string]time.Time{}, now: ctx.now(), watch: true}
	in := func(owner string) bool {
		if dept == "" {
			return true
		}
		if strings.TrimSpace(owner) == "" {
			owner = "Product"
		}
		return proj.InScope(dept, owner)
	}
	next := leadObserveState{Since: t.now, Statuses: map[string]string{}, Shelf: map[string]bool{}, Threads: map[string]int{}, Live: map[string]bool{}}

	// Due jobs: said once per period; whoever starts them, it isn't this tick.
	if len(jobs.List(proj)) > 0 {
		if jin, err := jobInput(ctx, nil); err == nil {
			for _, s := range jobs.Evaluate(proj, issues, jin) {
				if s.Due && !s.LastOpen && in(s.Department) {
					t.say("job:"+s.Key+":"+s.Period, fmt.Sprintf("job: %s is due (%s): %s", s.Key, s.Period, tracker.InjectProject(proj, "issue-cli job run "+s.Key)))
				}
			}
		}
	}

	// Issues: status moves and new comments.
	var scoped []*tracker.Issue
	for _, issue := range issues {
		if in(proj.DepartmentOf(issue)) {
			scoped = append(scoped, issue)
		}
	}
	from := prev.Since.Truncate(time.Second).Add(-time.Second)
	type line struct {
		at        time.Time
		key, text string
	}
	var news []line
	for _, issue := range scoped {
		next.Statuses[issue.Slug] = issue.Status
		if first {
			continue
		}
		say := t.held
		if why := observeStatusNews(proj, issue); why != "" {
			say = func(line string) { t.note(line + " (" + why + ")") }
		}
		if was, ok := prev.Statuses[issue.Slug]; ok && was != issue.Status {
			say(fmt.Sprintf("status: %s %s → %s", issue.Slug, was, issue.Status))
		} else if !ok {
			say(fmt.Sprintf("status: %s new at %s", issue.Slug, issue.Status))
		}
		for _, c := range issue.Comments() {
			who := tracker.CommentAuthor(c)
			at, ok := tracker.CommentTime(c.Date)
			if !ok || at.Before(from) || (me != "" && who == me) {
				continue
			}
			text := strings.Join(strings.Fields(c.Text), " ")
			if r := []rune(text); len(r) > 100 {
				text = string(r[:100]) + "…"
			}
			news = append(news, line{at, fmt.Sprintf("comment:%s:%d", issue.Slug, c.ID), fmt.Sprintf("comment: %s %s: %s", issue.Slug, valueOr(who, "someone"), text)})
		}
	}
	sort.SliceStable(news, func(i, j int) bool { return news[i].at.Before(news[j].at) })
	for _, n := range news {
		t.say(n.key, n.text)
	}

	// The shelf: new outputs and new comment messages.
	list, _ := outputs.Load(proj)
	titles := map[string]string{}
	for _, o := range list {
		if !in(o.Name) && !in(o.Department) {
			continue
		}
		next.Shelf[o.ID] = true
		titles[o.ID] = o.Title
		if !first && !prev.Shelf[o.ID] {
			t.note(fmt.Sprintf("shelf: %s %q (%s)", valueOr(o.Name, o.Department), o.Title, o.ID))
		}
	}
	threads, _ := outputs.LoadComments(proj)
	for id, ts := range threads {
		if _, ok := titles[id]; !ok {
			continue
		}
		for _, th := range ts {
			next.Threads[th.ID] = len(th.Messages)
			if first || len(th.Messages) <= prev.Threads[th.ID] || len(th.Messages) == 0 {
				continue
			}
			m := th.Messages[len(th.Messages)-1]
			text := strings.Join(strings.Fields(m.Text), " ")
			if r := []rune(text); len(r) > 100 {
				text = string(r[:100]) + "…"
			}
			t.note(fmt.Sprintf("shelf comment: %q (%s) %s: %s", titles[id], id, valueOr(m.By, "someone"), text))
		}
	}

	// Workers that ended since the last tick.
	live := liveSessionNames(proj)
	for _, issue := range scoped {
		name := tracker.AgentSessionName(issue.Slug)
		if live[name] {
			next.Live[name] = true
			continue
		}
		if prev.Live[name] {
			t.held(fmt.Sprintf("ended: %s (%s at %s)", name, issue.Slug, issue.Status))
		}
	}

	lead := tracker.LeadSessionName(proj)
	if dept != "" {
		lead = tracker.DepartmentLeadName(proj, dept)
	}
	next.ContextOn = leadTickContext(ctx, t, lead, prev.ContextOn)
	if _, err := renderLeadState(ctx, lead, issues); err != nil {
		t.fail("state", err)
	}
	next.Said = t.seen

	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			if err := jsonfile.Write(path, next, 0o644); err != nil {
				fmt.Fprintf(ctx.Stderr, "warning: saving the observe state: %v\n", err)
			}
		}
	}
	return t.count(), nil
}
