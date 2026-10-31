package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/michal-franc/issue-viewer/internal/depttemplate"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

// The hall's Updates screen (L2, Workshop Board design): the workflow
// features the engine ships that the project's workflow.yaml does not use
// yet, as an upgrade tree and a card per feature. It reads the same upgrade
// plan as the classic tab (updates.go, still at ?classic=1) and posts to the
// same endpoints: updates/install, updates/decline, updates/reopen.
// Design: issues/Factory/designs/workshop-board/L2-updates.

// updatesFeatured is how many cards the page draws before "N more" when no
// feature unlocks anything locked on the board.
const updatesFeatured = 3

// UpdatesWbData is the whole page.
type UpdatesWbData struct {
	Header  WbHeader
	Prefix  string
	Project string
	// ClassicURL is the classic Updates tab (?classic=1).
	ClassicURL string
	Flash      WbUpgradeFlash
	Delivery   WbDelivery
	Tree       WbUpgradeTree
	Filters    []WbFilterChip
	// Featured are the cards drawn first: the features that unlock
	// something the board shows locked, else the newest few.
	Featured      []WbUpgradeCard
	FeaturedTitle string
	FeaturedNote  string
	// Rest are the other proposals: drawn as cards when Expanded, else
	// summed up in More.
	Rest     []WbUpgradeCard
	Expanded bool
	More     WbUpgradeMore
	Declined []WbDeclinedRow
	// ToDecide is every proposal, before the group filter.
	ToDecide int
	// Departments are the bundled department templates the project hasn't
	// installed. They are not in ToDecide or the sidebar count: a
	// department is a projects.yaml entry, not a workflow feature (D-3).
	Departments []WbDeptTemplate
	// DeptUpdates are installed departments whose template has newer
	// text for a file or value the owner never edited (depttemplate.Updates).
	// Like Departments, they are not in ToDecide.
	DeptUpdates []WbDeptUpdate
}

// WbDeptUpdate is one installed department with newer template text: what
// the update replaces, what it keeps, and the commands.
type WbDeptUpdate struct {
	Key, Name, Template, Color string
	// Replace are the files and projects.yaml values still holding an
	// earlier template's text; Keep the ones the owner edited.
	Replace, Keep    []string
	Command, Preview string
}

// updatesDeptUpdates are proj's departments with newer template text.
func updatesDeptUpdates(proj *tracker.Project) []WbDeptUpdate {
	var out []WbDeptUpdate
	for _, u := range depttemplate.Updates(proj) {
		w := WbDeptUpdate{Key: u.Key, Name: u.Name, Template: u.Template.Key, Color: u.Template.Color,
			Command: tracker.InjectProject(proj, "issue-cli department update "+u.Key),
			Preview: tracker.InjectProject(proj, "issue-cli department update "+u.Key+" --dry-run")}
		for _, it := range u.Items {
			what := it.What
			if !it.File {
				what += " in projects.yaml"
			}
			switch it.State {
			case depttemplate.UpdateOld:
				w.Replace = append(w.Replace, what)
			case depttemplate.UpdateEdited:
				w.Keep = append(w.Keep, what)
			}
		}
		out = append(out, w)
	}
	return out
}

// WbDeptTemplate is a department template the project can add, with the
// command that adds it.
type WbDeptTemplate struct {
	Key, Name, Description, Color, Workflow, Command string
	// AddURL is the preview page Add… opens.
	AddURL string
	Goals  int
	// Children are a group template's departments' keys, joined; "" for a
	// department template.
	Children string
}

// updatesDepartments are the bundled department templates proj hasn't
// installed.
func updatesDepartments(proj *tracker.Project, prefix string) []WbDeptTemplate {
	var out []WbDeptTemplate
	for _, t := range depttemplate.List() {
		if t.Installed(proj) {
			continue
		}
		out = append(out, WbDeptTemplate{Key: t.Key, Name: t.Name, Description: t.Description, Color: t.Color, Workflow: t.Workflow, Goals: len(t.Goals),
			Children: strings.Join(t.Children, ", "),
			Command:  tracker.InjectProject(proj, "issue-cli department add "+t.Key)})
		if proj.ConfigPath != "" {
			out[len(out)-1].AddURL = prefix + "/departments/new?template=" + url.QueryEscape(t.Key)
		}
	}
	return out
}

// WbUpgradeFlash is the line after a click: declined (with Undo),
// re-opened, install started, or an error.
type WbUpgradeFlash struct {
	Text, Undo, Prefix string
	Error              bool
}

// WbDelivery is what the engine delivers: the template the hall's
// workflow.yaml was made from, and the engine's version of it.
type WbDelivery struct {
	Template, From, To string
	// State is the classic tab's word for it (behind, up to date, …) as a
	// chip; nil while it would only repeat the count to decide.
	State *WbChip
}

