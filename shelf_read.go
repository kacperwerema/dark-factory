package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The output reader: a markdown output on the shelf as a document, at
// /p/<project>/shelf/read/<id>, with its contents, the owner's comments in
// the margin (internal/outputs comments.go) and what changed since the last
// version. Components in templates/_workshop_shelf_read.html, page
// templates/shelf_read.html, behaviour static/shelf-read.js, styles in the
// "L2: output reader" block of static/workshop.css. Comments are the
// owner's: the writes take s.fromOwner; agents read them with
// issue-cli output comments and answer with output reply.

// odocFoldRun is the run of unchanged diff lines folded into one bar.
const odocFoldRun = 6

// ShelfReadData is the reader page.
type ShelfReadData struct {
	Header WbHeader
	Name   string
	Row    WbShelfRow
	// Title is the document's own leading "# " heading, else the output's.
	Title string
	// View is "read" or "changes"; Night isn't here (the browser keeps it).
	View                                       string
	ReadURL, ChangesURL, ReviewURL, CommentURL string
	ResolveURL, Back                           string
	Revised                                    string
	// Prev is the version Changes compares with (0: one version only).
	Prev int
	Toc  []OdocTocItem
	Body template.HTML
	// Missing: the file is gone (the page says so; comments still show).
	Missing bool
	Diff    *OdocDiff
	// Threads are the open ones in document order; Gone the open ones whose
	// passage and heading are not in this version; Resolved the rest.
	Threads, Gone, Resolved []OdocThread
	Open                    int
	// NoIssue: the output has no issue, so comments don't reach a trail.
	NoIssue bool
	// Marks are the open threads' anchors for static/shelf-read.js.
	Marks template.JS
	// Command is how an agent reads the comments.
	Command string
}

// OdocTocItem is one heading in the contents.
type OdocTocItem struct {
	Level          int
	ID, Text       string
	Open, Resolved int
}

// OdocDiff is the previous version against this one.
type OdocDiff struct {
	From, To string
	Add, Del int
	Lines    []OdocDiffLine
}

// OdocDiffLine is a diff line ("add", "del", "same") or a fold of Gap
// unchanged lines.
type OdocDiffLine struct {
	Kind, Op, Text string
	Gap            int
}

// OdocThread is a thread in the margin.
type OdocThread struct {
	ID, Place       string
	Where, WhereURL string
	Quote           string
	Messages        []OdocMsg
	Resolved        bool
	Note            string
	at              int
}

// OdocMsg is one message of a thread.
type OdocMsg struct {
	Who, When, Text string
	Version         int
}

