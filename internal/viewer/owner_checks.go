package viewer

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Owner checks: /p/<project>/owner-checks?epic=<slug> lists every box only
// the owner can tick (proof.Report.OwnerWaiting: playtest judgement steps,
// boxes a guided check left to the owner, Human Testing boxes, boxes marked
// "(owner)") across an epic and its children, each with the playtest shot
// that goes with it. The tick (POST …/owner-checks/tick) is the owner's
// own: only a request from the viewer's page in a browser passes
// (s.fromOwner); curl and agent sessions are refused.

// ownerChecksAll is the epic value that lists every issue in the project.
const ownerChecksAll = "all"

// ownerChecksWorkers bounds how many issues' proof reports are read at once.
const ownerChecksWorkers = 8

// OwnerChecksData is the Owner checks page.
type OwnerChecksData struct {
	Header WbHeader
	Title  string
	// Epic is the picked epic ("all" for every issue); "" shows the picker.
	Epic, EpicTitle, EpicStatus, EpicURL string
	All                                  bool
	// Epics are the picker's cards (and the select's options).
	Epics   []OwnerChecksEpic
	Issues  []OwnerChecksIssue
	Count   int
	TickURL string
	// Unknown: the epic named in the URL is not an issue of this project.
	Unknown bool
}

// OwnerChecksEpic is one epic to pick: an issue some issue names as parent.
type OwnerChecksEpic struct {
	Slug, Title, Status, URL string
	Children                 int
	On                       bool
}

// OwnerChecksIssue is one issue with open owner boxes.
type OwnerChecksIssue struct {
	Slug, Title, Status, URL, EvidenceURL string
	Boxes                                 []OwnerCheckBox
}

// OwnerCheckBox is one box only the owner can tick, with its shot.
type OwnerCheckBox struct {
	// Slug is the box's issue, for its test ids.
	Slug, ID, Text, Why string
	// Step is the playtest step that goes with the box; RunLine names its
	// session and state.
	Step, RunLine string
	// Shot is the step's widest shot ("" for none), ShotLabel its viewport.
	Shot, ShotLabel string
}

