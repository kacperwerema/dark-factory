package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The hall (L2) is Product's factory floor: the stations of workflow.yaml as
// a snake line (along the top to the right, back along the bottom to the
// left), the owner's gates between them, a crate per issue carried by its
// worker, the CI scanner, the parking and the stage still to build (retro and
// feedback). On the right, the selected agent and the hall in numbers.
// Design: issues/Factory/designs/workshop-board/L2.

// hallCrates is how many crates a station draws before "+N more".
const hallCrates = 4

// hallParking is how many parked issues the parking lists.
const hallParking = 4

// HallData is the whole L2 page.
type HallData struct {
	Header  WbHeader
	Lot     MapLot
	Top     []WbHallStop
	Bottom  []WbHallStop
	Done    WbDoneStation
	CI      *WbCIScanner
	Parking WbParking
	Retro   WbPlanCard
	Agent   *WbAgentPanel
	Numbers WbNumbers
	Message WbMessageBox
}

func (s *Server) handleHall(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	data := s.buildHall(proj, prefix, r.URL.Query().Get("agent"), time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "hall.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildHall(p *tracker.Project, prefix, selected string, now time.Time) HallData {
	company := s.buildCompany(p, prefix, now)
	lot := company.Lot
	issues, _ := tracker.LoadIssues(p.IssueDir)
	sort.Slice(issues, func(i, j int) bool { return issues[i].Slug < issues[j].Slug })
	wf := p.LoadWorkflow()
	agents, _ := s.buildAgentsModel(p, prefix)
	// The hall is Product's: issues of the other departments stay in their
	// rooms.
	issues = productIssues(p, issues)
	open := openOnly(issues)
	waiting := waitingOnOwner(p, open)

	d := HallData{Lot: lot, Message: company.Message}
	d.Header = WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(2, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: company.Waiting, HideDone: true, Tabs: wbHallTabs(prefix, "line")}
	d.Header.Crumbs[1].Color = lot.Color
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	d.Header.Crumbs[2] = WbCrumb{Label: "Product hall", Current: true, Color: companyProductColor}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		d.Header.Chips = append(d.Header.Chips, *c)
	}

	// Who works which issue, and what each issue needs from the owner.
	workerOf := map[string]AgentNode{}
	if agents != nil {
		for _, w := range agents.Workers {
			if w.Issue != nil {
				workerOf[strings.ToLower(w.Issue.Slug)] = w
			}
		}
	}
	question := map[string]bool{}
	for _, it := range waiting {
		if it.Kind == "question" {
			question[strings.ToLower(it.Issue.Slug)] = true
		}
	}
	blamed := map[string]bool{}
	ci, _ := ciState(p)
	if ci.IsRed() {
		for _, slug := range ci.Blamed {
			blamed[strings.ToLower(slug)] = true
		}
		if ci.RedIssue != "" {
			blamed[strings.ToLower(ci.RedIssue)] = true
		}
	}
	atGate := map[string]int{}
	for _, it := range waiting {
		if it.Kind == "gate" {
			atGate[statusKey(it.Target)]++
		}
	}
	byStatus := map[string][]*tracker.Issue{}
	for _, issue := range open {
		byStatus[statusKey(issue.Status)] = append(byStatus[statusKey(issue.Status)], issue)
	}

	// The default selection: whoever waits on the owner, else whoever works.
	picked := selected != ""
	if selected == "" {
		for _, issue := range open {
			if w, ok := workerOf[strings.ToLower(issue.Slug)]; ok && workerRobot(w).State == "you" {
				selected = issue.Slug
				break
			}
		}
	}
	if selected == "" {
		for _, issue := range open {
			if _, ok := workerOf[strings.ToLower(issue.Slug)]; ok {
				selected = issue.Slug
				break
			}
		}
	}

	// In YOLO the gates stand raised, and a crate that passed one keeps the
	// yellow marker to the end of the line.
	raised := lot.YOLO != nil && lot.YOLO.On
	marked := yoloMarked(open)
	var stops []WbHallStop
	for _, stop := range factoryLine(wf) {
		if stop.Gated {
			stops = append(stops, WbHallStop{Gate: &WbGateCard{Name: "Gate", Status: stop.Name, Waiting: atGate[stop.Key], Raised: raised}})
		}
		st := &WbHallStation{Name: stationName(stop), Status: stop.Name, Who: stop.Description, Owner: stop.Owner}
		here := byStatus[stop.Key]
		st.Count = len(here)
		// Crates with a worker first, those waiting on the owner on top.
		sort.SliceStable(here, func(i, j int) bool {
			return crateRank(workerOf, here[i]) < crateRank(workerOf, here[j])
		})
		for i, issue := range here {
			if i == hallCrates {
				st.More = len(here) - hallCrates
				break
			}
			key := strings.ToLower(issue.Slug)
			c := WbCrate{Slug: issue.Slug, Title: issue.Title, URL: prefix + "/hall?agent=" + issue.Slug + "#agent", Question: question[key], CI: blamed[key], YOLO: marked[key], Selected: strings.EqualFold(issue.Slug, selected)}
			if w, ok := workerOf[key]; ok {
				r := workerRobot(w)
				c.Robot = &r
				if r.State == "idle" && w.LastCall != "" {
					if at, err := time.Parse(time.RFC3339, w.LastCall); err == nil {
						c.Note = "last issue-cli " + shortDuration(now.Sub(at)) + " ago — a long build?"
					}
				}
			}
			st.Crates = append(st.Crates, c)
		}
		stops = append(stops, WbHallStop{Station: st})
	}
	half := (len(stops) + 1) / 2
	d.Top, d.Bottom = stops[:half], stops[half:]

	stack := min(lot.DoneWeek, 9)
	d.Done = WbDoneStation{Count: lot.DoneWeek, Stack: make([]int, stack), Who: "merged to main; releases are cut from here"}

	if ci != nil {
		d.CI = &WbCIScanner{Red: ci.IsRed(), Command: "make validate", Branch: ci.Branch, Issue: ci.RedIssue}
		if ci.Failed != "" {
			d.CI.Command = ci.Failed
		}
		if n := len(ci.Failures); n > 0 {
			d.CI.Detail = fmt.Sprintf("%d failing", n)
		}
	}

	for _, issue := range issues {
		if statusKey(issue.Status) != "parked" {
			continue
		}
		d.Parking.Count++
		if len(d.Parking.Items) < hallParking {
			reason := ownerAttention(p, issue).ParkReason
			if reason == "" {
				reason = "no reason recorded"
			}
			d.Parking.Items = append(d.Parking.Items, WbParked{Slug: issue.Slug, URL: prefix + "/issue/" + issue.Slug, Reason: reason})
		}
	}

	d.Retro = WbPlanCard{
		Label: "MISSING STAGE · TO BUILD", Title: "Retro and feedback",
		Text:     "You already have retros/, report-bug, Stats and the Timeline. Put them into a station that closes the loop: done → idea.",
		LinkText: "See the retros", LinkURL: prefix + "/retros", TestID: "retro",
	}

	if selected != "" {
		if issue := tracker.FindIssue(issues, selected); issue != nil {
			d.Agent = hallAgentPanel(p, prefix, wf, issue, workerOf, blamed, ci != nil, now)
			// Only the owner's own pick addresses the box, and never with
			// approval words: one stray Enter must not approve anything.
			if picked {
				d.Message.Prefill = issue.Slug + ": "
			}
		}
	}

	d.Numbers = hallNumbers(issues, waiting, lot.DoneWeek, now)
	return d
}