// odocMark is what the page's script needs to light a passage.
type odocMark struct {
	ID      string `json:"id"`
	Quote   string `json:"quote,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
	Suffix  string `json:"suffix,omitempty"`
	Heading string `json:"heading,omitempty"`
	Place   string `json:"place"`
}

func shelfReadURL(prefix, id string) string { return prefix + "/shelf/read/" + id }

func renderOutputDoc(src []byte) odocRendered {
	d := outputs.Render(src)
	out := odocRendered{Title: d.Title, HTML: d.HTML, Text: d.Text, Headings: d.Headings}
	for _, h := range d.Toc {
		out.Toc = append(out.Toc, OdocTocItem{Level: h.Level, ID: h.ID, Text: h.Text})
	}
	return out
}

// odocRendered is a rendered output with the page's contents items.
type odocRendered struct {
	Title, HTML, Text string
	Toc               []OdocTocItem
	Headings          map[string]int
}

// outputFile is where a version's file is (a shelf file or a repo path).
func outputFile(p *tracker.Project, o *outputs.Output, file string) string {
	if file != "" {
		return filepath.Join(outputs.FilesDir(p), filepath.FromSlash(file))
	}
	return filepath.Join(p.Root(), filepath.FromSlash(o.Path))
}

func readOutputText(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > outputs.MaxFileBytes {
		return nil, fmt.Errorf("%s is over %d MB", filepath.Base(path), outputs.MaxFileBytes>>20)
	}
	return os.ReadFile(path)
}

// headingPath is "Brand › Proposal": the heading id's parents and itself.
func headingPath(toc []OdocTocItem, id string) string {
	for i, h := range toc {
		if h.ID != id {
			continue
		}
		path := []string{h.Text}
		level := h.Level
		for j := i - 1; j >= 0 && level > 2; j-- {
			if toc[j].Level < level {
				path = append([]string{toc[j].Text}, path...)
				level = toc[j].Level
			}
		}
		return strings.Join(path, " › ")
	}
	return ""
}

func (s *Server) handleShelfRead(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, id string) {
	list, _ := outputs.Load(proj)
	o, err := outputs.Find(list, id)
	if err != nil || o.ID != id || o.URL != "" || !o.IsMarkdown() {
		http.NotFound(w, r)
		return
	}
	data := s.buildShelfRead(proj, prefix, o, r.URL.Query().Get("view"), time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shelf_read.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildShelfRead(p *tracker.Project, prefix string, o *outputs.Output, view string, now time.Time) ShelfReadData {
	company := s.buildCompany(p, prefix, now)
	talk, _ := outputs.LoadComments(p)
	read := shelfReadURL(prefix, o.ID)
	d := ShelfReadData{
		Header: company.Header, Name: p.Name, Row: shelfRow(p, prefix, o, shelfColors(p), talk), Title: o.Title,
		View: "read", ReadURL: read, ChangesURL: read + "?view=changes", Back: read,
		ReviewURL: prefix + "/shelf/review", CommentURL: prefix + "/shelf/comment", ResolveURL: prefix + "/shelf/comment/resolve",
		NoIssue: o.Issue == "", Command: "issue-cli output comments " + o.ID[strings.LastIndex(o.ID, "-")+1:],
	}
	d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Shelf", URL: shelfURL(prefix, o.Department, "")}
	d.Header.Crumbs = append(d.Header.Crumbs, WbCrumb{Label: o.Title, Current: true})
	if v := o.Versions; len(v) > 1 {
		d.Revised = v[len(v)-1].Date.Local().Format("2006-01-02 15:04")
		d.Prev = v[len(v)-2].N
	}

	src, err := readOutputText(outputFile(p, o, o.File))
	var doc odocRendered
	if err != nil {
		d.Missing = true
	} else {
		doc = renderOutputDoc(src)
		d.Body = template.HTML(doc.HTML)
		d.Toc = doc.Toc
		if doc.Title != "" {
			d.Title = doc.Title
		}
	}
	if prev := o.Previous(); prev != nil && view == "changes" {
		d.View = "changes"
		old, _ := readOutputText(outputFile(p, o, prev.File))
		d.Diff = odocDiff(o, prev, string(old), string(src))
	}

	// The margin: each thread placed in this version.
	var marks []odocMark
	byHeading := map[string]*OdocTocItem{}
	for i := range d.Toc {
		byHeading[d.Toc[i].ID] = &d.Toc[i]
	}
	for _, t := range talk[o.ID] {
		place, at := outputs.PlaceGone, -1
		if !d.Missing {
			place, at = outputs.Locate(t, doc.Text, doc.Headings)
		} else if t.Anchor.Whole() {
			place = outputs.PlaceWhole
		}
		th := odocThread(prefix, o, t, d.Toc, place, at)
		heading := t.Anchor.Heading
		if place == outputs.PlaceHere && heading == "" {
			heading = headingAt(doc.Headings, at)
		}
		if h := byHeading[heading]; h != nil && place != outputs.PlaceGone {
			if t.Resolved {
				h.Resolved++
			} else {
				h.Open++
			}
		}
		switch {
		case t.Resolved:
			d.Resolved = append(d.Resolved, th)
		case place == outputs.PlaceGone:
			d.Gone = append(d.Gone, th)
		default:
			d.Threads = append(d.Threads, th)
			marks = append(marks, odocMark{ID: t.ID, Quote: t.Anchor.Quote, Prefix: t.Anchor.Prefix, Suffix: t.Anchor.Suffix, Heading: t.Anchor.Heading, Place: place})
		}
	}
	sort.SliceStable(d.Threads, func(i, j int) bool { return d.Threads[i].at < d.Threads[j].at })
	d.Open = len(d.Threads) + len(d.Gone)
	if marks == nil {
		marks = []odocMark{}
	}
	js, _ := json.Marshal(marks)
	d.Marks = template.JS(js)
	return d
}

// headingAt is the id of the last heading at or before offset at.
func headingAt(headings map[string]int, at int) string {
	best, bestAt := "", -1
	for id, off := range headings {
		if off <= at && off > bestAt {
			best, bestAt = id, off
		}
	}
	return best
}

func odocThread(prefix string, o *outputs.Output, t *outputs.Thread, toc []OdocTocItem, place string, at int) OdocThread {
	th := OdocThread{ID: t.ID, Place: place, Quote: t.Anchor.Quote, Resolved: t.Resolved, at: at}
	switch {
	case place == outputs.PlaceWhole:
		th.Where = "Whole document"
	case headingPath(toc, t.Anchor.Heading) != "":
		th.Where, th.WhereURL = headingPath(toc, t.Anchor.Heading), "#"+t.Anchor.Heading
	case t.Anchor.HeadingText != "":
		th.Where = t.Anchor.HeadingText
	default:
		th.Where = "A passage"
	}
	switch place {
	case outputs.PlaceHeading:
		if th.Quote != "" {
			th.Note = fmt.Sprintf("The passage is not in v%d; the heading still is.", o.Version())
		}
	case outputs.PlaceGone:
		th.Note = fmt.Sprintf("Neither the passage nor its heading is in v%d.", o.Version())
	}
	if t.Resolved {
		th.Note = "Resolved " + t.ResolvedAt.Local().Format("2006-01-02 15:04")
	}
	for _, m := range t.Messages {
		th.Messages = append(th.Messages, OdocMsg{Who: odocWho(m.By), When: m.Date.Local().Format("2006-01-02 15:04"), Text: m.Text, Version: m.Version})
	}
	return th
}

func odocWho(by string) string {
	if by == "" || by == tracker.OwnerAuthor || by == "owner" {
		return "You"
	}
	return by
}

func odocDiff(o *outputs.Output, prev *outputs.Version, old, cur string) *OdocDiff {
	d := &OdocDiff{
		From: fmt.Sprintf("v%d (%s)", prev.N, prev.Date.Local().Format("2006-01-02 15:04")),
		To:   fmt.Sprintf("v%d (%s)", o.Version(), o.Versions[len(o.Versions)-1].Date.Local().Format("2006-01-02 15:04")),
	}
	lines := outputs.LineDiff(old, cur)
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch l.Op {
		case '+':
			d.Add++
			d.Lines = append(d.Lines, OdocDiffLine{Kind: "add", Op: "+", Text: l.Text})
		case '-':
			d.Del++
			d.Lines = append(d.Lines, OdocDiffLine{Kind: "del", Op: "−", Text: l.Text})
		default:
			j := i
			for j < len(lines) && lines[j].Op == ' ' {
				j++
			}
			run := lines[i:j]
			if len(run) > odocFoldRun+2 {
				head, tail := 1, 1
				if i == 0 {
					head = 0
				}
				if j == len(lines) {
					tail = 0
				}
				for _, s := range run[:head] {
					d.Lines = append(d.Lines, OdocDiffLine{Kind: "same", Op: " ", Text: s.Text})
				}
				d.Lines = append(d.Lines, OdocDiffLine{Kind: "gap", Gap: len(run) - head - tail})
				for _, s := range run[len(run)-tail:] {
					d.Lines = append(d.Lines, OdocDiffLine{Kind: "same", Op: " ", Text: s.Text})
				}
			} else {
				for _, s := range run {
					d.Lines = append(d.Lines, OdocDiffLine{Kind: "same", Op: " ", Text: s.Text})
				}
			}
			i = j - 1
		}
	}
	return d
}

// shelfCommentRefused is the 403 for a comment write that isn't the owner's
// browser.
const shelfCommentRefused = "only the owner comments on an output, from its page in the browser; an agent reads the comments with issue-cli output comments <id> and answers with issue-cli output reply <id> <thread> --text \"...\""

type shelfCommentRequest struct {
	ID     string          `json:"id"`
	Thread string          `json:"thread"`
	Text   string          `json:"text"`
	Anchor *outputs.Anchor `json:"anchor"`
	Reopen bool            `json:"reopen"`
}

func (s *Server) shelfCommentTarget(w http.ResponseWriter, r *http.Request, proj *tracker.Project) (*outputs.Output, shelfCommentRequest, bool) {
	var req shelfCommentRequest
	if !s.fromOwner(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": shelfCommentRefused})
		return nil, req, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.ID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return nil, req, false
	}
	list, _ := outputs.Load(proj)
	o, err := outputs.Find(list, req.ID)
	if err != nil || o.ID != req.ID {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no output " + req.ID})
		return nil, req, false
	}
	return o, req, true
}

// handleShelfComment opens a thread (anchor or none: the whole document) or
// replies to one, for the owner; each goes on the output's issue as a
// revise: comment quoting the passage.
func (s *Server) handleShelfComment(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	o, req, ok := s.shelfCommentTarget(w, r, proj)
	if !ok {
		return
	}
	now := time.Now()
	var t *outputs.Thread
	var err error
	if req.Thread != "" {
		t, err = outputs.Reply(proj, o.ID, req.Thread, req.Text, tracker.OwnerAuthor, o.Version(), now)
	} else {
		a := outputs.Anchor{}
		if req.Anchor != nil {
			a = *req.Anchor
		}
		t, err = outputs.AddComment(proj, o.ID, a, req.Text, tracker.OwnerAuthor, o.Version(), now)
	}
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"status": "ok", "thread": t.ID}
	if o.Issue == "" {
		resp["trail"] = "no issue: the comment is on the shelf only"
	} else if issue := s.findIssueBySlug(proj, o.Issue); issue == nil {
		resp["trail"] = "issue " + o.Issue + " not found: the comment is on the shelf only"
	} else {
		var toc []OdocTocItem
		if src, err := readOutputText(outputFile(proj, o, o.File)); err == nil {
			toc = renderOutputDoc(src).Toc
		}
		text := reviseComment(o, t, toc, t.Messages[len(t.Messages)-1].Text, req.Thread != "")
		if err := tracker.AddCommentBy(issue.FilePath, 0, text, "app", tracker.OwnerAuthor); err != nil {
			resp["trail"] = "not posted on " + o.Issue + ": " + err.Error()
		} else {
			resp["trail"] = "posted on " + o.Issue
		}
	}
	writeJSONStatus(w, http.StatusCreated, resp)
}

// reviseComment is the trail line for a comment or reply:
// revise: output ab00 v1 · Brand › Proposal · "…" — text (thread c-1a2b · issue-cli output comments ab00)
func reviseComment(o *outputs.Output, t *outputs.Thread, toc []OdocTocItem, text string, reply bool) string {
	short := o.ID[strings.LastIndex(o.ID, "-")+1:]
	where := "whole document"
	if !t.Anchor.Whole() {
		where = headingPath(toc, t.Anchor.Heading)
		if where == "" {
			where = t.Anchor.HeadingText
		}
	}
	parts := []string{fmt.Sprintf("output %s v%d", short, o.Version())}
	if where != "" {
		parts = append(parts, where)
	}
	if t.Anchor.Quote != "" {
		parts = append(parts, "“"+t.Anchor.Quote+"”")
	}
	lead := "revise: "
	if reply {
		lead = "revise: (reply) "
	}
	text = strings.Join(strings.Fields(text), " ")
	return fmt.Sprintf("%s%s — %s (thread %s · issue-cli output comments %s)", lead, strings.Join(parts, " · "), text, t.ID, short)
}

// handleShelfCommentResolve resolves or reopens a thread, for the owner.
func (s *Server) handleShelfCommentResolve(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	o, req, ok := s.shelfCommentTarget(w, r, proj)
	if !ok {
		return
	}
	t, err := outputs.Resolve(proj, o.ID, req.Thread, tracker.OwnerAuthor, req.Reopen, time.Now())
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"status": "ok", "thread": t.ID, "resolved": t.Resolved})
}
