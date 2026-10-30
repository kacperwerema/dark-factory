package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Send back for fixes: the owner says no at a gate. The issue moves back one
// status on its path (or to the status asked for), the owner's words go on
// the trail as a rejected: comment, and the worker is nudged the way an
// approval nudges it.

type sendBackRequest struct {
	Text string `json:"text"`
	To   string `json:"to"`
}

type sendBackResponse struct {
	Status            string `json:"status"`
	From              string `json:"from"`
	BackTo            string `json:"back_to"`
	NotifiedSession   string `json:"notified_session,omitempty"`
	NotificationError string `json:"notification_error,omitempty"`
}

// handleSendBack serves POST issue/<slug>/send-back {text, to?}.
func (s *Server) handleSendBack(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"/issue/"), "/send-back")
	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}
	var req sendBackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeYOLOError(w, http.StatusBadRequest, "bad request")
		return
	}
	words := strings.TrimSpace(req.Text)
	if words == "" {
		writeYOLOError(w, http.StatusBadRequest, "say what to fix: your words reach the agent")
		return
	}
	wf := proj.LoadWorkflowForIssue(issue)
	to, err := wf.BackTarget(issue.Status, strings.TrimSpace(req.To))
	switch {
	case errors.Is(err, tracker.ErrNoBackTarget):
		writeYOLOError(w, http.StatusConflict, fmt.Sprintf("%s: no status to go back to", issue.Status))
		return
	case err != nil:
		writeYOLOError(w, http.StatusBadRequest, err.Error())
		return
	}
	from := issue.Status
	if !strings.EqualFold(from, to) {
		if from, err = wf.MoveBack(issue.FilePath, to); err != nil {
			writeTransitionError(w, err)
			return
		}
	}
	date := time.Now().Format("2006-01-02")
	if err := tracker.AddCommentBy(issue.FilePath, 0, tracker.RejectedComment(words, "viewer", date, from, to), "app", tracker.OwnerAuthor); err != nil {
		writeYOLOError(w, http.StatusInternalServerError, "moved to "+to+" but your words were not saved: "+err.Error())
		return
	}

	resp := sendBackResponse{Status: "ok", From: from, BackTo: to}
	sessionMap, _ := sessionsByIssueSlug(proj, []*tracker.Issue{issue})
	if sessions := sessionMap[issue.Slug]; len(sessions) == 0 {
		resp.NotificationError = "no worker is live on this issue: the lead sees it on the trail"
	} else if err := tmuxSendKeys(sessions[0].Name, []string{sendBackNudge(issue.Slug, from, to, words)}); err != nil {
		resp.NotificationError = err.Error()
	} else {
		resp.NotifiedSession = sessions[0].Name
	}
	if resp.NotificationError != "" {
		log.Printf("send back %s to %q: agent not nudged: %s", issue.Slug, to, resp.NotificationError)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// sendBackNudge is the line the worker's session gets.
func sendBackNudge(slug, from, to, words string) string {
	where := "Sent back to " + to
	if strings.EqualFold(from, to) {
		where = "Not approved, stays in " + to
	}
	return fmt.Sprintf("%s for fixes by the owner: %q. Run issue-cli start %s and fix that first.", where, words, slug)
}
