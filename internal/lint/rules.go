package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/briefing"
	"github.com/kacperwerema/dark-factory/internal/rules"
)

const rulesFile = "rules.md"

// checkRules reads the two layers of rules (docs/workflow.md, Rules) for
// what reaches nobody and for a project rule that drops a machine rule
// without saying so. A finding names a machine rule by key or line, never by
// its words: the file is the owner's, and a lint report travels further than
// a briefing.
func checkRules(env *Env) []Finding {
	machine, err := rules.LoadMachine()
	var out []Finding
	if err != nil {
		out = append(out, Finding{Where: rulesFile, Message: "the machine's rules can't be read, so no briefing carries them: " + err.Error(), Fix: "check the file's permissions: " + machine.Path})
	}
	out = append(out, machineFileFindings(machine)...)
	return append(out, projectRuleFindings(env, machine)...)
}

func machineFileFindings(machine rules.File) []Finding {
	var out []Finding
	for _, n := range machine.Stray {
		out = append(out, Finding{Where: fmt.Sprintf("%s:%d", rulesFile, n), Message: "this line is not a rule, so it reaches nobody", Fix: `start it with "- ", indent it under a rule, or make it a heading`})
	}
	seen := map[string]bool{}
	for _, r := range machine.Rules {
		where := fmt.Sprintf("%s:%d", rulesFile, r.Line)
		if r.Text == "" {
			out = append(out, Finding{Where: where, Message: "this rule has no text", Fix: "write it, or delete the line"})
		}
		if r.Key != "" && seen[r.Key] {
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("[%s] names two rules, so narrows: can't say which", r.Key), Fix: "rename one"})
		}
		seen[r.Key] = true
	}
	if _, more := rules.Cut(machine.Rules); more > 0 {
		out = append(out, Finding{Where: rulesFile, Message: fmt.Sprintf("%d rules don't fit the briefing's %d characters: agents see them only with issue-cli rules", more, rules.BriefChars), Fix: "shorten the file"})
	}
	return out
}

func projectRuleFindings(env *Env, machine rules.File) []Finding {
	where := env.where(env.lineOf("rules:"))
	keys := map[string]bool{}
	for _, r := range machine.Rules {
		keys[r.Key] = r.Key != ""
	}
	var out []Finding
	seen := map[string]bool{}
	for i, r := range env.Workflow.Rules {
		name := strings.TrimSpace(r.ID)
		if strings.TrimSpace(r.Text) == "" {
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("rule %d has no text, so it reaches nobody", i+1), Fix: "write it, or delete the entry"})
		}
		if name != "" && seen[name] {
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("two rules have the id %q", name), Fix: "rename one"})
		}
		seen[name] = true
		if n := strings.TrimSpace(r.Narrows); n != "" && !keys[n] {
			out = append(out, Finding{Where: where, Message: fmt.Sprintf("rule %d narrows [%s], which no rule in %s has", i+1, n, rulesFile), Fix: "name a key issue-cli rules lists, or remove narrows:"})
		}
	}
	project := briefing.ProjectRules(env.Workflow)
	if _, more := rules.Cut(project); more > 0 {
		out = append(out, Finding{Where: where, Message: fmt.Sprintf("%d rules don't fit the briefing's %d characters: agents see them only with issue-cli rules", more, rules.BriefChars), Fix: "shorten rules:"})
	}
	for _, c := range rules.Conflicts(machine.Rules, project) {
		out = append(out, conflictFinding(where, c))
	}
	return out
}

func conflictFinding(where string, c rules.Conflict) Finding {
	project := fmt.Sprintf("%q", c.Project.Text)
	if c.Project.Key != "" {
		project = c.Project.Key
	}
	f := Finding{Where: where, Fix: fmt.Sprintf(`if it narrows that rule, add narrows: %s to it; if not, change it (the machine rule stays in every briefing either way)`, c.Machine.Key)}
	if c.Machine.Key == "" {
		f.Fix = fmt.Sprintf(`give that rule a [key] in %s and add narrows: <key>, or change this rule`, rulesFile)
	}
	if c.Why == "name" {
		f.Message = fmt.Sprintf("project rule %s takes the name of machine rule %s without narrows:, so it reads as replacing it", project, c.Machine.Label())
		return f
	}
	f.Message = fmt.Sprintf("project rule %s contradicts machine rule %s (%s:%d): one forbids what the other asks for", project, c.Machine.Label(), rulesFile, c.Machine.Line)
	return f
}
