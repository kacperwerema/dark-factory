package viewer

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The hall's Retro page (L2, "Retro · sorting room"): the notes agents leave
// in retros/ after an issue, taken apart into findings and sent where they
// change something for good. The view models of its components live here,
// their templates in templates/_workshop_retro.html and their styles in the
// "L2: retro" block of static/workshop.css; templates/retro_wb.html composes
// them. The classic retros page stays at ?classic=1.
//
// Findings are derived, not stored: the retrospective an agent writes
// (issue-cli retrospective, briefed in internal/tracker/dispatch.go) has four
// labelled lines — Base workflow, Subsystem workflow for <system>, Missing
// system-specific instructions, Tooling/workflow friction — and each labelled
// line (split further at "(1) … (2) …") is one finding. A note without those
// labels has no findings and is shown as unsorted. Where a finding goes
// follows its label; nothing on disk records that a finding was sent, so a
// finding counts as distributed only when the data says so (the retro is
// processed, or a bug report under bugs/ names its issue) — sends from this
// page are marked in the browser only.

// RetroWbPage is the page.
type RetroWbPage struct {
	Header     WbHeader
	Project    string
	Prefix     string
	ClassicURL string
	Presort    WbRetroPresort
	Loop       WbRetroLoop
	ToSort     WbRetroList
	Processed  WbRetroList
	// Selected is the retro in the middle; nil when the project has none.
	Selected *WbRetroView
	Bins     []WbRetroBin
	Pattern  *WbRetroPattern
	// Empty explains a page without retros.
	Empty string
	// Bugs is the Bugs view (?view=bugs): the tool bug reports linked to
	// this project's issues. nil on the sorting room.
	Bugs *WbRetroBugs
	// View switches between the sorting room and the Bugs view.
	View WbSegLinks
}

// WbRetroBugs is the Bugs view: counts, a status filter, one card per bug
// report (what the classic page's #bugs section showed).
type WbRetroBugs struct {
	Open, Closed int
	Filters      []WbFilterPill
	Cards        []WbRetroBug
	Empty        string
}

// WbRetroBug is one tool bug report and its status buttons (POST
// retros/bug/<file>/status).
type WbRetroBug struct {
	FileName, IssueSlug, IssueURL string
	Tool, When, Description       string
	Status, StatusText, Tone      string
	StatusURL                     string
}

// WbRetroPresort is "Let an agent pre-sort": the Claude | Codex choice and
// the button that dispatches the review agent (POST retros/review).
type WbRetroPresort struct {
	URL, Session, AttachCmd string
	Live                    bool
	Agents                  []string
}

// WbRetroLoop is the loop strip: Done → Retro · N open → bins → Idea on the
// line, and the counters on its right.
type WbRetroLoop struct {
	DoneURL, LineURL string
	Open             int
	Steps            []WbRetroLoopStep
	Processed        int
	LinkedBugs       int
}

// WbRetroLoopStep is one bin on the loop strip.
type WbRetroLoopStep struct {
	Label, URL string
}

// WbRetroList is a column of retros in the left panel.
type WbRetroList struct {
	Key, Title string
	Count      int
	Items      []WbRetroItem
	// More counts the retros past the list; MoreURL shows them all.
	More    int
	MoreURL string
	Empty   string
}

// WbRetroItem is one retro in a list.
type WbRetroItem struct {
	Title, System, Date, URL string
	Findings                 int
	Selected                 bool
}

// WbRetroView is the selected retro.
type WbRetroView struct {
	FileName, Path       string
	Title, System        string
	IssueStatus          string
	When                 string
	IssueURL             string
	Processed            bool
	StatusURL            string
	BodyHTML             string
	Findings             []WbRetroFinding
	Progress             WbRetroProgress
	Unsorted             bool
	CEOLive              bool
	CEOSession, CEOSpawn string
}

// WbRetroProgress is the "Distributed k/n" box and the close/reopen action.
type WbRetroProgress struct {
	Done, Total int
	Segs        []bool
	Processed   bool
	StatusURL   string
	// Key is the retro's file name: the page marks sends from this browser
	// under it.
	Key string
}

// WbRetroFinding is one finding: its kind, what it says and where it goes.
type WbRetroFinding struct {
	Index      int
	Kind, Tone string
	Title      string
	Text       string
	Send       WbRetroSend
}

