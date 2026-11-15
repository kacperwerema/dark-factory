package viewer

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The post-mortem queue: /p/<project>/postmortems lists every open action
// across post-mortems by place, with the ones that say the same thing
// together. POST …/postmortems/mark refuses the ticked rows or marks them
// overtaken by a commit on main, and POST …/postmortems/undo reopens a
// recent mark, from the owner's browser only. Nothing is applied here:
// Agree stays on each post-mortem's page, which is the only place that
// makes a commit, a memory or an experiment.

// PostmortemsData is the queue page.
type PostmortemsData struct {
	Header                  WbHeader
	Title, MarkURL, UndoURL string
	// Count is the open actions, Posts the post-mortems they come from,
	// Joined how many of them are in a group of Groups.
	Count, Posts, Joined, Groups int
	Places                       []PostmortemsPlace
	// Marked is what Undo can reopen, newest mark first; MarkedDays its
	// window.
	Marked     []PostmortemsRow
	MarkedDays int
}

// PostmortemsPlace is one place's card.
type PostmortemsPlace struct {
	Place, Warn   string
	Count, Groups int
	Alone         int
	Rows          []PostmortemsGroup
}

// PostmortemsGroup is one group: copies of one thing, or the place's rest.
type PostmortemsGroup struct {
	// Key names the group on the page (<place>-<n>).
	Key, Title string
	Count      int
	Alone      bool
	// Ticked is how many rows start ticked: all but the newest copy, and
	// none in the rest.
	Ticked int
	Rows   []PostmortemsRow
}

// PostmortemsRow is one action.
type PostmortemsRow struct {
	ID, Title, File, Issue, Place, URL string
	Ticked, Kept                       bool
	// Mark is a marked row's line as the retro has it; Overtaken which kind.
	Mark      string
	Overtaken bool
}

// workflowCopiesWarn is the workflow card's band: apply.go files one
// experiment per agreed action.
const workflowCopiesWarn = "Each agreed copy files its own experiment: agree one, refuse the rest."

func (s *Server) buildPostmortems(p *tracker.Project, prefix string, now time.Time) PostmortemsData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := PostmortemsData{Title: p.Name + " · Post-mortem actions", MarkURL: prefix + "/postmortems/mark", UndoURL: prefix + "/postmortems/undo", MarkedDays: postmortem.MarkedDays}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Post-mortem actions", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	items := postmortem.Queue(postmortemRetros(p))
	d.Count = len(items)
	files := map[string]bool{}
	for _, it := range items {
		files[it.File] = true
	}
	d.Posts = len(files)
	for _, pg := range postmortem.GroupItems(items) {
		place := PostmortemsPlace{Place: pg.Place, Count: pg.Count}
		if pg.Place == postmortem.PlaceWorkflow {
			place.Warn = workflowCopiesWarn
		}
		for i, g := range pg.Groups {
			group := PostmortemsGroup{Key: fmt.Sprintf("%s-%d", pg.Place, i), Title: g.Items[0].Title, Count: len(g.Items), Alone: g.Alone}
			for j, it := range g.Items {
				row := PostmortemsRow{ID: it.ID, Title: it.Title, File: it.File, Issue: it.Issue, Place: it.PlaceString(), URL: prefix + "/postmortem/" + it.Issue}
				if !g.Alone {
					row.Ticked, row.Kept = j > 0, j == 0
				}
				if row.Ticked {
					group.Ticked++
				}
				group.Rows = append(group.Rows, row)
			}
			if g.Alone {
				place.Alone = len(g.Items)
			} else {
				place.Groups++
				d.Groups++
				d.Joined += len(g.Items)
			}
			place.Rows = append(place.Rows, group)
		}
		d.Places = append(d.Places, place)
	}
	for _, it := range postmortem.Marked(postmortemRetros(p), tracker.OwnerAuthor, now) {
		d.Marked = append(d.Marked, PostmortemsRow{ID: it.ID, Title: it.Title, File: it.File, Issue: it.Issue, Place: it.PlaceString(), URL: prefix + "/postmortem/" + it.Issue, Mark: it.Mark(), Overtaken: it.Overtaken != ""})
	}
	return d
}

