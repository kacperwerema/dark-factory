package viewer

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/deptfamily"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/weekplan"
)

// The HQ report (L1 · HQ): an executive summary of the company, one
// headline line per box (HQ, Product, each group and department in the
// company tree's order) that unfolds to the box's detail. Everything is
// derived from what the engine already keeps: the week sections
// (weekplan.Sections: goals and their move, the shelf, jobs, handoffs,
// blocked), the newest health snapshot (the departments family), spend
// from this machine's transcripts, groups' latest week reports and the
// leads' progress: comments. No agent writes it and no model is called;
// /hq/report.md is the same model as text for the CEO
// (factory/hq-report-an-executive-summary-of-each-department-in-the-ceos-hq).

// HQReportData is the page and the markdown twin.
type HQReportData struct {
	Header WbHeader
	Name   string
	// Week is the ISO week (2026-W44); Window the days looked back over.
	Week, Window string
	MDURL        string
	// Snapshot says where the health numbers come from, or that there is
	// no snapshot yet (SnapshotCmd takes one).
	Snapshot, SnapshotCmd string
	HQ                    HQRBox
	Boxes                 []HQRBox
	// NoDepts is the line for a project without departments.
	NoDepts string
}

// HQRBox is one box's section: its headline and its detail.
type HQRBox struct {
	Key, Name, Color string
	// Kind is hq, product, group or dept; Depth how many groups it sits in.
	Kind  string
	Depth int
	// Tone is the on-track tone (green, amber, red; idle without one).
	Tone    string
	OnTrack string
	Parts   []HQRPart
	// Empty says the box has nothing at all yet, and what fills it.
	Empty string
	Rows  []HQRRow
	// Link is the box's own page (the company room, Product's Report).
	Link, LinkText string
}

// HQRPart is one piece of a headline: "1 of 2 goals moved".
type HQRPart struct {
	Text, Tone string
}

// HQRRow is one labelled row of a box's detail.
type HQRRow struct {
	// Key names the row in test ids: goals, outputs, spend…
	Key, Label string
	Lines      []HQRLine
}

// HQRLine is one line of a row, linked when it names an issue or a page.
type HQRLine struct {
	Text, URL, Tone string
}

func (s *Server) handleHQReport(w http.ResponseWriter, r *http.Request, p *tracker.Project, prefix string) {
	d := s.buildHQReport(p, prefix, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "hq_report.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleHQReportMD(w http.ResponseWriter, r *http.Request, p *tracker.Project, prefix string) {
	d := s.buildHQReport(p, prefix, time.Now())
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(hqReportMarkdown(d)))
}

// hqReportURL is the report's page.
func hqReportURL(prefix string) string { return prefix + "/hq/report" }

