package main

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/checks"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The station (L3) is one issue to verify: its belt (the path through the
// statuses, time per status, the owner's gates), where its time went, the
// owner's move (guided checks, playtest, a question, then the stamp: the
// gate approval with the owner's words), the quality check (decisions, the
// change, tests and proofs, the checklist), the timeline and, on the right,
// its agent, sessions, body sections and fields. It reads what the classic
// detail page reads (timeline sources, attention, proofs, the work
// branch's commits) and invents nothing: an element with no data is left
// out. ?classic=1 (and the classic tabs, ?tab=) keep the old page.
// Design: issues/Factory/designs/workshop-board/L3-station.

// stationTimelineRows is how many timeline rows the station lists.
const stationTimelineRows = 40

// stationFiles is how many changed files the change card lists.
const stationFiles = 12

// StationData is the whole L3 page.
type StationData struct {
	Header WbHeader
	Title  WbIssueTitle
	Belt   WbBelt
	Time   WbTimeSplit
	Move   WbYourMove
	// YOLO is "Passed in YOLO": the gates this issue passed without the
	// owner, each with Agree / Back to <status>; nil when none waits.
	YOLO       *WbYOLOReview
	Quality    []WbQualityCard
	Decisions  WbDecisions
	Change     WbChangeCard
	Tests      WbTestsCard
	Timeline   WbTimelineList
	Agent      WbStationAgent
	Sessions   WbSessionList
	Outputs    *WbStationOutputs
	Body       WbBodyIndex
	Fields     WbFields
	IssueURL   string
	ClassicURL string
	// What the station took over from the classic page (station_more.go):
	// the brief, what you're approving, mockups, relations, an epic's
	// children, the body itself and the sources it could not read.
	Brief     *WbBrief
	Approving WbApproving
	Mockups   *WbMockups
	Relations *WbRelations
	Epic      *WbEpicChildren
	FullBody  WbBody
	Missing   []string
}

// WbStationOutputs is what the issue made, from the outputs shelf.
type WbStationOutputs struct {
	Rows     []WbShelfRow
	ToReview int
	URL      string
}

// WbIssueTitle is the station's title: the crate, the title, its chips and
// slug.
type WbIssueTitle struct {
	Title, Slug string
	Chips       []WbChip
	ClassicURL  string
	// EditURL renames the issue (POST {title}); DeleteURL deletes it, then
	// the page goes to StoreURL.
	EditURL, DeleteURL, StoreURL string
	Prefix                       string
}

// WbBelt is an issue's own belt: its path through the statuses.
type WbBelt struct {
	Label, HallURL string
	Steps          []WbBeltStep
}

// WbBeltStep is one status on the belt. State is done, now or next; You
// marks the current status when it waits on the owner; Note is the gate
// line under it ("gate ✓", "gate: you", "your checks").
type WbBeltStep struct {
	Status, Time, Note, State string
	You, NoteYou              bool
}

// WbTimeSplit is "where the time goes": the total and its parts.
type WbTimeSplit struct {
	Total, Label string
	Known        bool
	Parts        []WbTimePart
}

// WbTimePart is one part of the split; Tone is agent, you or idle.
type WbTimePart struct {
	Label, Value, Tone string
	Pct                float64
}

// WbYourMove is the owner's panel: what the issue needs from the owner
// now, step by step, then the stamp. On paints it orange (waits on you).
type WbYourMove struct {
	On          bool
	Title, Meta string
	Text        string
	Tasks       []WbMoveTask
	Stamp       *WbStamp
	Also        []string
	// Command is the lead's approve line, for the owner who'd rather tell
	// the CEO than press the stamp.
	Command string
}

// WbMoveTask is one numbered step before the stamp. Kind is checks,
// playtest or question.
type WbMoveTask struct {
	N                  int
	Kind, Title, Badge string
	Text, Command      string
	RunURL, CommentURL string
	Question, Asked    string
	Sessions           []string
	CommandLabel       string
}

// WbStamp is the gate approval: the targets the status's gated exits offer,
// the owner's words, Stamp it and Send back for fixes. Always usable: Note
// says when steps before it are still open; Approved is the stamp already
// given.
type WbStamp struct {
	N                       int
	Targets                 []string
	Approved                string
	Note                    string
	ApproveURL, SendBackURL string
}

// WbQualityCard is one card of the quality check. Tone is the lamp: you,
// green, red or idle.
type WbQualityCard struct {
	Title, Big, Line, Verdict, Tone, Href, Key string
}

// WbDecisions is the issue's decisions: the owner's words and the agent's
// records.
type WbDecisions struct {
	Items []WbDecision
}

