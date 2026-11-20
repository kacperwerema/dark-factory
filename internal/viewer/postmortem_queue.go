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
// across post-mortems by place, the ones that say the same thing as one row
// of copies, and the ones fixed already apart (they need no answer). POST
// …/postmortems/mark agrees, refuses or marks overtaken the ticked rows,
// and POST …/postmortems/undo reopens a recent mark, from the owner's
// browser only. Agree applies once per group: the newest copy is applied as
// the post-mortem page's round would apply it, and the other copies are
// ticked as copies of it, so a group makes one memory, commit or experiment.

// PostmortemsData is the queue page.
type PostmortemsData struct {
	Header                  WbHeader
	Title, MarkURL, UndoURL string
	// Count is the open actions, Posts the post-mortems they come from,
	// Joined how many of them are in a group of Groups, Rows how many
	// answers cover them all (a group is one).
	Count, Posts, Joined, Groups, Rows int
	Places                             []PostmortemsPlace
	// Fixed are the actions that need no answer: a linked issue done, a
	// linked commit on main, or a memory that says it already.
	Fixed []PostmortemsRow
	// Marked is what Undo can reopen, newest mark first; MarkedDays its
	// window.
	Marked     []PostmortemsRow
	MarkedDays int
}

// PostmortemsPlace is one place's card.
type PostmortemsPlace struct {
	Place         string
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
	// From is every post-mortem a group of copies comes from.
	From []PostmortemsFrom
	// Ticked is how many rows start ticked: every copy, so one answer
	// covers the group, and none in the rest.
	Ticked int
	Rows   []PostmortemsRow
}

// PostmortemsFrom is one post-mortem a group's copy comes from.
type PostmortemsFrom struct {
	Issue, URL string
}

// PostmortemsRow is one action.
type PostmortemsRow struct {
	ID, Title, File, Issue, Place, URL string
	Ticked                             bool
	// Mark is a marked row's line as the retro has it; Overtaken which kind.
	Mark      string
	Overtaken bool
	// Fix is a linked fix as words: "fixed by <slug>", "known as m-…", or
	// "fix underway: <slug> (<status>)" on a row that still waits.
	Fix string
}

// postmortemFixes is what tells, on p's board, that an action is fixed.
func postmortemFixes(p *tracker.Project, now time.Time) *postmortem.Fixes {
	root := projectRoot(p)
	f := &postmortem.Fixes{Repo: root, Memories: postmortem.MemoriesFor(p.IssueDir, now)}
	f.MainOf = func() string {
		name, _ := tracker.MainBranch(root)
		return name
	}
	var by map[string]*tracker.Issue
	f.IssueState = func(slug string) (string, bool, bool) {
		if by == nil {
			by = map[string]*tracker.Issue{}
			issues, _ := p.LoadIssues()
			for _, is := range issues {
				by[is.Slug] = is
			}
		}
		is := by[slug]
		if is == nil {
			return "", false, false
		}
		return is.Status, p.IssueDone(is), true
	}
	return f
}

// openPostmortems is the queue's actions that still wait, and the ones
// fixed already.
func openPostmortems(p *tracker.Project, now time.Time) (open, fixed []postmortem.Item) {
	return postmortemFixes(p, now).Split(postmortem.Queue(postmortemRetros(p)))
}

func pmRow(it postmortem.Item, prefix string) PostmortemsRow {
	return PostmortemsRow{ID: it.ID, Title: it.Title, File: it.File, Issue: it.Issue, Place: it.PlaceString(), URL: prefix + "/postmortem/" + it.Issue, Fix: it.Fix.Words()}
}

