package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// YOLO mode in the viewer (internal/yolo): the band every project page shows
// while it is on, the switch's endpoints (on, extend, off), the owner's
// after-the-fact review of each gate passed without them, and the watcher
// that closes an expired window or one the CI valve tripped.

// yoloPollInterval is how often the watcher looks at each project's window.
const yoloPollInterval = 30 * time.Second

// YOLOBand is what the pages show of YOLO for one project.
type YOLOBand struct {
	On     bool
	Since  time.Time
	End    string // "08:00", "until turned off"
	Passed int
	Valves string // "CI guards · no releases"
	// Queue is the issues waiting for the owner's review after the fact,
	// one entry per issue (its gates and owner checks): every count is its
	// length. Shown after YOLO ends too, until it is empty.
	Queue []yolo.Entry
	// EndReason is why the last window closed.
	EndReason string
	Ended     time.Time
	// Never is the gates YOLO never passes while on (yolo.OwnerGates).
	Never []yolo.OwnerGate
}

// NeverLabels is the never-pass gates as one line each, for a title.
func (b *YOLOBand) NeverLabels() string {
	var lines []string
	for _, g := range b.Never {
		lines = append(lines, g.Label())
	}
	return strings.Join(lines, "\n")
}

// Show reports whether the band (or the after-the-fact counter) has
// anything to say.
func (b *YOLOBand) Show() bool { return b != nil && (b.On || len(b.Queue) > 0) }

// yoloBand reads the project's YOLO state and the review queue.
func yoloBand(proj *tracker.Project, issues []*tracker.Issue, now time.Time) *YOLOBand {
	st, on, err := yolo.Active(ciRootFor(proj), proj.Slug, now)
	if err != nil {
		log.Printf("yolo: %s: %v", proj.Slug, err)
		return nil
	}
	b := &YOLOBand{On: on, Queue: yolo.QueueEntries(issues), EndReason: st.EndReason, Ended: st.Ended}
	if !on {
		return b
	}
	b.Since, b.End = st.Since, st.EndLabel(now)
	b.Valves = yoloValvesShort(st.Valves)
	b.Never = yolo.OwnerGates(proj, st.Valves)
	for _, issue := range issues {
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil {
			continue
		}
		for _, p := range yolo.Passes(issue, comments) {
			if at, ok := tracker.CommentTime(p.At); !ok || !at.Before(st.Since) {
				b.Passed++
			}
		}
	}
	return b
}

// yoloValvesShort is the band's valve part.
func yoloValvesShort(v yolo.Valves) string {
	parts := []string{}
	if v.CIRedMinutes > 0 {
		parts = append(parts, "CI guards")
	}
	if v.Releases {
		parts = append(parts, "releases unlocked")
	} else {
		parts = append(parts, "no releases")
	}
	return strings.Join(parts, " · ")
}

