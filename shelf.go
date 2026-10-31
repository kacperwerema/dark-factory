package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The outputs shelf: what each department made (internal/outputs), at
// /p/<project>/shelf. Rooms on the company page link here with their
// "📦 N to review"; rows in review carry the owner's Approve / Reject.

// companyShelfLatest is how many outputs HQ lists.
const companyShelfLatest = 5

// ShelfData is the shelf page.
type ShelfData struct {
	Header WbHeader
	Name   string
	// Dept and State are the filters; Depts and States their tabs.
	Dept, State   string
	Depts, States []WbShelfTab
	Rows          []WbShelfRow
	// Empty: nothing on the shelf at all (not only nothing matching).
	Empty     bool
	Example   string
	ReviewURL string
}

// WbShelfTab is a filter tab with its count.
type WbShelfTab struct {
	Label, URL, Color string
	Count             int
	Current           bool
}

// WbShelfRow is one output on the shelf.
type WbShelfRow struct {
	ID, Short, Icon, Kind, Title string
	// Link is where the title goes ("" for a repo path); Thumb an image.
	Link, Thumb, Path string
	Missing           bool
	Dept, Color       string
	Issue, IssueURL   string
	Job, By, When     string
	State             string
	Review            bool
	// Raw is the sandboxed file when Link is the reader; Version the newest
	// version (shown from 2); Open the comments not resolved.
	Raw           string
	Version, Open int
}

// WbShelfLink is a room's way to its shelf: in review and made this week.
type WbShelfLink struct {
	ToReview, Week int
	URL            string
}

// WbHQShelf is the shelf in HQ: in review across departments, the newest.
type WbHQShelf struct {
	ToReview int
	URL      string
	Latest   []WbShelfRow
}

// WbShelfWait is a department's outputs waiting for the owner's review,
// as a row of the company panel's Waiting on you.
type WbShelfWait struct {
	Name, Color, URL string
	Count            int
}

func shelfURL(prefix, dept, state string) string {
	q := url.Values{}
	if dept != "" {
		q.Set("dept", dept)
	}
	if state != "" {
		q.Set("state", state)
	}
	if len(q) == 0 {
		return prefix + "/shelf"
	}
	return prefix + "/shelf?" + q.Encode()
}

// shelfColors is each department key's colour, as on the company page.
func shelfColors(p *tracker.Project) map[string]string {
	colors := map[string]string{goals.ProductKey: companyProductColor}
	for i, d := range p.Departments {
		colors[outputs.DeptKey(d.Name)] = deptColorOf(p, d, i)
	}
	return colors
}

// shelfRow is one output as a row; talk is the comments per output id (nil
// for none). A markdown output's title opens the reader.
func shelfRow(p *tracker.Project, prefix string, o *outputs.Output, colors map[string]string, talk map[string][]*outputs.Thread) WbShelfRow {
	row := WbShelfRow{
		ID: o.ID, Short: o.ID[strings.LastIndex(o.ID, "-")+1:], Icon: o.Icon(), Kind: o.Kind, Title: o.Title,
		Missing: o.Missing, Dept: o.Name, Color: colors[o.Department], Job: o.Job, By: o.By,
		When: o.Added.Local().Format("2006-01-02 15:04"), State: o.State, Review: o.State == outputs.StateReview,
	}
	if row.Color == "" {
		row.Color = "#8A93A6"
	}
	switch {
	case o.URL != "":
		row.Link = o.URL
	case o.File != "" && !o.Missing:
		row.Link = prefix + "/shelf/file/" + o.File
		if o.IsImage() {
			row.Thumb = row.Link
		}
	case o.Path != "":
		row.Path = o.Path
	}
	if o.URL == "" && o.IsMarkdown() {
		row.Raw, row.Link = row.Link, shelfReadURL(prefix, o.ID)
	}
	row.Version, row.Open = o.Version(), outputs.OpenCount(talk[o.ID])
	if o.Issue != "" {
		row.Issue, row.IssueURL = o.Issue, prefix+"/issue/"+o.Issue
	}
	return row
}