// WbDecision is one D-n. Yours: the owner said it (a quote, not an
// agent's record).
type WbDecision struct {
	ID, Text, Quote, How, Date string
	Also, Replaces             string
	Yours, Deferred            bool
	// YOLO: recorded while YOLO was on, without the owner's words.
	YOLO bool
}

// WbChangeCard is the issue's change: the work branch's commits, the files
// they touched and the areas.
type WbChangeCard struct {
	Range, Head, Unreadable string
	Commits, Files          int
	Add, Del, TestAdd       int
	PerFile                 []WbFileStat
	More                    int
	Areas                   []WbFileStat
}

// WbFileStat is a file (or an area) with its lines added and deleted.
type WbFileStat struct {
	Path     string
	Files    int
	Add, Del int
}

// WbTestsCard is tests and proofs: the criteria proven, the agent's tests:
// line, CI and the checklist by section.
type WbTestsCard struct {
	CI             *WbChip
	Big, BigLabel  string
	Tests, TestsAt string
	Criteria       []WbCriterion
	Groups         []WbCheckGroup
	Done, Total    int
	Short          string
	// Label is the Evidence tab's word for the proofs ("3/4 proven").
	Label *WbChip
}

// WbCriterion is one acceptance criterion and its proof.
type WbCriterion struct {
	ID, Text, Proof string
	Proven          bool
}

// WbCheckGroup is a section's checkboxes. You: the owner ticks them (human
// testing).
type WbCheckGroup struct {
	Name        string
	Done, Total int
	You         bool
	Boxes       []WbBox
}

// WbBox is one checkbox.
type WbBox struct {
	ID, Text string
	Checked  bool
}

// WbTimelineList is the station's timeline: newest first, with filters.
type WbTimelineList struct {
	Filters []WbFilter
	Rows    []WbTimelineRow
	More    int
	FullURL string
	Empty   string
}

// WbFilter is one filter link.
type WbFilter struct {
	Label, URL string
	On         bool
}

// WbTimelineRow is one line on the timeline. Tone is the dot.
type WbTimelineRow struct {
	At, Text, Kind, Tone, Quote string
}

// WbStationAgent is the issue's agent in the side column.
type WbStationAgent struct {
	Robot              WbRobot
	Name, StateLabel   string
	Tone               string
	Live, Known        bool
	Facts              []WbFact
	OpenURL, PromptURL string
	AgentsURL          string
	Dispatch           WbDispatch
}

// WbSessionList is the tmux sessions on the issue.
type WbSessionList struct {
	Rows []WbSessionRow
}

// WbSessionRow is one session; You: it waits on the owner.
type WbSessionRow struct {
	Name, Started string
	You           bool
}

// WbBodyIndex is the issue body by section, with counts.
type WbBodyIndex struct {
	Sections   []WbBodySection
	NotStarted []string
	Comments   int
	EditURL    string
}

// WbBodySection is one section; Tone of its count is green, you or idle.
type WbBodySection struct {
	Title, Count, Tone, URL string
}

// WbFields is the issue's fields: the editable ones (Edit, nil on a page
// that only shows them) and read-only rows under them.
type WbFields struct {
	Title string
	Edit  *WbFieldEdit
	Rows  []WbFieldRow
}

// stationPage reports whether GET /issue/<slug> shows the station: not
// when the classic page is asked for (?classic=1) or one of its tabs.
func stationPage(r *http.Request) bool {
	q := r.URL.Query()
	return q.Get("classic") == "" && q.Get("tab") == ""
}