// ownerChecksEpics lists the project's epics: issues named as parent by at
// least one issue, most children first.
func ownerChecksEpics(issues []*tracker.Issue, prefix, on string) []OwnerChecksEpic {
	count := map[string]int{}
	for _, it := range issues {
		if p := tracker.ParentSlug(it); p != "" {
			count[strings.ToLower(p)]++
		}
	}
	var out []OwnerChecksEpic
	for _, it := range issues {
		if n := count[strings.ToLower(it.Slug)]; n > 0 {
			out = append(out, OwnerChecksEpic{Slug: it.Slug, Title: it.Title, Status: it.Status, Children: n,
				URL: prefix + "/owner-checks?epic=" + url.QueryEscape(it.Slug), On: strings.EqualFold(it.Slug, on)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Children != out[j].Children {
			return out[i].Children > out[j].Children
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// ownerCheckIssue reads one issue's proof and returns its open owner boxes
// with their shots; ok is false when nothing waits for the owner.
func ownerCheckIssue(proj *tracker.Project, prefix string, issue *tracker.Issue) (OwnerChecksIssue, bool) {
	comments, _ := tracker.LoadComments(issue.FilePath)
	r := evidenceReport(proj, issue, comments)
	waiting := r.OwnerWaiting()
	if len(waiting) == 0 {
		return OwnerChecksIssue{}, false
	}
	root := projectRoot(proj)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	runs, _ := timeline.LoadPlaytestRuns(nil, issue.Slug, root, filepath.Join(root, ".worktrees", issue.Slug))
	in := evidenceIn{Prefix: prefix, Slug: issue.Slug, Root: root, Runs: runs,
		Exists: func(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }}
	out := OwnerChecksIssue{Slug: issue.Slug, Title: issue.Title, Status: issue.Status,
		URL: prefix + "/issue/" + issue.Slug, EvidenceURL: prefix + "/issue/" + issue.Slug + "?tab=evidence"}
	for _, w := range waiting {
		b := ownerCheckBox(in, w)
		b.Slug = issue.Slug
		out.Boxes = append(out.Boxes, b)
	}
	return out, true
}

// ownerCheckBox fills a box's step and shot: the judgement step its reason
// names, else a step whose id is the box's id (step id = box id).
func ownerCheckBox(in evidenceIn, w proof.OwnerItem) OwnerCheckBox {
	b := OwnerCheckBox{ID: w.ID, Text: w.Text, Why: w.Why}
	step := ""
	if s, ok := strings.CutPrefix(w.Why, "playtest judgement step "); ok {
		step = strings.TrimSpace(s)
	}
	if step == "" {
		for _, r := range in.Runs {
			for _, st := range r.Steps {
				if strings.EqualFold(st.ID, w.ID) {
					step = st.ID
				}
			}
		}
	}
	if step == "" {
		return b
	}
	b.Step = step
	run, st := latestShotRun(in, "auto", "", step)
	if run == nil || !hasShot(in, run, st) {
		if jr, jst := latestShotRun(in, "judge", "", step); jr != nil {
			run, st = jr, jst
		}
	}
	if run == nil {
		b.RunLine = "step " + step + " · no playtest run yet"
		return b
	}
	state, _ := stepState(st.Status)
	if v := judgeVerdict(st); st.Judgement && v != "" {
		state = "judged " + v
	}
	b.RunLine = run.Session + " · " + step + " " + state
	views, _ := shotViews(in, run, st)
	for _, v := range views {
		if v.On && v.Src != "" {
			b.Shot, b.ShotLabel = v.Src, v.Label
		}
	}
	return b
}

// buildOwnerChecks is the page: the picker without an epic, else every open
// owner box of the epic and its children (or of every issue for "all").
func (s *Server) buildOwnerChecks(p *tracker.Project, prefix, epic string, now time.Time) OwnerChecksData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := OwnerChecksData{Title: p.Name + " · Owner checks", TickURL: prefix + "/owner-checks/tick"}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Owner checks", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)

	issues, _ := tracker.LoadIssues(p.IssueDir)
	epic = strings.TrimSpace(epic)
	d.Epics = ownerChecksEpics(issues, prefix, epic)
	if epic == "" {
		return d
	}
	var pick []*tracker.Issue
	if strings.EqualFold(epic, ownerChecksAll) {
		d.Epic, d.All, d.EpicTitle = ownerChecksAll, true, "Every issue in the project"
		pick = issues
	} else {
		e := tracker.FindIssue(issues, epic)
		if e == nil {
			d.Epic, d.Unknown = epic, true
			return d
		}
		d.Epic, d.EpicTitle, d.EpicStatus, d.EpicURL = e.Slug, e.Title, e.Status, prefix+"/issue/"+e.Slug
		children := tracker.EpicChildren(issues, e.Slug)
		sort.SliceStable(children, func(i, j int) bool { return children[i].Slug < children[j].Slug })
		pick = append([]*tracker.Issue{e}, children...)
	}

	res := make([]*OwnerChecksIssue, len(pick))
	sem := make(chan struct{}, ownerChecksWorkers)
	var wg sync.WaitGroup
	for i, issue := range pick {
		wg.Add(1)
		go func(i int, issue *tracker.Issue) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if oc, ok := ownerCheckIssue(p, prefix, issue); ok {
				res[i] = &oc
			}
		}(i, issue)
	}
	wg.Wait()
	for _, oc := range res {
		if oc != nil {
			d.Issues = append(d.Issues, *oc)
			d.Count += len(oc.Boxes)
		}
	}
	return d
}

func (s *Server) handleOwnerChecks(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	d := s.buildOwnerChecks(proj, prefix, r.URL.Query().Get("epic"), time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if d.Unknown {
		w.WriteHeader(http.StatusNotFound)
	}
	if err := s.tmpl.ExecuteTemplate(w, "owner_checks.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ownerCheckRefused is what an agent or a script gets for a tick.
const ownerCheckRefused = "only the owner ticks an owner box, from the Owner checks page in the browser; an agent asks on its issue trail (issue-cli comment <slug> \"question: …\")"

// errOwnerBox is a tick the box's current state refuses (409).
var errOwnerBox = errors.New("owner box")

// handleOwnerCheckTick ticks one owner box for the owner: the request must
// come from the viewer's own page in a browser, and the box must still be
// open, the owner's and worded as the page showed it. It adds the owner's
// owner-check: comment on the issue.
func (s *Server) handleOwnerCheckTick(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if !s.fromOwner(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": ownerCheckRefused})
		return
	}
	var body struct {
		Slug string `json:"slug"`
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Slug == "" || body.ID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "slug and id required"})
		return
	}
	issue := s.findIssueBySlug(proj, body.Slug)
	if issue == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "issue not found: " + body.Slug})
		return
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	owners := false
	for _, it := range evidenceReport(proj, issue, comments).OwnerWaiting() {
		owners = owners || strings.EqualFold(it.ID, body.ID)
	}
	if !owners {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": body.ID + " no longer waits for you (ticked, or not an owner box). Reload to see it."})
		return
	}
	var label string
	_, _, err := tracker.UpdateIssueBody(issue.FilePath, func(text string) (string, bool, error) {
		for _, it := range tracker.ListCheckboxes(text) {
			if !strings.EqualFold(it.ID, body.ID) {
				continue
			}
			switch {
			case it.Checked:
				return "", false, fmt.Errorf("%w: %s is already ticked. Reload to see it", errOwnerBox, it.ID)
			case body.Text != "" && strings.TrimSpace(it.Label()) != strings.TrimSpace(body.Text):
				return "", false, fmt.Errorf("%w: %s changed since this page loaded. Reload to see it", errOwnerBox, it.ID)
			}
			label = it.Label()
			return tracker.CheckLines(text, []int{it.Line}), true, nil
		}
		return "", false, fmt.Errorf("%w: no box %s on the issue any more. Reload to see it", errOwnerBox, body.ID)
	})
	switch {
	case errors.Is(err, errOwnerBox):
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": strings.TrimPrefix(err.Error(), errOwnerBox.Error()+": ")})
		return
	case err != nil:
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "could not tick: " + err.Error()})
		return
	}
	id := strings.ToUpper(body.ID)
	res := map[string]any{"ok": true, "id": id}
	if err := tracker.AddCommentBy(issue.FilePath, 0, "owner-check: "+id+" ticked on the Owner checks page: "+label, "app", tracker.OwnerAuthor); err != nil {
		res["comment_error"] = err.Error()
	}
	log.Printf("owner checks: %s %s ticked by the owner", issue.Slug, id)
	writeJSONStatus(w, http.StatusOK, res)
}
