package viewer

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ownerstep"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Owner steps on Needs you (internal/ownerstep, issue-cli owner-step): one
// row per open step with its due day, the exact body and Done. Done posts
// POST /p/<project>/owner-steps/<id>/done {text?}, an O route
// (ownerOnlyRoute), so anything but the owner's signed-in browser gets 403.

// ownerStepRefused is the agent's way around in the 403: ask, never tick.
const ownerStepRefused = "a worker never ticks an owner step; ask on your issue trail: issue-cli comment <slug> \"question: did you do owner step <id>?\" (a lead relays the owner's words: issue-cli owner-step done <id> --quote \"…\")"

var ownerStepDonePathRe = regexp.MustCompile(`^owner-steps/(st-[0-9a-f]+)/done$`)

// ownerStepDoneURL is a step's Done endpoint.
func ownerStepDoneURL(prefix, id string) string { return prefix + "/owner-steps/" + id + "/done" }

// ownerStepNeedsYou is one Needs you row per open owner step: overdue ones
// red, each dated by its due day so the oldest due sorts first.
func ownerStepNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	if p == nil || p.IssueDir == "" {
		return nil
	}
	open, err := ownerstep.LoadOpen(p.IssueDir, now)
	if err != nil || len(open) == 0 {
		return nil
	}
	var rows []WbQueueCard
	for _, s := range open {
		since, err := time.ParseInLocation("2006-01-02", s.Due, time.Local)
		if err != nil {
			since = now
		}
		overdue := s.Overdue(now)
		row := WbQueueCard{
			Kind: "step", Label: "Your step", Tone: "you",
			Project: p.Name, ProjectSlug: p.Slug, Color: color,
			Slug: s.ID, Title: s.Title, Since: since, Age: s.DueLine(now),
			Note: s.Title, Body: s.Body, Due: s.Due, Overdue: overdue,
			CTA: "Open", URL: prefix + "/needs-you#" + s.ID,
			StepURL: ownerStepDoneURL(prefix, s.ID),
		}
		if overdue {
			row.Tone, row.Label = "red", "Your step · overdue"
		}
		var extra []string
		if s.Repeat > 0 {
			extra = append(extra, "repeats "+ownerstep.RepeatLabel(s.Repeat)+" after each tick")
		}
		if s.Issue != "" {
			extra = append(extra, "for "+s.Issue)
			row.URL = prefix + "/issue/" + s.Issue
		}
		if s.By != "" {
			extra = append(extra, "added by "+s.By)
		}
		if len(extra) > 0 {
			row.Note += " · " + strings.Join(extra, " · ")
		}
		rows = append(rows, row)
	}
	return rows
}

type ownerStepDoneRequest struct {
	Text string `json:"text"`
}

// handleOwnerStepDone serves POST owner-steps/<id>/done {text?}: the owner
// did the step. The route is owner-only (ownerOnlyRoute).
func (s *Server) handleOwnerStepDone(w http.ResponseWriter, r *http.Request, proj *tracker.Project, rest string) {
	m := ownerStepDonePathRe.FindStringSubmatch(rest)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	var req ownerStepDoneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeYOLOError(w, http.StatusBadRequest, "bad request")
		return
	}
	now := time.Now()
	evs, err := ownerstep.Read(proj.IssueDir)
	if err != nil {
		writeYOLOError(w, http.StatusInternalServerError, err.Error())
		return
	}
	step := ownerstep.Find(ownerstep.Fold(evs), m[1])
	if step == nil {
		writeYOLOError(w, http.StatusNotFound, "no owner step "+m[1])
		return
	}
	if !step.IsOpen(now) {
		writeYOLOError(w, http.StatusConflict, m[1]+" is not open: it was ticked or dropped")
		return
	}
	ev := ownerstep.Event{Op: ownerstep.OpDone, ID: step.ID, At: now.Format(time.RFC3339), By: tracker.OwnerAuthor, Role: ownerstep.RoleOwnerBrowser, Words: strings.TrimSpace(req.Text)}
	if err := ownerstep.Append(proj.IssueDir, ev); err != nil {
		writeYOLOError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("owner-steps: %s done by the owner", step.ID)
	out := map[string]any{"status": "ok", "id": step.ID}
	if step.Repeat > 0 {
		out["reopens"] = ownerstep.AddDays(ownerstep.DayOf(now), step.Repeat)
	}
	writeJSONStatus(w, http.StatusOK, out)
}