func (s *Server) handleStation(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string, issue *tracker.Issue, issues []*tracker.Issue) {
	data := s.buildStation(proj, prefix, issue, issues, r.URL.Query().Get("show"), time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "station.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildStation(p *tracker.Project, prefix string, issue *tracker.Issue, issues []*tracker.Issue, show string, now time.Time) StationData {
	wf := p.LoadWorkflowForIssue(issue)
	issueURL := prefix + "/issue/" + issue.Slug
	hallURL := prefix + "/hall?agent=" + url.QueryEscape(issue.Slug) + "#agent"
	d := StationData{IssueURL: issueURL, ClassicURL: issueURL + "?classic=1"}
	d.Outputs = stationOutputs(p, prefix, issue)

	// The header: Map › project › Hall › Station.
	company := s.buildCompany(p, prefix, now)
	lot := company.Lot
	d.Header = WbHeader{WaitingURL: prefix + "/needs-you", Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, Waiting: company.Waiting, HideDone: true}
	d.Header.Crumbs = append(wbCrumbs(1, p.Name, prefix+"/company"), WbCrumb{Label: "Station", Current: true})
	d.Header.Crumbs[1] = WbCrumb{Label: p.Name, URL: prefix + "/company", Color: lot.Color}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	d.Header.Crumbs[2] = WbCrumb{Label: "Hall", URL: hallURL, Color: companyProductColor}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		d.Header.Chips = append(d.Header.Chips, *c)
	}

	// What the page reads, once: the timeline's sources, the owner's
	// attention, the agent's last call, the proofs.
	targets, _ := issueApprovals(wf, issue)
	src := loadTimelineSources(p, issue, wf)
	tl := buildTimeline(src, timelineInput{Status: issue.Status, Now: now, Show: show, WF: wf, OpenGated: len(targets) > 0})
	att := ownerAttention(p, issue)
	ca := newCardAgents(p, prefix, now)
	wt, _, _ := resolveWorktree(ca.workDir, issue.Slug, wf)
	call, known := lastCliCall(p.Slug, agentLogPaths(ca.workDir, wt, tracker.AgentSessionName(issue.Slug), issue.Assignee)...)
	footer := ca.footer(wf, issue, att)
	root, mdDir := projectRoot(p), filepath.Dir(issue.FilePath)
	tab := buildIssueTab(issueTabInput{
		Project: p.Slug, Issue: issue, WF: wf, Src: src, TL: tl, Att: att, Targets: targets,
		Parent: tracker.FindIssue(issues, tracker.ParentSlug(issue)), Footer: footer,
		Epic: epicProgress(p, prefix, issue, issues),
		Call: call, Known: known, Now: now, Prefix: prefix,
		ImgSrc: func(path string) string {
			if m := imgSrcRe.FindStringSubmatch(rewriteRelativeImages(`<img src="`+template.HTMLEscapeString(path)+`">`, prefix, mdDir, root)); m != nil {
				return html.UnescapeString(m[2])
			}
			return path
		},
	})
	report := evidenceReport(p, issue, src.Comments)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	stats, _ := tracker.LoadStats(issue.FilePath)
	ev := buildEvidence(evidenceIn{
		Prefix: prefix, Slug: issue.Slug, Root: root, Status: issue.Status, Report: report,
		Required: wf.ProofRequired(issue), PastDesign: pastDesign(wf, issue.Status),
		Comments: src.Comments, Decisions: src.Decisions, Body: issue.BodyRaw, Transitions: stats.Transitions,
		Change: loadEvidenceChange(root, report.Ref, report.RefName, stats), Total: timelineTotalLabel(p, issue),
		Waiting: tl.Waiting, WaitKnown: tl.Known, CI: tl.CI, Now: now,
		RunPlaytest: playtestRunURL(issueURL, att, wf), PlaytestCommand: wf.PlaytestCommandFor(issue.Slug),
	})

	d.Title = stationTitle(wf, issue, d.ClassicURL)
	d.Title.EditURL, d.Title.DeleteURL, d.Title.StoreURL, d.Title.Prefix = issueURL, issueURL+"/delete", prefix+"/", prefix
	d.Belt = stationBelt(wf, issue, tab.Path, att, hallURL)
	d.Time = stationTime(tl)
	d.Move = stationMove(p.Slug, wf, issue, issueURL, tab.Needs, att, targets, src.Comments, now)
	d.YOLO = stationYOLOReview(issue, wf, src.Comments, issueURL)
	d.Decisions = stationDecisions(src.Decisions)
	d.Change = stationChange(ev.Change)
	d.Tests = stationTests(ev, src.Boxes, tl.CI)
	if lbl := evidenceLabel(report, wf.ProofRequired(issue)); lbl.Text != "" {
		tone := map[string]string{"green": "green", "amber": "you", "red": "red"}[lbl.Class]
		if tone == "" {
			tone = "idle"
		}
		d.Tests.Label = &WbChip{Text: lbl.Text, Tone: tone}
	}
	d.Quality = stationQuality(d, issue)
	d.Timeline = stationTimeline(tl, issueURL, d.ClassicURL, now)
	d.Agent = stationAgent(prefix, issueURL, issue, tab.Agent, footer, wt, root, now)
	d.Agent.Dispatch = stationDispatch(wf, issueURL)
	sessions, _ := sessionsByIssueSlug(p, []*tracker.Issue{issue})
	d.Sessions = stationSessions(sessions[issue.Slug], tracker.AgentSessionName(issue.Slug), d.Move.On)
	d.Body = stationBody(tab.Fold, tab.NotStarted, len(src.Comments), len(src.Decisions), d.ClassicURL, issueURL)
	closed := tracker.ClosedIssueFunc(p)
	d.Fields = WbFields{Title: "FIELDS", Edit: stationFieldEdit(wf, issue, issueURL),
		Rows: stationFieldRows(wf, issue, prefix, detailLead(p, issue, issues).Route, tracker.IssueSharedFiles(projectRoot(p), issue, issues), now)}

	d.Brief = stationBrief(tab)
	d.Approving = stationApproving(tab.Approving, d.Timeline.FullURL, now)
	d.Mockups = stationMockups(tab)
	d.Relations = stationRelations(tracker.IssueDeps(issue, issues, tracker.BuildDecisionIndex(issues, closed), closed), prefix)
	d.Epic = stationEpic(buildEpicTab(wf, issue, issues), issue.Slug, prefix, wf)
	d.FullBody = WbBody{Raw: issue.BodyRaw, Hash: tracker.BodyHash(issue.BodyRaw), URL: issueURL, CheckboxURL: issueURL + "/checkbox",
		Parts: stationBodyParts(issue, prefix, projectRoot(p), stationSlugMap(issues), tab.Fold, d.ClassicURL)}
	d.FullBody.Empty = len(d.FullBody.Parts) == 0
	d.Missing = stationMissing(tl, ev)
	return d
}

