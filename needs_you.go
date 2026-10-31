package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The Needs you list: everything that waits on the owner, at
// /p/<project>/needs-you and across projects at /needs-you. needsYouRows is
// the one list every "N waiting on you" count is the length of: the
// sidebar, the Workshop headers, the lots on the map, the company panel.
// Ready gates can be stamped together (POST …/needs-you/approve); every
// other row keeps its own action.

// needsYouKinds are the list's sections, in order: the kinds of row each
// holds and its title.
var needsYouKinds = []struct {
	Key, Title string
	Kinds      []string
}{
	{"ci", "CI red", []string{"ci"}},
	{"approve", "To approve", []string{"gate"}},
	{"test", "To test", []string{"playtest", "checks"}},
	{"answer", "To answer", []string{"question", "clarify"}},
	{"outputs", "Outputs to review", []string{"outputs"}},
	{"jobs", "Due jobs", []string{"job"}},
	{"other", "Other", nil},
}

// NeedsYouData is the Needs you page, for one project or every project.
type NeedsYouData struct {
	Header WbHeader
	// Title is the page's name; All marks the page across projects.
	Title  string
	All    bool
	Count  int
	Groups []NeedsYouGroup
	// Ready is how many gates can be selected, on the whole page.
	Ready int
	// OwnerChecksURL is the project's Owner checks page ("" across projects).
	OwnerChecksURL string
}

// NeedsYouGroup is one project's rows, by section.
type NeedsYouGroup struct {
	Project, Slug, Color string
	// URL is the project's own Needs you page, ApproveURL where its gates
	// are stamped.
	URL, ApproveURL string
	Count           int
	Sections        []NeedsYouSection
	// After is YOLO's after-the-fact queue, one row per issue: shown, not
	// counted; at most needsYouAfterRows rows, AfterMore the rest.
	// AfterCount is the issues waiting (the review unit), AfterGates and
	// AfterChecks what they hold, AfterOldest the oldest item's age,
	// AfterURL the review page.
	After                               []WbYOLORow
	AfterCount, AfterGates, AfterChecks int
	AfterMore                           int
	AfterOldest, AfterURL               string
	// Error: the project's issues could not be read.
	Error string
	// Named: the group's heading names the project (the page across projects).
	Named bool
}

// NeedsYouSection is one kind of row: To approve, To test, …
type NeedsYouSection struct {
	Key, Title string
	Rows       []WbQueueCard
}

// NeedsYouJSON is /p/<project>/needs-you.json: the page's rows and count.
type NeedsYouJSON struct {
	Project string        `json:"project"`
	Count   int           `json:"count"`
	Rows    []WbQueueCard `json:"rows"`
	After   []WbYOLORow   `json:"after"`
	// AfterCount is the issues waiting for review after the fact (After
	// lists them all, one row each), AfterGates and AfterChecks what they
	// hold, AfterOldest the oldest item's time ("" when none), AfterURL the
	// review page.
	AfterCount  int    `json:"after_count"`
	AfterGates  int    `json:"after_gates"`
	AfterChecks int    `json:"after_checks"`
	AfterOldest string `json:"after_oldest,omitempty"`
	AfterURL    string `json:"after_url,omitempty"`
	URL         string `json:"url"`
}

// needsYouRows is what waits on the owner in the project: CI red, ready
// gates, playtests and check scripts to run, open questions, outputs to
// review (one row per department), due jobs without a standing start and
// unsent bug reports; CI red first, then the oldest.
func needsYouRows(p *tracker.Project, color, prefix string, open []*tracker.Issue, now time.Time) []WbQueueCard {
	return needsYouRowsWith(p, color, prefix, waitingOnOwner(p, open), now)
}

