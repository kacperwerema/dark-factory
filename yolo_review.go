package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// The review page: /p/<project>/yolo/review lists YOLO's after-the-fact
// queue one entry per issue (yolo.Entry: the gates it passed without the
// owner and the owner checks they left open), oldest first, each with its
// evidence (shots, the tests line, the change) from the Evidence tab's
// model. The issue is the review unit: one Agree covers all its gates and
// Back to sends it back once (POST yolo/review, owner-only under the yolo/
// prefix); each owner box is ticked (…/owner-checks/tick) or agreed without
// (…/issue/<slug>/yolo-review) on its own.

// yoloReviewPageSize is how many issues one page of the review lists.
const yoloReviewPageSize = 15

// yoloReviewWorkers bounds how many issues' evidence is read at once.
const yoloReviewWorkers = 4

// YOLOReviewData is the review page.
type YOLOReviewData struct {
	Header WbHeader
	Title  string
	// Issues is the queue's length: one entry per issue, the review unit.
	// Gates and OwnerChecks are what the entries hold; Oldest the oldest
	// waiting item's age.
	Issues, Gates, OwnerChecks int
	Oldest                     string
	Groups                     []YOLOReviewGroup
	// Page is 1-based; Prev/NextURL are "" at the ends.
	Page, Pages      int
	PrevURL, NextURL string
	// IssueURL is where an issue's Agree / Back posts, TickURL where an
	// owner box is ticked.
	IssueURL, TickURL string
}

// YOLOReviewGroup is one issue's entry: its passed gates (one Agree covers
// them), its open owner checks (each ticked or agreed on its own), the
// issue's Back to and its evidence.
type YOLOReviewGroup struct {
	Slug, Title, Status         string
	URL, EvidenceURL, ReviewURL string
	Oldest, Detail              string
	Gates, Checks               []WbYOLOPass
	// Questions are the answers the agent assumed in its clarify round;
	// the issue's Agree covers them with its gates.
	Questions []WbYOLOPass
	// Back is the status Back to sends the issue to: the one it left at its
	// latest gate ("" when there is none).
	Back     string
	Evidence YOLOReviewEvidence
}

// YOLOReviewEvidence is the strip under an issue's head: the Evidence tab's
// shots, tests line and change, in a line each.
type YOLOReviewEvidence struct {
	Shots []YOLOReviewShot
	// Tests is the last tests: comment; HasTests whether tests ran at all,
	// TestsAfter whether after the last change.
	Tests                string
	HasTests, TestsAfter bool
	// Change is "6 commits · 11 files · +412 −58 · tests +190"; Areas the
	// largest areas; NoChange why the commits can't be listed.
	Change, Areas, NoChange string
}

// YOLOReviewShot is one shot: its URL and what it shows.
type YOLOReviewShot struct {
	Src, Label string
}

// yoloReviewGroups is the entries as the page's groups, the issue with
// the oldest waiting item first.
func yoloReviewGroups(entries []yolo.Entry, now time.Time) []YOLOReviewGroup {
	type dated struct {
		g YOLOReviewGroup
		t time.Time
	}
	list := make([]dated, 0, len(entries))
	for _, e := range entries {
		g := YOLOReviewGroup{Slug: e.Slug, Title: e.Title, Detail: e.Detail()}
		for _, p := range e.Gates {
			g.Gates = append(g.Gates, wbYOLOPass(p))
		}
		for _, p := range e.Checks {
			g.Checks = append(g.Checks, wbYOLOPass(p))
		}
		for _, p := range e.Questions {
			g.Questions = append(g.Questions, wbYOLOPass(p))
		}
		t, ok := tracker.CommentTime(e.Oldest)
		if ok {
			g.Oldest = shortDuration(now.Sub(t))
		}
		list = append(list, dated{g, t})
	}
	sort.SliceStable(list, func(a, b int) bool {
		ta, tb := list[a].t, list[b].t
		if ta.IsZero() != tb.IsZero() {
			return !ta.IsZero()
		}
		return ta.Before(tb)
	})
	out := make([]YOLOReviewGroup, len(list))
	for i, d := range list {
		out[i] = d.g
	}
	return out
}

// wbYOLOPass is a pass as a row: its date in words.
func wbYOLOPass(p yolo.Pass) WbYOLOPass {
	at := p.At
	if t, ok := tracker.CommentTime(p.At); ok {
		at = t.Format("Mon 2 Jan, 15:04")
	}
	return WbYOLOPass{Status: p.Status, From: p.From, By: p.By, At: at, Check: p.Check, What: p.What, Why: p.Why, Question: p.Question}
}

// yoloQueueOldest is the oldest waiting item's time across the entries; ok
// is false when no date can be read.
func yoloQueueOldest(entries []yolo.Entry) (time.Time, bool) {
	var oldest time.Time
	ok := false
	for _, e := range entries {
		if t, tok := tracker.CommentTime(e.Oldest); tok && (!ok || t.Before(oldest)) {
			oldest, ok = t, true
		}
	}
	return oldest, ok
}

