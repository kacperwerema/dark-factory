package tracker

import (
	"fmt"
	"strconv"
	"strings"
)

const approvalPrefix = "approval: "

// ApprovalWords is the owner's words saved with an approval: what they said,
// how it reached the issue (viewer, in the lead chat) and the statuses it
// approved. Statuses is empty for comments written before the → suffix.
type ApprovalWords struct {
	Words    string
	How      string
	Date     string
	Statuses []string
}

// ApprovalComment is the comment text that saves the owner's words with an
// approval, shared by the viewer's approve button and issue-cli approve:
//
//	approval: "<words>" (<how>, <date>) → <status>[, <status>]
func ApprovalComment(words, how, date string, statuses ...string) string {
	s := fmt.Sprintf("%s%q (%s, %s)", approvalPrefix, words, how, date)
	if len(statuses) > 0 {
		s += " → " + strings.Join(statuses, ", ")
	}
	return s
}

// DispatchComment is the comment issue-cli dispatch leaves with the human's
// words that started (or re-prompted) the issue's worker, the same shape as
// an approval:
//
//	dispatch: "<words>" (<how>, <date>)
func DispatchComment(words, how, date string) string {
	return fmt.Sprintf("dispatch: %q (%s, %s)", words, how, date)
}

// ParseApprovalComment reads a comment written by ApprovalComment. It also
// takes the older form without "→ <status>" and a trailing
// " — recorded by <name>"; ok is false for any other comment.
func ParseApprovalComment(text string) (ApprovalWords, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(text), approvalPrefix)
	if !found {
		return ApprovalWords{}, false
	}
	quoted, err := strconv.QuotedPrefix(rest)
	if err != nil {
		return ApprovalWords{}, false
	}
	words, err := strconv.Unquote(quoted)
	if err != nil {
		return ApprovalWords{}, false
	}
	a := ApprovalWords{Words: words}
	rest = strings.TrimSpace(rest[len(quoted):])
	rest, _, _ = strings.Cut(rest, " — recorded by ")
	if inner, after, ok := strings.Cut(strings.TrimPrefix(rest, "("), ")"); ok && strings.HasPrefix(rest, "(") {
		if i := strings.LastIndex(inner, ", "); i >= 0 {
			a.How, a.Date = inner[:i], inner[i+2:]
		} else {
			a.How = inner
		}
		rest = strings.TrimSpace(after)
	}
	if to, ok := strings.CutPrefix(rest, "→"); ok {
		for _, s := range strings.Split(to, ",") {
			if s = strings.TrimSpace(s); s != "" {
				a.Statuses = append(a.Statuses, s)
			}
		}
	}
	return a, true
}

// Approves reports whether these words were for an approval of status. Words
// that name no status (the older form) match any.
func (a ApprovalWords) Approves(status string) bool {
	if len(a.Statuses) == 0 {
		return true
	}
	for _, s := range a.Statuses {
		if strings.EqualFold(s, status) {
			return true
		}
	}
	return false
}

// YOLOHow is the how of an approval YOLO mode wrote when agent moved the
// issue on from status from: the gate passed without the owner.
//
//	yolo · <agent> · from <status>
func YOLOHow(agent, from string) string {
	how := "yolo"
	if agent != "" {
		how += " · " + agent
	}
	if from != "" {
		how += " · from " + from
	}
	return how
}

// YOLOFrom splits a YOLO how into the agent and the status it left.
func (a ApprovalWords) YOLOFrom() (agent, from string) {
	for _, part := range strings.Split(a.How, " · ")[1:] {
		if f, ok := strings.CutPrefix(part, "from "); ok {
			from = f
		} else {
			agent = part
		}
	}
	return agent, from
}

// YOLO reports whether the approval passed in YOLO mode, not by the owner.
func (a ApprovalWords) YOLO() bool {
	return a.How == "yolo" || strings.HasPrefix(a.How, "yolo · ")
}

const yoloReviewPrefix = "yolo-review: "

// YOLOReview is the owner's after-the-fact review of a gate passed in YOLO:
// Verdict "agreed" or "back to <status>".
type YOLOReview struct {
	Verdict string
	Words   string
	How     string
	Date    string
	Status  string
}

// YOLOReviewComment is the comment the review writes:
//
//	yolo-review: <verdict> "<words>" (<how>, <date>) → <status>
func YOLOReviewComment(verdict, words, how, date, status string) string {
	return fmt.Sprintf("%s%s %q (%s, %s) → %s", yoloReviewPrefix, verdict, words, how, date, status)
}

// ParseYOLOReviewComment reads a comment written by YOLOReviewComment.
func ParseYOLOReviewComment(text string) (YOLOReview, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(text), yoloReviewPrefix)
	if !found {
		return YOLOReview{}, false
	}
	i := strings.Index(rest, ` "`)
	if i < 0 {
		return YOLOReview{}, false
	}
	r := YOLOReview{Verdict: strings.TrimSpace(rest[:i])}
	a, ok := ParseApprovalComment(approvalPrefix + rest[i+1:])
	if !ok || len(a.Statuses) != 1 {
		return YOLOReview{}, false
	}
	r.Words, r.How, r.Date, r.Status = a.Words, a.How, a.Date, a.Statuses[0]
	return r, true
}