// WbRetroSend is a finding's "Send to" box: the target and the action — a
// message to the CEO (POST lead/send) — or the state the data shows.
type WbRetroSend struct {
	Bin, Dest string
	CTA       string
	Message   string
	SendURL   string
	Session   string
	Live      bool
	Done      bool
	DoneLabel string
	Key       string
	Index     int
}

// WbRetroBin is a bin on the right: how many findings of this retro go
// there and how many are distributed.
type WbRetroBin struct {
	Key, Title, Sub, URL, Color string
	Done, Total                 int
}

// WbRetroPattern is "A pattern that comes back": a term from this retro that
// other issues' retros mention too.
type WbRetroPattern struct {
	Term       string
	Issues     int
	Example    string
	ExampleURL string
}

const (
	retroListMax = 8
	// retroPatternMin is how many other issues' retros must mention a term
	// before it is called a pattern.
	retroPatternMin = 3
)

// retroBins are the bins in the order the page shows them.
var retroBins = []struct{ Key, Title, Sub, Color string }{
	{"docs", "Docs", "system instructions", "#E9D7B5"},
	{"decisions", "Decisions", "a decision to confirm", "#7FD1BF"},
	{"workflow", "Workflow", "a proposed update", "#C5CCD7"},
	{"bugs", "Bugs", "report-bug", "#F8CDD2"},
}