// yoloReviewEvidence reads one issue's Evidence tab and keeps the strip.
func yoloReviewEvidence(proj *tracker.Project, prefix string, issue *tracker.Issue) YOLOReviewEvidence {
	comments, _ := tracker.LoadComments(issue.FilePath)
	wf := proj.LoadWorkflowForIssue(issue)
	t := loadEvidenceTab(proj, prefix, issue, wf, evidenceReport(proj, issue, comments))
	var ev YOLOReviewEvidence
	for _, s := range t.Shots {
		if s.Src == "" {
			continue
		}
		label := s.Step
		if s.Title != "" {
			label += " · " + s.Title
		}
		ev.Shots = append(ev.Shots, YOLOReviewShot{Src: s.Src, Label: label})
		if len(ev.Shots) == 2 {
			break
		}
	}
	ev.HasTests, ev.Tests, ev.TestsAfter = t.Verdict.HasTests, t.Verdict.Tests, t.Verdict.TestsAfter
	c := t.Change
	switch {
	case c.Unreadable != "":
		ev.NoChange = c.Unreadable
	case len(c.Commits) == 0:
		ev.NoChange = "No commits on its branch."
	default:
		ev.Change = fmt.Sprintf("%s · %s · +%d −%d", plural(len(c.Commits), "commit", "commits"), plural(c.Files, "file", "files"), c.Add, c.Del)
		if c.TestAdd > 0 {
			ev.Change += fmt.Sprintf(" · tests +%d", c.TestAdd)
		}
		var areas []string
		for i, a := range c.Areas {
			if i == 3 {
				break
			}
			areas = append(areas, fmt.Sprintf("%s +%d", a.Name, a.Add))
		}
		ev.Areas = strings.Join(areas, " · ")
	}
	return ev
}

// buildYOLOReview is the page: one page of the queue's issues with their
// gates, owner checks, Back to and evidence.
func (s *Server) buildYOLOReview(p *tracker.Project, prefix string, page int, now time.Time) YOLOReviewData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := YOLOReviewData{Title: p.Name + " · Review after YOLO", IssueURL: prefix + "/yolo/review", TickURL: prefix + "/owner-checks/tick"}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Review after YOLO", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)

	var entries []yolo.Entry
	if lot.YOLO != nil {
		entries = lot.YOLO.Queue
	}
	d.Issues = len(entries)
	d.Gates, d.OwnerChecks = yolo.Totals(entries)
	if t, ok := yoloQueueOldest(entries); ok {
		d.Oldest = shortDuration(now.Sub(t))
	}
	groups := yoloReviewGroups(entries, now)
	d.Pages = max((len(groups)+yoloReviewPageSize-1)/yoloReviewPageSize, 1)
	page = min(max(page, 1), d.Pages)
	d.Page = page
	if page > 1 {
		d.PrevURL = prefix + "/yolo/review?page=" + strconv.Itoa(page-1)
	}
	if page < d.Pages {
		d.NextURL = prefix + "/yolo/review?page=" + strconv.Itoa(page+1)
	}
	lo := (page - 1) * yoloReviewPageSize
	hi := min(lo+yoloReviewPageSize, len(groups))
	d.Groups = groups[lo:hi]

	issues, _ := tracker.LoadIssues(p.IssueDir)
	sem := make(chan struct{}, yoloReviewWorkers)
	var wg sync.WaitGroup
	for i := range d.Groups {
		g := &d.Groups[i]
		issueURL := prefix + "/issue/" + g.Slug
		g.URL, g.EvidenceURL, g.ReviewURL = issueURL, issueURL+"?tab=evidence", issueURL+"/yolo-review"
		issue := tracker.FindIssue(issues, g.Slug)
		if issue == nil {
			continue
		}
		g.Status = issue.Status
		g.Back = yoloIssueBack(p.LoadWorkflowForIssue(issue), g.Gates, append(append([]WbYOLOPass(nil), g.Checks...), g.Questions...))
		for j := range g.Questions {
			g.Questions[j].Link = issueURL + "#clarify"
		}
		wg.Add(1)
		go func(g *YOLOReviewGroup, issue *tracker.Issue) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			g.Evidence = yoloReviewEvidence(p, prefix, issue)
		}(g, issue)
	}
	wg.Wait()
	return d
}

// yoloIssueBack is where Back to sends an issue: the status it left at its
// latest gate, else at its latest owner check.
func yoloIssueBack(wf *tracker.WorkflowConfig, gates, checks []WbYOLOPass) string {
	for _, list := range [][]WbYOLOPass{gates, checks} {
		if n := len(list); n > 0 {
			last := list[n-1]
			return yolo.BackTo(wf, yolo.Pass{Status: last.Status, From: last.From, Check: last.Check})
		}
	}
	return ""
}

