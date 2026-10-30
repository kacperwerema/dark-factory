package main

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The decisions log (L2, a tab of the Product hall): every issue's
// `## Decisions` D-n lines, grouped by day and then by issue. Nobody edits
// it; agents and the CEO add a line with `issue-cli decide`. A decision
// without the owner's words is boxed orange, with Confirm and Question it
// going to the CEO through /lead/send. The classic table stays at
// ?classic=1. Design: issues/Factory/designs/workshop-board/L2-decisions.

// Who-filter values (?who=).
const (
	decisionsWhoWords = "words"
	decisionsWhoAlone = "alone"
)

// decisionsCitedMax is how many citing issues a row links before "+N".
const decisionsCitedMax = 3

// DecisionsWbData is the whole decisions log page.
type DecisionsWbData struct {
	Header     WbHeader
	Count      WbStat
	Summary    WbSummaryBox
	Search     WbSearchBar
	Days       []WbDecisionDay
	Total      int  // every decision in the project
	NoMatch    bool // decisions exist, none passes the filters
	ClearURL   string
	ClassicURL string
	BySystem   WbBars
	Hygiene    WbNumbers
	HygieneTip string
	Add        WbCommandCard
	Message    WbMessageBox
}

// WbSummaryBox is a one-line verdict in a box: a tick when OK, a warning
// otherwise.
type WbSummaryBox struct {
	Text, Key string
	OK        bool
}

// WbSearchBar is a GET search box with filter pills. Keep carries the
// other filters through a search; Key names it (search.<Key>).
type WbSearchBar struct {
	Action, Name, Value, Placeholder, Key string
	Keep                                  []WbHiddenField
	Filters                               []WbFilterPill
}

// WbHiddenField is a hidden form field.
type WbHiddenField struct{ Name, Value string }

// WbFilterPill is a filter as a link. Tone "you" marks the owner's filter
// (orange); a pill with Remove is an applied filter to drop. Key names it
// (filter.<Key>).
type WbFilterPill struct {
	Label, URL, Tone, Key string
	On, Remove            bool
}

// WbDecisionDay is one day of the log.
type WbDecisionDay struct {
	Label   string
	Threads []WbDecisionThread
}

// WbDecisionThread is one issue's decisions on a day.
type WbDecisionThread struct {
	Title, Slug, URL  string
	System, SystemURL string
	Status            string
	Rows              []WbDecisionRow
}

// WbDecisionRow is one D-n line.
type WbDecisionRow struct {
	ID, Ref      string
	Text         template.HTML
	Quote        string
	By, ByTone   string // ByTone: ceo, agent or other
	Cited        []WbLink
	CitedMore    int
	RefersTo     []WbLink
	ReplacedBy   *WbLink
	Also         string
	State        WbChip
	Confirm      *WbConfirm
	Slug         string
	Stale, Faded bool
	// YOLO: recorded while YOLO was on, without the owner's words.
	YOLO bool
}

// WbLink is a labelled link.
type WbLink struct{ Label, URL string }

// WbConfirm is the orange box on a decision the agent took alone: Confirm
// sends ConfirmText to the CEO; Question it fills the message box with
// QuestionText for the owner to finish.
type WbConfirm struct {
	SendURL, ConfirmText, QuestionText string
	Live                               bool
}

// WbBars is a titled list of labelled bars (a count per row).
type WbBars struct {
	Title, Empty, Key string
	Rows              []WbCountBar
}

// WbCountBar is one bar: Pct of the longest row.
type WbCountBar struct {
	Label, URL string
	Count, Pct int
	On         bool
}

// WbCommandCard is a titled note with the command to run.
type WbCommandCard struct {
	Title, Text, Command, Key string
}