// buildHQReport reads the company page's model for the header, the HQ and
// Product rooms and the groups' week blocks, and the week sections for
// every department.
func (s *Server) buildHQReport(p *tracker.Project, prefix string, now time.Time) HQReportData {
	company := s.buildCompany(p, prefix, now)
	issues := company.Lot.issues
	d := HQReportData{
		Header: company.Header, Name: p.Name, Week: weekplan.Key(now), MDURL: hqReportURL(prefix) + ".md",
		Window: now.Add(-weekplan.Window).Format("Jan 2") + " – " + now.Format("Jan 2"),
	}
	d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "HQ report", Current: true}

	snap := newestHealth(p, now)
	if snap != nil && snap.Families[deptfamily.Family].Metrics > 0 {
		d.Snapshot = "health from the snapshot of " + snap.Time().Format("Jan 2 15:04")
	} else {
		d.Snapshot, d.SnapshotCmd = "no health snapshot with department measures yet", deptHealthCommand(p)
		snap = nil
	}
	sp := companySpend(p, issues, now)

	// One comment read per issue, shared by the blocked lines and the
	// leads' progress lines.
	var mu sync.Mutex
	cache := map[string][]tracker.Comment{}
	comments := func(is *tracker.Issue) []tracker.Comment {
		mu.Lock()
		defer mu.Unlock()
		c, ok := cache[is.FilePath]
		if !ok {
			c, _ = tracker.LoadComments(is.FilePath)
			cache[is.FilePath] = c
		}
		return c
	}
	sections := map[string]*weekplan.Section{}
	if len(p.Departments) > 0 {
		in := weekplan.Input{Project: p, Issues: issues, Jobs: viewerJobs(p, issues, now), Comments: comments, Now: now}
		for _, sec := range weekplan.Sections(in) {
			sections[sec.Box] = sec
		}
	}

	d.HQ = hqBox(p, prefix, company, snap, sp)
	d.Boxes = append(d.Boxes, productBox(p, prefix, company, issues, sp))
	weeks := map[string]*WbWeekReport{}
	var collect func(rooms []WbRoomDept)
	collect = func(rooms []WbRoomDept) {
		for _, r := range rooms {
			if r.Week != nil {
				weeks[r.Box] = r.Week
			}
			collect(r.Children)
		}
	}
	collect(company.Rooms)

	var walk func(dept tracker.Department, depth int)
	walk = func(dept tracker.Department, depth int) {
		i := 0
		for j := range p.Departments {
			if p.Departments[j].Name == dept.Name {
				i = j
			}
		}
		key := tracker.DepartmentKey(dept.Name)
		b := deptBox(p, prefix, dept, deptColorOf(p, dept, i), sections[key], snap, sp, weeks[key], issues, comments, now)
		b.Depth = depth
		d.Boxes = append(d.Boxes, b)
		for _, c := range p.Children(dept.Name) {
			walk(c, depth+1)
		}
	}
	for _, dept := range p.Departments {
		if p.GroupOf(dept.Name) == nil {
			walk(dept, 0)
		}
	}
	if len(p.Departments) == 0 {
		d.NoDepts = "No departments yet: HQ and Product only. Add one from a template: " + tracker.InjectProject(p, "issue-cli department list")
	}
	keyRows(&d.HQ)
	for i := range d.Boxes {
		keyRows(&d.Boxes[i])
	}
	return d
}

// keyRows names each row by its label for the test ids: "Week report" is
// week_report.
func keyRows(b *HQRBox) {
	for i := range b.Rows {
		b.Rows[i].Key = strings.ReplaceAll(strings.ToLower(b.Rows[i].Label), " ", "_")
	}
}

// hqrValue is box's measure from the snapshot.
func hqrValue(s *health.Snapshot, name, box string) (float64, bool) {
	if s == nil {
		return 0, false
	}
	return s.Value(name, map[string]string{"department": box})
}

// onTrack sets the box's on-track words and tone, and returns the Health
// row's line; "" when the snapshot has no row for it.
func onTrack(b *HQRBox, s *health.Snapshot) string {
	v, ok := hqrValue(s, deptfamily.OnTrack, b.Key)
	if !ok {
		b.Tone = "idle"
		return ""
	}
	b.OnTrack = fmt.Sprintf("♥ %.0f%% on track", v*100)
	b.Tone = map[string]string{"good": "green", "medium": "amber", "bad": "red"}[shareTone(v)]
	parts := []string{fmt.Sprintf("%.0f%% on track", v*100)}
	for _, m := range []struct{ name, word string }{
		{deptfamily.GoalsMoving, "goals moving"}, {deptfamily.OutputsReview, "outputs reviewed in time"}, {deptfamily.JobsOnTime, "jobs on time"},
	} {
		if x, ok := hqrValue(s, m.name, b.Key); ok {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", m.word, x*100))
		}
	}
	return strings.Join(parts, " · ")
}

// spendParts is the box's spend this week as a headline part and a row;
// nothing when it spent nothing.
func spendParts(rep *spend.Report, key string) (*HQRPart, *HQRRow) {
	if rep == nil {
		return nil, nil
	}
	b := rep.Find(key)
	if b == nil || b.ThisWeek().Empty() {
		return nil, nil
	}
	t := b.ThisWeek()
	part := &HQRPart{Text: "spent " + spend.CostText(t)}
	line := HQRLine{Text: spend.Line(t) + " this week"}
	if s := b.Budget; s != nil && s.Err == "" {
		part.Text += " · budget " + s.Text
		line.Text += " · budget " + s.Text + " (shown only)"
		if s.Over {
			part.Tone, line.Tone = "red", "red"
		}
	}
	if n := len(b.RolledFrom); n > 0 {
		line.Text += " · incl. " + plural(n, "box", "boxes")
	}
	return part, &HQRRow{Label: "Spend", Lines: []HQRLine{line}}
}

