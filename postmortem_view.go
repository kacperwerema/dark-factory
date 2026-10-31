package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/postmortem"
	"github.com/michal-franc/issue-viewer/internal/retro"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The post-mortem's one round: /p/<project>/postmortem/<slug> shows the
// newest post-mortem of an issue (issue-cli postmortem write) and every
// proposed action with Agree / Refuse; one Submit posts them all to
// …/postmortem/<slug>/decide. Only the owner's browser passes (s.fromOwner,
// an O route in docs/API/security.md). postmortem.Decide applies the agreed
// ones and writes the outcome lines back into the retro.

// PostmortemData is the post-mortem page.
type PostmortemData struct {
	Header             WbHeader
	Title              string
	Slug, IssueTitle   string
	IssueURL, File     string
	DecideURL          string
	Sections           []PostmortemSection
	Actions            []postmortem.Action
	Pending            int
	None, NoPostmortem bool
}

// PostmortemSection is one of the six sections, folded on the page.
type PostmortemSection struct {
	Name, Text string
}

func postmortemRetros(p *tracker.Project) string {
	return filepath.Join(projectRoot(p), "retros")
}

// newestPostmortem is issue slug's newest post-mortem, nil when none.
func newestPostmortem(p *tracker.Project, slug string) *retro.Entry {
	entries, _ := retro.Load(postmortemRetros(p))
	if pms := postmortem.Of(entries, slug); len(pms) > 0 {
		return pms[0]
	}
	return nil
}

func (s *Server) buildPostmortem(p *tracker.Project, prefix string, issue *tracker.Issue, now time.Time) PostmortemData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := PostmortemData{Title: p.Name + " · Post-mortem", Slug: issue.Slug, IssueTitle: issue.Title,
		IssueURL: prefix + "/issue/" + issue.Slug, DecideURL: prefix + "/postmortem/" + issue.Slug + "/decide"}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Post-mortem", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	e := newestPostmortem(p, issue.Slug)
	if e == nil {
		d.NoPostmortem = true
		return d
	}
	d.File = "retros/" + e.FileName
	secs, _ := postmortem.SplitSections(e.Body)
	for _, name := range postmortem.Sections[:len(postmortem.Sections)-1] {
		d.Sections = append(d.Sections, PostmortemSection{Name: name, Text: secs[strings.ToLower(name)]})
	}
	for _, a := range postmortem.Actions(e.Body) {
		if a.ID != "" {
			d.Actions = append(d.Actions, a)
		}
	}
	d.Pending = len(postmortem.Pending(d.Actions))
	d.None = len(d.Actions) == 0
	return d
}

func (s *Server) handlePostmortem(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, slug string) {
	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.Error(w, "issue not found: "+slug, http.StatusNotFound)
		return
	}
	d := s.buildPostmortem(proj, prefix, issue, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if d.NoPostmortem {
		w.WriteHeader(http.StatusNotFound)
	}
	if err := s.tmpl.ExecuteTemplate(w, "postmortem.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// postmortemRefused is what an agent or a script gets for the round.
const postmortemRefused = "only the owner agrees to a post-mortem's actions, from the post-mortem page in the browser; an agent asks on its issue trail (issue-cli comment <slug> \"question: …\")"

// handlePostmortemDecide is the owner's one round: {answers: [{id, agree,
// note}]} covering every open action.
func (s *Server) handlePostmortemDecide(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, slug string) {
	if !s.fromOwner(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": postmortemRefused})
		return
	}
	var body struct {
		Answers []postmortem.Answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "answers required"})
		return
	}
	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "issue not found: " + slug})
		return
	}
	e := newestPostmortem(proj, issue.Slug)
	if e == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": issue.Slug + " has no post-mortem"})
		return
	}
	now := time.Now()
	env := postmortem.Env{
		Repo: projectRoot(proj), IssueDir: proj.IssueDir, Project: proj.Slug,
		Issue: issue.Slug, System: issue.System,
		RetroPath: e.FilePath, RetroRef: "retros/" + e.FileName,
		Owner: tracker.OwnerAuthor, Now: now,
		NewExperiment: func(a postmortem.Action) (string, error) {
			return postmortemExperiment(proj, issue, e.FileName, a, now)
		},
	}
	outs, err := postmortem.Decide(env, body.Answers)
	switch {
	case errors.Is(err, postmortem.ErrDecided):
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error() + ". Reload to see the outcomes."})
		return
	case err != nil && len(outs) == 0:
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	applied, refused, failed := 0, 0, 0
	for _, o := range outs {
		switch {
		case o.Err != "":
			failed++
		case o.Agreed:
			applied++
		default:
			refused++
		}
	}
	line := fmt.Sprintf("postmortem: decided on the post-mortem page — %d applied, %d refused", applied, refused)
	if failed > 0 {
		line += fmt.Sprintf(", %d failed and still open", failed)
	}
	res := map[string]any{"ok": err == nil, "outcomes": outs}
	if err != nil {
		res["error"] = err.Error()
	}
	if cerr := tracker.AddCommentBy(issue.FilePath, 0, line+" (retros/"+e.FileName+")", "app", tracker.OwnerAuthor); cerr != nil {
		res["comment_error"] = cerr.Error()
	}
	log.Printf("postmortem: %s decided by the owner: %d applied, %d refused, %d failed", issue.Slug, applied, refused, failed)
	writeJSONStatus(w, http.StatusOK, res)
}

// postmortemExperiment files an agreed workflow action as an experiment
// proposal: an idea issue labelled experiment whose source is the retro.
func postmortemExperiment(proj *tracker.Project, issue *tracker.Issue, retroFile string, a postmortem.Action, now time.Time) (string, error) {
	title := "Experiment: " + strings.TrimSuffix(strings.TrimSpace(a.Title), ".")
	body := fmt.Sprintf(`## What the owner asked

Agreed on the post-mortem of %s (retros/%s, %s) on %s.

## Change to try

%s

## Why

%s

## Next

issue-cli experiment init <this slug> --hypothesis "..." --target <metric>:<up|down>:<threshold> --source retros/%s
`, issue.Slug, retroFile, a.ID, now.Format("2006-01-02"), strings.TrimSpace(a.Text), strings.TrimSpace(a.Differently), retroFile)
	path, slug, err := tracker.CreateIssueFileOpts(proj.IssueDir, tracker.CreateIssueOpts{
		Title: title, Status: "idea", System: issue.System, Labels: []string{"experiment"}, Body: body, Now: now,
	})
	if err != nil {
		return "", err
	}
	if fresh, err := tracker.LoadIssues(proj.IssueDir); err == nil {
		for _, is := range fresh {
			if is.FilePath == path {
				slug = is.Slug
			}
		}
	}
	return slug, nil
}

// postmortemNeedsYou is one Needs you row per issue whose newest
// post-mortem has actions the owner hasn't answered.
func postmortemNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	var rows []WbQueueCard
	for _, w := range postmortem.WaitingIn(postmortemRetros(p)) {
		since := w.Since
		if since.IsZero() {
			since = now
		}
		name := w.Title
		if name == "" {
			name = w.Issue
		}
		n := "actions wait"
		if w.Pending == 1 {
			n = "action waits"
		}
		rows = append(rows, WbQueueCard{
			Kind: "postmortem", Label: "Post-mortem", Tone: "you",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: w.Issue, Title: w.Title, Note: fmt.Sprintf("Post-mortem of %s: %d %s for your agree or refuse.", name, w.Pending, n),
			Since: since, Age: shortDuration(now.Sub(since)),
			CTA: "Open", URL: prefix + "/postmortem/" + w.Issue,
		})
	}
	return rows
}
