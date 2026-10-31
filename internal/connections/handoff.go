package connections

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Handoffs: one box asks another for work as an issue (issue-cli handoff).
// A handoff is an ordinary issue of the receiving box with requested_by:,
// handoff_route: (the route taken, box keys joined by " → ") and, when the
// asker has one, handoff_for: (the asker's issue it serves). Its state is
// derived from its status and its handoff: comments; nothing is stored
// elsewhere. See docs/departments.md#handoffs.

// Frontmatter fields a handoff carries besides requested_by:.
const (
	HandoffRouteField = "handoff_route"
	HandoffForField   = "handoff_for"
)

// HandoffPrefix starts every comment the handoff commands write.
const HandoffPrefix = "handoff:"

// Handoff states, in the order a handoff moves through them.
const (
	StateWaiting  = "waiting"
	StateOpen     = "open"
	StateDone     = "done"
	StateDeclined = "declined"
	StateParked   = "parked"
)

// RouteSep joins a route's boxes.
const RouteSep = " → "

// Handoff is one handoff with its state.
type Handoff struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Status string `json:"status"`
	From   string `json:"from"`
	To     string `json:"to"`
	// Via is the box that forwards it ("" for a straight route).
	Via string `json:"via,omitempty"`
	// For is the asker's issue it serves; "" when none.
	For   string `json:"for,omitempty"`
	State string `json:"state"`
	// Word is the state as it reads on a line ("waiting on HQ to forward").
	Word string `json:"word"`
	// Reason is the decline's reason.
	Reason string `json:"reason,omitempty"`
}

// Open reports whether the handoff is still being worked or waited on.
func (h Handoff) Open() bool { return h.State == StateWaiting || h.State == StateOpen }

// PlanRoute is the route a handoff from → to takes: the boxes in order,
// with the relay box in the middle when there is one. It refuses a target
// that is no box of this project, the asker's own box, and a pair off the
// declared routes (CheckRequest). With no declared requests:, a
// department's handoff to Product goes via HQ (Settled rule "Engine
// requests go via the CEO").
func PlanRoute(p *tracker.Project, from, toName string) ([]string, error) {
	to := ResolveBox(p, toName)
	if to == "" {
		hint := ""
		if strings.Contains(toName, "/") {
			hint = " Handoffs stay inside one project."
		}
		return nil, fmt.Errorf("%q is no box of project %s.%s Hand off to one of: %s", toName, p.Name, hint, strings.Join(Boxes(p), ", "))
	}
	if from == "" {
		from = HQ
	}
	if from == to {
		return nil, fmt.Errorf("%s can't hand off to itself: file it with issue-cli create", BoxName(p, from))
	}
	d, err := CheckRequest(p, from, to)
	if err != nil {
		return nil, err
	}
	via := ""
	switch {
	case d != nil:
		via = d.Via
	case !DeclaresKind(p, Request) && from != HQ && to == Product:
		via = HQ
	}
	if via != "" {
		return []string{from, via, to}, nil
	}
	return []string{from, to}, nil
}

// ParseRoute splits a handoff_route: field into box keys.
func ParseRoute(s string) []string {
	var out []string
	for _, part := range strings.Split(s, "→") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, strings.ToLower(part))
		}
	}
	return out
}

// RouteNames is a route as box names, "Marketing → HQ → Product".
func RouteNames(p *tracker.Project, route []string) string {
	names := make([]string, len(route))
	for i, b := range route {
		names[i] = BoxName(p, b)
	}
	return strings.Join(names, RouteSep)
}

// IsHandoff reports whether issue was filed by issue-cli handoff.
func IsHandoff(issue *tracker.Issue) bool {
	return len(ParseRoute(issue.Field(HandoffRouteField))) >= 2
}

// handoffVia is the relay box of a handoff's recorded route, "" for none.
func handoffVia(issue *tracker.Issue) string {
	r := ParseRoute(issue.Field(HandoffRouteField))
	if len(r) == 3 {
		return r[1]
	}
	return ""
}

// forwarded reports whether the relay lead, or the owner, wrote a
// "handoff: forwarded" comment.
func forwarded(comments []tracker.Comment, lead string) bool {
	for _, c := range comments {
		if !strings.HasPrefix(strings.TrimSpace(c.Text), HandoffPrefix+" forwarded") {
			continue
		}
		if a := tracker.CommentAuthor(c); a == lead || a == tracker.OwnerAuthor {
			return true
		}
	}
	return false
}

