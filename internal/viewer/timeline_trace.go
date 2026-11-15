package viewer

import (
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/issuetrace"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// traceProjectsDir is where Claude Code keeps transcripts for the Timeline
// tab's By stage table; "" is ~/.claude/projects. Tests point it elsewhere.
var traceProjectsDir = ""

// stageRow is one row of the Timeline tab's By stage table: one span of
// the issue's trace (internal/trace), each value as shown, "—" when
// nothing recorded it.
type stageRow struct {
	Status, Agent, Briefing, Tokens, Wall, Calls, Gate, Back string
	Open                                                     bool
	// Tone colours the gate: green first try, amber more tries, "" else.
	Tone string
}

// stageRows is the By stage table of spans, and the line naming what was
// not read (transcripts, briefings), "" when everything was.
func stageRows(spans []trace.Span, missing []missingSource) ([]stageRow, string) {
	rows := make([]stageRow, 0, len(spans))
	for _, s := range spans {
		r := stageRow{Status: s.Status, Agent: trace.AgentText(s), Briefing: trace.BriefingText(s.Briefing),
			Tokens: trace.TokensText(s.Tokens), Wall: trace.Wall(s.WallSec), Calls: trace.CallsText(s),
			Gate: trace.GateText(s), Back: trace.BackText(s), Open: s.Open}
		if s.Gate != nil && s.Gate.FirstTry != nil {
			r.Tone = "green"
			if !*s.Gate.FirstTry {
				r.Tone = "amber"
			}
		}
		rows = append(rows, r)
	}
	var names []string
	for _, m := range missing {
		switch m.Name {
		case "Transcripts":
			names = append(names, "transcripts (tokens unknown)")
		case "Briefings":
			names = append(names, "briefings (sizes unknown)")
		case "CLI log":
			names = append(names, "the CLI log (calls and first tries unknown)")
		}
	}
	note := ""
	if len(names) > 0 {
		note = "Not read: " + strings.Join(names, ", ") + "."
	}
	return rows, note
}

// fillStages puts issue's trace on the tab, built from src like the rest.
func fillStages(tl *Timeline, proj *tracker.Project, issue *tracker.Issue, src timelineSources, now time.Time) {
	t := issuetrace.From(proj, issue, src.core(), traceProjectsDir, now)
	var extra []missingSource
	for _, m := range t.Missing {
		if m.Name == "Transcripts" || m.Name == "Briefings" || m.Name == "CLI log" {
			extra = append(extra, m)
		}
	}
	tl.Stages, tl.StagesNote = stageRows(t.Spans, extra)
	tl.TraceFile = tracker.InjectProject(proj, "issue-cli trace "+issue.Slug)
}
