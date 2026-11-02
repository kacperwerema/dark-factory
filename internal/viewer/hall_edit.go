package viewer

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/wfedit"
)

// The Edit form of a station opened in the hall (/hall/station/<status>/edit):
// the status's description and prompt, the actions on the base edges into
// and out of it, and the owner's gate on each edge in. Preview shows the
// diff of the workflow file and its checks; Confirm writes it and commits
// that one file on main with the owner's reason quoted. The editing itself
// is internal/wfedit; this file is the form and its routes.
// Design: issues/Factory/hall-edit-the-workflow-from-the-hall.md.

// HallEditData is the whole Edit page.
type HallEditData struct {
	Header  WbHeader
	Status  string
	Title   string
	PartURL string
	EditURL string
	// File is the workflow file as the page names it; Hash the version the
	// form was made from.
	File, Hash          string
	Description, Prompt string
	Reason              string
	Edges               []HallEditEdge
	ReadOnly            []wfedit.Ref
	ActionTypes         []string
	// Here is how many issues sit in the station: they pick the change up
	// on their next issue-cli call.
	Here    int
	YOLO    bool
	Preview *HallEditPreview
	Error   string
}

// HallEditEdge is one base edge in the form. In: it enters the station, so
// it has the gate switch and its gate is not among Actions; GatePos is
// where the gate sat, so keeping it on keeps it there.
type HallEditEdge struct {
	Index    int
	Label    string
	From, To string
	In       bool
	Gate     bool
	GatePos  int
	Line     int
	Actions  []tracker.WorkflowAction
	// Rows are Actions as the form draws them; Blank is the row + Add
	// action copies.
	Rows  []HallEditRow
	Blank HallEditRow
}

// HallEditRow is one action row: its fields are named Pre+field, and every
// row posts every field so the n-th values make the n-th action.
type HallEditRow struct {
	Pre    string
	A      tracker.WorkflowAction
	Values string
	Types  []string
}

// HallEditPreview is Apply's result as the page shows it.
type HallEditPreview struct {
	OK       bool
	Summary  string
	Diff     []HallDiffLine
	Errors   []string
	Warnings []string
	Gates    []string
}

// HallDiffLine is one line of the diff; Kind is add, del, hunk or "".
type HallDiffLine struct{ Kind, Text string }

// hallEditFile is the workflow file the Product hall's stations come
// from, "" when the project runs on the built-in workflow.
func hallEditFile(p *tracker.Project) string { return p.WorkflowPath() }

// hallEditStatus is the status the URL key names, "" when none.
func hallEditStatus(p *tracker.Project, key string) string {
	for _, st := range p.LoadWorkflow().Statuses {
		if hallPartKey(st.Name) == key {
			return st.Name
		}
	}
	return ""
}

