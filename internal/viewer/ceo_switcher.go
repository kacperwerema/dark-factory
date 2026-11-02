package viewer

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The CEO chat's switcher (?part=switcher on /p/<project>/lead): every
// project's CEO and its department leads, each with what it is doing, read
// only when the owner opens the sheet. /leads shares the rows (leadRows).
// Opening it or following a row never starts a lead: rows are links.

// Lead states, the switcher's and /leads' chips.
const (
	leadWorking = "working"
	leadYou     = "you"
	leadIdle    = "idle"
	leadUnknown = "unknown"
	leadOff     = "off"
)

// leadStateText is a state in words.
var leadStateText = map[string]string{
	leadWorking: "working",
	leadYou:     "waits on you",
	leadIdle:    "idle",
	leadUnknown: "couldn't read its screen",
	leadOff:     "not running",
}

// leadStateBudget is how long the switcher waits for each pane and each
// project's Needs-you count; a capture still running then is killed.
var leadStateBudget = 2 * time.Second

// leadStateLines is the scrollback a state read captures: enough for the
// working line, a menu or a question widget at the bottom.
const leadStateLines = 80

// leadCaptures are the state reads still running, by target: a second
// open waits for the running one's answer instead of starting another tmux
// child, so a hung tmux never piles them up.
var leadCaptures sync.Map

// leadCapture is one running state read; state is set before done closes.
type leadCapture struct {
	done  chan struct{}
	state string
}

// paneLeadState is the state a live lead's cleaned pane shows: a menu or a
// question it waits on, its working line, or nothing (idle). An empty pane
// is unknown: nothing to read is not the same as idle.
func paneLeadState(pane string) string {
	if pane == "" {
		return leadUnknown
	}
	cp := parseCEOPane(pane)
	switch {
	case cp.Prompt != nil || cp.Ask != nil:
		return leadYou
	case cp.Working != nil:
		return leadWorking
	}
	return leadIdle
}

// readLeadState is the state of the lead called name: off when its session
// is not live, else what its pane shows, read under ctx.
func readLeadState(ctx context.Context, p *tracker.Project, name string) string {
	target := agentrun.Target(p, name)
	if !agentrun.HasSession(target) {
		return leadOff
	}
	c := &leadCapture{done: make(chan struct{})}
	if v, busy := leadCaptures.LoadOrStore(target, c); busy {
		running := v.(*leadCapture)
		select {
		case <-running.done:
			return running.state
		case <-ctx.Done():
			return leadUnknown
		}
	}
	defer func() {
		leadCaptures.Delete(target)
		close(c.done)
	}()
	pane, err := agentrun.CaptureHistoryCtx(ctx, target, leadStateLines)
	if err != nil || ctx.Err() != nil {
		c.state = leadUnknown
	} else {
		c.state = paneLeadState(cleanPane(pane))
	}
	return c.state
}

