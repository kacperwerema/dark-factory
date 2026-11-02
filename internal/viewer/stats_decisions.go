package viewer

import (
	"fmt"

	"github.com/kacperwerema/dark-factory/internal/chart"
	"github.com/kacperwerema/dark-factory/internal/health"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/decisionstats"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Stats page's Decisions card (wb/decisions-card): the decision ledger
// counted by decisionstats.Measure, the same computation the `decisions`
// health family snapshots. Holds and briefings cover the page's range.

// WbStatsDecisions is the Decisions card.
type WbStatsDecisions struct {
	// Empty is set when the project has no decision and no logged event:
	// the card is then only this (no zeros dressed as data).
	Empty *WbEmpty
	Sub   string
	Tiles []WbStatsDecisionTile
	// Scopes are the in-force decisions by scope kind, most specific first;
	// only kinds that have any.
	Scopes    []WbStatsDecisionChip
	Holds     WbTable
	Briefings WbTable
	// Err says what could not be read; the card still renders.
	Err string
}

// WbStatsDecisionTile is one number on the card.
type WbStatsDecisionTile struct {
	Key   string
	Label string
	Value string
	Note  string
	URL   string
}

// WbStatsDecisionChip is one scope kind and its count.
type WbStatsDecisionChip struct {
	Key   string
	Label string
	N     int
}

// decisionsCardIn is what the card reads.
type decisionsCardIn struct {
	Slug   string
	Prefix string
	Counts decisionstats.Counts
	// RangeLabel names the window holds and briefings cover.
	RangeLabel string
}

// loadDecisionsCard measures proj's ledger for the card over [from, to).
func loadDecisionsCard(proj *tracker.Project, prefix, rangeLabel string, from, to, now time.Time) WbStatsDecisions {
	x, evs, err := decisionstats.Load(proj)
	if err != nil {
		return WbStatsDecisions{Err: "Could not read the decisions: " + err.Error()}
	}
	c := decisionstats.Measure(decisionstats.Input{Index: x, Events: evs, Today: now, From: from, To: to})
	return buildDecisionsCard(decisionsCardIn{Slug: proj.Slug, Prefix: prefix, Counts: c, RangeLabel: rangeLabel})
}

var scopeLabels = map[string]string{
	tracker.ScopeIssue: "issue", tracker.ScopeType: "type", tracker.ScopeSystem: "system",
	tracker.ScopeDepartment: "department", tracker.ScopeProject: "project",
}

var briefingLabels = map[string]string{
	tracker.BriefingStart: "issue-cli start", tracker.BriefingDispatch: "worker dispatch", tracker.BriefingReprompt: "worker re-prompt",
	tracker.BriefingLead: "CEO briefing", tracker.BriefingDepartmentLead: "department lead briefing",
}

func buildDecisionsCard(in decisionsCardIn) WbStatsDecisions {
	c := in.Counts
	decisionsURL := in.Prefix + "/decisions"
	if c.Empty() {
		return WbStatsDecisions{Empty: &WbEmpty{
			Text: "No decisions recorded yet: nothing is in force, held or briefed. A decision on an issue starts the ledger.",
			Cmd:  fmt.Sprintf(`issue-cli --project %s decide <slug> --text "..."`, in.Slug),
		}}
	}
	card := WbStatsDecisions{Sub: fmt.Sprintf("%s recorded · in force now; holds and briefings %s", plural(c.Total, "decision", "decisions"), strings.ToLower(in.RangeLabel))}
	challenges := WbStatsDecisionTile{Key: "challenges", Label: "Open challenges", Value: fmt.Sprint(c.ChallengesOpen)}
	if c.ChallengesEver == 0 {
		challenges.Value, challenges.Note = "none raised", "no decision has been challenged"
	} else {
		challenges.Note = plural(c.ChallengesEver, "challenge", "challenges") + " raised in all"
	}
	card.Tiles = []WbStatsDecisionTile{
		{Key: "in_force", Label: "In force", Value: fmt.Sprint(c.InForce), Note: "not replaced, deferred or expired", URL: decisionsURL + "?state=active"},
		{Key: "hard", Label: "Hard", Value: fmt.Sprint(c.Hard), Note: "the owner's words; gates check them"},
		{Key: "soft", Label: "Soft", Value: fmt.Sprint(c.Soft), Note: "briefed, not enforced"},
		{Key: "provisional", Label: "Provisional", Value: fmt.Sprint(c.Provisional), Note: "in force with no owner's words on the line yet", URL: decisionsURL + "?state=active"},
		challenges,
	}
	for _, s := range decisionstats.Scopes {
		if n := c.ByScope[s]; n > 0 {
			card.Scopes = append(card.Scopes, WbStatsDecisionChip{Key: s, Label: scopeLabels[s], N: n})
		}
	}

	card.Holds = WbTable{Key: "decision-holds", Title: "Gate holds by decision", Sub: "Real transitions a decision held (refused, or passed under YOLO and queued for review)",
		Head: []WbTH{{Text: "Decision"}, {Text: "Holds", Num: true}, {Text: "Issues", Num: true}, {Text: "Passed (YOLO)", Num: true}, {Text: "Rule"}}, RowHead: true}
	for _, h := range c.Holds {
		card.Holds.Rows = append(card.Holds.Rows, []WbCell{
			{Text: h.Ref, URL: in.Prefix + "/issue/" + h.Issue},
			{Text: fmt.Sprint(h.Events), Num: true}, {Text: fmt.Sprint(h.Issues), Num: true}, {Text: fmt.Sprint(h.Passed), Num: true},
			{Text: strings.Join(h.Rules, ", "), Code: true},
		})
	}
	if len(card.Holds.Rows) == 0 {
		card.Holds.Empty = &WbEmpty{Text: "No decision held a gate in this range."}
	}

	card.Briefings = WbTable{Key: "decision-briefings", Title: "Briefings that carried the block",
		Sub:  fmt.Sprintf("%s, %s", plural(c.Briefings, "briefing", "briefings"), plural(c.BriefedIssues, "issue briefed", "issues briefed")),
		Head: []WbTH{{Text: "Briefing"}, {Text: "Times", Num: true}}, RowHead: true}
	for _, b := range decisionstats.Briefings {
		if n := c.Injected[b]; n > 0 {
			card.Briefings.Rows = append(card.Briefings.Rows, []WbCell{{Text: briefingLabels[b]}, {Text: fmt.Sprint(n), Num: true}})
		}
	}
	if len(card.Briefings.Rows) == 0 {
		card.Briefings.Sub = ""
		card.Briefings.Empty = &WbEmpty{Text: "No briefing carried the decisions block in this range: a briefing logs one only when a decision is in force for it."}
	}
	return card
}

// decisionSeries is the Decisions in force trend: hard, soft, provisional
// and holds (30 days) from each snapshot with the decisions family, once
// any snapshot has a decision; nil before that (the card's empty state
// covers it).
func decisionSeries(snaps []*health.Snapshot) []chart.Series {
	seen := false
	for _, s := range snaps {
		if v, ok := s.Value("decisions.total", nil); ok && v > 0 {
			seen = true
			break
		}
	}
	if !seen {
		return nil
	}
	out := []chart.Series{
		{Name: "hard", Class: "s-bugfix"},
		{Name: "soft", Class: "s-feature"},
		{Name: "provisional", Class: "s-tweak"},
		{Name: "gate holds, 30 days", Class: "s-all", Faint: true},
	}
	for _, s := range snaps {
		for i, m := range []string{"decisions.hard", "decisions.soft", "decisions.provisional", "decisions.holds"} {
			if v, ok := s.Value(m, nil); ok {
				out[i].Points = append(out[i].Points, chart.Point{At: s.Time(), Value: v})
			}
		}
	}
	return out
}
