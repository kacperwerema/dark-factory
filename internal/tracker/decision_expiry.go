package tracker

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Expiry: a decision with expires: <date> is in force through that day
// and expired from the next (DecisionAttrs.ExpiredOn). Expired is a state
// that depends on the day, so it is computed by the readers that take one
// (StateOn), never stored by BuildDecisionIndex. An expired hard decision
// waits on the owner (ExpiredHard, the Needs you row) until they renew it
// (a new expires:, RenewDecisionAs) or let it go (ended: <day> in its
// group, LetGoDecisionAs). Both change only the attribute group.

// DecisionExpired is the state of a decision past its expires: day (not
// replaced).
const DecisionExpired = "expired"

// Decision log events for expiry: a renewed decision got a new expires:;
// expired is the owner closing out an expired one (let go).
const (
	DecisionRenewedEvent = "renewed"
	DecisionExpiredEvent = "expired"
)

// StateOn is the entry's state on day today: replaced and stale as
// indexed, else expired when its expires: day has passed, else as indexed.
func (e *DecisionEntry) StateOn(today time.Time) string {
	if e.ReplacedBy == nil && e.ExpiredOn(today) {
		return DecisionExpired
	}
	return e.State
}

// WaitsOnOwner reports whether the entry is an expired hard decision
// nobody renewed or let go: a Needs you row.
func (e *DecisionEntry) WaitsOnOwner(today time.Time) bool {
	return e.Hard && e.Ended == "" && e.StateOn(today) == DecisionExpired
}

// Refusals of renew and let go.
var (
	ErrNoExpiry      = errors.New("it has no expires: date")
	ErrDecisionEnded = errors.New("it was let go")
	ErrNotExpired    = errors.New("it has not expired")
)

// RenewDecisionAs sets decision id's expires: to date (YYYY-MM-DD) in one
// locked write, logged as a renewed event. It refuses a decision without
// expires:, one let go, and the same date. check runs on the line as it is
// under the lock and may refuse.
func RenewDecisionAs(filePath, slug, id, date string, check func(cur Decision) error, now time.Time, w DecisionWrite) (before, after Decision, err error) {
	return changeAttrs(filePath, slug, id, now, w, func(cur Decision) (DecisionAttrs, string, string, error) {
		switch {
		case cur.Expires == "":
			return cur.DecisionAttrs, "", "", ErrNoExpiry
		case cur.Ended != "":
			return cur.DecisionAttrs, "", "", ErrDecisionEnded
		}
		if check != nil {
			if err := check(cur); err != nil {
				return cur.DecisionAttrs, "", "", err
			}
		}
		next := cur.DecisionAttrs
		next.Expires = date
		return next, DecisionRenewedEvent, "renewed until " + date, nil
	})
}

// LetGoDecisionAs closes out an expired decision: ended: <now's day> in
// its group, logged as an expired event. It refuses one not expired on
// now's day and one already let go.
func LetGoDecisionAs(filePath, slug, id string, check func(cur Decision) error, now time.Time, w DecisionWrite) (before, after Decision, err error) {
	return changeAttrs(filePath, slug, id, now, w, func(cur Decision) (DecisionAttrs, string, string, error) {
		switch {
		case cur.Ended != "":
			return cur.DecisionAttrs, "", "", ErrDecisionEnded
		case !cur.ExpiredOn(now):
			return cur.DecisionAttrs, "", "", ErrNotExpired
		}
		if check != nil {
			if err := check(cur); err != nil {
				return cur.DecisionAttrs, "", "", err
			}
		}
		next := cur.DecisionAttrs
		next.Ended = now.Format(decisionDateLayout)
		return next, DecisionExpiredEvent, "let go (expired after " + cur.Expires + ")", nil
	})
}

// changeAttrs rewrites one decision's group to what fn returns, logged
// as fn's event; a relayed act or the owner's words post the decision:
// comment with fn's action.
func changeAttrs(filePath, slug, id string, now time.Time, w DecisionWrite, fn func(cur Decision) (DecisionAttrs, string, string, error)) (before, after Decision, err error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	var line string
	err = writeDecision(filePath, w, now, func(body string) (decisionChange, error) {
		lines, i, d, err := findDecisionLine(body, slug, id)
		if err != nil {
			return decisionChange{}, err
		}
		before = d
		next, event, action, err := fn(d)
		if err != nil {
			return decisionChange{}, err
		}
		if next.Scope == "" {
			next.Scope = ScopeIssue
		}
		old, nw := RenderAttrs(d.DecisionAttrs), RenderAttrs(next)
		if old == nw {
			return decisionChange{}, ErrNothingChanged
		}
		d.DecisionAttrs = next
		line = spliceAttrs(d)
		lines[i] = lineIndent(lines[i]) + line
		return decisionChange{Body: strings.Join(lines, "\n"), Events: []DecisionEvent{{Event: event, Ref: d.Ref, Old: old, New: nw}},
			RelayRef: d.Ref, RelayAction: action}, nil
	})
	if err != nil {
		return before, Decision{}, err
	}
	after, _ = parseDecisionLine(slug, line)
	return before, after, nil
}

// ExpiredDecision is an expired hard decision waiting on the owner.
type ExpiredDecision struct {
	Decision
	Issue *Issue
	// Since is the first day it is out of force (the day after expires:).
	Since time.Time
}

// ExpiredHard lists the project's expired hard decisions that nobody
// renewed or let go, on day today, oldest first. It reads every issue file
// but parses only those with a D-n line that has an expires: or replaces
// another (a replacer takes the old one out), so a Needs you render does
// not load the whole board.
func ExpiredHard(issueDir string, today time.Time) ([]ExpiredDecision, error) {
	issues, err := LoadIssuesWhere(issueDir, func(data []byte) bool {
		return bytes.Contains(data, []byte("**D-")) && (bytes.Contains(data, []byte("expires: ")) || bytes.Contains(data, []byte("(replaces ")))
	})
	if err != nil {
		return nil, err
	}
	var out []ExpiredDecision
	for _, e := range BuildDecisionIndex(issues, nil).Entries {
		if !e.WaitsOnOwner(today) {
			continue
		}
		exp, _ := e.ExpiresOn()
		out = append(out, ExpiredDecision{Decision: e.Decision, Issue: e.Issue, Since: exp.AddDate(0, 0, 1)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return out[i].Ref < out[j].Ref
	})
	return out, nil
}

// ExpiryRefusal words a renew or let-go refusal for ref.
func ExpiryRefusal(ref string, err error) string {
	switch {
	case errors.Is(err, ErrNoExpiry):
		return fmt.Sprintf("%s has no expires: date: nothing to renew (add one with --amend %s --expires YYYY-MM-DD)", ref, idOf(ref))
	case errors.Is(err, ErrDecisionEnded):
		return fmt.Sprintf("%s was let go: that is final; record a new decision that replaces it (--replaces %s)", ref, ref)
	case errors.Is(err, ErrNotExpired):
		return fmt.Sprintf("%s has not expired: let go closes out an expired decision; to end it early, --amend %s --expires <today> or replace it", ref, idOf(ref))
	case errors.Is(err, ErrNothingChanged):
		return fmt.Sprintf("%s already expires on that day", ref)
	}
	return err.Error()
}

func idOf(ref string) string {
	if _, id, ok := strings.Cut(ref, "#"); ok {
		return id
	}
	return ref
}
