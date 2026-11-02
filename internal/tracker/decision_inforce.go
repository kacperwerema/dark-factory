package tracker

import (
	"sort"
	"strings"
	"time"
)

// InForceOptions are what InForce needs besides the index.
type InForceOptions struct {
	// Department names an issue's department ("" is Product);
	// Project.DepartmentOf. Nil: department scopes match nothing but
	// department:product.
	Department func(*Issue) string
	// Today drops a decision whose expires: date is before it; zero keeps
	// every decision.
	Today time.Time
}

// InForceEntry is a decision in force for an issue, and why it reaches it:
// "own", "parent <slug>", "cites", or its scope ("project", "system:UI"…).
type InForceEntry struct {
	Entry *DecisionEntry
	Why   string
}

// scopeRank orders the in-force list: most specific scope first.
var scopeRank = map[string]int{ScopeIssue: 0, ScopeType: 1, ScopeSystem: 2, ScopeDepartment: 3, ScopeProject: 4}

// InForce returns the decisions in force for issue at status: not
// replaced, not deferred, not expired, at: empty or naming the status, and
// a scope that reaches the issue. Issue scope reaches the decision's own
// issue, every issue below it in the parent: chain and every issue citing
// it. Hard decisions come first, then the most specific scope, then newest.
func InForce(x *DecisionIndex, issue *Issue, status string, o InForceOptions) []InForceEntry {
	if x == nil || issue == nil {
		return nil
	}
	ancestors := map[string]bool{}
	for p, seen := ParentSlug(issue), map[string]bool{issue.Slug: true}; p != "" && !seen[p]; {
		seen[p] = true
		ancestors[p] = true
		parent := x.issues[p]
		if parent == nil {
			break
		}
		p = ParentSlug(parent)
	}
	cited := map[string]bool{}
	for _, ref := range x.cites[issue.Slug] {
		cited[ref] = true
	}
	dept := ""
	if o.Department != nil {
		dept = o.Department(issue)
	}
	deptKey := DepartmentKey(dept)
	if dept == "" {
		deptKey = "product"
	}

	var out []InForceEntry
	for _, e := range x.Entries {
		if e.ReplacedBy != nil || e.Deferred || e.ExpiredOn(o.Today) || !e.AtStatus(status) {
			continue
		}
		why := ""
		kind, name := e.ScopeKind()
		switch kind {
		case ScopeIssue:
			switch {
			case e.Slug == issue.Slug:
				why = "own"
			case ancestors[e.Slug]:
				why = "parent " + e.Slug
			case cited[e.Ref]:
				why = "cites"
			}
		case ScopeProject:
			why = ScopeProject
		case ScopeSystem:
			if issue.System != "" && strings.EqualFold(issue.System, name) {
				why = e.ScopeOrDefault()
			}
		case ScopeDepartment:
			if strings.EqualFold(DepartmentKey(name), deptKey) {
				why = e.ScopeOrDefault()
			}
		case ScopeType:
			if issue.Type != "" && strings.EqualFold(issue.Type, name) {
				why = e.ScopeOrDefault()
			}
		}
		if why != "" {
			out = append(out, InForceEntry{Entry: e, Why: why})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Entry, out[j].Entry
		if a.Hard != b.Hard {
			return a.Hard
		}
		ka, _ := a.ScopeKind()
		kb, _ := b.ScopeKind()
		return scopeRank[ka] < scopeRank[kb] // Entries are newest first already
	})
	return out
}
