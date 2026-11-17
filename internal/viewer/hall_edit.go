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

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/lint"
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
	// PreviewURL and SaveURL are the form's two POSTs.
	PreviewURL, SaveURL string
	// Label is the hall's name ("Marketing hall"). InPlace: the file is a
	// department's that git does not track, so Save writes it in place,
	// checked and read back, and commits nothing (no reason box).
	Label   string
	InPlace bool
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
	// GateAction is the gate as the file has it: keeping the gate on puts
	// it back unchanged (owner_only:, hint:); OwnerOnly is its mark.
	GateAction *tracker.WorkflowAction
	OwnerOnly  bool
	Line       int
	Actions    []tracker.WorkflowAction
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

func (s *Server) handleHallEdit(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, key, action string) {
	sc, err := hallScopeOf(proj, prefix, r)
	if err != nil {
		hallScopeError(w, err)
		return
	}
	file := sc.File
	st0 := sc.status(key)
	if st0 == nil {
		http.NotFound(w, r)
		return
	}
	status := st0.Name
	if file == "" {
		http.Error(w, "this project runs on the built-in workflow: there is no workflow.yaml to edit (issue-cli workflow init writes one)", http.StatusNotFound)
		return
	}
	st, err := wfedit.Open(file, status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := s.hallEditPage(proj, sc, key, file, st)
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
	pv, err := wfedit.Apply(file, status, hash, ch, editCheck)
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
	if d.InPlace {
		// A department file git does not track: written in place, never
		// committed (and never added to git).
		if err := wfedit.Write(pv); err != nil {
			d.Error = err.Error()
			code := http.StatusBadRequest
			if errors.Is(err, wfedit.ErrStale) {
				code = http.StatusConflict
			}
			s.renderHallEdit(w, d, code)
			return
		}
		http.Redirect(w, r, hallSavedURL(d.PartURL, hallSavedInPlace), http.StatusSeeOther)
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
	http.Redirect(w, r, hallSavedURL(d.PartURL, saved.Commit), http.StatusSeeOther)
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
func (s *Server) hallEditPage(p *tracker.Project, sc *hallScope, key, file string, st *wfedit.Station) HallEditData {
	part, _ := s.buildHallPart(p, sc, "station", key, time.Now())
	d := HallEditData{Header: part.Header, Status: st.Name, Title: part.Title, PartURL: sc.url("station", st.Name),
		File: hallEditFileName(p, file), Hash: st.Hash, Description: st.Description, Prompt: st.Prompt, ReadOnly: st.ReadOnly, Label: sc.Label}
	d.EditURL, d.PreviewURL, d.SaveURL = sc.editURL(st.Name, ""), sc.editURL(st.Name, "preview"), sc.editURL(st.Name, "save")
	d.InPlace = sc.Dept != "" && !gitfile.Tracked(file)
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
					gate := a
					he.Gate, he.GatePos, he.GateAction, he.OwnerOnly = true, i, &gate, a.OwnerOnly
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
			acts = placeGate(rows, e.To, e.Gate, e.GatePos, e.GateAction)
		}
		ch.Edges[e.Index] = acts
		edges = append(edges, e)
	}
	return ch, edges
}

// placeGate is rows with the gate for to: the file's own gate action at
// pos when it was there before (so keeping it on changes nothing, its
// owner_only: and hint: included), else where wfedit.SetGate puts it.
func placeGate(rows []tracker.WorkflowAction, to string, on bool, pos int, was *tracker.WorkflowAction) []tracker.WorkflowAction {
	if !on {
		return rows
	}
	if pos < 0 || was == nil {
		return wfedit.SetGate(rows, to, true)
	}
	pos = min(pos, len(rows))
	out := append([]tracker.WorkflowAction(nil), rows[:pos]...)
	out = append(out, *was)
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
// save, "" when absent or not a short sha; hallSavedInPlace stands for a
// file written in place, with no commit.
var hallSavedSha = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

const hallSavedInPlace = "in-place"

func hallSaved(r *http.Request) string {
	if v := r.URL.Query().Get("saved"); v == hallSavedInPlace || hallSavedSha.MatchString(v) {
		return v
	}
	return ""
}

// hallSavedURL is the station page reporting saved.
func hallSavedURL(partURL, saved string) string {
	sep := "?"
	if strings.Contains(partURL, "?") {
		sep = "&"
	}
	return partURL + sep + "saved=" + saved
}

// editCheck is lint's check of a workflow file for the station editor,
// handed to wfedit.Apply so that wfedit never imports lint.
func editCheck(path string, raw []byte, now time.Time) ([]wfedit.Finding, error) {
	fs, err := lint.EditCheck(path, raw, now)
	out := make([]wfedit.Finding, len(fs))
	for i, f := range fs {
		out[i] = wfedit.Finding{Check: f.Check, Where: f.Where, Message: f.Message, Fix: f.Fix}
	}
	return out, err
}
