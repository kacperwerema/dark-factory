package main

import (
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// YOLO mode on the Workshop Board (yolo_view.go reads the state): the switch
// in the company's header (L1) with its turn-on dialog, the hazard band under
// the header on every project screen while it is on, the lot in stripes on
// the map (L0), raised gates and marked crates in the hall (L2), and the
// after-the-fact review on the station (L3). Yellow-black stripes mean YOLO
// only; orange stays the owner's colour. Design:
// issues/Factory/designs/workshop-board/YOLO-*.

// WbYOLO is YOLO in a project page's header: the switch (L1 only), the
// after-the-fact count and the hazard band while it is on.
type WbYOLO struct {
	Prefix string
	On     bool
	// Since and End are clock times ("22:10", "08:00" or "until turned off").
	Since, End string
	Passed     int
	// Queue is how many passes wait for the owner's review.
	Queue  int
	Valves string
	// EndReason is why the last window closed, shown while the queue waits.
	EndReason string
	// Switch draws the switch in the header (the company, L1); Dialog is its
	// turn-on dialog.
	Switch bool
	Dialog *WbYOLODialog
}

// wbYOLO is the header's YOLO from the project's band; nil when YOLO has
// nothing to show and the switch is not drawn.
func wbYOLO(b *YOLOBand, prefix string) *WbYOLO {
	if b == nil {
		return nil
	}
	y := &WbYOLO{Prefix: prefix, On: b.On, Passed: b.Passed, Queue: len(b.Queue), Valves: b.Valves, EndReason: b.EndReason}
	if b.On {
		y.Since, y.End = b.Since.Format("15:04"), b.End
	}
	return y
}

// headerYOLO is the header's YOLO on a project page other than the company:
// the band and the count only, nil when there is nothing to show.
func headerYOLO(b *YOLOBand, prefix string) *WbYOLO {
	if !b.Show() {
		return nil
	}
	return wbYOLO(b, prefix)
}

// WbYOLODialog is the turn-on dialog: the gates that will pass without the
// owner, what still guards, the duration and the valves.
type WbYOLODialog struct {
	Project string
	Gates   []WbYOLOGate
	Guards  []WbFact
	// CI: the project runs local CI, so the CI valve can work.
	CI bool
}

// WbYOLOGate is one gate the dialog lists: what it approves and the step.
type WbYOLOGate struct {
	Name, Step, Note string
}

// yoloDialog lists the project's gates from workflow.yaml and what keeps
// guarding while YOLO is on.
func yoloDialog(p *tracker.Project, wf *tracker.WorkflowConfig) *WbYOLODialog {
	d := &WbYOLODialog{Project: p.Name, CI: wf.CI.Enabled()}
	for _, g := range yolo.Gates(wf) {
		gate := WbYOLOGate{Name: yoloGateName(g), Step: g.From + " → " + g.To}
		if statusKey(g.From) == "human-testing" {
			gate.Note = "the agent runs the guided checks itself"
		}
		d.Gates = append(d.Gates, gate)
	}
	if d.CI {
		d.Guards = append(d.Guards, WbFact{Label: "CI", Value: "local CI on every merge"})
	}
	d.Guards = append(d.Guards,
		WbFact{Label: "Agent tests", Value: "testing, proofs"},
		WbFact{Label: "Agent code review", Value: "findings on the issue"},
		WbFact{Label: "Retro", Value: "a note after every issue"},
		WbFact{Label: "Permission prompts", Value: "workers still ask before risky commands"},
	)
	return d
}

// yoloGateName names a gate by the status it lets in.
func yoloGateName(g yolo.Gate) string {
	name := g.Approves
	if name == "" {
		name = g.To
	}
	return "Approve " + name
}

// WbYOLOReview is the station's "Passed in YOLO" panel (L3): each pass the
// owner has not reviewed, with Agree and Back to <status>.
type WbYOLOReview struct {
	ReviewURL string
	Passes    []WbYOLOPass
}

// WbYOLOPass is one gate the issue passed without the owner.
type WbYOLOPass struct {
	Status, From, Back, By, At string
}

// stationYOLOReview is the issue's unreviewed passes; nil when none waits.
func stationYOLOReview(issue *tracker.Issue, wf *tracker.WorkflowConfig, comments []tracker.Comment, issueURL string) *WbYOLOReview {
	passes := yolo.Unreviewed(issue, comments)
	if len(passes) == 0 {
		return nil
	}
	r := &WbYOLOReview{ReviewURL: issueURL + "/yolo-review"}
	for _, p := range passes {
		at := p.At
		if t, ok := tracker.CommentTime(p.At); ok {
			at = t.Format("Mon 2 Jan, 15:04")
		}
		r.Passes = append(r.Passes, WbYOLOPass{Status: p.Status, From: p.From, Back: yolo.BackTo(wf, p), By: p.By, At: at})
	}
	return r
}

// yoloMarked is the issues that passed a gate in YOLO, by lower-case slug:
// their crates carry the yellow marker to the end of the line.
func yoloMarked(issues []*tracker.Issue) map[string]bool {
	out := map[string]bool{}
	for _, issue := range issues {
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil || len(comments) == 0 {
			continue
		}
		if len(yolo.Passes(issue, comments)) > 0 {
			out[strings.ToLower(issue.Slug)] = true
		}
	}
	return out
}

// WbYOLORow is one row of the company's "To review after YOLO" list.
type WbYOLORow struct {
	Slug, Status, URL string
}

// yoloQueueRows is the queue as the company's rows, max at most.
func yoloQueueRows(b *YOLOBand, prefix string, max int) (rows []WbYOLORow, more int) {
	if b == nil {
		return nil, 0
	}
	for i, p := range b.Queue {
		if i == max {
			return rows, len(b.Queue) - max
		}
		rows = append(rows, WbYOLORow{Slug: p.Slug, Status: p.Status, URL: prefix + "/issue/" + p.Slug + "#yolo"})
	}
	return rows, 0
}

// yoloLotLabel is the lot's tag on the map: "YOLO until 08:00".
func yoloLotLabel(b *YOLOBand) string {
	if b == nil || !b.On {
		return ""
	}
	if strings.HasPrefix(b.End, "until") {
		return "YOLO " + b.End
	}
	return "YOLO until " + b.End
}

// yoloDecision reports whether a decision was recorded in YOLO without the
// owner's words (issue-cli decide records how YOLO).
func yoloDecision(how string) bool { return strings.EqualFold(strings.TrimSpace(how), "yolo") }