func (s *Server) handleYOLOReviewPage(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	d := s.buildYOLOReview(proj, prefix, page, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "yolo_review.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleYOLOReviewIssue serves POST yolo/review {slug, verdict, words}:
// the owner's verdict on one issue's entry (reviewYOLOIssue).
func (s *Server) handleYOLOReviewIssue(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	var body struct {
		Slug    string `json:"slug"`
		Verdict string `json:"verdict"`
		Words   string `json:"words"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Slug == "" {
		writeYOLOError(w, http.StatusBadRequest, "slug required: {slug, verdict: agree|back, words}")
		return
	}
	issue := s.findIssueBySlug(proj, body.Slug)
	if issue == nil {
		writeYOLOError(w, http.StatusNotFound, "issue not found: "+body.Slug)
		return
	}
	res, code, msg := reviewYOLOIssue(proj, issue, body.Verdict, body.Words)
	if code != 0 {
		writeYOLOError(w, code, msg)
		return
	}
	log.Printf("yolo review: %s: the owner's %s settles %d", issue.Slug, body.Verdict, res.Settled)
	writeJSONStatus(w, http.StatusOK, res)
}

// YOLOIssueReview is the answer to an issue's Agree or Back to.
type YOLOIssueReview struct {
	OK      bool   `json:"ok"`
	Verdict string `json:"verdict"`
	// Settled is how many gates (and, for back, owner checks) it settled;
	// ChecksLeft the owner checks still waiting after an Agree.
	Settled    int `json:"settled"`
	ChecksLeft int `json:"checks_left"`
	// To is the status Back sent the issue to.
	To string `json:"to,omitempty"`
}

// reviewYOLOIssue records the owner's verdict on one issue's entry. Agree
// covers every passed gate still waiting (one yolo-review: comment each,
// the record the station writes), and every assumed clarify answer; its
// owner checks stay, each ticked or agreed on its own. Back moves the issue once, to the status it left at
// its latest gate, and settles its gates and owner checks with the
// owner's words. On failure code is the HTTP status and msg why.
func reviewYOLOIssue(proj *tracker.Project, issue *tracker.Issue, verdict, words string) (YOLOIssueReview, int, string) {
	res := YOLOIssueReview{Verdict: verdict}
	comments, _ := tracker.LoadComments(issue.FilePath)
	var gates, checks, questions []yolo.Pass
	for _, p := range yolo.Unreviewed(issue, comments) {
		switch {
		case p.Check != "":
			checks = append(checks, p)
		case p.Question != "":
			questions = append(questions, p)
		default:
			gates = append(gates, p)
		}
	}
	switch verdict {
	case "agree":
		// The assumed answers go with the gates: one Agree covers both.
		if len(gates)+len(questions) == 0 {
			if len(checks) > 0 {
				return res, http.StatusConflict, "no gate waits on this issue: tick or agree each owner check"
			}
			return res, http.StatusConflict, "nothing waits for review on this issue"
		}
		for _, k := range append(yoloKeys(gates), yoloKeys(questions)...) {
			if code, msg := reviewYOLOPass(proj, issue, k, "agree", words); code != 0 {
				return res, code, msg
			}
			res.Settled++
		}
		res.OK, res.ChecksLeft = true, len(yoloKeys(checks))
		return res, 0, ""
	case "back":
		words = strings.TrimSpace(words)
		if words == "" {
			return res, http.StatusBadRequest, "say why it goes back: your words reach the agent"
		}
		latest := checks
		if len(questions) > 0 {
			latest = questions
		}
		if len(gates) > 0 {
			latest = gates
		}
		if len(latest) == 0 {
			return res, http.StatusConflict, "nothing waits for review on this issue"
		}
		wf := proj.LoadWorkflowForIssue(issue)
		back := yolo.BackTo(wf, latest[len(latest)-1])
		if back == "" {
			return res, http.StatusConflict, "no status to go back to"
		}
		if !strings.EqualFold(issue.Status, back) {
			if _, err := wf.MoveBack(issue.FilePath, back); err != nil {
				return res, http.StatusConflict, err.Error()
			}
		}
		date := time.Now().Format("2006-01-02")
		for _, k := range append(append(yoloKeys(gates), yoloKeys(checks)...), yoloKeys(questions)...) {
			if err := tracker.AddCommentBy(issue.FilePath, 0, tracker.YOLOReviewComment("back to "+back, words, "viewer", date, k), "app", tracker.OwnerAuthor); err != nil {
				return res, http.StatusInternalServerError, err.Error()
			}
			res.Settled++
		}
		res.OK, res.To = true, back
		return res, 0, ""
	}
	return res, http.StatusBadRequest, "verdict is agree or back"
}

// yoloKeys is the passes' review keys, once each: one review settles every
// pass of the same status.
func yoloKeys(list []yolo.Pass) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range list {
		if k := strings.ToLower(p.Key()); !seen[k] {
			seen[k] = true
			out = append(out, p.Key())
		}
	}
	return out
}

// yoloTotals is yolo.Totals, named for the pages.
func yoloTotals(entries []yolo.Entry) (gates, checks int) { return yolo.Totals(entries) }
