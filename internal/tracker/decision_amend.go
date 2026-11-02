package tracker

import (
	"errors"
	"strings"
	"time"
)

// Amending a decision changes only the trailing attribute group of its
// line; its date, how, text, quote, also-considered and replaces stay byte
// for byte, so its ref and every citation stay valid. Changing the text or
// the quote is a new decision (decide --replaces).

// ErrNothingChanged: the amend leaves the group as it was.
var ErrNothingChanged = errors.New("nothing changed")

// AmendDecisionAs rewrites decision id's attribute group to what fn returns
// for the current line, in one locked write, logged as an amended event
// with the old and new group. fn sees the line as it is under the lock and
// may refuse with an error; nothing is written then. It returns the line
// before and after.
func AmendDecisionAs(filePath, slug, id string, fn func(cur Decision) (DecisionAttrs, error), now time.Time, w DecisionWrite) (before, after Decision, err error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	var line string
	err = writeDecision(filePath, w, now, func(body string) (decisionChange, error) {
		lines, i, d, err := findDecisionLine(body, slug, id)
		if err != nil {
			return decisionChange{}, err
		}
		before = d
		next, err := fn(d)
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
		action := "amended " + orBraces(old) + " → " + orBraces(nw)
		if !before.Hard && next.Hard {
			action = "made hard " + nw
		}
		c := decisionChange{Body: strings.Join(lines, "\n"), Events: []DecisionEvent{{Event: DecisionAmended, Ref: d.Ref, Old: old, New: nw}}}
		if before.Hard || next.Hard { // an owner act: a relay leaves its decision: comment
			c.RelayRef, c.RelayAction = d.Ref, action
		}
		return c, nil
	})
	if err != nil {
		return before, Decision{}, err
	}
	after, _ = parseDecisionLine(slug, line)
	return before, after, nil
}

func orBraces(g string) string {
	if g == "" {
		return "{}"
	}
	return g
}
