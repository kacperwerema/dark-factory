package viewer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Decision challenges in the viewer (docs/workflow.md#decision-ledger): one
// Needs you row per open challenge (tracker.LoadOpenChallenges), with the
// decision's text, why, the proposal and the other issues on the decision,
// and the owner's Keep it: POST /p/<project>/decisions/challenge/resolve, an
// O route (owner.go) so anything but the owner's signed-in browser gets 403.

// challengeResolveURL is the Keep it endpoint.
func challengeResolveURL(prefix string) string { return prefix + "/decisions/challenge/resolve" }

// challengeAlsoMax caps the other issues a row names.
const challengeAlsoMax = 6

// challengeNeedsYou is one row per open challenge.
func challengeNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	if p == nil || p.IssueDir == "" {
		return nil
	}
	open, err := tracker.LoadOpenChallenges(p.IssueDir)
	if err != nil || len(open) == 0 {
		return nil
	}
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		return nil
	}
	closed := tracker.ClosedIssueFunc(p)
	index := tracker.BuildDecisionIndex(issues, closed)
	var rows []WbQueueCard
	for _, c := range open {
		row := WbQueueCard{
			Kind: "challenge", Label: "Challenge", Tone: "you",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: c.Issue, Since: c.Since, Age: shortDuration(now.Sub(c.Since)),
			CTA: "Change it", ChatURL: prefix + "/lead",
			ResolveURL: challengeResolveURL(prefix), ResolveRef: c.Ref,
		}
		if is := tracker.FindIssue(issues, c.Issue); is != nil {
			row.Title = is.Title
		}
		decSlug, _, _ := strings.Cut(c.Ref, "#")
		row.URL = prefix + "/issue/" + decSlug + "#decisions"
		row.Note = fmt.Sprintf("%s challenges %s. Keep it, or change it: decide %s --replaces %s --text \"…\" (the lead relays your words).", c.Issue, c.Ref, decSlug, c.Ref)
		q := "Why: " + c.Why
		if c.Propose != "" {
			q += " · Proposes: " + c.Propose
		}
		row.Question = clipRunes(q, needsYouQuestionMax)
		if e := index.Lookup(c.Ref); e != nil {
			row.Decision = clipRunes(e.Text, needsYouQuestionMax)
			row.Also = challengeAlso(p, issues, closed, e, c.Issue)
		}
		rows = append(rows, row)
	}
	return rows
}

// challengeAlso is the other open issues on a challenged decision: those
// that cite it, and those whose work/<slug> branch touches its paths:. They
// go on: decisions_respected judges them by citation.
func challengeAlso(p *tracker.Project, issues []*tracker.Issue, closed func(*tracker.Issue) bool, e *tracker.DecisionEntry, challenger string) []string {
	why := map[string]string{}
	var order []string
	add := func(slug, w string) {
		if strings.EqualFold(slug, challenger) || strings.EqualFold(slug, e.Slug) {
			return
		}
		if _, ok := why[slug]; !ok {
			order = append(order, slug)
			why[slug] = w
			return
		}
		why[slug] += ", " + w
	}
	for _, is := range e.CitedOpen {
		add(is.Slug, "cites it")
	}
	if len(e.Paths) > 0 {
		root := projectRoot(p)
		for _, is := range issues {
			if closed(is) {
				continue
			}
			if files := cachedBranchTouches(root, is.Slug, e.Paths); len(files) > 0 {
				add(is.Slug, "branch touches "+files[0])
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i] < order[j] })
	var out []string
	for i, s := range order {
		if i == challengeAlsoMax {
			out = append(out, fmt.Sprintf("… %d more", len(order)-i))
			break
		}
		out = append(out, s+" ("+why[s]+")")
	}
	return out
}

// The branch-touch cache: one git diff per branch and main head pair.
var (
	branchTouchMu    sync.Mutex
	branchTouchCache = map[string][]string{}
)

func cachedBranchTouches(root, slug string, globs []string) []string {
	branch, main := tracker.BranchHeads(root, slug)
	if branch == "" {
		return nil
	}
	key := root + "\x00" + branch + "\x00" + main + "\x00" + strings.Join(globs, ",")
	branchTouchMu.Lock()
	hit, ok := branchTouchCache[key]
	branchTouchMu.Unlock()
	if ok {
		return hit
	}
	hit = tracker.BranchTouches(root, slug, globs)
	branchTouchMu.Lock()
	if len(branchTouchCache) > 512 {
		branchTouchCache = map[string][]string{}
	}
	branchTouchCache[key] = hit
	branchTouchMu.Unlock()
	return hit
}

type challengeResolveRequest struct {
	Ref   string `json:"ref"`
	Issue string `json:"issue"`
	Text  string `json:"text"`
}

// handleChallengeResolve serves POST decisions/challenge/resolve {ref,
// issue?, text?}: the owner keeps the decision, closing its open challenges
// (only issue's when set). The route is owner-only (ownerOnlyRoute).
func (s *Server) handleChallengeResolve(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	var req challengeResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeYOLOError(w, http.StatusBadRequest, "bad request")
		return
	}
	ref := strings.TrimSpace(req.Ref)
	if !strings.Contains(ref, "#D-") {
		writeYOLOError(w, http.StatusBadRequest, "ref required: <slug>#D-n")
		return
	}
	n, err := resolveChallengesAsOwner(proj, ref, strings.TrimSpace(req.Issue), req.Text, time.Now())
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errNoOpenChallenge) {
			code = http.StatusConflict
		}
		writeYOLOError(w, code, err.Error())
		return
	}
	log.Printf("decisions: %s kept by the owner, %d challenge(s) resolved", ref, n)
	writeJSONStatus(w, http.StatusOK, map[string]any{"status": "ok", "ref": ref, "resolved": n})
}

var errNoOpenChallenge = errors.New("no open challenge on it")

// resolveChallengesAsOwner closes the open challenges on ref (only issue's
// when set) as kept, written as the owner's browser; it returns how many.
func resolveChallengesAsOwner(proj *tracker.Project, ref, issue, words string, now time.Time) (int, error) {
	open, err := tracker.LoadOpenChallenges(proj.IssueDir)
	if err != nil {
		return 0, err
	}
	list := tracker.ChallengesOn(open, ref)
	if issue != "" {
		list = tracker.ChallengesBy(list, issue)
	}
	if len(list) == 0 {
		return 0, fmt.Errorf("%s: %w", ref, errNoOpenChallenge)
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return 0, err
	}
	cw := tracker.ChallengeWrite{
		IssueDir: proj.IssueDir, Actor: ownerBrowserWrite(proj).Actor, Author: tracker.OwnerAuthor, Source: "app",
		File: func(slug string) string {
			if is := tracker.FindIssue(issues, slug); is != nil {
				return is.FilePath
			}
			return ""
		},
	}
	return len(list), tracker.ResolveChallenges(cw, list, tracker.ChallengeKept, "", words, now)
}
