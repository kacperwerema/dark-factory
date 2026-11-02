package tracker

import (
	"sort"
	"strings"
	"time"
)

// Holds is what one real transition attempt's ledger holds were: the
// issue, the transition, whether YOLO passed them, and who ran it.
type Holds struct {
	Issue    string
	From, To string
	Passed   bool
	Actor    DecisionActor
	Holds    []DecisionHold
}

// RecordHolds appends one held event (op done) per hold: a real transition
// a ledger rule refused, or YOLO passed. Nothing for no holds or an empty
// issueDir. A dry run never calls it.
func RecordHolds(issueDir string, h Holds, now time.Time) error {
	if issueDir == "" || len(h.Holds) == 0 {
		return nil
	}
	id, at := newWriteID(), now.Format(time.RFC3339)
	evs := make([]DecisionEvent, 0, len(h.Holds))
	for _, x := range h.Holds {
		evs = append(evs, DecisionEvent{Write: id, Op: DecisionOpDone, Event: DecisionHeld, Ref: x.Ref, At: at,
			By: h.Actor.Name, Role: h.Actor.Role, Issue: h.Issue, Rule: x.Rule, From: h.From, To: h.To, Passed: h.Passed})
	}
	return AppendDecisionEvents(issueDir, evs)
}

// DecisionLogFilter narrows the log for issue-cli decisions log.
type DecisionLogFilter struct {
	// Ref keeps the events about this decision, and injected events that
	// carried it.
	Ref string
	// Issue keeps events about this issue: its own decisions' events, and
	// briefings, holds and challenges naming it.
	Issue string
	// Since keeps events at or after it; zero keeps all.
	Since time.Time
}

// EventTime is an event's time; zero when its at doesn't parse.
func (ev DecisionEvent) EventTime() time.Time {
	t, _ := time.Parse(time.RFC3339, ev.At)
	return t
}

// Mentions reports whether the event is about ref: its ref, or one of the
// refs an injected event carried.
func (ev DecisionEvent) Mentions(ref string) bool {
	if ev.Ref == ref {
		return true
	}
	for _, r := range ev.Refs {
		if r == ref {
			return true
		}
	}
	return false
}

// RefIssue is the issue slug of a ref (<slug>#D-n).
func RefIssue(ref string) string {
	slug, _, _ := strings.Cut(ref, "#D-")
	return slug
}

// FilterDecisionLog returns the events f keeps, in time order (the log's
// order where times tie or don't parse).
func FilterDecisionLog(evs []DecisionEvent, f DecisionLogFilter) []DecisionEvent {
	var out []DecisionEvent
	for _, ev := range evs {
		if f.Ref != "" && !ev.Mentions(f.Ref) {
			continue
		}
		if f.Issue != "" && ev.Issue != f.Issue && RefIssue(ev.Ref) != f.Issue && RefIssue(ev.ByRef) != f.Issue {
			continue
		}
		if !f.Since.IsZero() {
			if t := ev.EventTime(); t.IsZero() || t.Before(f.Since) {
				continue
			}
		}
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].EventTime(), out[j].EventTime()
		if ti.IsZero() || tj.IsZero() {
			return false
		}
		return ti.Before(tj)
	})
	return out
}