// WbUpgradeTree is a square per feature: filled installed, grey declined,
// dashed orange still to decide.
type WbUpgradeTree struct {
	Installed, Declined, ToDecide int
	Recorded                      bool
	Squares                       []WbTreeSquare
	// Pill is "N to decide".
	Pill WbChip
	// InstalledIDs are the installed features, listed in the tree's fold.
	InstalledIDs []string
}

// WbTreeSquare is one feature in the tree. State is installed, declined or
// decide.
type WbTreeSquare struct {
	ID, State string
}

// Label is the square's tooltip.
func (q WbTreeSquare) Label() string {
	switch q.State {
	case "installed":
		return q.ID + " · installed"
	case "declined":
		return q.ID + " · declined"
	}
	return q.ID + " · to decide"
}

// WbFilterChip is a group filter: a pill link with a count.
type WbFilterChip struct {
	Label, URL, TestID string
	Count              int
	Active             bool
}

// WbUnlockLink is something the board shows locked until the feature is in
// workflow.yaml, linked to the page that shows it.
type WbUnlockLink struct {
	Text, URL string
}

// WbUpgradeCard is one proposed feature.
type WbUpgradeCard struct {
	UpdateCard
	Letter, Group string
	// Key is where the feature lives in workflow.yaml (the catalog's first
	// detection path); WhereText is the catalog's full where.
	Key     string
	Unlocks []WbUnlockLink
	Prefix  string
	// GroupChip is the category; HaveChip the installed version of an
	// update; InstallingChip shows while its install agent runs.
	GroupChip, InstallingChip WbChip
	HaveChip                  *WbChip
	Steps                     []WbSetupStep
}

// WbSetupStep is a setup step beyond the snippet: who does it (you or the
// agent) and what.
type WbSetupStep struct {
	Who WbChip
	Do  string
}

// WbUpgradeMore is the "N more" row: the proposals not drawn, by group.
type WbUpgradeMore struct {
	Count   int
	Groups  []string
	ShowURL string
}

// WbDeclinedRow is a declined feature with Re-open.
type WbDeclinedRow struct {
	UpdateRecordRow
	Prefix string
}

// updatesGroupName is a catalog category as a filter label.
func updatesGroupName(category string) string {
	if category == "" {
		return ""
	}
	return strings.ToUpper(category[:1]) + category[1:]
}