func (s *Server) handleShelf(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	data := s.buildShelf(proj, prefix, r.URL.Query().Get("dept"), r.URL.Query().Get("state"), time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shelf.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildShelf(p *tracker.Project, prefix, dept, state string, now time.Time) ShelfData {
	company := s.buildCompany(p, prefix, now)
	d := ShelfData{Header: company.Header, Name: p.Name, Dept: dept, State: state, ReviewURL: prefix + "/shelf/review"}
	d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Shelf", Current: true}

	all, _ := outputs.Load(p)
	talk, _ := outputs.LoadComments(p)
	d.Empty = len(all) == 0
	d.Example = "issue-cli output add --issue <slug> --title \"...\" --kind page --url <link>"
	colors := shelfColors(p)
	counts := outputs.Counts(all, now)

	d.Depts = append(d.Depts, WbShelfTab{Label: "All", URL: shelfURL(prefix, "", state), Count: len(all), Current: dept == ""})
	for _, key := range outputs.Keys(p) {
		d.Depts = append(d.Depts, WbShelfTab{Label: outputs.DepartmentName(p, key), URL: shelfURL(prefix, key, state), Color: colors[key], Count: counts[key].Total, Current: dept == key})
	}
	inDept := outputs.Select(all, outputs.Filter{Department: dept})
	d.States = append(d.States, WbShelfTab{Label: "Any state", URL: shelfURL(prefix, dept, ""), Count: len(inDept), Current: state == ""})
	for _, st := range outputs.States {
		d.States = append(d.States, WbShelfTab{Label: st, URL: shelfURL(prefix, dept, st), Count: len(outputs.Select(inDept, outputs.Filter{State: st})), Current: state == st})
	}
	for _, o := range outputs.Select(inDept, outputs.Filter{State: state}) {
		d.Rows = append(d.Rows, shelfRow(p, prefix, o, colors, talk))
	}
	return d
}

// handleShelfReview records the owner's Approve / Reject from a shelf row,
// then goes back to the shelf with the same filters.
func (s *Server) handleShelfReview(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	verdict := map[string]string{"approve": outputs.StateApproved, "reject": outputs.StateRejected}[r.FormValue("verdict")]
	if verdict == "" {
		http.Error(w, "verdict must be approve or reject", http.StatusBadRequest)
		return
	}
	quote := "approved on the shelf"
	if verdict == outputs.StateRejected {
		quote = "rejected on the shelf"
	}
	if _, err := outputs.Review(proj, r.FormValue("id"), verdict, quote, "on the shelf", "owner", time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	back := shelfURL(prefix, r.FormValue("dept"), r.FormValue("state"))
	if b := r.FormValue("back"); strings.HasPrefix(b, prefix+"/shelf/read/") && !strings.ContainsAny(b, "\\\r\n") {
		back = b
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handleShelfFile serves a file output add copied onto the shelf; nothing
// outside .outputs/files. A sandbox CSP keeps an SVG or HTML file from
// running script on the viewer's origin.
func (s *Server) handleShelfFile(w http.ResponseWriter, r *http.Request, proj *tracker.Project, rel string) {
	root := outputs.FilesDir(proj)
	path := filepath.Join(root, filepath.FromSlash(rel))
	if back, err := filepath.Rel(root, path); err != nil || back == "." || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

// companyShelf is the shelf on the company page: each department's link
// (by key) and HQ's block; both empty without outputs.
func companyShelf(p *tracker.Project, prefix string, now time.Time) (map[string]*WbShelfLink, *WbHQShelf, []WbShelfWait) {
	all, _ := outputs.Load(p)
	if len(all) == 0 {
		return nil, nil, nil
	}
	colors := shelfColors(p)
	talk, _ := outputs.LoadComments(p)
	links := map[string]*WbShelfLink{}
	hq := &WbHQShelf{URL: shelfURL(prefix, "", "")}
	var waits []WbShelfWait
	counts := outputs.Counts(all, now)
	for _, key := range outputs.Keys(p) {
		c := counts[key]
		if c.Total == 0 {
			continue
		}
		links[key] = &WbShelfLink{ToReview: c.ToReview, Week: c.Week, URL: shelfURL(prefix, key, "")}
		hq.ToReview += c.ToReview
		if c.ToReview > 0 {
			waits = append(waits, WbShelfWait{Name: outputs.DepartmentName(p, key), Color: colors[key], Count: c.ToReview, URL: shelfURL(prefix, key, outputs.StateReview)})
		}
	}
	for i, o := range all {
		if i == companyShelfLatest {
			break
		}
		hq.Latest = append(hq.Latest, shelfRow(p, prefix, o, colors, talk))
	}
	return links, hq, waits
}