func (s *Server) handleHallEdit(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, key, action string) {
	file, status := hallEditFile(proj), hallEditStatus(proj, key)
	if status == "" {
		http.NotFound(w, r)
		return
	}
	if file == "" {
		http.Error(w, "this project runs on the built-in workflow: there is no workflow.yaml to edit (issue-cli workflow init writes one)", http.StatusNotFound)
		return
	}
	st, err := wfedit.Open(file, status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := s.hallEditPage(proj, prefix, key, file, st)
	if action == "" {
		s.renderHallEdit(w, d, http.StatusOK)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hash := r.PostForm.Get("hash")
	ch, edges := hallEditChange(r, st)
	d.Hash, d.Edges = hash, edges
	d.Description, d.Prompt, d.Reason = *ch.Description, *ch.Prompt, r.PostForm.Get("reason")
	pv, err := wfedit.Apply(file, status, hash, ch)
	if err != nil {
		d.Error = err.Error()
		if errors.Is(err, wfedit.ErrStale) {
			s.renderHallEdit(w, d, http.StatusConflict)
			return
		}
		s.renderHallEdit(w, d, http.StatusBadRequest)
		return
	}
	d.Preview = hallEditPreview(pv)
	if action == "preview" {
		s.renderHallEdit(w, d, http.StatusOK)
		return
	}
	saved, err := wfedit.Save(pv, d.Reason)
	if err != nil {
		d.Error = err.Error()
		code := http.StatusBadRequest
		if errors.Is(err, wfedit.ErrStale) {
			code = http.StatusConflict
		}
		s.renderHallEdit(w, d, code)
		return
	}
	http.Redirect(w, r, d.PartURL+"?saved="+saved.Commit, http.StatusSeeOther)
}

func (s *Server) renderHallEdit(w http.ResponseWriter, d HallEditData, code int) {
	for i := range d.Edges {
		e := &d.Edges[i]
		pre := fmt.Sprintf("e%d-", e.Index)
		e.Rows = nil
		for _, a := range e.Actions {
			e.Rows = append(e.Rows, HallEditRow{Pre: pre, A: a, Values: strings.Join(a.Values, ", "), Types: d.ActionTypes})
		}
		e.Blank = HallEditRow{Pre: pre, A: tracker.WorkflowAction{Type: "validate"}, Types: d.ActionTypes}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := s.tmpl.ExecuteTemplate(w, "hall_edit.html", d); err != nil {
		fmt.Fprintf(w, "<pre>%s</pre>", err)
	}
}

// hallEditPage is the form as the file is now.
func (s *Server) hallEditPage(p *tracker.Project, prefix, key, file string, st *wfedit.Station) HallEditData {
	part, _ := s.buildHallPart(p, prefix, "station", key, time.Now())
	d := HallEditData{Header: part.Header, Status: st.Name, Title: part.Title, PartURL: hallPartURL(prefix, "station", st.Name),
		File: hallEditFileName(p, file), Hash: st.Hash, Description: st.Description, Prompt: st.Prompt, ReadOnly: st.ReadOnly}
	d.EditURL = d.PartURL + "/edit"
	// "Edit" stands in for the station's crumb (the title bar links back to
	// it), so the header keeps its width on a phone.
	if n := len(d.Header.Crumbs); n > 0 {
		d.Header.Crumbs[n-1] = WbCrumb{Label: "Edit", Current: true}
	}
	if part.Station != nil {
		d.Here = part.Station.Count
	}
	d.YOLO = part.YOLOOn
	for _, t := range tracker.WorkflowActionTypes {
		d.ActionTypes = append(d.ActionTypes, t.Name)
	}
	d.Edges = hallEditEdges(st)
	return d
}

// hallEditFileName is file relative to the project's repo when it is in
// it, else its base name.
func hallEditFileName(p *tracker.Project, file string) string {
	if root := projectRoot(p); root != "" {
		if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(file)
}

// hallEditEdges is the station's base edges for the form: the ways in
// first (a self edge only there), the gate taken out of an edge in.
func hallEditEdges(st *wfedit.Station) []HallEditEdge {
	var out []HallEditEdge
	seen := map[int]bool{}
	for _, e := range st.In {
		he := HallEditEdge{Index: e.Index, Label: e.Label(), From: e.From, To: e.To, In: true, Line: e.Line, GatePos: -1}
		for i, a := range e.Actions {
			if a.Type == "require_human_approval" && (strings.TrimSpace(a.Status) == "" || a.Status == e.To) {
				if !he.Gate {
					he.Gate, he.GatePos = true, i
				}
				continue
			}
			he.Actions = append(he.Actions, a)
		}
		out = append(out, he)
		seen[e.Index] = true
	}
	for _, e := range st.Out {
		if !seen[e.Index] {
			out = append(out, HallEditEdge{Index: e.Index, Label: e.Label(), From: e.From, To: e.To, Line: e.Line, GatePos: -1, Actions: e.Actions})
		}
	}
	return out
}

// hallEditChange reads the posted form as a Change, and the edges as the
// form showed them (to render it again).
func hallEditChange(r *http.Request, st *wfedit.Station) (wfedit.Change, []HallEditEdge) {
	f := r.PostForm
	desc, prompt := f.Get("description"), crlf(f.Get("prompt"))
	ch := wfedit.Change{Description: &desc, Prompt: &prompt, Edges: map[int][]tracker.WorkflowAction{}}
	var edges []HallEditEdge
	for _, e := range hallEditEdges(st) {
		pre := fmt.Sprintf("e%d-", e.Index)
		if f.Get(pre+"present") == "" {
			continue
		}
		var rows []tracker.WorkflowAction
		for n := range f[pre+"type"] {
			rows = append(rows, hallEditAction(f, pre, n))
		}
		e.Actions = rows
		acts := rows
		if e.In {
			e.Gate = f.Get(pre+"gate") != ""
			acts = placeGate(rows, e.To, e.Gate, e.GatePos)
		}
		ch.Edges[e.Index] = acts
		edges = append(edges, e)
	}
	return ch, edges
}

// placeGate is rows with the gate for to: at pos when it was there before
// (so keeping it on changes nothing), else where wfedit.SetGate puts it.
func placeGate(rows []tracker.WorkflowAction, to string, on bool, pos int) []tracker.WorkflowAction {
	if !on {
		return rows
	}
	if pos < 0 {
		return wfedit.SetGate(rows, to, true)
	}
	pos = min(pos, len(rows))
	out := append([]tracker.WorkflowAction(nil), rows[:pos]...)
	out = append(out, tracker.WorkflowAction{Type: "require_human_approval", Status: to})
	return append(out, rows[pos:]...)
}

// hallEditAction is row n of an edge's action rows. Every row posts every
// field, so the n-th value of each belongs to row n.
func hallEditAction(f map[string][]string, pre string, n int) tracker.WorkflowAction {
	get := func(name string) string {
		if v := f[pre+name]; n < len(v) {
			return crlf(v[n])
		}
		return ""
	}
	num := func(name string) int {
		v, _ := strconv.Atoi(strings.TrimSpace(get(name)))
		return v
	}
	a := tracker.WorkflowAction{Type: get("type"), Rule: get("rule"), Status: get("status"), Title: get("title"), Body: get("body"),
		Prompt: get("prompt"), Field: get("field"), Value: get("value"), Pattern: get("pattern"), Section: get("section"),
		Min: num("min"), Max: num("max"), Command: get("command"), TimeoutSeconds: num("timeout_seconds"),
		RefKey: get("ref_key"), LinkedStatus: get("linked_status"), Hint: get("hint")}
	for _, v := range strings.Split(get("values"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			a.Values = append(a.Values, v)
		}
	}
	return a
}

func crlf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func hallEditPreview(pv *wfedit.Preview) *HallEditPreview {
	out := &HallEditPreview{OK: pv.OK(), Summary: pv.Summary, Errors: pv.Errors, Warnings: pv.Warnings, Gates: pv.Gates}
	for _, l := range strings.Split(strings.TrimRight(pv.Diff, "\n"), "\n") {
		if l == "" || strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") {
			continue
		}
		kind := ""
		switch l[0] {
		case '+':
			kind = "add"
		case '-':
			kind = "del"
		case '@':
			kind = "hunk"
		}
		out.Diff = append(out.Diff, HallDiffLine{Kind: kind, Text: l})
	}
	return out
}

// hallSavedSha is the ?saved= commit the station page reports after a
// save, "" when absent or not a short sha.
var hallSavedSha = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func hallSaved(r *http.Request) string {
	if v := r.URL.Query().Get("saved"); hallSavedSha.MatchString(v) {
		return v
	}
	return ""
}