// updatesLetter is the card's badge: an acronym in the title (CI, PR), else
// the title's first letter.
func updatesLetter(title string) string {
	for _, w := range strings.FieldsFunc(title, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if n := len(w); n >= 2 && n <= 3 && strings.ToUpper(w) == w {
			return w
		}
	}
	for _, r := range title {
		if unicode.IsLetter(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return "?"
}

// updatesFlash is the flash a redirect after a POST asks for.
func updatesFlash(q url.Values) (text, undo string, isErr bool) {
	switch {
	case q.Get("declined") != "":
		id := q.Get("declined")
		return id + " declined. It won't be proposed again; the record is in workflow.yaml (features.declined).", id, false
	case q.Get("reopened") != "":
		return q.Get("reopened") + " re-opened: it is proposed again.", "", false
	case q.Get("installing") != "":
		return "Install agent for " + q.Get("installing") + " started. It asks you its question in tmux; the card leaves the list once workflow scan records it.", "", false
	case q.Get("error") != "":
		return q.Get("error"), "", true
	}
	return "", "", false
}

func (s *Server) handleUpdatesWb(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string, plan *workflowupgrade.Plan) {
	q := r.URL.Query()
	data := s.buildUpdatesWb(proj, prefix, plan, q, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "updates_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// updatesHeader is the hall's header: Map › project › Product hall.
func (s *Server) updatesHeader(p *tracker.Project, prefix string, now time.Time) WbHeader {
	company := s.buildCompany(p, prefix, now)
	lot := company.Lot
	h := WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(2, p.Name, prefix+"/company"), Tabs: wbHallTabs(prefix, "updates"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: company.Waiting, HideDone: true}
	h.Crumbs[1].Color = lot.Color
	h.YOLO = headerYOLO(lot.YOLO, prefix)
	h.Crumbs[2] = WbCrumb{Label: "Product hall", Current: true, Color: companyProductColor}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		h.Chips = append(h.Chips, *c)
	}
	return h
}

func (s *Server) buildUpdatesWb(p *tracker.Project, prefix string, plan *workflowupgrade.Plan, q url.Values, now time.Time) UpdatesWbData {
	category := q.Get("category")
	classic := buildUpdatesData(p, prefix, plan, "", q.Get("install"))
	d := UpdatesWbData{
		Header:      s.updatesHeader(p, prefix, now),
		Prefix:      prefix,
		Project:     p.Name,
		ClassicURL:  prefix + "/updates?classic=1",
		ToDecide:    classic.Total,
		Expanded:    q.Get("all") == "1" || category != "",
		Departments: updatesDepartments(p, prefix),
		DeptUpdates: updatesDeptUpdates(p),
	}
	if text, undo, isErr := updatesFlash(q); text != "" {
		d.Flash = WbUpgradeFlash{Text: text, Undo: undo, Error: isErr, Prefix: prefix}
	}

	// The state chip says what the versions don't: behind, no stamp, not
	// recorded or up to date ("N undecided" is the tree's pill).
	d.Delivery = WbDelivery{Template: plan.Template, From: plan.Template, To: plan.Latest}
	if plan.Current != "" {
		d.Delivery.From = plan.Template + "@" + plan.Current
	}
	if !strings.HasSuffix(classic.State, "undecided") {
		chip := WbChip{Text: classic.State, Tone: "idle", TestID: "upd_state"}
		switch classic.StateClass {
		case "chip-amber":
			chip.Tone = "you"
		case "chip-green":
			chip.Tone = "green"
		}
		d.Delivery.State = &chip
	}

	d.Tree = WbUpgradeTree{Installed: len(classic.Installed), Declined: len(classic.Declined), ToDecide: classic.Total, Recorded: classic.HasRecord,
		Pill: WbChip{Tone: "you", Text: fmt.Sprintf("%d to decide", classic.Total), TestID: "to_decide"}}
	for _, id := range classic.Installed {
		d.Tree.Squares = append(d.Tree.Squares, WbTreeSquare{ID: id, State: "installed"})
	}
	d.Tree.InstalledIDs = classic.Installed
	for _, row := range classic.Declined {
		d.Tree.Squares = append(d.Tree.Squares, WbTreeSquare{ID: row.ID, State: "declined"})
	}
	for _, c := range classic.Cards {
		d.Tree.Squares = append(d.Tree.Squares, WbTreeSquare{ID: c.ID, State: "decide"})
	}

	// Group filters: the catalog's categories with something to decide.
	base := prefix + "/updates"
	for _, chip := range classic.Chips {
		f := WbFilterChip{Label: updatesGroupName(chip.Name), Count: chip.Count, URL: base, TestID: chip.Name, Active: chip.Name == category}
		if chip.Name == "all" {
			f.Label, f.Active = "All", category == ""
		} else {
			f.URL = base + "?category=" + url.QueryEscape(chip.Name)
		}
		d.Filters = append(d.Filters, f)
	}

	entries, _ := workflowupgrade.Catalog()
	keys := map[string]string{}
	unlocks := map[string][]workflowupgrade.Unlock{}
	for _, e := range entries {
		if len(e.Detect) > 0 {
			keys[e.ID] = e.Detect[0].Path
		}
		unlocks[e.ID] = e.Unlocks
	}
	var unlocking, other []WbUpgradeCard
	for _, c := range classic.Cards {
		if category != "" && c.Category != category {
			continue
		}
		card := WbUpgradeCard{UpdateCard: c, Letter: updatesLetter(c.Title), Group: c.Category, Key: keys[c.ID], Prefix: prefix,
			GroupChip: WbChip{Tone: "outline", Text: c.Category}, InstallingChip: WbChip{Tone: "green", Text: "installing", Dot: true}}
		if c.Kind == workflowupgrade.ProposalUpdate {
			card.HaveChip = &WbChip{Tone: "idle", Text: "you have " + c.Installed}
		}
		for _, st := range c.Setup {
			who := WbChip{Tone: "idle", Text: "agent"}
			if st.Who == "owner" {
				who = WbChip{Tone: "you", Text: "you"}
			}
			card.Steps = append(card.Steps, WbSetupStep{Who: who, Do: strings.TrimSpace(st.Do)})
		}
		for _, u := range unlocks[c.ID] {
			card.Unlocks = append(card.Unlocks, WbUnlockLink{Text: u.Text, URL: prefix + u.URL})
		}
		if len(card.Unlocks) > 0 {
			unlocking = append(unlocking, card)
		} else {
			other = append(other, card)
		}
	}
	if len(unlocking) > 0 {
		d.Featured, d.Rest = unlocking, other
		d.FeaturedTitle = "They unlock what you see locked"
	} else {
		n := min(updatesFeatured, len(other))
		d.Featured, d.Rest = other[:n], other[n:]
		d.FeaturedTitle = "To decide"
	}
	if len(d.Featured) > 0 {
		d.FeaturedNote = "each install starts an agent: it asks you its question, edits workflow.yaml and shows you the diff before any commit"
	}
	if !d.Expanded && len(d.Rest) > 0 {
		counts := map[string]int{}
		for _, c := range d.Rest {
			counts[c.Category]++
		}
		d.More = WbUpgradeMore{Count: len(d.Rest), ShowURL: base + "?all=1"}
		for _, c := range workflowupgrade.Categories {
			if counts[c] > 0 {
				d.More.Groups = append(d.More.Groups, fmt.Sprintf("%s %d", updatesGroupName(c), counts[c]))
			}
		}
	}

	for _, row := range classic.Declined {
		row.Just = row.ID == q.Get("declined")
		d.Declined = append(d.Declined, WbDeclinedRow{UpdateRecordRow: row, Prefix: prefix})
	}
	return d
}