// needsYouRowsWith is needsYouRows with waitingOnOwner already read: the
// company page reads it once for the lot's queue and its rooms (it read
// every open issue's comments and workflow twice a render).
func needsYouRowsWith(p *tracker.Project, color, prefix string, owner []WaitingItem, now time.Time) []WbQueueCard {
	var rows []WbQueueCard
	if st, enabled := ciState(p); enabled && st != nil && st.IsRed() {
		slug := st.RedIssue
		if slug == "" {
			slug = st.Branch
		}
		rows = append(rows, WbQueueCard{
			Kind: "ci", Label: "CI red", Tone: "red", Blocking: true,
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: slug, Note: ciRedLine(st) + " fails on " + st.Branch + ".",
			Since: st.Updated, Age: shortDuration(now.Sub(st.Updated)),
			CTA: "View status", URL: prefix + "/status",
		})
	}
	for _, it := range owner {
		rows = append(rows, mapQueueRow(p, color, prefix, it, now))
	}
	_, _, waits := companyShelf(p, prefix, now)
	for _, sw := range waits {
		rows = append(rows, WbQueueCard{
			Kind: "outputs", Label: "Outputs", Tone: "you",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: sw.Name, Note: plural(sw.Count, "output waits for your review", "outputs wait for your review") + " on the shelf.",
			Since: now, CTA: "Open shelf", URL: sw.URL,
		})
	}
	rows = append(rows, connectorWaits(p, color, prefix, now)...)
	rows = append(rows, jobNeedsYouRows(p, color, prefix, now)...)
	rows = append(rows, postmortemNeedsYou(p, color, prefix, now)...)
	if n := unsentBugReports(); n > 0 {
		rows = append(rows, WbQueueCard{
			Kind: "bugs", Label: "Bug reports", Tone: "you",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: "issue-cli report-bug", Note: plural(n, "bug report is", "bug reports are") + " not sent yet: issue-cli report-bug --send.",
			Since: now, CTA: "Open status", URL: prefix + "/status",
		})
	}
	sortMapQueue(rows)
	return rows
}

// needsYouCount is how many things wait on the owner in the project: the
// sidebar's Status note.
func needsYouCount(p *tracker.Project, prefix string, open []*tracker.Issue) int {
	return len(needsYouRows(p, "", prefix, open, time.Now()))
}

// appendNeedsYou adds a project's rows to a list across projects. Unsent bug
// reports are this user's, not a project's: they are listed once.
func appendNeedsYou(all, rows []WbQueueCard) []WbQueueCard {
	bugs := false
	for _, r := range all {
		bugs = bugs || r.Kind == "bugs"
	}
	for _, r := range rows {
		if r.Kind == "bugs" && bugs {
			continue
		}
		all = append(all, r)
	}
	return all
}

// ciRedLine is the command that failed on red CI.
func ciRedLine(st *ci.State) string {
	if st.Failed != "" {
		return st.Failed
	}
	return "make validate"
}

// mapQueueRow words one waiting item for the owner, with its action.
func mapQueueRow(p *tracker.Project, color, prefix string, it WaitingItem, now time.Time) WbQueueCard {
	row := WbQueueCard{
		Kind: it.Kind, Tone: "you", Project: p.Name, ProjectSlug: p.Slug, Color: color,
		Slug: it.Issue.Slug, Title: it.Issue.Title, Since: it.Since, Age: shortDuration(now.Sub(it.Since)),
		URL: prefix + "/issue/" + it.Issue.Slug,
	}
	if it.Approx {
		row.Age = "≈ " + row.Age
	}
	switch it.Kind {
	case "gate":
		row.Label, row.CTA = "Gate: "+it.Target, "Review"
		row.Note = it.Issue.Title + " · " + it.Issue.Status + " → " + it.Target
		row.URL += "#approve-" + strings.ReplaceAll(strings.TrimSpace(it.Target), " ", "-")
		row.From, row.Target, row.Ready = it.Issue.Status, it.Target, true
	case "playtest":
		row.Label, row.CTA = "Playtest", "Open"
		row.Note = it.Issue.Title + " · " + strings.Join(it.Sessions, ", ")
		row.RunURL, row.RunLabel = prefix+"/issue/"+it.Issue.Slug+"/playtest", "▶ Play"
	case "checks":
		row.Label, row.CTA = "Human testing", "Check"
		row.Note = it.Issue.Title + " · " + strings.Join(it.Sessions, ", ")
		row.RunURL, row.RunLabel = prefix+"/issue/"+it.Issue.Slug+"/check-script", "▶ Run checks"
	case "question":
		row.Label, row.CTA = "Question", "Answer"
		row.Note = it.Issue.Title
		row.URL += "#comments"
	case "clarify":
		row.Label, row.CTA = "Clarify", "Answer"
		row.Note = it.Issue.Title + " · " + plural(it.Count, "question to answer", "questions to answer")
		row.URL += "#clarify"
	default:
		row.Label, row.CTA, row.Note = it.Kind, "Open", it.Issue.Title
	}
	return row
}