// stationTitle is the title with its chips: type, priority, system.
func stationTitle(wf *tracker.WorkflowConfig, issue *tracker.Issue, classic string) WbIssueTitle {
	t := WbIssueTitle{Title: issue.Title, Slug: issue.Slug, ClassicURL: classic}
	typ := issue.Type
	if typ == "" {
		typ = wf.ActiveType
	}
	if typ != "" {
		t.Chips = append(t.Chips, WbChip{Tone: "plain", Text: typ})
	}
	if issue.Priority != "" {
		t.Chips = append(t.Chips, WbChip{Tone: "plain", Text: "priority: " + issue.Priority})
	}
	if issue.System != "" {
		t.Chips = append(t.Chips, WbChip{Tone: "plain", Text: "system: " + issue.System})
	}
	return t
}

// stationBelt is the issue's path with the time in each status and the
// owner's gates.
func stationBelt(wf *tracker.WorkflowConfig, issue *tracker.Issue, path []pathStep, att Attention, hallURL string) WbBelt {
	b := WbBelt{Label: "THE ISSUE'S BELT", HallURL: hallURL}
	if typ := firstNonEmpty(issue.Type, wf.ActiveType); typ != "" {
		b.Label += " · " + strings.ToUpper(typ) + " PATH"
	}
	waits := att.PlaytestDue || att.ChecksDue || att.OpenQuestion || (att.Gate != nil && att.Gate.Ready)
	for _, st := range path {
		step := WbBeltStep{Status: st.Status, State: st.State}
		if st.Dur > 0 {
			step.Time = fmtDur(st.Dur)
		}
		switch st.State {
		case "current":
			step.State = "now"
			step.You = waits
			step.Time = strings.TrimPrefix(step.Time+" · now", " · ")
			switch {
			case att.ChecksDue:
				step.Note, step.NoteYou = "your checks", true
			case att.PlaytestDue:
				step.Note, step.NoteYou = "your playtest", true
			case att.OpenQuestion:
				step.Note, step.NoteYou = "your answer", true
			}
		}
		if step.Note == "" && st.Gate {
			if st.State == "done" {
				step.Note = "gate ✓"
			} else {
				step.Note, step.NoteYou = "gate: you", true
			}
		}
		b.Steps = append(b.Steps, step)
	}
	return b
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// stationTime is the split of the issue's time: agent, waiting on you,
// idle.
func stationTime(tl Timeline) WbTimeSplit {
	t := WbTimeSplit{Known: tl.Known}
	if tl.Total == 0 {
		t.Total, t.Label, t.Known = "—", "nothing recorded yet", true
		return t
	}
	t.Total = fmtDur(tl.Total)
	t.Label = "start to done"
	if tl.InFlight {
		t.Label = "so far"
	}
	if !tl.Known {
		return t
	}
	add := func(label string, d time.Duration, tone string) {
		if d > 0 {
			t.Parts = append(t.Parts, WbTimePart{Label: label, Value: fmtDur(d), Tone: tone, Pct: float64(d) * 100 / float64(tl.Total)})
		}
	}
	add("agent", tl.Working, "agent")
	add("waiting on you", tl.Waiting, "you")
	add("idle", tl.Idle, "idle")
	return t
}

// stationMove is the owner's panel. The steps are what the attention says
// is due (a question, the playtest, the guided checks), then the stamp
// when the status has gated exits; the stamp stays locked until the steps
// are done.
func stationMove(project string, wf *tracker.WorkflowConfig, issue *tracker.Issue, issueURL string, needs needsYou, att Attention, targets []string, comments []tracker.Comment, now time.Time) WbYourMove {
	m := WbYourMove{On: needs.Amber}
	n := 0
	var names []string
	if att.OpenQuestion && len(comments) > 0 {
		n++
		last := comments[len(comments)-1]
		q := strings.TrimSpace(strings.TrimSpace(last.Text)[len("question:"):])
		task := WbMoveTask{N: n, Kind: "question", Title: "Answer the agent's question", Question: q, CommentURL: issueURL + "/comments",
			Text: "Your answer is saved as a comment on the issue; the agent reads it with issue-cli."}
		asked := "asked"
		if author := tracker.CommentAuthor(last); author != "" {
			asked += " by " + author
		}
		if at, ok := tracker.CommentTime(last.Date); ok {
			task.Asked = asked + " " + at.Local().Format("Mon 2 Jan, 15:04")
		} else if asked != "asked" {
			task.Asked = asked
		}
		m.Tasks = append(m.Tasks, task)
		names = append(names, "an answer")
	}
	if att.PlaytestDue {
		n++
		text := "Session " + joinAnd(att.Playtests) + " names this issue and has no run judged by you yet."
		if len(att.Playtests) > 1 {
			text = "Sessions " + joinAnd(att.Playtests) + " name this issue and have no run judged by you yet."
		}
		m.Tasks = append(m.Tasks, WbMoveTask{N: n, Kind: "playtest", Title: "Run the playtest", Badge: fmt.Sprintf("0/%d", len(att.Playtests)),
			Text: text, Command: wf.PlaytestCommandFor(issue.Slug), RunURL: playtestRunURL(issueURL, att, wf), Sessions: att.Playtests})
		names = append(names, "the playtest")
	}
	if att.ChecksDue {
		n++
		text := "Script " + joinAnd(att.Checks) + " walks you through the manual checks and ticks the boxes that pass. It opens in its own tmux window, " + checks.WindowName(issue.Slug) + "."
		if len(att.Checks) > 1 {
			text = "Scripts " + joinAnd(att.Checks) + " walk you through the manual checks and tick the boxes that pass. They open in their own tmux window, " + checks.WindowName(issue.Slug) + "."
		}
		m.Tasks = append(m.Tasks, WbMoveTask{N: n, Kind: "checks", Title: "Go through the guided checks", Badge: fmt.Sprintf("0/%d", len(att.Checks)),
			Text: text, Command: "issue-cli --project " + project + " check-script " + issue.Slug, RunURL: issueURL + "/check-script", Sessions: att.Checks})
		names = append(names, "guided checks")
	}
	gt := gateTargets(targets, att)
	if len(gt) > 0 && !strings.EqualFold(issue.Status, "done") {
		n++
		st := &WbStamp{N: n, Targets: gt, Approved: issue.HumanApproval, ApproveURL: issueURL + "/approve", SendBackURL: issueURL + "/send-back"}
		// The owner may stamp ahead of their own steps, as on the classic page.
		if len(m.Tasks) == 1 {
			st.Note = "Step 1 is still open: you can stamp ahead."
		} else if len(m.Tasks) > 1 {
			st.Note = fmt.Sprintf("Steps 1–%d are still open: you can stamp ahead.", len(m.Tasks))
		} else if att.Gate == nil || !att.Gate.Ready {
			st.Note = "The gate's checklist is still open: you can stamp ahead, the agent continues once it is done."
		}
		m.Stamp = st
		m.Command = needs.Command
		if att.Gate != nil && att.Gate.Ready || len(m.Tasks) > 0 {
			names = append(names, "the stamp")
		}
	}
	m.Also = needs.Also
	if m.On {
		m.Title = "Your move: " + joinAnd(names)
		if len(names) == 0 {
			m.Title = "Your move"
		}
		m.Meta = "waiting " + needs.Wait + " · since " + needs.Since.Local().Format("Mon 2 Jan, 15:04")
		if needs.Approx {
			m.Meta = "waiting about " + needs.Wait + " · since " + needs.Since.Local().Format("Mon 2 Jan, 15:04")
		}
		return m
	}
	m.Title = needs.Label + ": " + strings.TrimSuffix(needs.Title, ".")
	m.Text = needs.Text
	m.Meta = needs.Meta
	return m
}

// stationDecisions lists the issue's D-n, the owner's first-class.
func stationDecisions(decisions []tracker.Decision) WbDecisions {
	var out WbDecisions
	for _, dec := range decisions {
		q := unquote(dec.Quote)
		item := WbDecision{ID: dec.ID, Text: dec.Text, How: dec.How, YOLO: yoloDecision(dec.How), Also: dec.AlsoConsidered, Deferred: dec.Deferred, Replaces: strings.Join(dec.Replaces, ", ")}
		if !dec.Date.IsZero() {
			item.Date = dec.Date.Format("2 Jan")
		}
		item.Yours = q != "" && !strings.EqualFold(dec.How, "recorded by agent") && !strings.Contains(q, "(Recommended)")
		if item.Yours {
			item.Quote = q
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// stationChange is the change card from the work branch's commits.
func stationChange(c evChange) WbChangeCard {
	out := WbChangeCard{Range: c.Range, Unreadable: c.Unreadable, Commits: len(c.Commits), Files: c.Files, Add: c.Add, Del: c.Del, TestAdd: c.TestAdd}
	if n := len(c.Commits); n > 0 {
		head := c.Commits[0]
		out.Head = head.SHA + " · " + clipRunes(head.Subject, 70)
	}
	for i, f := range c.PerFile {
		if i == stationFiles {
			out.More = len(c.PerFile) - stationFiles
			break
		}
		out.PerFile = append(out.PerFile, WbFileStat{Path: f.Path, Add: f.Add, Del: f.Del})
	}
	for _, a := range c.Areas {
		out.Areas = append(out.Areas, WbFileStat{Path: a.Name, Files: a.Files, Add: a.Add, Del: a.Del})
	}
	return out
}

// stationTests is tests and proofs: criteria proven, the agent's newest
// tests: comment, CI and the checklist by section.
func stationTests(ev *EvidenceTab, boxes []tracker.CheckboxItem, ci *ciSummary) WbTestsCard {
	t := WbTestsCard{Tests: ev.Verdict.Tests, TestsAt: ev.Verdict.TestsAt, Short: ev.Verdict.Short}
	if ci != nil {
		t.CI = wbCIChip(ci.State, ci.Command)
	}
	if ev.Total > 0 {
		t.Big = fmt.Sprintf("%d/%d", ev.Proven, ev.Total)
		t.BigLabel = "acceptance criteria proven"
	}
	for _, c := range ev.Criteria {
		cr := WbCriterion{ID: c.ID, Text: c.Text, Proven: c.Proven, Proof: "no proof linked"}
		if len(c.Links) > 0 {
			l := c.Links[0]
			cr.Proof = strings.TrimSpace(l.Kind + " " + l.Ref + " · " + l.State)
		}
		t.Criteria = append(t.Criteria, cr)
	}
	idx := map[string]int{}
	for _, b := range boxes {
		name := b.Section
		if name == "" {
			name = "Intro"
		}
		i, ok := idx[name]
		if !ok {
			i = len(t.Groups)
			idx[name] = i
			t.Groups = append(t.Groups, WbCheckGroup{Name: name, You: ownerSection(name)})
		}
		g := &t.Groups[i]
		g.Total++
		t.Total++
		if b.Checked {
			g.Done++
			t.Done++
		}
		id := b.ID
		if id == "" {
			id = fmt.Sprint(b.Index)
		}
		g.Boxes = append(g.Boxes, WbBox{ID: id, Text: b.Label(), Checked: b.Checked})
	}
	return t
}

// ownerSection: the owner ticks this section's boxes (human testing).
func ownerSection(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "human test") || strings.Contains(n, "human-test") || strings.Contains(n, "playtest")
}

// stationQuality is the four cards at the top of the quality check.
func stationQuality(d StationData, issue *tracker.Issue) []WbQualityCard {
	var out []WbQualityCard

	dec := WbQualityCard{Key: "decisions", Title: "Decisions", Href: "#decisions", Big: fmt.Sprint(len(d.Decisions.Items)), Tone: "idle"}
	yours, agents := 0, 0
	for _, it := range d.Decisions.Items {
		if it.Yours {
			yours++
		} else {
			agents++
		}
	}
	switch {
	case len(d.Decisions.Items) == 0:
		dec.Line, dec.Verdict = "No decision recorded on this issue.", "nothing to read"
	default:
		dec.Line = plural(yours, "in your words", "in your words") + ", " + plural(agents, "recorded by the agent", "recorded by the agent") + "."
		dec.Verdict = "to read"
		if agents > 0 && d.Move.Stamp != nil {
			dec.Tone, dec.Verdict = "you", "read before the stamp"
		}
	}
	out = append(out, dec)

	ch := WbQualityCard{Key: "change", Title: "Change", Href: "#change", Tone: "idle", Verdict: "to look at"}
	switch {
	case d.Change.Unreadable != "":
		ch.Big, ch.Line, ch.Verdict = "—", d.Change.Unreadable, "no diff"
	case d.Change.Files == 0:
		ch.Big, ch.Line, ch.Verdict = "0 files", "No commit on the work branch yet.", "nothing yet"
	default:
		ch.Big = plural(d.Change.Files, "file", "files")
		var areas []string
		for i, a := range d.Change.Areas {
			if i == 3 {
				areas = append(areas, "…")
				break
			}
			areas = append(areas, a.Path)
		}
		ch.Line = fmt.Sprintf("+%d −%d in %s · %s.", d.Change.Add, d.Change.Del, plural(d.Change.Commits, "commit", "commits"), strings.Join(areas, ", "))
	}
	out = append(out, ch)

	ts := WbQualityCard{Key: "tests", Title: "Tests", Href: "#tests", Tone: "idle"}
	switch {
	case d.Tests.Big != "":
		ts.Big = d.Tests.Big
		ts.Line = "Acceptance criteria with a passing proof."
		if d.Tests.Tests != "" {
			ts.Line = d.Tests.Tests
		}
		if strings.HasPrefix(ts.Big, fmt.Sprint(len(d.Tests.Criteria))+"/") {
			ts.Tone, ts.Verdict = "green", "checked by the machine"
		} else {
			ts.Tone, ts.Verdict = "you", "short: "+clipRunes(d.Tests.Short, 40)
		}
	case d.Tests.Tests != "":
		ts.Big, ts.Line, ts.Verdict = "reported", d.Tests.Tests, "the agent's word"
	default:
		ts.Big, ts.Line, ts.Verdict = "—", "No acceptance criteria and no tests: comment yet.", "nothing recorded"
	}
	if d.Tests.CI != nil && d.Tests.CI.Tone == "red" {
		ts.Tone, ts.Verdict = "red", "CI red"
	}
	out = append(out, ts)

	cl := WbQualityCard{Key: "checklist", Title: "Checklist", Href: "#tests", Tone: "idle", Big: fmt.Sprintf("%d/%d", d.Tests.Done, d.Tests.Total)}
	var done, open []string
	yourOpen := false
	for _, g := range d.Tests.Groups {
		if g.Done == g.Total {
			done = append(done, g.Name)
		} else {
			open = append(open, g.Name)
			yourOpen = yourOpen || g.You
		}
	}
	switch {
	case d.Tests.Total == 0:
		cl.Big, cl.Line, cl.Verdict = "—", "The body has no checkboxes.", "nothing to tick"
	case len(open) == 0:
		cl.Line, cl.Tone, cl.Verdict = joinAnd(done)+" ticked.", "green", "all ticked"
	default:
		cl.Line = "Open: " + joinAnd(open) + "."
		if len(done) > 0 {
			cl.Line = joinAnd(done) + " ticked. Open: " + joinAnd(open) + "."
		}
		cl.Verdict = fmt.Sprintf("%d open", d.Tests.Total-d.Tests.Done)
		if yourOpen {
			cl.Tone, cl.Verdict = "you", "your part"
		}
	}
	out = append(out, cl)
	return out
}

// stationTimeline is the issue's life, newest first: status changes and the
// timeline's events under the chosen filter.
func stationTimeline(tl Timeline, issueURL, classic string, now time.Time) WbTimelineList {
	out := WbTimelineList{FullURL: classic[:strings.Index(classic, "?")] + "?tab=timeline"}
	for _, f := range timelineFilters {
		u := issueURL
		if f.Key != showAll {
			u += "?show=" + f.Key
		}
		out.Filters = append(out.Filters, WbFilter{Label: f.Label, URL: u + "#timeline", On: f.Key == tl.Show})
	}
	var rows []WbTimelineRow
	for _, b := range tl.Bands {
		if b.Status != "before dispatch" && b.Status != "after done" && !b.Start.IsZero() {
			rows = append(rows, WbTimelineRow{At: clock(b.Start, now), Text: "→ " + b.Status, Kind: "status", Tone: "status"})
		}
		for _, e := range b.Events {
			if e.Row != "event" && e.Row != "fold" {
				continue
			}
			text := e.Text
			if e.Note != "" {
				text += " · " + e.Note
			}
			kind := e.Chip
			if kind == "" {
				kind = e.Row
			}
			rows = append(rows, WbTimelineRow{At: clock(e.At, now), Text: text, Kind: kind, Tone: e.Tone, Quote: e.Quote})
		}
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if len(rows) > stationTimelineRows {
		out.More = len(rows) - stationTimelineRows
		rows = rows[:stationTimelineRows]
	}
	out.Rows = rows
	if len(rows) == 0 {
		out.Empty = "Nothing recorded under this filter."
		if tl.Total == 0 {
			out.Empty = "Nothing recorded yet: no transition, call or commit for this issue."
		}
	}
	return out
}

// stationAgent is the agent card: the worker's state, dispatch, calls,
// the last call, the worktree and its buttons.
func stationAgent(prefix, issueURL string, issue *tracker.Issue, card *agentCard, footer *CardAgent, wt, root string, now time.Time) WbStationAgent {
	name := tracker.AgentSessionName(issue.Slug)
	a := WbStationAgent{Name: name, Robot: WbRobot{State: "idle", Label: name}, StateLabel: "No agent on it", Tone: "idle",
		OpenURL: prefix + "/agents/open", PromptURL: issueURL + "/dispatch/reprompt", AgentsURL: prefix + "/agents?agent=" + name}
	if footer != nil {
		a.StateLabel = strings.ToUpper(footer.Text[:1]) + footer.Text[1:]
		a.Live = footer.State != "stopped"
		switch footer.Tone {
		case "amber":
			a.Robot.State, a.Tone = "you", "you"
		case "green":
			a.Robot.State, a.Tone = "working", "green"
		}
	}
	if card != nil {
		a.Known = card.Known
		if !card.Since.IsZero() {
			a.Facts = append(a.Facts, WbFact{Label: "Dispatched", Value: card.Since.Local().Format("Mon 2 Jan, 15:04")})
		}
		if card.Calls > 0 {
			a.Facts = append(a.Facts, WbFact{Label: "Calls", Value: fmt.Sprintf("%d × issue-cli", card.Calls)})
		}
		if card.Known {
			a.Facts = append(a.Facts, WbFact{Label: "Last", Value: clock(card.LastAt, now) + " · " + clipRunes(card.LastCmd, 50), Mono: true})
		}
	}
	if wt != "" && fileExists(wt) {
		if rel, err := filepath.Rel(root, wt); err == nil && !strings.HasPrefix(rel, "..") {
			wt = rel
		}
		a.Facts = append(a.Facts, WbFact{Label: "Worktree", Value: wt, Mono: true})
	}
	if issue.Assignee != "" {
		a.Facts = append(a.Facts, WbFact{Label: "Assignee", Value: issue.Assignee})
	}
	return a
}

// stationSessions lists the tmux sessions on the issue; the agent's own
// is marked when it waits on the owner.
func stationSessions(sessions []AgentSession, agent string, waits bool) WbSessionList {
	var out WbSessionList
	for _, s := range sessions {
		row := WbSessionRow{Name: s.Name, Started: s.StartTime}
		if at, err := time.ParseInLocation("2006-01-02 15:04:05", s.StartTime, time.Local); err == nil {
			row.Started = at.Format("2 Jan 15:04")
		}
		row.You = waits && strings.HasSuffix(s.Name, agent)
		out.Rows = append(out.Rows, row)
	}
	sort.SliceStable(out.Rows, func(i, j int) bool { return out.Rows[i].You && !out.Rows[j].You })
	return out
}

// stationBody is the body by "## " section with its counts, each linking
// its section in the station's own body panel.
func stationBody(fold []foldSection, notStarted []string, comments, decisions int, classic, issueURL string) WbBodyIndex {
	b := WbBodyIndex{NotStarted: notStarted, Comments: comments, EditURL: issueURL + "/edit-in-nvim"}
	for _, f := range fold {
		s := WbBodySection{Title: f.Title, URL: "#body-" + f.ID, Tone: "idle"}
		switch {
		case f.Total > 0:
			s.Count = fmt.Sprintf("%d/%d", f.Done, f.Total)
			switch {
			case f.Done == f.Total:
				s.Tone = "green"
			case ownerSection(f.Title):
				s.Tone = "you"
			}
		case strings.EqualFold(f.Title, "Decisions") && decisions > 0:
			s.Count = fmt.Sprint(decisions)
		}
		b.Sections = append(b.Sections, s)
	}
	return b
}

// stationOutputs is the issue's outputs on the shelf; nil when it made none.
func stationOutputs(p *tracker.Project, prefix string, issue *tracker.Issue) *WbStationOutputs {
	all, _ := outputs.Load(p)
	mine := outputs.Select(all, outputs.Filter{Issue: issue.Slug})
	if len(mine) == 0 {
		return nil
	}
	colors := shelfColors(p)
	talk, _ := outputs.LoadComments(p)
	out := &WbStationOutputs{URL: shelfURL(prefix, "", "")}
	for _, o := range mine {
		out.Rows = append(out.Rows, shelfRow(p, prefix, o, colors, talk))
		if o.State == outputs.StateReview {
			out.ToReview++
		}
	}
	return out
}

// stationSlugMap maps every way the body may name an issue (file name, slug,
// last segment) to its slug, as the classic page links issue refs.
func stationSlugMap(issues []*tracker.Issue) map[string]string {
	m := map[string]string{}
	for _, is := range issues {
		m[strings.TrimSuffix(filepath.Base(is.FilePath), ".md")] = is.Slug
		m[is.Slug] = is.Slug
		m[filepath.Base(is.Slug)] = is.Slug
	}
	return m
}