// retroTitleText splits a finding into a title (its first sentence) and the
// rest; a long first sentence is cut and the whole kept as the text.
func retroTitleText(s string) (string, string) {
	s = strings.TrimSpace(s)
	cut := -1
	for _, sep := range []string{". ", "; ", "! ", "? "} {
		if i := strings.Index(s, sep); i > 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	title, rest := s, ""
	if cut > 0 {
		title, rest = s[:cut+1], strings.TrimSpace(s[cut+2:])
		title = strings.TrimSuffix(title, ";")
	}
	if utf8.RuneCountInString(title) > 120 {
		t := string([]rune(title)[:100])
		if i := strings.LastIndex(t, " "); i > 60 {
			t = t[:i]
		}
		return retroCap(strings.TrimRight(t, " ,;:") + "…"), "…" + strings.TrimSpace(s[len(t):])
	}
	return retroCap(title), rest
}

// retroFindings derives the findings of one retro.
func retroFindings(r *RetroEntry, filedBug string, ceo WbMessageBox) []WbRetroFinding {
	var out []WbRetroFinding
	system := r.System
	for _, sec := range retro.Sections(r.BodyRaw) {
		if retro.None(sec.Text) {
			continue
		}
		var kind, tone, bin, dest, cta, ask string
		switch {
		case sec.Label == "base workflow":
			kind, tone, bin = "Workflow", "ink", "workflow"
			dest, cta = "Workflow: the base workflow", "Propose an update"
			ask = "Propose an update to the base workflow in workflow.yaml for it; show me the change before applying it."
		case strings.HasPrefix(sec.Label, "subsystem workflow"):
			sys := sec.System
			if sys == "" {
				sys = system
			}
			kind, tone, bin = "System "+orDash(sys), "sys", "workflow"
			dest, cta = "Workflow: "+orDash(sys)+"'s workflow", "Propose an update"
			ask = "Propose an update to the " + orDash(sys) + " system's workflow in workflow.yaml for it; show me the change before applying it."
		case sec.Label == "missing system-specific instructions":
			kind, tone, bin = "System "+orDash(system), "sys", "docs"
			if system == "" {
				kind = "Instructions"
			}
			dest, cta = "Docs: "+orDash(system)+" instructions", "Add to docs"
			ask = "Add it to the docs (the " + orDash(system) + " system's instructions) so the next agent has it."
		default:
			kind, tone, bin = "Tools", "tool", "bugs"
			dest, cta = "Bugs: report-bug", "File a bug report"
			ask = "File it as a tool bug with report-bug, unless it is already filed."
		}
		for _, item := range retro.Items(sec.Text) {
			title, text := retroTitleText(item)
			i := len(out)
			f := WbRetroFinding{Index: i, Kind: kind, Tone: tone, Title: title, Text: text}
			f.Send = WbRetroSend{Bin: bin, Dest: dest, CTA: cta, SendURL: ceo.SendURL, Session: ceo.Session, Live: ceo.Live,
				Key: r.FileName, Index: i,
				Message: fmt.Sprintf("From retro retros/%s (issue %s): %s — %s", r.FileName, orDash(r.IssueSlug), oneLine(item), ask)}
			switch {
			case normalizeRetroReviewStatus(r.ReviewStatus) == "processed":
				f.Send.Done, f.Send.DoneLabel = true, "processed with the retro"
			case bin == "bugs" && filedBug != "":
				f.Send.Done, f.Send.DoneLabel = true, "filed · bugs/"+filedBug
			}
			out = append(out, f)
		}
	}
	return out
}

// retroCap capitalises a title's first letter unless its first word is
// code (check-script, packages/web, prove).
func retroCap(s string) string {
	first, _, _ := strings.Cut(s, " ")
	if strings.ContainsAny(first, "-/._`") {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// retroTime is when the retro was written: its Date line, else the file's
// time.
func retroTime(r *RetroEntry) time.Time {
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(r.Date)); err == nil {
		return t.Local()
	}
	return r.ModTime
}

func retroTitle(r *RetroEntry) string {
	if strings.TrimSpace(r.IssueTitle) != "" {
		return r.IssueTitle
	}
	return strings.TrimSuffix(r.FileName, ".md")
}

// retroPattern finds the term of the selected retro that the retros of the
// most other issues mention, if at least retroPatternMin do.
func retroPattern(sel *RetroEntry, all []*RetroEntry, stop map[string]bool, link func(*RetroEntry) string) *WbRetroPattern {
	var best *WbRetroPattern
	for _, term := range retro.Terms(retro.SectionsText(sel.BodyRaw), stop) {
		lt := strings.ToLower(term)
		issues := map[string]bool{}
		var example *RetroEntry
		for _, r := range all {
			if r == sel || r.IssueSlug == sel.IssueSlug {
				continue
			}
			if strings.Contains(strings.ToLower(retro.SectionsText(r.BodyRaw)), lt) {
				key := r.IssueSlug
				if key == "" {
					key = r.FileName
				}
				issues[key] = true
				if example == nil {
					example = r
				}
			}
		}
		if len(issues) >= retroPatternMin && (best == nil || len(issues) > best.Issues) {
			best = &WbRetroPattern{Term: term, Issues: len(issues), Example: retroTitle(example), ExampleURL: link(example)}
		}
	}
	return best
}

type retroWbIn struct {
	Proj       *tracker.Project
	Prefix     string
	Header     WbHeader
	Retros     []*RetroEntry
	Bugs       []*ToolBugReportView
	CEO        WbMessageBox
	Selected   string
	All        bool
	Presort    WbRetroPresort
	StatusStop []string
	// View is "bugs" for the Bugs view; BugStatus its filter (open by
	// default, as on the classic page; "all" shows every report).
	View, BugStatus string
}

// buildRetroWb builds the page from the retros (newest first) and the
// related bug reports.
func buildRetroWb(in retroWbIn) *RetroWbPage {
	prefix := in.Prefix
	base := prefix + "/retros"
	link := func(r *RetroEntry) string {
		q := url.Values{"retro": {r.FileName}}
		if in.All {
			q.Set("all", "1")
		}
		return base + "?" + q.Encode()
	}
	p := &RetroWbPage{Header: in.Header, Project: in.Proj.Name, Prefix: prefix, ClassicURL: base + "?classic=1", Presort: in.Presort}

	var open, done []*RetroEntry
	for _, r := range in.Retros {
		if normalizeRetroReviewStatus(r.ReviewStatus) == "processed" {
			done = append(done, r)
		} else {
			open = append(open, r)
		}
	}
	bugFor := map[string]string{}
	for _, b := range in.Bugs {
		if _, ok := bugFor[b.IssueSlug]; !ok && b.IssueSlug != "" {
			bugFor[b.IssueSlug] = b.FileName
		}
	}

	p.Loop = WbRetroLoop{DoneURL: prefix + "/hall", LineURL: prefix + "/hall", Open: len(open), Processed: len(done), LinkedBugs: len(in.Bugs),
		Steps: []WbRetroLoopStep{{"Docs", prefix + "/docs"}, {"Decisions", prefix + "/decisions"}, {"Workflow", prefix + "/updates"}, {"Bugs", base + "?view=bugs"}}}
	p.View = WbSegLinks{Label: "View", Name: "retro_view", Items: []statsOption{
		{Key: "room", Label: "Sorting room", Href: base, On: in.View != "bugs"},
		{Key: "bugs", Label: fmt.Sprintf("Bugs · %d", len(in.Bugs)), Href: base + "?view=bugs", On: in.View == "bugs"}}}
	if in.View == "bugs" {
		p.Bugs = retroBugs(in.Bugs, prefix, in.BugStatus)
	}

	// The selected retro: the one asked for, else the newest to sort, else
	// the newest processed.
	var sel *RetroEntry
	for _, r := range in.Retros {
		if r.FileName == in.Selected {
			sel = r
		}
	}
	if sel == nil && len(open) > 0 {
		sel = open[0]
	}
	if sel == nil && len(done) > 0 {
		sel = done[0]
	}

	findings := map[string]int{}
	countOf := func(r *RetroEntry) int {
		n, ok := findings[r.FileName]
		if !ok {
			n = len(retroFindings(r, "", in.CEO))
			findings[r.FileName] = n
		}
		return n
	}
	list := func(key, title string, rs []*RetroEntry, empty string) WbRetroList {
		l := WbRetroList{Key: key, Title: title, Count: len(rs), Empty: empty}
		shown := rs
		if !in.All && len(shown) > retroListMax {
			shown = shown[:retroListMax]
			l.More = len(rs) - retroListMax
			q := url.Values{"all": {"1"}}
			if sel != nil {
				q.Set("retro", sel.FileName)
			}
			l.MoreURL = base + "?" + q.Encode()
		}
		for _, r := range shown {
			l.Items = append(l.Items, WbRetroItem{Title: retroTitle(r), System: r.System, Date: retroTime(r).Format("2 Jan"),
				URL: link(r), Findings: countOf(r), Selected: r == sel})
		}
		return l
	}
	p.ToSort = list("to_sort", "To sort", open, "Nothing to sort: every retro is processed.")
	p.Processed = list("processed", "Processed", done, "")

	if sel == nil {
		p.Empty = "No retros yet. After every issue the agent writes one with issue-cli retrospective <slug> --body \"…\"; it lands in retros/ and shows up here."
		for _, b := range retroBins {
			p.Bins = append(p.Bins, WbRetroBin{Key: b.Key, Title: b.Title, Sub: b.Sub, Color: b.Color, URL: binURL(b.Key, p.Loop)})
		}
		return p
	}

	v := &WbRetroView{FileName: sel.FileName, Path: "retros/" + sel.FileName, Title: retroTitle(sel), System: sel.System,
		IssueStatus: sel.Status, When: retroTime(sel).Format("Mon 2 Jan, 15:04"), BodyHTML: sel.BodyHTML,
		Processed: normalizeRetroReviewStatus(sel.ReviewStatus) == "processed",
		StatusURL: base + "/retro/" + url.PathEscape(sel.FileName) + "/status",
		CEOLive:   in.CEO.Live, CEOSession: in.CEO.Session, CEOSpawn: in.CEO.SpawnURL}
	if sel.IssueSlug != "" {
		v.IssueURL = prefix + "/issue/" + sel.IssueSlug
	}
	v.Findings = retroFindings(sel, bugFor[sel.IssueSlug], in.CEO)
	v.Unsorted = len(v.Findings) == 0
	v.Progress = WbRetroProgress{Total: len(v.Findings), Processed: v.Processed, StatusURL: v.StatusURL, Key: sel.FileName}
	for _, f := range v.Findings {
		v.Progress.Segs = append(v.Progress.Segs, f.Send.Done)
		if f.Send.Done {
			v.Progress.Done++
		}
	}
	p.Selected = v

	for _, b := range retroBins {
		bin := WbRetroBin{Key: b.Key, Title: b.Title, Sub: b.Sub, Color: b.Color, URL: binURL(b.Key, p.Loop)}
		for _, f := range v.Findings {
			if f.Send.Bin == b.Key {
				bin.Total++
				if f.Send.Done {
					bin.Done++
				}
			}
		}
		if b.Key == "decisions" {
			bin.Sub = "no line of the retro note asks for a decision"
		}
		if b.Key == "docs" && sel.System != "" {
			bin.Sub = sel.System + " instructions"
		}
		p.Bins = append(p.Bins, bin)
	}

	stop := map[string]bool{"issue-cli": true}
	for _, s := range in.StatusStop {
		stop[strings.ToLower(strings.ReplaceAll(s, " ", "-"))] = true
	}
	p.Pattern = retroPattern(sel, in.Retros, stop, link)
	return p
}

// retroBugs is the Bugs view over the related tool bug reports.
func retroBugs(bugs []*ToolBugReportView, prefix, want string) *WbRetroBugs {
	base := prefix + "/retros"
	filter := "open"
	if strings.EqualFold(strings.TrimSpace(want), "all") {
		filter = "all"
	} else if want != "" {
		filter = normalizeBugStatus(want)
	}
	counts := countBugsByStatus(bugs)
	v := &WbRetroBugs{Open: counts["open"], Closed: counts["fixed"] + counts["wontfix"]}
	for _, f := range []struct{ key, label, tone string }{{"open", "Open", "red"}, {"fixed", "Fixed", ""}, {"wontfix", "Won't fix", ""}, {"all", "All", ""}} {
		q := url.Values{"view": {"bugs"}, "bug_status": {f.key}}
		label := f.label
		if f.key != "all" {
			label = fmt.Sprintf("%s · %d", f.label, counts[f.key])
		}
		v.Filters = append(v.Filters, WbFilterPill{Label: label, URL: base + "?" + q.Encode(), On: filter == f.key, Key: "bugs_" + f.key})
	}
	for _, b := range bugs {
		st := normalizeBugStatus(b.Status)
		if filter != "all" && st != filter {
			continue
		}
		c := WbRetroBug{FileName: b.FileName, IssueSlug: b.IssueSlug, Tool: b.Tool, When: b.Timestamp, Description: strings.TrimSpace(b.Description),
			Status: st, StatusText: bugStatusWords(st), Tone: "idle", StatusURL: base + "/bug/" + url.PathEscape(b.FileName) + "/status"}
		if st == "open" {
			c.Tone = "red"
		} else if st == "fixed" {
			c.Tone = "green"
		}
		if b.IssueSlug != "" {
			c.IssueURL = prefix + "/issue/" + b.IssueSlug
		}
		v.Cards = append(v.Cards, c)
	}
	switch {
	case len(bugs) == 0:
		v.Empty = "No tool bug reports name this project's issues. A worker files one with issue-cli report-bug \"…\"; it lands under bugs/ and shows here."
	case len(v.Cards) == 0:
		v.Empty = "No bug report with this status."
	}
	return v
}

// bugStatusWords names a bug report's status as the buttons do.
func bugStatusWords(st string) string {
	switch st {
	case "fixed":
		return "fixed"
	case "wontfix":
		return "won't fix"
	}
	return "open"
}

func binURL(key string, loop WbRetroLoop) string {
	for _, s := range loop.Steps {
		if strings.EqualFold(s.Label, key) {
			return s.URL
		}
	}
	return ""
}

// handleRetroWb serves GET /p/<project>/retros, the Retro page.
func (s *Server) handleRetroWb(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string, retros []*RetroEntry, bugs []*ToolBugReportView) {
	now := time.Now()
	company := s.buildCompany(proj, prefix, now)
	lot := company.Lot
	h := WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(2, proj.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait,
		DoneWeek: lot.DoneWeek, Waiting: company.Waiting, HideDone: true, Tabs: wbHallTabs(prefix, "retro")}
	h.Crumbs[1].Color = lot.Color
	h.YOLO = headerYOLO(lot.YOLO, prefix)
	h.Crumbs[2] = WbCrumb{Label: "Product hall", URL: prefix + "/hall", Color: companyProductColor}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		h.Chips = append(h.Chips, *c)
	}
	session := tmuxSessionName(proj.Slug + "-retros-review")
	var statuses []string
	if wf := proj.LoadWorkflow(); wf != nil {
		for _, st := range wf.Statuses {
			statuses = append(statuses, st.Name)
		}
	}
	sort.Strings(statuses)
	q := r.URL.Query()
	page := buildRetroWb(retroWbIn{Proj: proj, Prefix: prefix, Header: h, Retros: retros, Bugs: bugs, CEO: company.Message,
		Selected: q.Get("retro"), All: q.Get("all") != "", StatusStop: statuses, View: q.Get("view"), BugStatus: q.Get("bug_status"),
		Presort: WbRetroPresort{URL: prefix + "/retros/review", Session: session, Live: agentSessionLive(proj, session),
			AttachCmd: agentAttachCmd(proj, session), Agents: []string{"claude", "codex"}}})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "retro_wb.html", page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
