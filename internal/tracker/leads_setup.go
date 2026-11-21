package tracker

import (
	"fmt"
	"slices"
	"strings"
)

// LeadToolsOff are the Claude Code built-in tools a lean lead starts
// without unless leads: tools: keeps one (docs/agent-dispatch.md "Lead
// setup"). Report o-20261120-07ad: in 7 days of 20 lead sessions Artifact
// had 3 calls (one lead session) and the others none, about 18k
// tokens of a 60k first turn. ListAgents stays: leads call it.
var LeadToolsOff = []string{WorkerToolArtifact, "Workflow", "SendFeedback", "ReportFindings"}

// LeadSetupFull reports whether this project's leads keep the owner's
// whole Claude Code setup (leads: setup: full).
func (l LeadsConfig) LeadSetupFull() bool {
	return strings.TrimSpace(l.Setup) == WorkerSetupFull
}

// LeadKeepsTool reports whether leads: tools: names tool.
func (l LeadsConfig) LeadKeepsTool(tool string) bool {
	return slices.ContainsFunc(l.Tools, func(t string) bool { return strings.EqualFold(strings.TrimSpace(t), tool) })
}

// LeadToolsDropped are the tools of LeadToolsOff this project's lean leads
// start without, in that order.
func (l LeadsConfig) LeadToolsDropped() []string {
	var out []string
	for _, t := range LeadToolsOff {
		if !l.LeadKeepsTool(t) {
			out = append(out, t)
		}
	}
	return out
}

// LeadsSetupProblems are leads: setup values lint warns about: a setup:
// other than lean or full, a tools: name leads: cannot keep.
func (l LeadsConfig) LeadsSetupProblems() []string {
	var out []string
	if s := strings.TrimSpace(l.Setup); s != "" && s != WorkerSetupLean && s != WorkerSetupFull {
		out = append(out, fmt.Sprintf("leads.setup %q is neither lean nor full (docs/agent-dispatch.md, Lead setup)", l.Setup))
	}
	for _, t := range l.Tools {
		if !slices.ContainsFunc(LeadToolsOff, func(k string) bool { return strings.EqualFold(strings.TrimSpace(t), k) }) {
			out = append(out, fmt.Sprintf("leads.tools %q is not a tool leads: can keep (%s)", t, strings.Join(LeadToolsOff, ", ")))
		}
	}
	return out
}

// clone is a copy of l that shares no slice with it.
func (l LeadsConfig) clone() LeadsConfig {
	l.Tools = slices.Clone(l.Tools)
	return l
}
