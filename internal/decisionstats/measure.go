// Package decisionstats counts the decision ledger: what is in force, at
// what scope, how strong, which decisions held gates and which briefings
// carried the block. The Stats page's Decisions card and the `decisions`
// health family both read Measure, so their numbers are one computation.
package decisionstats

import (
	"sort"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Scopes is the order the card lists scope kinds in: most specific first,
// as the in-force block orders them.
var Scopes = []string{tracker.ScopeIssue, tracker.ScopeType, tracker.ScopeSystem, tracker.ScopeDepartment, tracker.ScopeProject}

// Briefings is the order the card lists briefing kinds in.
var Briefings = []string{tracker.BriefingStart, tracker.BriefingDispatch, tracker.BriefingReprompt, tracker.BriefingLead, tracker.BriefingDepartmentLead}

// Input is what Measure reads.
type Input struct {
	Index *tracker.DecisionIndex
	// Events are the log's done events (tracker.ReadDecisionLog).
	Events []tracker.DecisionEvent
	// Today decides expiry, as InForce does.
	Today time.Time
	// From and To bound the window holds and briefings are counted in
	// (From inclusive, To exclusive); zero From counts from the start, zero
	// To up to now.
	From, To time.Time
}

// Counts is the ledger measured.
type Counts struct {
	// Total is every D-n line in the project; Logged every done event.
	Total, Logged int
	// InForce is the decisions not replaced, deferred or expired; ByScope
	// splits them by scope kind (issue, type, system, department, project).
	InForce int
	ByScope map[string]int
	// Hard and Soft split InForce.
	Hard, Soft int
	// Provisional is in force with no owner's words on the line yet
	// (tracker.NeedsOwnerWords). A ledger state, not a waiting-on-you
	// count: those are needsYouRows alone.
	Provisional int
	// ChallengesOpen is the refs whose last challenged event has no later
	// resolved one; ChallengesEver every challenged event.
	ChallengesOpen, ChallengesEver int
	// Holds are the held events in the window, by ref (most first);
	// HoldEvents their total.
	Holds      []RefHolds
	HoldEvents int
	// Injected is the injected events in the window by briefing kind;
	// Briefings their total and BriefedIssues the distinct issues briefed.
	Injected      map[string]int
	Briefings     int
	BriefedIssues int
}

// RefHolds is one decision's holds in the window.
type RefHolds struct {
	Ref    string
	Issue  string // the decision's own issue
	Events int
	// Issues is the distinct issues it held; Passed the events YOLO let
	// through, the rest were refusals.
	Issues int
	Passed int
	Rules  []string
}

// Empty reports whether the project has no ledger to show: no D-n line
// and no logged event.
func (c Counts) Empty() bool { return c.Total == 0 && c.Logged == 0 }

// Measure counts the ledger from the index and the log.
func Measure(in Input) Counts {
	c := Counts{ByScope: map[string]int{}, Injected: map[string]int{}, Logged: len(in.Events)}
	if in.Index != nil {
		c.Total = len(in.Index.Entries)
		for _, e := range in.Index.Entries {
			if e.ReplacedBy != nil || e.Deferred || e.ExpiredOn(in.Today) {
				continue
			}
			c.InForce++
			kind, _ := e.ScopeKind()
			c.ByScope[kind]++
			if e.Hard {
				c.Hard++
			} else {
				c.Soft++
			}
			if tracker.NeedsOwnerWords(e.Decision) {
				c.Provisional++
			}
		}
	}

	open := map[string]bool{}
	holds := map[string]*RefHolds{}
	holdIssues := map[string]map[string]bool{}
	briefed := map[string]bool{}
	for _, ev := range in.Events {
		switch ev.Event {
		case tracker.DecisionChallenged:
			c.ChallengesEver++
			open[ev.Ref] = true
		case tracker.DecisionResolved:
			delete(open, ev.Ref)
		}
		if !inWindow(ev, in.From, in.To) {
			continue
		}
		switch ev.Event {
		case tracker.DecisionHeld:
			h := holds[ev.Ref]
			if h == nil {
				h = &RefHolds{Ref: ev.Ref, Issue: tracker.RefIssue(ev.Ref)}
				holds[ev.Ref] = h
				holdIssues[ev.Ref] = map[string]bool{}
			}
			h.Events++
			c.HoldEvents++
			if ev.Passed {
				h.Passed++
			}
			if ev.Issue != "" && !holdIssues[ev.Ref][ev.Issue] {
				holdIssues[ev.Ref][ev.Issue] = true
				h.Issues++
			}
			if ev.Rule != "" && !contains(h.Rules, ev.Rule) {
				h.Rules = append(h.Rules, ev.Rule)
			}
		case tracker.DecisionInjected:
			c.Injected[ev.Briefing]++
			c.Briefings++
			if ev.Issue != "" {
				briefed[ev.Issue] = true
			}
		}
	}
	c.ChallengesOpen = len(open)
	c.BriefedIssues = len(briefed)
	for _, h := range holds {
		c.Holds = append(c.Holds, *h)
	}
	sort.Slice(c.Holds, func(i, j int) bool {
		if c.Holds[i].Events != c.Holds[j].Events {
			return c.Holds[i].Events > c.Holds[j].Events
		}
		return c.Holds[i].Ref < c.Holds[j].Ref
	})
	return c
}

func inWindow(ev tracker.DecisionEvent, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	t := ev.EventTime()
	if t.IsZero() {
		return false
	}
	return (from.IsZero() || !t.Before(from)) && (to.IsZero() || t.Before(to))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Load reads a project's ledger: its issues' D-n lines and the log's done
// events. The project's own closed-issue rule decides citations, as the
// Decisions tab does.
func Load(proj *tracker.Project) (*tracker.DecisionIndex, []tracker.DecisionEvent, error) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return nil, nil, err
	}
	evs, err := tracker.ReadDecisionLog(proj.IssueDir)
	if err != nil {
		return nil, nil, err
	}
	return tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(proj)), evs, nil
}