// declineReason is the reason of the handoff's decline comment; ok false
// when it was not declined.
func declineReason(comments []tracker.Comment) (string, bool) {
	for i := len(comments) - 1; i >= 0; i-- {
		t := strings.TrimSpace(comments[i].Text)
		if !strings.HasPrefix(t, HandoffPrefix+" declined") {
			continue
		}
		if _, r, ok := strings.Cut(t, ": "); ok {
			if _, r2, ok := strings.Cut(r, ": "); ok {
				return strings.TrimSpace(r2), true
			}
			return strings.TrimSpace(r), true
		}
		return "", true
	}
	return "", false
}

// HandoffWaiting is the box that still has to forward the handoff: its
// route's relay box while that box's lead hasn't forwarded it (a
// "handoff: forwarded" comment, or the approval: / dispatch: the declared
// routes count). "" once forwarded, for a straight route, or when closed.
func HandoffWaiting(p *tracker.Project, issue *tracker.Issue, comments []tracker.Comment) string {
	via := handoffVia(issue)
	if via == "" || closed(issue) {
		return ""
	}
	if relayedBy(comments, RelayLead(p, via)) {
		return ""
	}
	return via
}

// done reports whether a closed issue finished rather than was parked.
func done(issue *tracker.Issue) bool {
	s := strings.ToLower(strings.TrimSpace(issue.Status))
	return s == "done" || (s != "parked" && strings.TrimSpace(issue.DoneAt) != "")
}

// HandoffOf is issue's handoff with its state; ok false when it isn't one.
func HandoffOf(p *tracker.Project, issue *tracker.Issue, comments []tracker.Comment) (Handoff, bool) {
	route := ParseRoute(issue.Field(HandoffRouteField))
	if len(route) < 2 {
		return Handoff{}, false
	}
	h := Handoff{
		Slug: issue.Slug, Title: issue.Title, Status: issue.Status,
		From: route[0], To: BoxOf(p, issue), Via: handoffVia(issue),
		For: strings.TrimSpace(issue.Field(HandoffForField)),
	}
	switch {
	case !closed(issue):
		if via := HandoffWaiting(p, issue, comments); via != "" {
			h.State, h.Word = StateWaiting, "waiting on "+BoxName(p, via)+" to forward"
		} else {
			h.State, h.Word = StateOpen, "at "+issue.Status
		}
	case done(issue):
		h.State, h.Word = StateDone, "done"
	default:
		if r, ok := declineReason(comments); ok {
			h.State, h.Reason, h.Word = StateDeclined, r, "declined"
			if r != "" {
				h.Word += ": " + r
			}
		} else {
			h.State, h.Word = StateParked, "parked"
		}
	}
	return h, true
}

// HandoffFilter picks handoffs: from or to a box, or waiting on one.
type HandoffFilter struct{ From, To, WaitingOn string }

// ListHandoffs are the project's handoffs matching f: the open ones and
// those closed within ClosedWindow, open first, newest slug order within.
func ListHandoffs(p *tracker.Project, issues []*tracker.Issue, comments func(*tracker.Issue) []tracker.Comment, f HandoffFilter, now time.Time) []Handoff {
	var out []Handoff
	for _, issue := range issues {
		if !IsHandoff(issue) {
			continue
		}
		if closed(issue) && !recent(issue, now) {
			continue
		}
		var cs []tracker.Comment
		if comments != nil {
			cs = comments(issue)
		}
		h, _ := HandoffOf(p, issue, cs)
		if f.From != "" && h.From != f.From {
			continue
		}
		if f.To != "" && h.To != f.To {
			continue
		}
		if f.WaitingOn != "" && (h.State != StateWaiting || h.Via != f.WaitingOn) {
			continue
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Open() != out[j].Open() {
			return out[i].Open()
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// AskerLead is the session that leads the box that asked: the CEO for hq,
// the department's lead otherwise; "" for Product (no lead of its own
// besides the CEO, who is then named).
func AskerLead(p *tracker.Project, box string) string {
	if box == HQ || box == Product {
		return tracker.LeadSessionName(p)
	}
	if d := p.DepartmentByKey(box); d != nil {
		return tracker.DepartmentLeadName(p, d.Name)
	}
	return ""
}