func (s *Server) handlePostmortems(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	d := s.buildPostmortems(proj, prefix, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "postmortems.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// postmortemMark is one row of a mark's answer.
type postmortemMark struct {
	File string `json:"file"`
	ID   string `json:"id"`
	Line string `json:"line,omitempty"`
	Err  string `json:"error,omitempty"`
}

// handlePostmortemsMark refuses rows or marks them overtaken: {act:
// "refuse"|"overtaken", items: [{file, id}], note, sha}. Each row is
// answered on its own. A file is looked up in the queue, never used as a
// path, so a row that isn't open (decided on another page, or made up) is
// that row's error.
func (s *Server) handlePostmortemsMark(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if !s.fromOwner(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": postmortemRefused})
		return
	}
	var body struct {
		Act   string           `json:"act"`
		Items []postmortemMark `json:"items"`
		Note  string           `json:"note"`
		Sha   string           `json:"sha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Items) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "tick at least one action"})
		return
	}
	sha := ""
	switch body.Act {
	case "refuse":
	case "overtaken":
		root := projectRoot(proj)
		mainBranch, err := tracker.MainBranch(root)
		if err == nil {
			sha, err = postmortem.OnMain(root, mainBranch, body.Sha)
		}
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "act is refuse or overtaken"})
		return
	}
	open := map[string]postmortem.Item{}
	for _, it := range postmortem.Queue(postmortemRetros(proj)) {
		open[it.File+"#"+it.ID] = it
	}
	now := time.Now()
	done := map[string][]string{} // issue: the ids it answered
	retros := map[string]string{}
	failed := 0
	outs := make([]postmortemMark, 0, len(body.Items))
	for _, m := range body.Items {
		out := postmortemMark{File: m.File, ID: m.ID}
		it, ok := open[m.File+"#"+strings.ToUpper(strings.TrimSpace(m.ID))]
		var err error
		switch {
		case !ok:
			err = fmt.Errorf("%s is not open any more: reload", m.ID)
		case body.Act == "overtaken":
			out.Line, err = postmortem.Overtake(it.Path, it.ID, sha, tracker.OwnerAuthor, body.Note, now)
		default:
			out.Line, err = postmortem.Refuse(it.Path, it.ID, tracker.OwnerAuthor, body.Note, now)
		}
		if err != nil {
			out.Err = err.Error()
			failed++
		} else {
			done[it.Issue] = append(done[it.Issue], it.ID)
			retros[it.Issue] = retroRef(proj, it.Path)
		}
		outs = append(outs, out)
	}
	what := "refused"
	if sha != "" {
		what = "overtaken by " + sha
	}
	res := map[string]any{"ok": failed == 0, "outcomes": outs}
	s.postmortemTrail(proj, done, retros, what, res)
	log.Printf("postmortem: the owner marked %d action(s) %s, %d failed", len(body.Items)-failed, what, failed)
	writeJSONStatus(w, http.StatusOK, res)
}

// handlePostmortemsUndo reopens marked rows: {items: [{file, id}]}. Each row
// is answered on its own, and a file is looked up in postmortem.Marked,
// never used as a path, so a row that isn't undoable (applied, too old,
// already reopened, or made up) is that row's error.
func (s *Server) handlePostmortemsUndo(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if !s.fromOwner(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": postmortemRefused})
		return
	}
	var body struct {
		Items []postmortemMark `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Items) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "tick at least one mark"})
		return
	}
	now := time.Now()
	marked := map[string]postmortem.Item{}
	for _, it := range postmortem.Marked(postmortemRetros(proj), tracker.OwnerAuthor, now) {
		marked[it.File+"#"+it.ID] = it
	}
	done := map[string][]string{}
	retros := map[string]string{}
	failed := 0
	outs := make([]postmortemMark, 0, len(body.Items))
	for _, m := range body.Items {
		out := postmortemMark{File: m.File, ID: m.ID}
		it, ok := marked[m.File+"#"+strings.ToUpper(strings.TrimSpace(m.ID))]
		var err error
		if ok {
			out.Line, err = postmortem.Reopen(it.Path, it.ID, tracker.OwnerAuthor, now)
		} else {
			err = fmt.Errorf("%s has no recent mark to undo: reload", m.ID)
		}
		if err != nil {
			out.Err = err.Error()
			failed++
		} else {
			done[it.Issue] = append(done[it.Issue], it.ID)
			retros[it.Issue] = retroRef(proj, it.Path)
		}
		outs = append(outs, out)
	}
	res := map[string]any{"ok": failed == 0, "outcomes": outs}
	s.postmortemTrail(proj, done, retros, "reopened", res)
	log.Printf("postmortem: the owner reopened %d action(s), %d failed", len(body.Items)-failed, failed)
	writeJSONStatus(w, http.StatusOK, res)
}

// postmortemTrail gives each issue one line for what the actions page did
// to its ids. A failed comment goes on res: the retro's lines are the record.
func (s *Server) postmortemTrail(proj *tracker.Project, done map[string][]string, retros map[string]string, what string, res map[string]any) {
	for slug, ids := range done {
		issue := s.findIssueBySlug(proj, slug)
		if issue == nil {
			continue // the retro outlived its issue
		}
		line := fmt.Sprintf("postmortem: %s %s on the actions page (%s)", strings.Join(ids, ", "), what, retros[slug])
		if err := tracker.AddCommentBy(issue.FilePath, 0, line, "app", tracker.OwnerAuthor); err != nil {
			res["comment_error"] = err.Error()
		}
	}
}

// retroRef is a retro's path from the project root ("retros/archive/x.md").
func retroRef(p *tracker.Project, path string) string {
	if rel, err := filepath.Rel(projectRoot(p), path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return "retros/" + filepath.Base(path)
}