// hqBox is HQ: the company goals, the forwards waiting on the CEO, the
// shelf and the company's spend.
func hqBox(p *tracker.Project, prefix string, c CompanyData, snap *health.Snapshot, sp *spend.Report) HQRBox {
	b := HQRBox{Key: connections.HQ, Name: "HQ", Color: companyHQColor, Kind: "hq", Tone: "idle", Link: prefix + "/company", LinkText: "HQ on the company page"}
	if line := deptHealthLine(snap); line != "" {
		part := "departments " + strings.TrimPrefix(line, "Departments: ")
		if _, ok := snap.Value(deptfamily.OnTrack, nil); !ok {
			part = "departments not measured yet"
		}
		b.Parts = append(b.Parts, HQRPart{Text: part})
		b.Rows = append(b.Rows, HQRRow{Label: "Health", Lines: []HQRLine{{Text: line, URL: prefix + "/health#departments"}}})
		if v, ok := snap.Value(deptfamily.OnTrack, nil); ok {
			b.Tone = map[string]string{"good": "green", "medium": "amber", "bad": "red"}[shareTone(v)]
		}
	}
	if c.HQ.Mission != "" {
		b.Rows = append(b.Rows, HQRRow{Label: "Mission", Lines: []HQRLine{{Text: c.HQ.Mission}}})
	}
	if len(c.HQ.Goals) > 0 {
		row := HQRRow{Label: "Goals"}
		for _, g := range c.HQ.Goals {
			row.Lines = append(row.Lines, HQRLine{Text: g.Goal.Progress.Label + ": " + g.Goal.Text})
			for _, ch := range g.Children {
				row.Lines = append(row.Lines, HQRLine{Text: "↳ " + ch.Progress.Label + ": " + ch.Text})
			}
		}
		b.Rows = append(b.Rows, row)
		b.Parts = append(b.Parts, HQRPart{Text: c.HQ.Goals[0].Goal.Progress.Label + " " + c.HQ.Goals[0].Goal.Text})
	}
	if h := c.HQ.Handoffs; h != nil && len(h.Forward) > 0 {
		n := len(h.Forward) + h.MoreForward
		b.Parts = append(b.Parts, HQRPart{Text: plural(n, "handoff", "handoffs") + " to forward"})
		row := HQRRow{Label: "To forward"}
		for _, f := range h.Forward {
			row.Lines = append(row.Lines, HQRLine{Text: f.Title + " · from " + f.Other, URL: f.URL})
		}
		if h.MoreForward > 0 {
			row.Lines = append(row.Lines, HQRLine{Text: fmt.Sprintf("+%d more: %s", h.MoreForward, tracker.InjectProject(p, "issue-cli handoffs --waiting"))})
		}
		b.Rows = append(b.Rows, row)
	}
	if sh := c.HQ.Shelf; sh != nil && sh.ToReview > 0 {
		b.Parts = append(b.Parts, HQRPart{Text: plural(sh.ToReview, "output", "outputs") + " to review", Tone: "you"})
		b.Rows = append(b.Rows, HQRRow{Label: "Shelf", Lines: []HQRLine{{Text: plural(sh.ToReview, "output", "outputs") + " waiting on your review", URL: sh.URL}}})
	}
	if part, row := spendParts(sp, spend.HQ); part != nil {
		b.Parts = append(b.Parts, *part)
		b.Rows = append(b.Rows, *row)
	}
	if len(b.Rows) == 0 {
		b.Empty = "No company goals, forwards or spend yet. HQ's goals go under company: in projects.yaml (" + tracker.InjectProject(p, "issue-cli goals") + ")."
	}
	return b
}