// stationName is the station's sign: the status in words.
func stationName(stop lineStop) string {
	if stop.Owner {
		return "Human test"
	}
	name := stop.Name
	return strings.ToUpper(name[:1]) + name[1:]
}

// crateRank orders crates on a station: waiting on the owner, working,
// idle, no worker.
func crateRank(workerOf map[string]AgentNode, issue *tracker.Issue) int {
	w, ok := workerOf[strings.ToLower(issue.Slug)]
	if !ok {
		return 3
	}
	switch workerRobot(w).State {
	case "you":
		return 0
	case "working":
		return 1
	}
	return 2
}

// hallAgentPanel is the selected issue and its worker: where it is on the
// line, its session and worktree, its checklist, proofs and review.
func hallAgentPanel(p *tracker.Project, prefix string, wf *tracker.WorkflowConfig, issue *tracker.Issue, workerOf map[string]AgentNode, blamed map[string]bool, hasCI bool, now time.Time) *WbAgentPanel {
	a := &WbAgentPanel{Slug: issue.Slug, Title: issue.Title, URL: prefix + "/issue/" + issue.Slug, Robot: WbRobot{State: "idle", Label: issue.Slug}, StateLabel: "No worker", Session: tracker.LeadSessionName(p)}
	w, live := workerOf[strings.ToLower(issue.Slug)]
	if live {
		a.Robot = workerRobot(w)
		a.StateLabel = strings.ToUpper(a.Robot.StateLabel()[:1]) + a.Robot.StateLabel()[1:]
		if w.Gate != "" {
			a.StateLabel += " · gate " + w.Gate
		}
		a.Facts = append(a.Facts, WbFact{Label: "tmux", Value: w.Name, Mono: true})
		if w.LastCall != "" {
			if at, err := time.Parse(time.RFC3339, w.LastCall); err == nil {
				a.Facts = append(a.Facts, WbFact{Label: "issue-cli", Value: shortDuration(now.Sub(at)) + " ago · " + clipRunes(w.LastCmd, 60)})
			}
		}
	}
	a.Facts = append(a.Facts, WbFact{Label: "status", Value: issue.Status, Mono: true})
	if path, _, ok := tracker.ResolveWorktree(projectRoot(p), issue.Slug, wf); ok {
		if rel, err := filepath.Rel(projectRoot(p), path); err == nil {
			path = rel
		}
		a.Facts = append(a.Facts, WbFact{Label: "worktree", Value: path, Mono: true})
	}
	if hasCI {
		if blamed[strings.ToLower(issue.Slug)] {
			a.Facts = append(a.Facts, WbFact{Label: "CI", Value: "red since it landed", Tone: "red"})
		} else {
			a.Facts = append(a.Facts, WbFact{Label: "CI", Value: "not blamed for red", Tone: "green"})
		}
	}

	// Where the issue is on the line: done, now, still to come.
	reached := true
	for _, stop := range factoryLine(wf) {
		step := WbPathStep{Status: stop.Name, Tone: "todo"}
		switch {
		case strings.EqualFold(stop.Name, issue.Status):
			step.Tone, reached = "now", false
		case reached:
			step.Tone = "done"
		}
		a.Path = append(a.Path, step)
	}

	total, checked := tracker.CountCheckboxes(issue.BodyRaw)
	a.Stats = append(a.Stats, WbStat{Value: fmt.Sprintf("%d/%d", checked, total), Label: "checklist"})
	store, _ := tracker.LoadData(issue.FilePath)
	proofs := 0
	for _, links := range store.Proofs {
		proofs += len(links)
	}
	a.Stats = append(a.Stats, WbStat{Value: fmt.Sprint(proofs), Label: "proofs"})
	resolved := 0
	for _, e := range store.Entries {
		if strings.Contains(strings.ToLower(e.Status), "resolved") || strings.Contains(e.Status, "✅") {
			resolved++
		}
	}
	a.Stats = append(a.Stats, WbStat{Value: fmt.Sprintf("%d/%d", resolved, len(store.Entries)), Label: "review resolved"})
	return a
}

