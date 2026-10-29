package yolo

import (
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Pass is one gate an issue passed in YOLO without the owner.
type Pass struct {
	Slug   string `json:"slug"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"`
	// From is the status the issue left when the gate passed.
	From string `json:"from,omitempty"`
	By     string `json:"by,omitempty"`
	At     string `json:"at"`
	// Comment is the approval comment's id on the issue.
	Comment int `json:"comment"`
	// Review is the owner's verdict ("agreed", "back to <status>"); empty
	// while the pass waits in the queue.
	Review string `json:"review,omitempty"`
}

// Passes is every YOLO pass on one issue, oldest first, each with the
// review that settled it, if any: a yolo-review: for the same status after
// the pass.
func Passes(issue *tracker.Issue, comments []tracker.Comment) []Pass {
	var out []Pass
	for _, c := range comments {
		if a, ok := tracker.ParseApprovalComment(c.Text); ok && a.YOLO() {
			by, from := a.YOLOFrom()
			for _, s := range a.Statuses {
				out = append(out, Pass{Slug: issue.Slug, Title: issue.Title, Status: s, From: from, By: by, At: c.Date, Comment: c.ID})
			}
			continue
		}
		if r, ok := tracker.ParseYOLOReviewComment(c.Text); ok {
			for i := range out {
				if out[i].Review == "" && strings.EqualFold(out[i].Status, r.Status) {
					out[i].Review = r.Verdict
				}
			}
		}
	}
	return out
}

// Queue is the passes across issues still waiting for the owner's review.
func Queue(issues []*tracker.Issue) []Pass {
	var out []Pass
	for _, issue := range issues {
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil || len(comments) == 0 {
			continue
		}
		for _, p := range Passes(issue, comments) {
			if p.Review == "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// Unreviewed is the issue's passes still waiting for review.
func Unreviewed(issue *tracker.Issue, comments []tracker.Comment) []Pass {
	var out []Pass
	for _, p := range Passes(issue, comments) {
		if p.Review == "" {
			out = append(out, p)
		}
	}
	return out
}

// BackTo is the status an issue goes back to when the owner undoes p: the
// status it left, else the first forward step whose gate approves p.Status.
func BackTo(wf *tracker.WorkflowConfig, p Pass) string {
	if p.From != "" {
		return p.From
	}
	status := p.Status
	order := wf.GetStatusOrder()
	for i := 0; i+1 < len(order); i++ {
		for _, to := range order[i+1:] {
			if strings.EqualFold(wf.RequiredHumanApproval(order[i], to), status) {
				return order[i]
			}
		}
	}
	return ""
}

// Gate is one gate YOLO lets through, for the turn-on dialog.
type Gate struct {
	From, To, Approves string
}

// Gates is the project's gates YOLO passes: each forward step between two
// neighbouring statuses on the base path that needs a human approval.
func Gates(wf *tracker.WorkflowConfig) []Gate {
	var out []Gate
	order := wf.GetStatusOrder()
	for i := 0; i+1 < len(order); i++ {
		to := order[i+1]
		if wf.IsSideStatus(to) {
			continue
		}
		if req := wf.RequiredHumanApproval(order[i], to); req != "" {
			out = append(out, Gate{From: order[i], To: to, Approves: req})
		}
	}
	return out
}
