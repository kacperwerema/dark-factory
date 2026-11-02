package viewer

import (
	"time"

	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// WbStationCritique is the station's Critique block: the newest fresh
// critic run on the issue (docs/Workflow/critique.md). Empty is set when
// the issue's workflow has a critique gate but no critic has read it yet.
type WbStationCritique struct {
	Slug     string
	Empty    bool
	Gates    []string // the critique edges, "in design → backlog"
	Verdict  string   // clear, holds, overridden, failed
	Holds    bool
	Edge     string
	Agent    string
	When     string
	Error    string
	Override string // the owner's words, when overridden
	Findings []WbCritiqueFinding
	Notes    []WbCritiqueFinding
	Problems []string
	Coverage string
	Unread   []string
	Blocks   int
	Should   int
}

// WbCritiqueFinding is one finding line.
type WbCritiqueFinding struct {
	Severity, Where, Text string
	Malformed             bool
}

// stationCritique is the block for issue: nil when its workflow has no
// critique gate and no critic ever ran on it.
func stationCritique(p *tracker.Project, wf *tracker.WorkflowConfig, issue *tracker.Issue, now time.Time) *WbStationCritique {
	runs, _ := critique.Load(p.IssueDir, issue.Slug)
	var gates []string
	if wf != nil {
		for _, t := range wf.ForIssue(issue).CritiqueEdges() {
			gates = append(gates, t.From+" → "+t.To)
		}
	}
	latest := critique.Latest(runs, issue.Slug, "")
	if latest == nil {
		if len(gates) == 0 {
			return nil
		}
		return &WbStationCritique{Slug: issue.Slug, Empty: true, Gates: gates}
	}
	c := &WbStationCritique{
		Slug: issue.Slug, Gates: gates, Verdict: latest.Verdict(), Holds: latest.Holds(),
		Edge: latest.From + " → " + latest.To, Agent: latest.Agent, When: agoText(now.Sub(latest.At)) + " ago",
		Error: latest.Error, Problems: latest.Report.Problems, Coverage: latest.Report.Coverage, Unread: latest.Report.Unread,
		Blocks: latest.Report.Count(critique.Blocks), Should: latest.Report.Count(critique.Should),
	}
	if latest.Override != nil {
		c.Override = latest.Override.Quote
	}
	for _, f := range latest.Report.Findings {
		row := WbCritiqueFinding{Severity: f.Severity, Where: f.Where(), Text: f.Text, Malformed: f.Malformed}
		if f.Malformed {
			row.Text = f.Raw
		}
		if f.Severity == critique.Note && !f.Malformed {
			c.Notes = append(c.Notes, row)
			continue
		}
		c.Findings = append(c.Findings, row)
	}
	return c
}
