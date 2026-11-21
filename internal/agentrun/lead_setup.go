package agentrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Lead setup (docs/agent-dispatch.md "Lead setup"): a Claude Code lead (the
// CEO, a department or group lead) starts without what leads never used in
// a week of transcripts (report o-20261120-07ad): the skill list, the
// Artifact tool and the Workflow, SendFeedback and ReportFindings tools,
// about 29k tokens of a 60k first turn that every lead turn re-reads. Its
// MCP servers, connectors and plugins stay as they are. leads: tools: in
// workflow.yaml keeps one, leads: setup: full the owner's whole setup.

// LeanLead reports whether spec launches a Claude Code lead with the lean
// lead setup: claude, a lead, and its workflow not leads: setup: full.
func LeanLead(spec Spec) bool {
	return spec.AgentType == "claude" && spec.LeadScope != "" && !(spec.Workflow != nil && spec.Workflow.Leads.LeadSetupFull())
}

// LeadSetupFlags adds the lean lead setup to base, the lead's connector
// flags (ConnectorFlags): --settings=<sessionLogDir>/settings.json turning
// every skill off (plugins untouched), the dropped tools but Artifact in
// the one --disallowedTools list, and CLAUDE_CODE_DISABLE_ARTIFACT=1 unless
// leads: tools: keeps Artifact. A settings file that can't be written is a
// warning step: the lead starts with its skills on.
func LeadSetupFlags(proj *tracker.Project, spec Spec, workDir, sessionLogDir string, base []string, steps *[]Step) WorkerSetup {
	var leads tracker.LeadsConfig
	if spec.Workflow != nil {
		leads = spec.Workflow.Leads
	}
	dropped := leads.LeadToolsDropped()
	flags := mergeDisallowed(base, workerDisallowed("", dropped))

	settings, off := WorkerSettings(ProjectWorkDir(proj), workDir, filepath.Dir(sessionLogDir), workersKeep{allPlugins: true})
	setPath := filepath.Join(sessionLogDir, "settings.json")
	sb, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(setPath, append(sb, '\n'), 0600); err != nil {
		*steps = append(*steps, Step{Name: "Write lead settings", Status: "warning", Detail: err.Error() + "; skills stay on"})
		off = workerOff{}
	} else {
		flags = append(flags, "--settings="+setPath)
	}
	var env []string
	if slices.Contains(dropped, tracker.WorkerToolArtifact) {
		env = append(env, artifactToolOff)
	}
	*steps = append(*steps, Step{
		Name:   "Lead setup (leads:): " + off.skillsAndTools(dropped),
		Status: "ok",
		Detail: setPath + " (MCP servers, connectors and plugins as they are; leads: tools: keeps one, setup: full the owner's whole setup; docs/agent-dispatch.md#lead-setup)",
	})
	return WorkerSetup{Flags: flags, Env: env}
}

// mergeDisallowed is flags with more (comma-separated tools) added to its
// --disallowedTools= list, or a new one when it has none: one list, so no
// flag shadows another. more "" leaves flags as they are.
func mergeDisallowed(flags []string, more string) []string {
	out := slices.Clone(flags)
	if more == "" {
		return out
	}
	const flag = "--disallowedTools="
	for i, f := range out {
		if list, ok := strings.CutPrefix(f, flag); ok {
			if list != "" {
				more = list + "," + more
			}
			out[i] = flag + more
			return out
		}
	}
	return append(out, flag+more)
}
