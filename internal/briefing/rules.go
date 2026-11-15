package briefing

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/rules"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ProjectRules are a workflow file's rules: in the shape the two layers share. A
// rule with no text reaches nobody; lint names it.
func ProjectRules(wf *tracker.WorkflowConfig) []rules.Rule {
	if wf == nil {
		return nil
	}
	var out []rules.Rule
	for _, r := range wf.Rules {
		if text := strings.TrimSpace(r.Text); text != "" {
			out = append(out, rules.Rule{Key: strings.TrimSpace(r.ID), Text: text, Narrows: strings.TrimSpace(r.Narrows)})
		}
	}
	return out
}

// writeRuleLayers is the two layers under their own labels, the one wording
// of both briefings. Nothing a project says takes a machine rule out: a
// narrowing rule prints beside it.
func writeRuleLayers(b *strings.Builder, machine rules.File, machineErr error, project []rules.Rule) {
	if machineErr != nil {
		fmt.Fprintf(b, "The machine's rules could not be read (%s): tell your lead, and go on with the rest.\n", machineErr)
	}
	if len(machine.Rules) > 0 {
		fmt.Fprintf(b, "On every project (the owner's %s):\n", machine.Path)
		writeRuleList(b, machine.Rules)
	}
	if len(project) > 0 {
		b.WriteString("On this project (workflow.yaml rules:):\n")
		writeRuleList(b, project)
	}
	if len(machine.Rules) > 0 && len(project) > 0 {
		b.WriteString("A project rule adds to the machine's or narrows one; it never cancels one. If two seem to disagree, follow the machine's and ask.\n")
	}
}

func writeRuleList(b *strings.Builder, all []rules.Rule) {
	shown, more := rules.Cut(all)
	for _, r := range shown {
		b.WriteString("- ")
		if r.Key != "" {
			fmt.Fprintf(b, "[%s] ", r.Key)
		}
		b.WriteString(r.Text)
		if r.Narrows != "" {
			fmt.Fprintf(b, " (narrows [%s])", r.Narrows)
		}
		b.WriteString("\n")
	}
	if more > 0 {
		fmt.Fprintf(b, "- +%d more: issue-cli rules\n", more)
	}
}

// RulesPromptSection is the "## Rules" part of a worker's briefing, or ""
// when neither layer has a rule. wf is the issue's workflow; a department's
// file carries Product's rules: (ProjectWideKeys).
func RulesPromptSection(wf *tracker.WorkflowConfig) string {
	machine, err := rules.LoadMachine()
	project := ProjectRules(wf)
	if err == nil && len(machine.Rules) == 0 && len(project) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Rules\n\n")
	writeRuleLayers(&b, machine, err, project)
	return b.String()
}

// RulesLeadLines is a lead brief's rules: where each layer lives, always, so
// a lead told "on every project…" knows where that goes, then both layers.
func RulesLeadLines(wf *tracker.WorkflowConfig) string {
	machine, err := rules.LoadMachine()
	var b strings.Builder
	fmt.Fprintf(&b, "- Rules: what applies on every project is in %s, the owner's to edit, no agent's; what applies only here goes under rules: in workflow.yaml. issue-cli rules lists both.\n", rules.MachinePath())
	writeRuleLayers(&b, machine, err, ProjectRules(wf))
	return b.String()
}