func (s *Server) handleDecisionsWb(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(proj))
	now := time.Now()
	q := r.URL.Query()
	data := buildDecisionsWb(index.Entries, prefix, decisionsFilter{
		Query:  strings.TrimSpace(q.Get("q")),
		Who:    q.Get("who"),
		System: q.Get("system"),
		State:  strings.ToLower(q.Get("state")),
	}, now)
	lot, waiting := s.decisionsLot(proj, now)
	data.Header = WbHeader{Crumbs: wbCrumbs(2, proj.Name, prefix+"/company"), Tabs: wbHallTabs(prefix, "decisions"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, Waiting: waiting, HideDone: true}
	data.Header.Crumbs[1].Color = lot.Color
	data.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	// Decisions is a tab of the hall: the hall stays the current level.
	data.Header.Crumbs[2] = WbCrumb{Label: "Product hall", Current: true, Color: companyProductColor}
	data.Message = WbMessageBox{Session: lot.CEO.Session, SendURL: prefix + "/lead/send", SpawnURL: prefix + "/lead/spawn", Live: lot.CEO.Live}
	for di := range data.Days {
		for ti := range data.Days[di].Threads {
			for _, row := range data.Days[di].Threads[ti].Rows {
				if row.Confirm != nil {
					row.Confirm.SendURL, row.Confirm.Live = data.Message.SendURL, lot.CEO.Live
				}
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "decisions_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// decisionsLot is the project's lot (counts, CEO) and how much waits on the
// owner, for the header and the message box.
func (s *Server) decisionsLot(p *tracker.Project, now time.Time) (MapLot, int) {
	color := p.Color
	if color == "" {
		for i := range s.projects {
			if s.projects[i].Slug == p.Slug {
				color = mapProjectColors[i%len(mapProjectColors)]
			}
		}
	}
	lot, queue := s.buildLot(p, color, now)
	return lot, len(queue)
}

// decisionsFilter is the page's query: search text, whose words, system and
// state (the classic page's filters, kept for links from Systems).
type decisionsFilter struct {
	Query, Who, System, State string
}

func (f decisionsFilter) values() url.Values {
	v := url.Values{}
	for k, val := range map[string]string{"q": f.Query, "who": f.Who, "system": f.System, "state": f.State} {
		if val != "" {
			v.Set(k, val)
		}
	}
	return v
}

func (f decisionsFilter) url(prefix string) string {
	if v := f.values(); len(v) > 0 {
		return prefix + "/decisions?" + v.Encode()
	}
	return prefix + "/decisions"
}

func (f decisionsFilter) match(e *tracker.DecisionEntry) bool {
	if f.System != "" && !strings.EqualFold(e.Issue.System, f.System) {
		return false
	}
	if !matchesDecisionStateFilter(e, f.State) {
		return false
	}
	switch f.Who {
	case decisionsWhoWords:
		if e.Quote == "" {
			return false
		}
	case decisionsWhoAlone:
		if e.Quote != "" {
			return false
		}
	}
	if f.Query == "" {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{e.Ref, e.Text, e.Quote, e.How, e.AlsoConsidered, e.Issue.Title, e.Issue.System}, "\n"))
	for _, word := range strings.Fields(strings.ToLower(f.Query)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

// buildDecisionsWb lays out the log from the index's entries (newest
// first): the counts over every decision, the rows that pass f.
func buildDecisionsWb(entries []*tracker.DecisionEntry, prefix string, f decisionsFilter, now time.Time) DecisionsWbData {
	d := DecisionsWbData{Total: len(entries), ClearURL: prefix + "/decisions", ClassicURL: prefix + "/decisions?classic=1"}
	var active, deferred, replaced, stale, cited int
	systems := map[string]int{}
	for _, e := range entries {
		switch e.State {
		case tracker.DecisionActive:
			active++
		case tracker.DecisionDeferred:
			deferred++
		case tracker.DecisionStale:
			stale++
		}
		if e.ReplacedBy != nil {
			replaced++
		}
		if len(e.CitedOpen)+len(e.CitedClosed) > 0 {
			cited++
		}
		if e.Issue.System != "" {
			systems[e.Issue.System]++
		}
	}

	d.Count = WbStat{Value: fmt.Sprint(len(entries)), Label: "decisions"}
	switch {
	case len(entries) == 0:
	case active == len(entries):
		d.Count.Label += " · all active"
	default:
		d.Count.Label += fmt.Sprintf(" · %d active", active)
	}
	d.Summary = WbSummaryBox{Text: fmt.Sprintf("%d superseded · %d stale citations", replaced, stale), OK: stale == 0, Key: "decisions"}

	d.Search = WbSearchBar{Action: prefix + "/decisions", Name: "q", Value: f.Query, Placeholder: "e.g. pantry, limit, label", Key: "decisions"}
	for _, k := range []struct{ name, val string }{{"who", f.Who}, {"system", f.System}, {"state", f.State}} {
		if k.val != "" {
			d.Search.Keep = append(d.Search.Keep, WbHiddenField{Name: k.name, Value: k.val})
		}
	}
	for _, p := range []struct{ who, label, tone string }{
		{"", "All", ""},
		{decisionsWhoWords, "With your words", ""},
		{decisionsWhoAlone, "Without your words · to confirm", "you"},
	} {
		g := f
		g.Who = p.who
		id := p.who
		if id == "" {
			id = "all"
		}
		d.Search.Filters = append(d.Search.Filters, WbFilterPill{Label: p.label, URL: g.url(prefix), Tone: p.tone, On: f.Who == p.who, Key: "who_" + id})
	}
	if f.System != "" {
		g := f
		g.System = ""
		d.Search.Filters = append(d.Search.Filters, WbFilterPill{Label: "System: " + f.System, URL: g.url(prefix), Remove: true, Key: "clear_system"})
	}
	if f.State != "" {
		g := f
		g.State = ""
		d.Search.Filters = append(d.Search.Filters, WbFilterPill{Label: "State: " + f.State, URL: g.url(prefix), Remove: true, Key: "clear_state"})
	}

	// The log: by day, then by issue in order of first appearance.
	var day *WbDecisionDay
	var dayKey string
	threadOf := map[string]int{}
	for _, e := range entries {
		if !f.match(e) {
			continue
		}
		key := e.Date.Format("2006-01-02")
		if day == nil || key != dayKey {
			d.Days = append(d.Days, WbDecisionDay{Label: decisionsDayLabel(e.Date, now)})
			day, dayKey = &d.Days[len(d.Days)-1], key
			threadOf = map[string]int{}
		}
		ti, ok := threadOf[e.Slug]
		if !ok {
			th := WbDecisionThread{Title: e.Issue.Title, Slug: e.Slug, URL: prefix + "/issue/" + e.Slug, System: e.Issue.System, Status: e.Issue.Status}
			if th.Title == "" {
				th.Title = e.Slug
			}
			if th.System != "" {
				g := f
				g.System = th.System
				th.SystemURL = g.url(prefix)
			}
			day.Threads = append(day.Threads, th)
			ti = len(day.Threads) - 1
			threadOf[e.Slug] = ti
		}
		day.Threads[ti].Rows = append(day.Threads[ti].Rows, decisionsRow(e, prefix))
	}
	d.NoMatch = len(entries) > 0 && len(d.Days) == 0

	d.BySystem = WbBars{Title: "By system", Empty: "No decision sits in an issue with a system yet.", Key: "decisions_by_system"}
	maxN := 0
	for name, n := range systems {
		g := f
		g.System = name
		d.BySystem.Rows = append(d.BySystem.Rows, WbCountBar{Label: name, Count: n, URL: g.url(prefix), On: strings.EqualFold(name, f.System)})
		maxN = max(maxN, n)
	}
	sort.Slice(d.BySystem.Rows, func(i, j int) bool {
		a, b := d.BySystem.Rows[i], d.BySystem.Rows[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Label < b.Label
	})
	for i := range d.BySystem.Rows {
		d.BySystem.Rows[i].Pct = d.BySystem.Rows[i].Count * 100 / maxN
	}

	citedValue := "none yet"
	if cited > 0 {
		citedValue = plural(cited, "decision", "decisions")
	}
	staleTone := "green"
	if stale > 0 {
		staleTone = "red"
	}
	d.Hygiene = WbNumbers{Title: "Log hygiene", Rows: []WbFact{
		{Label: "Deferred", Value: fmt.Sprint(deferred)},
		{Label: "Superseded", Value: fmt.Sprint(replaced)},
		{Label: "Stale citations", Value: fmt.Sprint(stale), Tone: staleTone},
		{Label: "Cited by other issues", Value: citedValue},
	}}
	example := "a D-n"
	if len(entries) > 0 {
		example = entries[0].ID
	}
	d.HygieneTip = "A decision nobody cites is easy to lose. When an agent cites " + example + " in a new issue, the log shows it still holds the code in line."
	d.Add = WbCommandCard{Title: "Add a decision", Text: "Tell the CEO in chat, or an agent records it itself:", Command: "issue-cli decide <slug> --text \"…\"", Key: "decide"}
	return d
}

// decisionsRow is one entry as a row of the log.
func decisionsRow(e *tracker.DecisionEntry, prefix string) WbDecisionRow {
	row := WbDecisionRow{ID: e.ID, Ref: e.Ref, Text: boldOnly(e.Text), Quote: e.Quote, Also: e.AlsoConsidered, Slug: e.Slug}
	row.By, row.ByTone = decisionsBy(e.How)
	row.YOLO = yoloDecision(e.How)
	cites := append(append([]*tracker.Issue(nil), e.CitedOpen...), e.CitedClosed...)
	for i, c := range cites {
		if i == decisionsCitedMax {
			row.CitedMore = len(cites) - decisionsCitedMax
			break
		}
		row.Cited = append(row.Cited, WbLink{Label: c.Slug, URL: prefix + "/issue/" + c.Slug})
	}
	for _, ref := range e.Replaces {
		slug, id, _ := strings.Cut(ref, "#")
		label := ref
		if slug == e.Slug {
			label = id
		}
		row.RefersTo = append(row.RefersTo, WbLink{Label: label, URL: prefix + "/issue/" + slug})
	}
	if r := e.ReplacedBy; r != nil {
		label := r.Ref
		if r.Slug == e.Slug {
			label = r.ID
		}
		row.ReplacedBy = &WbLink{Label: label, URL: prefix + "/issue/" + r.Slug}
	}
	switch e.State {
	case tracker.DecisionActive:
		row.State = WbChip{Tone: "green", Text: "active"}
	case tracker.DecisionDeferred:
		row.State = WbChip{Tone: "idle", Text: "deferred"}
	case tracker.DecisionReplaced:
		row.State, row.Faded = WbChip{Tone: "idle", Text: "superseded"}, true
	case tracker.DecisionStale:
		row.State, row.Stale = WbChip{Tone: "red", Text: "superseded · still cited"}, true
	}
	// Only a decision still in force asks for the owner's words.
	if e.Quote == "" && e.ReplacedBy == nil {
		row.Confirm = &WbConfirm{
			ConfirmText:  fmt.Sprintf("I confirm %s: %q. Record it with my words (issue-cli decide %s --replaces %s).", e.Ref, e.Text, e.Slug, e.Ref),
			QuestionText: fmt.Sprintf("I question %s (%q): ", e.Ref, e.Text),
		}
	}
	return row
}

// decisionsBy is who recorded a decision, from its how: the CEO in the lead
// chat, an agent, or the how as written.
func decisionsBy(how string) (string, string) {
	low := strings.ToLower(how)
	switch {
	case strings.Contains(low, "lead chat"):
		return "CEO (lead chat)", "ceo"
	case low == "" || strings.Contains(low, "agent"):
		return "agent", "agent"
	case yoloDecision(how):
		return "agent in YOLO", "agent"
	}
	return how, "other"
}

// decisionsDayLabel is a day of the log: "Thu 29 Oct", with the year when
// it is not this one.
func decisionsDayLabel(t, now time.Time) string {
	if t.Year() == now.Year() {
		return t.Format("Mon 2 Jan")
	}
	return t.Format("Mon 2 Jan 2006")
}