// watchYOLO closes windows that expired or tripped a valve, so the pages and
// the log say why YOLO ended even when no agent ran issue-cli meanwhile.
func (s *Server) watchYOLO(stop <-chan struct{}) {
	ticker := time.NewTicker(yoloPollInterval)
	defer ticker.Stop()
	for {
		for i := range s.projects {
			proj := &s.projects[i]
			before, _ := yolo.Load(ciRootFor(proj), proj.Slug)
			st, _, err := yolo.Active(ciRootFor(proj), proj.Slug, time.Now())
			if err != nil {
				log.Printf("yolo: %s: %v", proj.Slug, err)
			} else if before != nil && before.On && !st.On {
				log.Printf("yolo: %s: off (%s)", proj.Slug, st.EndReason)
			}
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// yoloRequest is the switch's form: the owner's words, the end, the valves.
type yoloRequest struct {
	Until    string `json:"until"`
	Words    string `json:"words"`
	CIValve  *bool  `json:"ci_valve"`
	Releases bool   `json:"releases"`
	Reason   string `json:"reason"`
}

func writeYOLOError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleYOLO serves POST yolo/on, yolo/extend, yolo/off and GET yolo.json.
func (s *Server) handleYOLO(w http.ResponseWriter, r *http.Request, proj *tracker.Project, action string) {
	now := time.Now()
	root := ciRootFor(proj)
	if action == "json" {
		issues, _ := tracker.LoadIssues(proj.IssueDir)
		b := yoloBand(proj, issues, now)
		if b == nil {
			writeYOLOError(w, http.StatusInternalServerError, "reading YOLO state failed")
			return
		}
		if b.Queue == nil {
			b.Queue = []yolo.Entry{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(b)
		return
	}
	var req yoloRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeYOLOError(w, http.StatusBadRequest, "bad request")
			return
		}
	}
	switch action {
	case "on":
		until, forever, err := yolo.ParseUntil(req.Until, now)
		if err != nil {
			writeYOLOError(w, http.StatusBadRequest, err.Error())
			return
		}
		valves := yolo.Valves{Releases: req.Releases}
		if (req.CIValve == nil || *req.CIValve) && proj.LoadWorkflow().CI.Enabled() {
			valves.CIRedMinutes = yolo.DefaultCIRedMinutes
		}
		st := yolo.State{Until: until, Forever: forever, By: "owner", Words: strings.TrimSpace(req.Words), How: "viewer", Valves: valves}
		if err := yolo.TurnOn(root, proj.Slug, st, now); err != nil {
			writeYOLOError(w, http.StatusConflict, err.Error())
			return
		}
	case "extend":
		until, forever, err := yolo.ParseUntil(req.Until, now)
		if err != nil {
			writeYOLOError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := yolo.Extend(root, proj.Slug, until, forever, now); err != nil {
			writeYOLOError(w, http.StatusConflict, err.Error())
			return
		}
	case "off":
		st, on, err := yolo.Active(root, proj.Slug, now)
		if err != nil {
			writeYOLOError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if on {
			reason := "turned off by the owner"
			if req.Reason != "" {
				reason += ": " + req.Reason
			}
			if err := yolo.TurnOff(root, proj.Slug, st, now, reason); err != nil {
				writeYOLOError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	s.handleYOLO(w, r, proj, "json")
}

// yoloReviewRequest is the owner's verdict on one YOLO pass.
type yoloReviewRequest struct {
	Status  string `json:"status"`  // the approved status of the pass
	Verdict string `json:"verdict"` // agree or back
	Words   string `json:"words"`
}

// handleYOLOReview serves POST issue/<slug>/yolo-review: Agree records the
// owner's words; Back moves the issue back to the status it left at the
// gate, with the words, and the worker or lead picks it up from the trail.
func (s *Server) handleYOLOReview(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"/issue/"), "/yolo-review")
	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}
	var req yoloReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeYOLOError(w, http.StatusBadRequest, "bad request")
		return
	}
	if code, msg := reviewYOLOPass(proj, issue, req.Status, req.Verdict, req.Words); code != 0 {
		writeYOLOError(w, code, msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// reviewYOLOPass records the owner's verdict on one unreviewed pass of the
// issue: key names it (the approved status or an owner check's box; ""
// takes the first waiting). Agree adds the owner's yolo-review: comment;
// back moves the issue back to the status it left at the gate, then adds
// it. It returns 0 on success, else an HTTP status and why (-1: the move
// back failed, msg is the transition error). The station's review and the
// review page's Agree all both go through it.
func reviewYOLOPass(proj *tracker.Project, issue *tracker.Issue, key, verdict, words string) (int, string) {
	words = strings.TrimSpace(words)
	comments, _ := tracker.LoadComments(issue.FilePath)
	var pass *yolo.Pass
	for _, p := range yolo.Unreviewed(issue, comments) {
		if key == "" || strings.EqualFold(p.Key(), key) {
			pass = &p
			break
		}
	}
	if pass == nil {
		if key != "" {
			return http.StatusConflict, key + " no longer waits for review on this issue"
		}
		return http.StatusConflict, "no YOLO pass waits for review on this issue"
	}
	date := time.Now().Format("2006-01-02")
	switch verdict {
	case "agree":
		if words == "" {
			words = "agreed"
		}
		if err := tracker.AddCommentBy(issue.FilePath, 0, tracker.YOLOReviewComment("agreed", words, "viewer", date, pass.Key()), "app", tracker.OwnerAuthor); err != nil {
			return http.StatusInternalServerError, err.Error()
		}
	case "back":
		if words == "" {
			return http.StatusBadRequest, "say why it goes back: your words reach the agent"
		}
		wf := proj.LoadWorkflowForIssue(issue)
		back := yolo.BackTo(wf, *pass)
		if back == "" {
			return http.StatusConflict, "no status to go back to"
		}
		if !strings.EqualFold(issue.Status, back) {
			if _, err := wf.MoveBack(issue.FilePath, back); err != nil {
				return http.StatusConflict, err.Error()
			}
		}
		if err := tracker.AddCommentBy(issue.FilePath, 0, tracker.YOLOReviewComment("back to "+back, words, "viewer", date, pass.Key()), "app", tracker.OwnerAuthor); err != nil {
			return http.StatusInternalServerError, err.Error()
		}
	default:
		return http.StatusBadRequest, "verdict is agree or back"
	}
	return 0, ""
}
