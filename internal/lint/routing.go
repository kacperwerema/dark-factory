package lint

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// agentName is what agentrun.ValidAgentType accepts: a plain command name.
var agentName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// checkRouting reads a workflow file's routing: (which agent CLI starts
// each worker, lead or job run, docs/agent-dispatch.md#routing-between-claude-and-codex)
// for mistakes that would route silently wrong: an agent that isn't a
// command name or isn't installed, a rule without an agent or matching
// nothing this file knows (type, status, department), a rule after a
// catch-all, a fallback that repeats or leaves out the default, a stop_at:
// that is no percent or sits in a department's file, a codex: value the
// launcher doesn't know (it adds nothing for one).
func checkRouting(env *Env) []Finding {
	wf := env.Workflow
	if wf == nil || wf.Routing == nil {
		return nil
	}
	cfg := wf.Routing
	where := env.where(env.lineOf("routing:"))
	var out []Finding
	add := func(msg, fix string) { out = append(out, Finding{Where: where, Message: msg, Fix: fix}) }
	agentOK := func(what, a string) {
		switch {
		case !agentName.MatchString(a):
			add(fmt.Sprintf("%s: %q is not an agent command name (claude, codex)", what, a), "agent: claude | codex")
		case !routing.Installed(a):
			add(fmt.Sprintf("%s: %s is not installed on this machine (not on PATH); a start routed to it fails", what, a), "install it, or route to an installed agent")
		}
	}
	agentOK("routing default", cfg.DefaultAgentName())
	seen := map[string]bool{}
	for _, a := range cfg.Fallback {
		a = strings.TrimSpace(a)
		if seen[a] {
			add("routing fallback: "+a+" is listed twice", "list each agent once")
		}
		seen[a] = true
		agentOK("routing fallback", a)
	}
	if len(cfg.Fallback) > 0 && !seen[cfg.DefaultAgentName()] {
		add("routing fallback: leaves out the default "+cfg.DefaultAgentName()+", so a start routed elsewhere never falls back to it", "add "+cfg.DefaultAgentName()+" to fallback:")
	}
	for _, a := range slices.Sorted(maps.Keys(cfg.StopAt)) {
		p := cfg.StopAt[a]
		if !agentName.MatchString(a) {
			add(fmt.Sprintf("routing stop_at: %q is not an agent command name (claude, codex)", a), "agent: claude | codex")
		}
		if p < 1 || p > 100 {
			add(fmt.Sprintf("routing stop_at: %s is %d, not a percent from 1 to 100", a, p), "98 is the default; 100 turns the stop point off")
		}
	}
	if env.Department != "" && len(cfg.StopAt) > 0 {
		add(env.Department+"'s own workflow file sets stop_at:, which only Product's workflow.yaml sets: a subscription rests for the whole machine", "move stop_at: to workflow.yaml")
	}
	if c := cfg.Codex; c != nil {
		for _, v := range c.Writable {
			if !slices.Contains(tracker.CodexWritableValues, v) {
				add(fmt.Sprintf("routing codex writable: %q is not a known folder, so a Codex start doesn't get it", v), "writable: "+strings.Join(tracker.CodexWritableValues, " | "))
			}
		}
		if c.Approval != "" && !slices.Contains(tracker.CodexApprovalValues, c.Approval) {
			add(fmt.Sprintf("routing codex approval: %q is not a known policy, so a Codex start keeps asking", c.Approval), "approval: "+strings.Join(tracker.CodexApprovalValues, " | "))
		}
	}
	catchAll := -1
	for i, r := range cfg.Rules {
		name := fmt.Sprintf("routing rule %d (%s)", i+1, r.Describe())
		if strings.TrimSpace(r.Agent) == "" {
			add(name+" has no agent:", "agent: claude | codex")
		} else {
			agentOK(name, strings.TrimSpace(r.Agent))
		}
		if catchAll >= 0 {
			add(fmt.Sprintf("%s comes after rule %d, which matches every start: it never applies", name, catchAll+1), "move the catch-all last, or give it a match key")
		}
		if r.CatchAll() && catchAll < 0 {
			catchAll = i
		}
		if r.Type != "" {
			if len(wf.Types) == 0 {
				add(name+" names a type, but this file has no types:", "drop type: or define types:")
			} else if _, ok := wf.Types[r.Type]; !ok {
				add(fmt.Sprintf("%s: type %q is not in this file's types: (%s)", name, r.Type, strings.Join(wf.TypeNames(), ", ")), "fix the type name")
			}
		}
		if r.Status != "" && wf.GetStatusIndex(r.Status) < 0 {
			add(fmt.Sprintf("%s: status %q is not in this file's statuses:", name, r.Status), "fix the status name")
		}
		if r.Department != "" && env.Project != nil {
			key := tracker.DepartmentKey(r.Department)
			if key != "product" && env.Project.DepartmentByKey(key) == nil {
				add(fmt.Sprintf("%s: department %q is not in projects.yaml departments:", name, r.Department), "use a department's key (issue-cli department list)")
			} else if env.Department != "" && key != tracker.DepartmentKey(env.Department) {
				add(fmt.Sprintf("%s: %s's own workflow file routes another department's work (%s); only its own issues walk this file", name, env.Department, r.Department), "move the rule to Product's workflow.yaml or that department's file")
			}
		}
	}
	return out
}