// needsYouGroup sorts a project's rows into the page's sections.
func needsYouGroup(p *tracker.Project, color, prefix string, rows []WbQueueCard, yolo *YOLOBand) NeedsYouGroup {
	g := NeedsYouGroup{Project: p.Name, Slug: p.Slug, Color: color, URL: prefix + "/needs-you", ApproveURL: prefix + "/needs-you/approve", Count: len(rows)}
	placed := map[int]bool{}
	for _, k := range needsYouKinds {
		sec := NeedsYouSection{Key: k.Key, Title: k.Title}
		for i, r := range rows {
			if placed[i] {
				continue
			}
			if k.Kinds == nil || containsFold(k.Kinds, r.Kind) {
				sec.Rows = append(sec.Rows, r)
				placed[i] = true
			}
		}
		if len(sec.Rows) > 0 {
			g.Sections = append(g.Sections, sec)
		}
	}
	g.After, g.AfterMore = yoloQueueRows(yolo, prefix, needsYouAfterRows)
	if yolo != nil && len(yolo.Queue) > 0 {
		g.AfterCount, g.AfterURL = len(yolo.Queue), prefix+"/yolo/review"
		g.AfterGates, g.AfterChecks = yoloTotals(yolo.Queue)
		if t, ok := yoloQueueOldest(yolo.Queue); ok {
			g.AfterOldest = shortDuration(time.Since(t))
		}
	}
	return g
}

// needsYouAfterRows is how many after-the-fact rows Needs you lists; the
// review page lists them all.
const needsYouAfterRows = 8

// readyCount is how many rows of the groups are gates that can be stamped.
func readyCount(groups []NeedsYouGroup) int {
	n := 0
	for _, g := range groups {
		for _, sec := range g.Sections {
			for _, r := range sec.Rows {
				if r.Ready {
					n++
				}
			}
		}
	}
	return n
}

// buildNeedsYou is one project's Needs you page.
func (s *Server) buildNeedsYou(p *tracker.Project, prefix string, now time.Time) NeedsYouData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := NeedsYouData{Title: p.Name + " · Needs you", Count: len(rows), OwnerChecksURL: prefix + "/owner-checks"}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Needs you", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	if g := needsYouGroup(p, color, prefix, rows, lot.YOLO); g.Count > 0 || len(g.After) > 0 {
		d.Groups = []NeedsYouGroup{g}
	}
	d.Ready = readyCount(d.Groups)
	return d
}

// buildNeedsYouAll is every project's Needs you, one group per project
// with something waiting.
func (s *Server) buildNeedsYouAll(now time.Time) NeedsYouData {
	type built struct {
		lot  MapLot
		rows []WbQueueCard
		err  error
	}
	res := make([]built, len(s.projects))
	var wg sync.WaitGroup
	for i := range s.projects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := &s.projects[i]
			if _, err := tracker.LoadIssues(p.IssueDir); err != nil {
				res[i].err = err
			}
			res[i].lot, res[i].rows = s.buildLot(p, s.projectColor(p), now)
		}(i)
	}
	wg.Wait()

	d := NeedsYouData{Title: "Needs you", All: true}
	var all []WbQueueCard
	for i := range s.projects {
		p, r := &s.projects[i], res[i]
		d.Header.Working += r.lot.Working
		d.Header.Idle += r.lot.Idle
		d.Header.AgentWait += r.lot.AgentWait
		d.Header.DoneWeek += r.lot.DoneWeek
		before := len(all)
		all = appendNeedsYou(all, r.rows)
		rows := append([]WbQueueCard(nil), all[before:]...)
		g := needsYouGroup(p, r.lot.Color, "/p/"+p.Slug, rows, r.lot.YOLO)
		g.Named = true
		if r.err != nil {
			g.Error = "could not read its issues: " + r.err.Error()
		}
		if g.Count > 0 || len(g.After) > 0 || g.Error != "" {
			d.Groups = append(d.Groups, g)
		}
	}
	d.Count = len(all)
	d.Header.Crumbs = []WbCrumb{{Label: "Map", URL: "/"}, {Label: "Needs you", Current: true}}
	d.Header.Waiting, d.Header.WaitingURL = d.Count, "/needs-you"
	d.Ready = readyCount(d.Groups)
	return d
}