func (s *Server) buildPostmortems(p *tracker.Project, prefix string, now time.Time) PostmortemsData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := PostmortemsData{Title: p.Name + " · Post-mortem actions", MarkURL: prefix + "/postmortems/mark", UndoURL: prefix + "/postmortems/undo", MarkedDays: postmortem.MarkedDays}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, OwnerWait: lot.You, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Post-mortem actions", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	items, fixed := openPostmortems(p, now)
	d.Count = len(items)
	files := map[string]bool{}
	for _, it := range items {
		files[it.File] = true
	}
	d.Posts = len(files)
	for _, it := range fixed {
		d.Fixed = append(d.Fixed, pmRow(it, prefix))
	}
	for _, pg := range postmortem.GroupItems(items) {
		place := PostmortemsPlace{Place: pg.Place, Count: pg.Count}
		for i, g := range pg.Groups {
			group := PostmortemsGroup{Key: fmt.Sprintf("%s-%d", pg.Place, i), Title: g.Items[0].Title, Count: len(g.Items), Alone: g.Alone}
			for _, it := range g.Items {
				row := pmRow(it, prefix)
				row.Ticked = !g.Alone
				if row.Ticked {
					group.Ticked++
				}
				if !g.Alone {
					group.From = append(group.From, PostmortemsFrom{Issue: it.Issue, URL: row.URL})
				}
				group.Rows = append(group.Rows, row)
			}
			if g.Alone {
				place.Alone = len(g.Items)
				d.Rows += len(g.Items)
			} else {
				place.Groups++
				d.Groups++
				d.Joined += len(g.Items)
				d.Rows++
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

// handlePostmortemsMark agrees, refuses or marks overtaken the posted rows:
// {act: "agree"|"refuse"|"overtaken", items: [{file, id}], note, sha}.
// Refuse and Overtaken answer each row on its own with the same note.
// Agree applies once per group of copies among the rows (the newest one
// posted) and ticks the other copies as copies of it; an alone row is a
// group of one. A file is looked up in the queue, never used as a path, so
// a row that isn't open (decided on another page, fixed already, or made
// up) is that row's error.
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
	case "refuse", "agree":
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
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "act is agree, refuse or overtaken"})
		return
	}
	now := time.Now()
	queue, _ := openPostmortems(proj, now)
	open := map[string]postmortem.Item{}
	for _, it := range queue {
		open[it.Key()] = it
	}
	groupOf := postmortem.GroupKeys(queue)
	done := map[string][]string{} // issue: the ids it answered
	retros := map[string]string{}
	failed := 0
	outs := make([]postmortemMark, len(body.Items))
	firsts := map[string]*pmAgreed{}
	for i, m := range body.Items {
		out := postmortemMark{File: m.File, ID: m.ID}
		it, ok := open[m.File+"#"+strings.ToUpper(strings.TrimSpace(m.ID))]
		var err error
		switch {
		case !ok:
			err = fmt.Errorf("%s is not open any more: reload", m.ID)
		case body.Act == "agree":
			out.Line, err = s.agreeQueued(proj, it, firsts, groupOf[it.Key()], body.Note, now)
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
		outs[i] = out
	}
	what := "refused"
	switch {
	case sha != "":
		what = "overtaken by " + sha
	case body.Act == "agree":
		what = "agreed"
	}
	res := map[string]any{"ok": failed == 0, "outcomes": outs}
	s.postmortemTrail(proj, done, retros, what, res)
	log.Printf("postmortem: the owner marked %d action(s) %s, %d failed", len(body.Items)-failed, what, failed)
	writeJSONStatus(w, http.StatusOK, res)
}

// pmAgreed is a group's first agreed copy on one post: its key and what it
// made, or why it failed.
type pmAgreed struct{ key, made, err string }

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

// agreeQueued agrees it, one of group's copies: the first one of a post is
// applied (postmortem.AgreeOne), and each later one is ticked as a copy of
// it, applying nothing. When the first failed, so does every copy.
func (s *Server) agreeQueued(proj *tracker.Project, it postmortem.Item, firsts map[string]*pmAgreed, group, note string, now time.Time) (string, error) {
	if first := firsts[group]; first != nil {
		if first.err != "" {
			return "", fmt.Errorf("not agreed: its copy %s failed (%s)", first.key, first.err)
		}
		return postmortem.AgreeAs(it.Path, it.ID, first.key, first.made, tracker.OwnerAuthor, note, now)
	}
	issue := s.findIssueBySlug(proj, it.Issue)
	if issue == nil {
		issue = &tracker.Issue{Slug: it.Issue, Title: it.IssueTitle} // the retro outlived its issue
	}
	made, line, err := postmortem.AgreeOne(postmortemEnv(proj, issue, it.Path, now), it.ID, note)
	first := &pmAgreed{key: it.Key(), made: made}
	if err != nil {
		first.err = err.Error()
	}
	firsts[group] = first
	return line, err
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