// hallNumbers is the hall over the last 7 days: done, the average time from
// created to done, and where work waits longest on the owner.
func hallNumbers(issues []*tracker.Issue, waiting []WaitingItem, doneWeek int, now time.Time) WbNumbers {
	n := WbNumbers{Title: "The hall in numbers · 7 days"}
	n.Rows = append(n.Rows, WbFact{Label: "Done", Value: fmt.Sprint(doneWeek)})
	var sum time.Duration
	count := 0
	for _, issue := range issues {
		if !strings.EqualFold(issue.Status, "done") {
			continue
		}
		at, _ := doneAt(issue)
		created, err := time.ParseInLocation("2006-01-02", issue.Created, time.Local)
		if err != nil || now.Sub(at) > mapDoneWindow || at.Before(created) {
			continue
		}
		sum += at.Sub(created)
		count++
	}
	if count > 0 {
		n.Rows = append(n.Rows, WbFact{Label: "Avg. created → done", Value: fmt.Sprintf("%.1f days", sum.Hours()/24/float64(count))})
	}
	if len(waiting) > 0 {
		oldest := waiting[0]
		for _, it := range waiting {
			if it.Since.Before(oldest.Since) {
				oldest = it
			}
		}
		where := "gate " + oldest.Target
		if oldest.Kind != "gate" {
			where = oldest.Kind
		}
		n.Rows = append(n.Rows, WbFact{Label: "Waits longest on you", Value: where + " · " + shortDuration(now.Sub(oldest.Since)), Tone: "you"})
	}
	return n
}
