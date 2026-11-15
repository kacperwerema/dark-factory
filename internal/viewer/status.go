package viewer

import (
	"bufio"
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clibug"
	"github.com/kacperwerema/dark-factory/internal/milestone"

	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Status page (/p/<project>/status) and its text form (status.md) say
// where the project stands: progress, work in flight, next steps, what waits
// on the owner and what shipped. Everything is read from repo files; nothing
// is stored.

const (
	statusReleases  = 3
	statusDoneDays  = 7
	statusStaleDays = 7
	statusVeryStale = 14
)

// Progress is done/total, with side statuses (parked) left out of total.
type Progress struct {
	Label string
	Done  int
	Total int
}

// Pct is the done share as a whole percentage, for bar widths.
func (p Progress) Pct() int {
	if p.Total == 0 {
		return 0
	}
	return p.Done * 100 / p.Total
}

// EpicProgress is one open epic and how its children are doing.
type EpicProgress struct {
	Issue    *tracker.Issue
	Progress Progress
	// Running is how many children have a live agent.
	Running int
	// AllDone: every child of the epic is done, in every version (Progress
	// may count only one version's children).
	AllDone bool
	// LaterOpen counts the epic's open children outside this version's panel;
	// LaterIn names their versions ("0.55, 0.56").
	LaterOpen int
	LaterIn   string
}

// ReadyToClose: every child is done but the epic itself is still open.
func (e EpicProgress) ReadyToClose() bool { return e.AllDone }

// Note is the epic row's second line: "3 children with agents", "all
// children done", …
func (e EpicProgress) Note() string {
	switch {
	case e.Progress.Total == 0:
		return "no children yet"
	case e.Progress.Done == e.Progress.Total:
		return "all children done"
	case e.Running > 0:
		return plural(e.Running, "child", "children") + " with agents"
	case e.Progress.Done == 0:
		return "nothing started"
	}
	return fmt.Sprintf("%d to go", e.Progress.Total-e.Progress.Done)
}

// ActiveRow is one issue in flight and the agent on it.
type ActiveRow struct {
	Issue *tracker.Issue
	// Agent is working, at gate, question, stopped or "" (no agent).
	Agent string
	Gate  string
	Age   Age
	// CLI is the agent CLI the worker runs (claude, codex); Limit the
	// usage-limit message its pane shows now.
	CLI, Limit string
	// Stalled says how the agent stalled ("stalled at Codex's trust
	// dialog", agentrun.ScanStalls).
	Stalled string
}

// AgentChip is a live agent on an issue not yet in flight (still in design).
type AgentChip struct {
	Issue *tracker.Issue
	State string
}

// Age is how long an issue has sat in its status.
type Age struct {
	Since  time.Time
	Approx bool
	Text   string
	// Level is "", "stale" (7 days or more) or "very-stale" (14 or more).
	Level string
}

// NextSteps is the ## Next section of the project lead's newest handoff note.
type NextSteps struct {
	// File is the note's path relative to the project root; "" when there is
	// no note.
	File  string
	Age   Age
	Text  string
	Found bool
}

// ParkedRow is a parked issue and why.
type ParkedRow struct {
	Issue  *tracker.Issue
	Reason string
}

// Release is one changelog entry or git tag.
type Release struct {
	Version string
	Date    string
	Summary string
}

// DoneRow is an issue that reached the final status recently.
type DoneRow struct {
	Issue  *tracker.Issue
	At     time.Time
	Approx bool
}

// StatusModel is everything the Status page and status.md show.
type StatusModel struct {
	Project string
	Prefix  string
	AsOf    time.Time

	All Progress
	// Versions are the versions not released yet (the newest release and
	// everything before it left out), oldest first; VersionsProgress sums them.
	Versions         []Progress
	VersionsProgress Progress
	Epics            []EpicProgress
	LeadName         string
	LeadState        string
	Active           []ActiveRow
	InDesign         []AgentChip
	Next             NextSteps
	Backlog          []*tracker.Issue
	Parked           []ParkedRow
	Waiting          []WaitingItem
	// NotReady counts gates whose checklist is still open.
	NotReady      int
	Releases      []Release
	ReleaseSource string
	// ChangelogDeclined: the owner declined status-changelog, so an empty
	// Recently released doesn't suggest workbench.changelog.
	ChangelogDeclined bool
	DoneRecent        []DoneRow
	// CI is the Local CI card; nil without a ci: block.
	CI *CIRow
	// Health is the Health card; nil without a health snapshot.
	Health *HealthCard
	// Privacy is the work-files and code badges after the title and their
	// lines on the Health card (privacy_view.go).
	Privacy *PrivacyView
	// ReleaseWait is the Waiting for release card; nil when it has nothing
	// to show.
	ReleaseWait *ReleaseWait
	// Behind is the installed build behind main; set only on the engine
	// project (build_behind.go).
	Behind *BehindRow

	// Milestone is the Current milestone card; nil without a version to
	// cut (then the card shows All issues).
	Milestone *MilestoneView
	// EpicsByArea is the Epics card: Epics grouped by area per version.
	EpicsByArea EpicsView
	// Shipped is DoneRecent grouped by area.
	Shipped []ShippedArea
	// UnsentBugReports counts issue-cli bug reports queued on this machine
	// because report-bug found no engine project.
	UnsentBugReports int
	// Shelf is the departments with outputs waiting for the owner's review.
	Shelf []WbShelfWait
	// NeedsYou is needsYouRows over Waiting: every count on the page is its
	// length.
	NeedsYou []WbQueueCard
	// AutoStart is the Auto-start card; nil when it is off.
	AutoStart *AutoStartView
	// Queue is the lead's queue card; nil when the project has none.
	Queue *QueueView
	// Jobs is the recurring jobs' one line (jobsStatusLine); "" without jobs.
	Jobs string
	// Nightly is the Last night: line (nightlyStatusLine); "" without nights.
	Nightly string
	// Push is the Push: line (devices, mute, quiet hours, removed devices); "" when nothing subscribed.
	Push string
	// Agents is the agent CLIs' subscriptions line (agentsStatusLine).
	Agents string
	// HQReport is the HQ report's text (/hq/report.md); "" without
	// departments.
	HQReport string
	// SiblingAsks are the open asks between sibling workers (issue-cli ask),
	// oldest first: the lead's view of the channel.
	SiblingAsks []tracker.SiblingAsk
}

// unsentBugReports counts this user's queued report-bug files. A var so
// tests never read the real queue.
var unsentBugReports = clibug.PendingCount

// IsCurrent reports whether version is the current milestone.
func (m *StatusModel) IsCurrent(version string) bool {
	return m.Milestone != nil && m.Milestone.Version == version
}

// WaitingCount is how many things wait on the owner: the length of
// needsYouRows, the number on every page (TestNeedsYouEveryKindCountsEverywhere).
func (m *StatusModel) WaitingCount() int { return len(m.NeedsYou) }

// statusOwnRows are the kinds the Status page words itself, each in its own
// list; every other row of the list shows through NeedsYouOther.
var statusOwnRows = map[string]bool{"ci": true, "bugs": true, "gate": true, "playtest": true, "checks": true, "question": true}

// NeedsYouOther is the rows the Status page has no list of its own for, so
// what it lists adds up to WaitingCount.
func (m *StatusModel) NeedsYouOther() []WbQueueCard {
	var out []WbQueueCard
	for _, r := range m.NeedsYou {
		if !statusOwnRows[r.Kind] {
			out = append(out, r)
		}
	}
	return out
}

// ownWaiting is how many waiting items the page words itself.
func (m *StatusModel) ownWaiting() int {
	n := 0
	for _, it := range m.Waiting {
		if statusOwnRows[it.Kind] {
			n++
		}
	}
	return n
}

// AgeOf is how long ago t was, as of the page: 25m, 1h 30m, 13d.
func (m *StatusModel) AgeOf(t time.Time) string { return shortDuration(m.AsOf.Sub(t)) }

// IssueURL is an issue's page, for the Report's release and CI cards.
func (m *StatusModel) IssueURL(slug string) string { return m.Prefix + "/issue/" + slug }

// Candidate is the release candidate being soaked (silver on the Waiting
// for release card, waiting to be released); nil without one.
func (m *StatusModel) Candidate() *CandidateRow {
	if m.ReleaseWait == nil || m.ReleaseWait.Milestone == nil {
		return nil
	}
	return m.ReleaseWait.Milestone.Candidate
}

// WaitingOf returns the waiting items of one kind (gate, playtest, checks,
// question).
func (m *StatusModel) WaitingOf(kind string) []WaitingItem {
	var out []WaitingItem
	for _, it := range m.Waiting {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

// gitTags lists the newest n tags as releases. A var so tests don't run git.
var gitTags = func(root string, n int) []Release {
	if gitdir.Outside(root) {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "for-each-ref", "--sort=-creatordate", fmt.Sprintf("--count=%d", n),
		"--format=%(refname:short)\t%(creatordate:short)\t%(contents:subject)", "refs/tags").Output()
	if err != nil {
		return nil
	}
	var rels []Release
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		r := Release{Version: parts[0], Date: parts[1]}
		if len(parts) == 3 && parts[2] != parts[0] {
			r.Summary = parts[2]
		}
		rels = append(rels, r)
	}
	return rels
}

// buildStatus assembles the Status page. agents may be nil (no tmux): every
// issue then shows no agent.
func issuesByVersion(issues []*tracker.Issue) map[string][]*tracker.Issue {
	byVersion := map[string][]*tracker.Issue{}
	for _, issue := range issues {
		if issue.Version != "" {
			byVersion[issue.Version] = append(byVersion[issue.Version], issue)
		}
	}
	return byVersion
}

// newestRelease is the version of the newest of the releases, newest first;
// "" with none.
func newestRelease(releases []Release) string {
	if len(releases) == 0 {
		return ""
	}
	return releases[0].Version
}

func buildStatus(proj *tracker.Project, issues []*tracker.Issue, agents *AgentsModel, now time.Time) *StatusModel {
	wf := proj.LoadWorkflow()
	cs := newCanvasStatuses(wf)
	m := &StatusModel{Project: proj.Name, AsOf: now}
	if agents != nil {
		m.Prefix = agents.Prefix
	}

	m.All = progressOf("All issues", cs, issues)
	byVersion := issuesByVersion(issues)
	for v, items := range byVersion {
		m.Versions = append(m.Versions, progressOf(v, cs, items))
	}
	sort.Slice(m.Versions, func(i, j int) bool { return compareVersions(m.Versions[i].Label, m.Versions[j].Label) < 0 })

	live := map[string]AgentNode{}
	stopped := map[string]AgentNode{}
	if agents != nil {
		m.LeadName, m.LeadState = agents.ProjectLead.Name, agents.ProjectLead.State
		for _, n := range agents.Workers {
			if n.Issue != nil {
				live[n.Issue.Slug] = n
			}
		}
		for _, n := range agents.Unfinished {
			if n.Issue != nil {
				stopped[n.Issue.Slug] = n
			}
		}
	}

	sorted := append([]*tracker.Issue(nil), issues...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	for _, issue := range sorted {
		if cs.closed(issue.Status) || !tracker.IsEpic(issue, issues) {
			continue
		}
		children := tracker.EpicChildren(issues, issue.Slug)
		e := EpicProgress{Issue: issue, Progress: progressOf(issue.Title, cs, children)}
		for _, c := range children {
			if _, ok := live[c.Slug]; ok {
				e.Running++
			}
		}
		m.Epics = append(m.Epics, e)
	}

	wfs := proj.WorkflowMemo()
	for _, issue := range sorted {
		iwf := wfs.ForIssue(issue)
		a := attentionWith(proj, issue, iwf)
		if a.Gate != nil && !a.Gate.Ready && !cs.closed(issue.Status) {
			m.NotReady++
		}
		switch {
		case activeStatus(iwf, issue.Status):
			row := ActiveRow{Issue: issue, Age: ageOf(a.Since, a.SinceApprox, now)}
			if n, ok := live[issue.Slug]; ok {
				row.Agent, row.Gate = agentLabel(n.State), n.Gate
				row.CLI, row.Limit, row.Stalled = n.Agent, n.Limit, n.Stalled
			} else if _, ok := stopped[issue.Slug]; ok {
				row.Agent = "stopped"
			}
			m.Active = append(m.Active, row)
		case a.Parked:
			m.Parked = append(m.Parked, ParkedRow{Issue: issue, Reason: a.ParkReason})
		case iwf.HasRole(issue.Status, tracker.StatusRoleQueue):
			m.Backlog = append(m.Backlog, issue)
		}
		if n, ok := live[issue.Slug]; ok && !activeStatus(iwf, issue.Status) && !cs.closed(issue.Status) {
			m.InDesign = append(m.InDesign, AgentChip{Issue: issue, State: agentLabel(n.State)})
		}
		if statusKey(issue.Status) == cs.final {
			if at, approx := doneAt(issue); !at.Before(period.DaysBack(now, statusDoneDays)) {
				m.DoneRecent = append(m.DoneRecent, DoneRow{Issue: issue, At: at, Approx: approx})
			}
		}
	}
	sort.SliceStable(m.Active, func(i, j int) bool {
		return wf.GetStatusIndex(m.Active[i].Issue.Status) > wf.GetStatusIndex(m.Active[j].Issue.Status)
	})
	sortBacklog(m.Backlog)
	sort.Slice(m.DoneRecent, func(i, j int) bool { return m.DoneRecent[i].At.After(m.DoneRecent[j].At) })

	m.Waiting = waitingOnOwner(proj, openOnly(proj, sorted))
	m.CI = ciRow(proj, m.Prefix, issues, now)
	m.Health = healthCard(proj, now)
	m.Privacy = privacyView(proj, m.Prefix, now)
	m.UnsentBugReports = unsentBugReports()
	_, _, m.Shelf = companyShelf(proj, m.Prefix, now)
	m.NeedsYou = needsYouRowsWith(proj, "", m.Prefix, m.Waiting, now)
	root := projectRoot(proj)
	m.Next = leadNextSteps(root, tracker.LeadSessionName(proj), now)
	text, changelog := readChangelog(root, wf.Workbench.Changelog)
	m.Releases, m.ReleaseSource = recentReleasesFrom(root, text, changelog, statusReleases)
	m.ChangelogDeclined = wf.DeclinesFeature("status-changelog")
	released := newestRelease(m.Releases)
	m.Versions, m.VersionsProgress = upcomingVersions(m.Versions, released)
	m.ReleaseWait = buildReleaseWait(proj, wf, cs, byVersion, released, changelog, text, now)
	if m.ReleaseWait != nil && m.ReleaseWait.Milestone != nil {
		ms := m.ReleaseWait.Milestone
		ms.Epic = map[string]bool{}
		for _, issue := range ms.Open {
			if tracker.IsEpic(issue, issues) {
				ms.Epic[issue.Slug] = true
			}
		}
	}
	if v, p, items := pickMilestone(byVersion, cs, released); v != "" {
		m.Milestone = buildMilestoneView(wf, cs, v, p, items)
		for _, issue := range items {
			if !cs.closed(issue.Status) && tracker.IsEpic(issue, issues) {
				m.Milestone.OpenEpics++
			}
		}
	}
	current := ""
	if m.Milestone != nil {
		current = m.Milestone.Version
	}
	m.EpicsByArea = buildEpicsView(cs, issues, m.Epics, current, released)
	m.Shipped = groupShipped(m.DoneRecent)
	m.SiblingAsks = tracker.ProjectOpenAsks(issues, tracker.ClosedIssueFunc(proj))
	return m
}

func progressOf(label string, cs *canvasStatuses, items []*tracker.Issue) Progress {
	p := Progress{Label: label}
	for _, it := range items {
		k := statusKey(it.Status)
		if cs.side[k] {
			continue
		}
		p.Total++
		if k == cs.final {
			p.Done++
		}
	}
	return p
}

// agentLabel turns an agents-canvas state into the page's wording.
func agentLabel(state string) string {
	if state == "at-gate" {
		return "at gate"
	}
	return state
}

// sortBacklog orders by priority (critical first, none last), then oldest
// created first.
func sortBacklog(issues []*tracker.Issue) {
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	r := func(p string) int {
		if v, ok := rank[strings.ToLower(p)]; ok {
			return v
		}
		return 4
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if ri, rj := r(issues[i].Priority), r(issues[j].Priority); ri != rj {
			return ri < rj
		}
		return issues[i].Created < issues[j].Created
	})
}

// doneAt is when the issue reached its final status: the stats sidecar's
// last transition, else done_at, else the file's mtime (approx).
func doneAt(issue *tracker.Issue) (time.Time, bool) {
	store, _ := tracker.StatsOf(issue.FilePath)
	for i := len(store.Transitions) - 1; i >= 0; i-- {
		if strings.EqualFold(store.Transitions[i].To, issue.Status) {
			return store.Transitions[i].TS, false
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, issue.DoneAt, time.Local); err == nil {
			return t, false
		}
	}
	return issue.ModTime, true
}

func ageOf(since time.Time, approx bool, now time.Time) Age {
	a := Age{Since: since, Approx: approx, Text: shortDuration(now.Sub(since))}
	switch days := int(now.Sub(since).Hours() / 24); {
	case days >= statusVeryStale:
		a.Level = "very-stale"
	case days >= statusStaleDays:
		a.Level = "stale"
	}
	return a
}

// shortDuration is 25m, 1h 30m or 13d.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h, mins := int(d.Hours()), int(d.Minutes())%60
		if mins == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, mins)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// leadNextSteps reads the ## Next section of the project lead's newest
// handoff note under <root>/sessions.
func leadNextSteps(root, lead string, now time.Time) NextSteps {
	path := tracker.NewestHandoffNote(filepath.Join(root, "sessions"), lead)
	if path == "" {
		return NextSteps{}
	}
	ns := NextSteps{File: filepath.ToSlash(relTo(root, path))}
	if info, err := os.Stat(path); err == nil {
		ns.Age = ageOf(info.ModTime(), false, now)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ns
	}
	ns.Text, ns.Found = tracker.SectionText(string(data), "Next")
	ns.Found = ns.Found && ns.Text != ""
	return ns
}

func relTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

// recentReleases reads the newest n releases from the changelog (configured,
// else CHANGELOG.md), else from git tags. source names where they came from.
func recentReleases(root, configured string, n int) (rels []Release, source string) {
	text, name := readChangelog(root, configured)
	return recentReleasesFrom(root, text, name, n)
}

// readChangelog reads the changelog (configured, else CHANGELOG.md). name is
// its slash path, "" when there is none.
func readChangelog(root, configured string) (text, name string) {
	name = configured
	if name == "" {
		name = "CHANGELOG.md"
	}
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return "", ""
	}
	return string(data), filepath.ToSlash(name)
}

