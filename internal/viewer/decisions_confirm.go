package viewer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Confirm a decision: the owner gives their words on a D-n an agent took
// alone or marked provisional. The same line keeps its ref (tracker.
// ConfirmDecision), so no citation goes stale and no CEO is needed.

var decisionConfirmPathRe = regexp.MustCompile(`^decisions/(.+)/(D-\d+)/confirm$`)

type decisionConfirmRequest struct {
	Text string `json:"text"`
	// Hard also makes the decision hard: the owner's act, from the owner's
	// browser only (the route is owner-only).
	Hard bool `json:"hard"`
}

type decisionConfirmResponse struct {
	Status string `json:"status"`
	Ref    string `json:"ref"`
	Quote  string `json:"quote"`
	Line   string `json:"line"`
	Hard   bool   `json:"hard"`
}

// ownerBrowserWrite logs a decision write the owner made in the viewer.
func ownerBrowserWrite(proj *tracker.Project) tracker.DecisionWrite {
	return tracker.DecisionWrite{IssueDir: proj.IssueDir, Actor: tracker.DecisionActor{Name: tracker.OwnerAuthor, Role: tracker.ActorOwnerBrowser}}
}

// decisionConfirmURL is the endpoint for one decision.
func decisionConfirmURL(prefix, slug, id string) string {
	return prefix + "/decisions/" + slug + "/" + id + "/confirm"
}

// handleDecisionConfirm serves POST decisions/<slug>/<D-n>/confirm
// {text?, hard?}: the owner's words on a decision, and with hard also the
// owner making it hard (on one that already has their words, only that).
func (s *Server) handleDecisionConfirm(w http.ResponseWriter, r *http.Request, proj *tracker.Project, rest string) {
	m := decisionConfirmPathRe.FindStringSubmatch(rest)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	slug, id := m[1], m[2]
	var req decisionConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeYOLOError(w, http.StatusBadRequest, "bad request")
		return
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		writeYOLOError(w, http.StatusInternalServerError, err.Error())
		return
	}
	issue := tracker.FindIssue(issues, slug)
	if issue == nil {
		writeYOLOError(w, http.StatusNotFound, "no issue "+slug)
		return
	}
	e := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(proj)).Lookup(issue.Slug + "#" + id)
	switch {
	case e == nil:
		writeYOLOError(w, http.StatusNotFound, fmt.Sprintf("%s has no %s", issue.Slug, id))
		return
	case e.ReplacedBy != nil:
		writeYOLOError(w, http.StatusConflict, fmt.Sprintf("%s is superseded by %s: confirm that one", e.Ref, e.ReplacedBy.Ref))
		return
	}
	d, err := tracker.ConfirmDecisionAs(issue.FilePath, issue.Slug, id, req.Text, req.Hard, time.Now(), ownerBrowserWrite(proj))
	switch {
	case errors.Is(err, tracker.ErrDecisionConfirmed) && req.Hard:
		writeYOLOError(w, http.StatusConflict, e.Ref+" is already hard")
		return
	case errors.Is(err, tracker.ErrDecisionConfirmed):
		writeYOLOError(w, http.StatusConflict, e.Ref+" already has your words")
		return
	case errors.Is(err, tracker.ErrDecisionNotFound):
		writeYOLOError(w, http.StatusNotFound, fmt.Sprintf("%s has no %s", issue.Slug, id))
		return
	case err != nil:
		writeYOLOError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Confirming a challenged decision keeps it: its challenges close.
	if _, err := resolveChallengesAsOwner(proj, d.Ref, "", req.Text, time.Now()); err != nil && !errors.Is(err, errNoOpenChallenge) {
		log.Printf("decisions: %s confirmed, but resolving its challenges failed: %v", d.Ref, err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(decisionConfirmResponse{Status: "ok", Ref: d.Ref, Quote: d.Quote, Line: strings.TrimSpace(d.Raw), Hard: d.Hard})
}