// productBox is Product, the software factory: open work, done this week,
// its goals and spend; its Report (/status) has the rest.
func productBox(p *tracker.Project, prefix string, c CompanyData, issues []*tracker.Issue, sp *spend.Report) HQRBox {
	b := HQRBox{Key: connections.Product, Name: "Product", Color: companyProductColor, Kind: "product", Tone: "idle", Link: prefix + "/status", LinkText: "Product's Report"}
	open := 0
	for _, is := range issues {
		if (len(p.Departments) == 0 || p.DepartmentOf(is) == "") && !strings.EqualFold(is.Status, "done") && !strings.EqualFold(is.Status, "parked") {
			open++
		}
	}
	b.Parts = append(b.Parts, HQRPart{Text: fmt.Sprintf("%d open", open)}, HQRPart{Text: fmt.Sprintf("✓ %d done this week", c.Lot.DoneWeek), Tone: "green"})
	b.Rows = append(b.Rows, HQRRow{Label: "Work", Lines: []HQRLine{{Text: fmt.Sprintf("%d open · ✓ %d done this week", open, c.Lot.DoneWeek)}}})
	if len(c.Product.Goals) > 0 {
		row := HQRRow{Label: "Goals"}
		for _, g := range c.Product.Goals {
			row.Lines = append(row.Lines, HQRLine{Text: g.Progress.Label + ": " + g.Text})
		}
		b.Rows = append(b.Rows, row)
	}
	if h := c.Product.Handoffs; h != nil && len(h.Inbox) > 0 {
		row := HQRRow{Label: "Handoffs in"}
		for _, f := range h.Inbox {
			row.Lines = append(row.Lines, HQRLine{Text: f.Title + " · from " + f.Other + " · " + f.Word, URL: f.URL})
		}
		b.Parts = append(b.Parts, HQRPart{Text: plural(len(h.Inbox)+h.MoreInbox, "handoff", "handoffs") + " in"})
		b.Rows = append(b.Rows, row)
	}
	if part, row := spendParts(sp, spend.Product); part != nil {
		b.Parts = append(b.Parts, *part)
		b.Rows = append(b.Rows, *row)
	}
	return b
}