// leadRows are every configured project's CEO and department leads, the
// rows /leads and the switcher draw. withState reads each lead's pane
// (live leads only, in parallel, within leadStateBudget); without it a row
// is only live or off. Each CEO row carries the project's Needs-you count,
// the same count as needs-you.json; a count not ready within the budget
// (withState only) is left out (-1).
func (s *Server) leadRows(withState bool) []LeadRow {
	ctx, cancel := context.WithTimeout(context.Background(), leadStateBudget)
	defer cancel()
	now := time.Now()
	rows := make([]LeadRow, len(s.projects))
	var wg sync.WaitGroup
	// state sets *dst from the lead called name: read now, or live/off.
	state := func(p *tracker.Project, name string, dst *string) {
		if !withState {
			if agentrun.IsLive(p, name) {
				*dst = leadIdle
			} else {
				*dst = leadOff
			}
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			*dst = readLeadState(ctx, p, name)
		}()
	}
	for i := range s.projects {
		p := &s.projects[i]
		name := tracker.LeadSessionName(p)
		rows[i] = LeadRow{
			Name:    p.Name,
			Slug:    p.Slug,
			Session: agentDisplayName(p, name),
			Live:    agentrun.IsLive(p, name),
			ChatURL: "/p/" + p.Slug + "/lead",
			Color:   s.projectColor(p),
		}
		state(p, name, &rows[i].State)
		var nested func(d tracker.Department) LeadDeptRow
		nested = func(d tracker.Department) LeadDeptRow {
			lead := tracker.DepartmentLeadName(p, d.Name)
			r := LeadDeptRow{
				Department: d.Name, Key: tracker.DepartmentKey(d.Name), Session: agentDisplayName(p, lead), Live: agentrun.IsLive(p, lead),
				ChatURL: "/p/" + p.Slug + "/lead" + chatQuery(&d),
			}
			for _, c := range p.Children(d.Name) {
				r.Children = append(r.Children, nested(c))
			}
			return r
		}
		for _, d := range p.Departments {
			if p.GroupOf(d.Name) != nil {
				continue
			}
			rows[i].Departments = append(rows[i].Departments, nested(d))
		}
		var walk func(ds []LeadDeptRow)
		walk = func(ds []LeadDeptRow) {
			for j := range ds {
				state(p, tracker.DepartmentLeadName(p, ds[j].Department), &ds[j].State)
				walk(ds[j].Children)
			}
		}
		walk(rows[i].Departments)
	}
	counts := s.needsYouCounts(withState, now)
	wg.Wait()
	for i := range rows {
		rows[i].Waiting = counts[i]
	}
	return rows
}

// needsYouCounts is each project's Needs-you count (len(needsYouRows)), in
// s.projects order. bounded waits at most leadStateBudget and leaves -1 for
// a project not counted by then; its count finishes in the background.
func (s *Server) needsYouCounts(bounded bool, now time.Time) []int {
	counts := make([]int, len(s.projects))
	done := make(chan struct{})
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range s.projects {
		counts[i] = -1
		wg.Add(1)
		p := &s.projects[i]
		run := func() {
			defer wg.Done()
			n := len(needsYouRows(p, s.projectColor(p), "/p/"+p.Slug, openIssuesOf(p), now))
			mu.Lock()
			counts[i] = n
			mu.Unlock()
		}
		if bounded {
			s.bg.Go("needs-you count "+p.Slug, run)
		} else {
			go run()
		}
	}
	go func() { wg.Wait(); close(done) }()
	if bounded {
		select {
		case <-done:
		case <-time.After(leadStateBudget):
		}
	} else {
		<-done
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]int(nil), counts...)
}

// CEOSwitcher is the switcher's sheet: every project's leads, the one the
// page talks to marked, and when the states were read.
type CEOSwitcher struct {
	Rows []LeadRow
	// Current is the chat's URL (/p/<slug>/lead[?dept=<key>]).
	Current string
	At      string
}

func (s *Server) handleCEOSwitcher(w http.ResponseWriter, proj *tracker.Project, dept *tracker.Department) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	sw := CEOSwitcher{Rows: s.leadRows(true), Current: "/p/" + proj.Slug + "/lead" + chatQuery(dept), At: time.Now().Format("15:04")}
	if err := s.tmpl.ExecuteTemplate(w, "wb/ceo-switcher", sw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// switchLead is one department lead row in the switcher: its project's
// slug, the current chat's URL and how deep it nests (1 under the CEO).
type switchLead struct {
	Slug, Current string
	Depth         int
	Row           LeadDeptRow
}

// Next is the depth of the row's children.
func (s switchLead) Next() int { return s.Depth + 1 }

func newSwitchLead(slug, current string, depth int, row LeadDeptRow) switchLead {
	return switchLead{Slug: slug, Current: current, Depth: depth, Row: row}
}

func leadStateWords(state string) string { return leadStateText[state] }
