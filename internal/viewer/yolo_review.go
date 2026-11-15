package viewer

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

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
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
	// SignedIn is whether the request came from the owner's signed-in
	// browser: only then does the summary carry Agree all.
	SignedIn bool
	// AllURL is where Agree all posts; All every item of the queue as the
	// page loaded it (every page, not only this one), the JSON list the
	// POST carries back; Questions and Decisions the confirm's other
	// counts, OldestDate the oldest item's day ("Tue 27 Oct").
	AllURL, All          string
	Questions, Decisions int
	OldestDate           string
	// Done is the line after Agree all ("Agreed 920 items on 229 issues ✓"),
	// read from the reload's query.
	Done string
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
	// Decisions are hard-decision acts a lead relayed; the same Agree.
	Decisions []WbYOLOPass
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
		for _, p := range e.Decisions {
			g.Decisions = append(g.Decisions, wbYOLOPass(p))
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
	return WbYOLOPass{Status: p.Status, From: p.From, By: p.By, At: at, Check: p.Check, What: p.What, Why: p.Why, Question: p.Question, Decision: p.Decision, Hold: p.Hold}
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
func yoloReviewEvidence(cache *timeline.Cache, proj *tracker.Project, prefix string, issue *tracker.Issue) YOLOReviewEvidence {
	comments := issue.Comments()
	wf := proj.LoadWorkflowForIssue(issue)
	t := loadEvidenceTab(cache, proj, prefix, issue, wf, evidenceReportWith(cache, proj, issue, comments))
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
		d.OldestDate = t.Format("Mon 2 Jan")
	}
	d.AllURL = prefix + "/yolo/review/all"
	var all []yoloAllItem
	for _, e := range entries {
		d.Questions += len(e.Questions)
		d.Decisions += len(e.Decisions)
		for _, p := range e.Items() {
			all = append(all, yoloAllItem{Slug: e.Slug, Key: p.Key(), Comment: p.Comment})
		}
	}
	if raw, err := json.Marshal(all); err == nil && len(all) > 0 {
		d.All = string(raw)
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

	issues, _ := p.LoadIssues()
	sem := make(chan struct{}, yoloReviewWorkers)
	var wg sync.WaitGroup
	cache := timeline.NewCache() // the page's entries read the same logs and sessions
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
			g.Evidence = yoloReviewEvidence(cache, p, prefix, issue)
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
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	d := s.buildYOLOReview(proj, prefix, page, time.Now())
	d.SignedIn = s.fromOwner(r)
	d.Done = yoloAllDoneLine(q.Get("agreed"), q.Get("issues"), q.Get("skipped"))
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
		case p.Question != "", p.Decision != "":
			// Assumed answers and relayed decisions go with the gates.
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

// yoloAllItem is one queue item as the review page loaded it: the issue,
// the key its review names, and the comment that put it in the queue.
type yoloAllItem struct {
	Slug    string `json:"slug"`
	Key     string `json:"key"`
	Comment int    `json:"comment"`
}

// YOLOAgreeAll is the answer to Agree all.
type YOLOAgreeAll struct {
	OK bool `json:"ok"`
	// Agreed is how many queue items it agreed, on Issues issues.
	Agreed int `json:"agreed"`
	Issues int `json:"issues"`
	// Skipped is each key it left waiting, and why.
	Skipped []YOLOAllSkip `json:"skipped"`
}

// YOLOAllSkip is one key Agree all left in the queue.
type YOLOAllSkip struct {
	Slug string `json:"slug"`
	Key  string `json:"key"`
	Why  string `json:"why"`
}

// Why Agree all leaves a key in the queue.
const (
	yoloAllGone    = "no longer waits for review"
	yoloAllChanged = "changed since the page loaded"
)

// handleYOLOReviewAll serves POST yolo/review/all {items, words}: the
// owner's Agree all on the review page. Owner-only through ownerOnlyRoute's
// yolo/ prefix; agents and the CLI have no way to it.
func (s *Server) handleYOLOReviewAll(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	var body struct {
		Items []yoloAllItem `json:"items"`
		Words string        `json:"words"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeYOLOError(w, http.StatusBadRequest, "bad request: {items: [{slug, key, comment}], words}")
		return
	}
	if len(body.Items) == 0 {
		writeYOLOError(w, http.StatusBadRequest, "nothing to agree: the page showed an empty queue")
		return
	}
	res := agreeAllYOLO(proj, body.Items, body.Words, time.Now())
	log.Printf("yolo review: %s: the owner's Agree all agreed %d items on %d issues, skipped %d", proj.Slug, res.Agreed, res.Issues, len(res.Skipped))
	writeJSONStatus(w, http.StatusOK, res)
}

// agreeAllYOLO agrees every item the page showed, issue by issue, through
// reviewYOLOPass (the record the issue's Agree and the Station write).
// A review names a key and settles every waiting pass with it, so a key is
// agreed only when each pass waiting under it is one the page showed: one
// that came in after the page loaded leaves the key waiting (changed), and
// a key nothing waits under any more is skipped (gone). Owner checks are
// agreed without a tick: their boxes stay open.
func agreeAllYOLO(proj *tracker.Project, items []yoloAllItem, words string, now time.Time) YOLOAgreeAll {
	res := YOLOAgreeAll{OK: true, Skipped: []YOLOAllSkip{}}
	issues, _ := proj.LoadIssues()
	date := now.Format("2006-01-02")
	if words = strings.TrimSpace(words); words == "" {
		words = "Agree all on the YOLO review page, " + date
	}
	type shownKey struct {
		key      string
		comments map[int]bool
	}
	var slugs []string
	shown := map[string][]*shownKey{}
	for _, it := range items {
		if it.Slug == "" || it.Key == "" {
			continue
		}
		if _, ok := shown[it.Slug]; !ok {
			slugs = append(slugs, it.Slug)
		}
		var sk *shownKey
		for _, k := range shown[it.Slug] {
			if strings.EqualFold(k.key, it.Key) {
				sk = k
			}
		}
		if sk == nil {
			sk = &shownKey{key: it.Key, comments: map[int]bool{}}
			shown[it.Slug] = append(shown[it.Slug], sk)
		}
		sk.comments[it.Comment] = true
	}
	for _, slug := range slugs {
		keys := shown[slug]
		issue := tracker.FindIssue(issues, slug)
		if issue == nil {
			for _, k := range keys {
				res.Skipped = append(res.Skipped, YOLOAllSkip{Slug: slug, Key: k.key, Why: "issue not found"})
			}
			continue
		}
		comments, _ := tracker.LoadComments(issue.FilePath)
		waiting := map[string][]int{}
		for _, p := range yolo.Unreviewed(issue, comments) {
			k := strings.ToLower(p.Key())
			waiting[k] = append(waiting[k], p.Comment)
		}
		agreed := 0
		for _, k := range keys {
			ids := waiting[strings.ToLower(k.key)]
			why := ""
			if len(ids) == 0 {
				why = yoloAllGone
			}
			for _, id := range ids {
				if !k.comments[id] {
					why = yoloAllChanged
				}
			}
			if why == "" {
				if code, msg := reviewYOLOPass(proj, issue, k.key, "agree", words); code != 0 {
					why = msg
				}
			}
			if why != "" {
				res.Skipped = append(res.Skipped, YOLOAllSkip{Slug: issue.Slug, Key: k.key, Why: why})
				continue
			}
			agreed += len(ids)
		}
		if agreed > 0 {
			res.Agreed += agreed
			res.Issues++
		}
	}
	return res
}

// yoloAllDoneLine is the line the page shows after Agree all reloads it,
// from the query's counts; "" when there are none.
func yoloAllDoneLine(agreed, issues, skipped string) string {
	a, err := strconv.Atoi(agreed)
	if err != nil || a < 0 {
		return ""
	}
	n, _ := strconv.Atoi(issues)
	k, _ := strconv.Atoi(skipped)
	line := fmt.Sprintf("Agreed %s on %s ✓", plural(a, "item", "items"), plural(max(n, 0), "issue", "issues"))
	if k > 0 {
		line += fmt.Sprintf(" · %d skipped: changed since the page loaded or no longer waiting; they stay below", k)
	} else {
		line += " · 0 skipped"
	}
	return line
}

// yoloTotals is yolo.Totals, named for the pages.
func yoloTotals(entries []yolo.Entry) (gates, checks int) { return yolo.Totals(entries) }