// deptBox is one department or group: the week section in words, health,
// spend, the group's week report and the lead's newest progress line.
func deptBox(p *tracker.Project, prefix string, dept tracker.Department, color string, sec *weekplan.Section, snap *health.Snapshot, sp *spend.Report, week *WbWeekReport, issues []*tracker.Issue, comments func(*tracker.Issue) []tracker.Comment, now time.Time) HQRBox {
	key := tracker.DepartmentKey(dept.Name)
	b := HQRBox{Key: key, Name: dept.Name, Color: color, Kind: "dept", Link: prefix + "/company", LinkText: dept.Name + " on the company page"}
	if p.IsGroup(dept.Name) {
		b.Kind = "group"
	}
	if line := onTrack(&b, snap); line != "" {
		b.Rows = append(b.Rows, HQRRow{Label: "Health", Lines: []HQRLine{{Text: line, URL: prefix + "/health#departments"}}})
	}
	unmeasured := snap != nil && b.OnTrack == ""
	if sec == nil {
		sec = &weekplan.Section{Box: key, Name: dept.Name}
	}
	if len(sec.Goals) > 0 {
		moved := 0
		row := HQRRow{Label: "Goals"}
		for _, g := range sec.Goals {
			if g.Moved {
				moved++
			}
			text := g.Name + ": " + g.Value
			switch {
			case g.Target != "" && g.Progress == nil:
				text += " (target " + g.Target + ")"
			case g.Target != "":
				text += " of " + g.Target
			}
			if g.Progress != nil {
				text += fmt.Sprintf(" (%d%%)", *g.Progress)
			}
			row.Lines = append(row.Lines, HQRLine{Text: text + " · " + g.Move})
		}
		b.Rows = append(b.Rows, row)
		b.Parts = append(b.Parts, HQRPart{Text: fmt.Sprintf("%d of %s moved", moved, plural(len(sec.Goals), "goal", "goals"))})
	}
	if n := len(sec.Review) + len(sec.Reviewed) + len(sec.Published); n > 0 {
		row := HQRRow{Label: "Outputs"}
		shelf := shelfURL(prefix, key, "")
		add := func(list []weekplan.OutputLine, word, tone string) {
			for _, o := range list {
				row.Lines = append(row.Lines, HQRLine{Text: word + ": " + o.Title + " (" + o.Kind + ", " + o.Date + ")", URL: shelf, Tone: tone})
			}
		}
		add(sec.Published, "shipped", "green")
		add(sec.Reviewed, "reviewed", "")
		add(sec.Review, "waiting on your review", "you")
		b.Rows = append(b.Rows, row)
		if len(sec.Published) > 0 {
			b.Parts = append(b.Parts, HQRPart{Text: fmt.Sprintf("%d shipped", len(sec.Published)), Tone: "green"})
		}
		if len(sec.Review) > 0 {
			b.Parts = append(b.Parts, HQRPart{Text: plural(len(sec.Review), "output", "outputs") + " to review", Tone: "you"})
		}
	}
	if len(sec.Jobs) > 0 {
		row := HQRRow{Label: "Jobs"}
		due, missed := 0, 0
		for _, j := range sec.Jobs {
			text := j.Name + " (" + j.Schedule + ")"
			tone := ""
			switch {
			case j.Error != "":
				text, tone = text+" · error: "+j.Error, "red"
			case j.Due:
				due++
				text += " · due"
				if j.Reason != "" {
					text += ": " + j.Reason
				}
			default:
				text += " · not due"
			}
			if j.Missed > 0 {
				missed += j.Missed
				text += " · missed " + plural(j.Missed, "period", "periods")
				tone = "red"
			}
			url := ""
			if j.Last != "" {
				text += " · last run " + j.Last + " (" + j.Status + ")"
				url = prefix + "/issue/" + j.Last
			} else {
				text += " · never run"
			}
			row.Lines = append(row.Lines, HQRLine{Text: text, URL: url, Tone: tone})
		}
		b.Rows = append(b.Rows, row)
		if due > 0 {
			b.Parts = append(b.Parts, HQRPart{Text: plural(due, "job", "jobs") + " due"})
		}
		if missed > 0 {
			b.Parts = append(b.Parts, HQRPart{Text: "missed " + plural(missed, "period", "periods"), Tone: "red"})
		}
	}
	if len(sec.Blocked) > 0 {
		row := HQRRow{Label: "Blocked"}
		var on []string
		seen, slugs := map[string]bool{}, map[string]bool{}
		for _, l := range sec.Blocked {
			slugs[l.Slug] = true
			row.Lines = append(row.Lines, HQRLine{Text: l.Title + " (" + l.Status + ") · on " + l.On + " · " + l.Why, URL: prefix + "/issue/" + l.Slug, Tone: "red"})
			who := l.On
			if i := strings.Index(who, " ("); i > 0 {
				who = who[:i]
			}
			if !seen[who] {
				seen[who] = true
				on = append(on, who)
			}
		}
		b.Rows = append(b.Rows, row)
		b.Parts = append(b.Parts, HQRPart{Text: fmt.Sprintf("%d blocked (on %s)", len(slugs), strings.Join(on, ", ")), Tone: "red"})
	}
	if len(sec.HandoffsIn)+len(sec.HandoffsOut) > 0 {
		row := HQRRow{Label: "Handoffs"}
		hl := func(h weekplan.HandoffLine, dir string) HQRLine {
			text := h.Title + " · " + dir + " " + h.Other
			switch {
			case h.Waiting != "":
				text += " · waiting on " + h.Waiting + " to forward it"
			case h.State != "":
				text += " · " + h.State
			default:
				text += " · at " + h.Status
			}
			return HQRLine{Text: text, URL: prefix + "/issue/" + h.Slug}
		}
		for _, h := range sec.HandoffsIn {
			row.Lines = append(row.Lines, hl(h, "from"))
		}
		for _, h := range sec.HandoffsOut {
			row.Lines = append(row.Lines, hl(h, "to"))
		}
		b.Rows = append(b.Rows, row)
		b.Parts = append(b.Parts, HQRPart{Text: fmt.Sprintf("handoffs %d in · %d out", len(sec.HandoffsIn), len(sec.HandoffsOut))})
	}
	if part, row := spendParts(sp, key); part != nil {
		b.Parts = append(b.Parts, *part)
		b.Rows = append(b.Rows, *row)
	}
	if week != nil {
		if week.URL != "" {
			b.Rows = append(b.Rows, HQRRow{Label: "Week report", Lines: []HQRLine{{Text: "Week " + week.Week + ": " + week.Line, URL: week.URL}}})
		} else {
			b.Rows = append(b.Rows, HQRRow{Label: "Week report", Lines: []HQRLine{{Text: "none written yet: " + week.Command}}})
		}
	}
	if m, slug, at := leadProgress(p, dept.Name, issues, comments); m != "" {
		b.Rows = append(b.Rows, HQRRow{Label: "Lead", Lines: []HQRLine{{Text: clipRunes(m, 240) + " · " + agoText(now.Sub(at)) + " ago on " + slug, URL: prefix + "/issue/" + slug}}})
	}
	open := HQRRow{Label: "Open work", Lines: []HQRLine{{Text: plural(sec.OpenIssues, "issue", "issues") + " open"}}}
	if sec.OpenIssues > 0 {
		b.Rows = append(b.Rows, open)
		if len(b.Parts) == 0 {
			b.Parts = append(b.Parts, HQRPart{Text: plural(sec.OpenIssues, "issue", "issues") + " open"})
		}
	}
	if len(b.Rows) == 0 {
		b.Empty = "Nothing here yet: no goals, outputs, jobs, handoffs, open work or spend. Start with " + tracker.InjectProject(p, "issue-cli department show "+key) + "."
	} else if unmeasured {
		health := HQRRow{Label: "Health", Lines: []HQRLine{{Text: "not measured yet: it starts with " + deptfamily.StartsMeasuring, URL: prefix + "/health#departments"}}}
		b.Rows = append([]HQRRow{health}, b.Rows...)
	}
	return b
}