// recentReleasesFrom is recentReleases on a changelog already read.
func recentReleasesFrom(root, text, name string, n int) (rels []Release, source string) {
	if name != "" {
		if rels = parseChangelog(text, n); len(rels) > 0 {
			return rels, name
		}
	}
	if rels = gitTags(root, n); len(rels) > 0 {
		return rels, "git tags"
	}
	return nil, ""
}

// changelogHeading is milestone.ChangelogHeading.
var changelogHeading = milestone.ChangelogHeading

// parseChangelog returns the first n "## <version> — <date>" entries, each
// summarised by its first bullet (whole, markdown and all: SummaryText and
// SummaryHTML clip or render it).
func parseChangelog(text string, n int) []Release {
	var rels []Release
	var cur *Release
	inSummary := false
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			if len(rels) == n {
				break
			}
			mm := changelogHeading.FindStringSubmatch(line)
			if mm == nil {
				cur = nil
				continue
			}
			rels = append(rels, Release{Version: mm[1], Date: mm[2]})
			cur, inSummary = &rels[len(rels)-1], false
			continue
		}
		if cur == nil {
			continue
		}
		t := strings.TrimSpace(line)
		switch {
		case cur.Summary == "" && strings.HasPrefix(t, "- "):
			cur.Summary, inSummary = strings.TrimPrefix(t, "- "), true
		case inSummary && t != "" && !strings.HasPrefix(t, "- ") && line != t:
			cur.Summary += " " + t
		default:
			inSummary = false
		}
	}
	return rels
}

// clip shortens s to at most n runes, ending in "…".
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