func (s *Server) handleNeedsYou(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	s.renderNeedsYou(w, s.buildNeedsYou(proj, prefix, time.Now()))
}

func (s *Server) handleNeedsYouAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.renderNeedsYou(w, s.buildNeedsYouAll(time.Now()))
}

func (s *Server) renderNeedsYou(w http.ResponseWriter, d NeedsYouData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "needs_you.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleNeedsYouJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	now := time.Now()
	lot, rows := s.buildLot(proj, s.projectColor(proj), now)
	after, _ := yoloQueueRows(lot.YOLO, prefix, 1<<30)
	out := NeedsYouJSON{Project: proj.Slug, Count: len(rows), Rows: rows, After: after, URL: prefix + "/needs-you"}
	if lot.YOLO != nil && len(lot.YOLO.Queue) > 0 {
		out.AfterCount, out.AfterURL = len(lot.YOLO.Queue), prefix+"/yolo/review"
		out.AfterGates, out.AfterChecks = yoloTotals(lot.YOLO.Queue)
		if t, ok := yoloQueueOldest(lot.YOLO.Queue); ok {
			out.AfterOldest = t.Format(time.RFC3339)
		}
	}
	if out.Rows == nil {
		out.Rows = []WbQueueCard{}
	}
	if out.After == nil {
		out.After = []WbYOLORow{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// NeedsYouApproval is one gate's result in a bulk approve.
type NeedsYouApproval struct {
	Slug   string `json:"slug"`
	Status string `json:"status"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	// WordsError: the approval is stamped, the words comment failed.
	WordsError string `json:"words_error,omitempty"`
}

// handleNeedsYouApprove stamps the owner's approval on each selected gate,
// the record the station writes, after checking each still waits on that
// approval and nothing else. A failed row does not stop the others. No
// transition and no message to the agent (D-4): the lead or the worker
// picks the stamp up.
func (s *Server) handleNeedsYouApprove(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var body struct {
		Items []struct {
			Slug   string `json:"slug"`
			Status string `json:"status"`
		} `json:"items"`
		// Words are the owner's for the whole batch: an approval: comment on
		// each stamped issue.
		Words string `json:"words"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Items) == 0 {
		http.Error(w, `{"error":"items required: [{slug, status}]"}`, http.StatusBadRequest)
		return
	}
	words, now := strings.TrimSpace(body.Words), time.Now()
	results := make([]NeedsYouApproval, 0, len(body.Items))
	for _, it := range body.Items {
		res := NeedsYouApproval{Slug: it.Slug, Status: it.Status}
		issue := s.findIssueBySlug(proj, it.Slug)
		if issue == nil {
			res.Error = "issue not found"
		} else if why := gateNotReady(proj, issue, it.Status); why != "" {
			res.Error = why
		} else if _, wordsErr, err := stampApproval(issue, it.Status, "", words, now); err != nil {
			res.Error = "could not write the approval: " + err.Error()
		} else {
			res.OK, res.WordsError = true, wordsErr
			log.Printf("needs you: %s approved for %q", issue.Slug, it.Status)
		}
		results = append(results, res)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// gateNotReady says why the issue's gate for status can't be stamped from
// the list; "" when its approval is the only thing it waits on.
func gateNotReady(proj *tracker.Project, issue *tracker.Issue, status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "no status to approve"
	}
	if strings.EqualFold(issue.HumanApproval, status) {
		return "already approved for " + status
	}
	a := ownerAttention(proj, issue)
	switch {
	case a.Gate == nil:
		return fmt.Sprintf("no longer waiting on an approval (now %s)", issue.Status)
	case !strings.EqualFold(a.Gate.Target, status):
		return fmt.Sprintf("now waits on approval for %s, not %s", a.Gate.Target, status)
	case !a.Gate.Ready:
		return "no longer ready: its checklist for " + a.Gate.Target + " is still open"
	}
	return ""
}