// leadProgress is the department lead's newest progress: comment on an
// issue in the department's scope: its text, the issue and when.
func leadProgress(p *tracker.Project, dept string, issues []*tracker.Issue, comments func(*tracker.Issue) []tracker.Comment) (string, string, time.Time) {
	lead := tracker.DepartmentLeadName(p, dept)
	var text, slug string
	var best time.Time
	for _, is := range issues {
		if !p.InScope(dept, p.DepartmentOf(is)) {
			continue
		}
		for _, c := range comments(is) {
			if tracker.CommentAuthor(c) != lead {
				continue
			}
			kind, rest, ok := radioKind(c.Text)
			if !ok || kind != "progress" {
				continue
			}
			at, ok := tracker.CommentTime(c.Date)
			if ok && at.After(best) {
				text, slug, best = rest, is.Slug, at
			}
		}
	}
	return text, slug, best
}

// hqReportMarkdown is the report as text: the headlines first, then each
// box's detail, the same words as the page.
func hqReportMarkdown(d HQReportData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · HQ report · week %s\n\n", d.Name, d.Week)
	fmt.Fprintf(&b, "Looking back over %s. Derived from goals, the shelf, jobs, handoffs, open issues, spend estimates and the newest health snapshot; no agent wrote it. %s", d.Window, upperFirst(d.Snapshot))
	if d.SnapshotCmd != "" {
		fmt.Fprintf(&b, " (`%s`)", d.SnapshotCmd)
	}
	b.WriteString(".\n\n## Headlines\n\n")
	all := append([]HQRBox{d.HQ}, d.Boxes...)
	for _, x := range all {
		fmt.Fprintf(&b, "%s- **%s** — %s\n", strings.Repeat("  ", x.Depth), x.Name, x.HeadlineText())
	}
	if d.NoDepts != "" {
		fmt.Fprintf(&b, "\n%s\n", d.NoDepts)
	}
	for _, x := range all {
		fmt.Fprintf(&b, "\n## %s\n\n", x.Path(all))
		if x.Empty != "" {
			fmt.Fprintf(&b, "%s\n", x.Empty)
			continue
		}
		for _, r := range x.Rows {
			if len(r.Lines) == 1 {
				fmt.Fprintf(&b, "- %s: %s\n", r.Label, r.Lines[0].Text)
				continue
			}
			fmt.Fprintf(&b, "- %s:\n", r.Label)
			for _, l := range r.Lines {
				fmt.Fprintf(&b, "  - %s\n", l.Text)
			}
		}
	}
	return b.String()
}

// HeadlineText is the headline as one line of text.
func (x HQRBox) HeadlineText() string {
	var parts []string
	if x.OnTrack != "" {
		parts = append(parts, x.OnTrack)
	}
	for _, p := range x.Parts {
		parts = append(parts, p.Text)
	}
	if len(parts) == 0 {
		return "nothing here yet"
	}
	return strings.Join(parts, " · ")
}

// Path is the box's name with the groups it sits in: "GTM › Marketing".
func (x HQRBox) Path(all []HQRBox) string {
	if x.Depth == 0 {
		return x.Name
	}
	names := []string{x.Name}
	depth := x.Depth
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Key == x.Key {
			for j := i - 1; j >= 0 && depth > 0; j-- {
				if all[j].Depth == depth-1 && (all[j].Kind == "group") {
					names = append([]string{all[j].Name}, names...)
					depth--
				}
			}
			break
		}
	}
	return strings.Join(names, " › ")
}
