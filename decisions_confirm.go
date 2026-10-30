package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Confirm a decision: the owner gives their words on a D-n an agent took
// alone or marked provisional. The same line keeps its ref (tracker.
// ConfirmDecision), so no citation goes stale and no CEO is needed.

var decisionConfirmPathRe = regexp.MustCompile(`^decisions/(.+)/(D-\d+)/confirm$`)

type decisionConfirmRequest struct {
	Text string `json:"text"`
}

type decisionConfirmResponse struct {
	Status string `json:"status"`
	Ref    string `json:"ref"`
	Quote  string `json:"quote"`
	Line   string `json:"line"`
}

// decisionConfirmURL is the endpoint for one decision.
func decisionConfirmURL(prefix, slug, id string) string {
	return prefix + "/decisions/" + slug + "/" + id + "/confirm"
}

// handleDecisionConfirm serves POST decisions/<slug>/<D-n>/confirm {text?}.
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
	d, err := tracker.ConfirmDecision(issue.FilePath, issue.Slug, id, req.Text, time.Now())
	switch {
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(decisionConfirmResponse{Status: "ok", Ref: d.Ref, Quote: d.Quote, Line: strings.TrimSpace(d.Raw)})
}
