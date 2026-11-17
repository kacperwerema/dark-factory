package viewer

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// Auto-start in the viewer (internal/autostart): the Report's card with the
// owner's words, the verdict now and the last start on this machine, and
// the owner's Pause / Resume / Turn off. Turning it on stays in issue-cli
// with the owner's words. The viewer never starts a worker for it.

// AutoStartView is what the Report and status.md show of auto-start.
type AutoStartView struct {
	Prefix      string
	Paused      bool
	PauseReason string
	Words       string
	By, How     string
	Since       string
	Below       int
	Cap         int
	GapMinutes  int
	// Load is "load 4.3 on 14 cores (max 14)" or "load unknown".
	Load string
	// Verdict is "start <slug> (…)" or "hold: <reason>"; Start and Slug
	// name the issue that starts next.
	Verdict string
	Start   bool
	Slug    string
	// Last is the last auto-start on this machine, in any project.
	Last string
	// Share is the turn between projects: each one's start_weight: and
	// starts over the last day ("atlas ×3 6 starts · dark-factory ×1 2
	// starts"), or this project's weight alone.
	Share string
}

// shareLine reads the machine's share (never records an ask: a page load
// is not a lead asking) and words it for proj.
func shareLine(proj *tracker.Project, share *autostart.Share, now time.Time) string {
	if line := autostart.ShareLine(share.Rows(proj.Slug, proj.Weight(), now)); line != "" {
		return autostart.ShareLinePrefix() + line
	}
	return fmt.Sprintf("start_weight: %d · no other project waited for the gap in the last day", proj.Weight())
}

// autostartView reads the project's auto-start and decides now; nil when
// it is off.
func autostartView(proj *tracker.Project, issues []*tracker.Issue, prefix string, now time.Time) *AutoStartView {
	root := ciRootFor(proj)
	last, _ := autostart.LastStart(autostart.SharedDir())
	share, _ := autostart.LoadShare(autostart.SharedDir())
	_, yoloOn, _ := yolo.Active(root, proj.Slug, now)
	v, st, err := agentrun.AutostartEvaluate(proj, issues, root, projectRoot(proj), yoloOn, last, share, "", now)
	if err != nil {
		log.Printf("autostart: %s: %v", proj.Slug, err)
		return nil
	}
	if !st.On {
		return nil
	}
	a := &AutoStartView{
		Prefix: prefix, Paused: st.Paused, PauseReason: st.PauseReason, Words: st.Words, By: st.By, How: st.How,
		Since: st.Since.Format("Mon 2 Jan, 15:04"), Below: st.Below, Cap: st.MaxWorkers, GapMinutes: st.GapMinutes,
		Start: v.Start, Slug: v.Slug, Share: shareLine(proj, share, now),
	}
	if !st.Paused {
		a.Load = autostart.LoadLine(v.CPU(), v.MaxLoad)
	}
	if v.Start {
		a.Verdict = fmt.Sprintf("start %s (memory %d%% < %d%%, %s, %d/%d workers)", v.Slug, v.Memory, v.Below, v.LoadPart(), v.Workers, v.Cap)
	} else {
		a.Verdict = "hold: " + v.Reason
	}
	if last != nil {
		a.Last = fmt.Sprintf("%s %s at %s (memory %d%%)", last.Project, last.Slug, last.At.Format("Mon 15:04"), last.Memory)
	}
	return a
}

// handleAutostart serves POST autostart/pause, autostart/resume and
// autostart/off: the owner's clicks on the Report card.
func (s *Server) handleAutostart(w http.ResponseWriter, r *http.Request, proj *tracker.Project, action string) {
	now := time.Now()
	root := ciRootFor(proj)
	var err error
	switch action {
	case "pause":
		_, err = autostart.Pause(root, proj.Slug, "paused by the owner (viewer)", now)
	case "resume":
		_, err = autostart.Resume(root, proj.Slug)
	case "off":
		var st *autostart.State
		if st, err = autostart.Load(root, proj.Slug); err == nil {
			err = autostart.TurnOff(root, proj.Slug, st, now, "turned off by the owner (viewer)")
		}
	default:
		http.NotFound(w, r)
		return
	}
	a := CrewAction{At: now, Who: crewWho(r), Project: proj.Slug, Action: action + " auto-start", OK: err == nil}
	if err != nil {
		a.Detail = err.Error()
	}
	crewRecord(a)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	st, _ := autostart.Load(root, proj.Slug)
	json.NewEncoder(w).Encode(st)
}
