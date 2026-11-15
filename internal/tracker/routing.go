package tracker

import (
	"slices"
	"strings"
)

// DefaultAgent is the agent CLI a start uses when nothing names one: no
// routing: block, no rule and no --agent.
const DefaultAgent = "claude"

// DefaultStopAt is an agent's stop point when stop_at: doesn't name it: the
// percent of its weekly limit at which new starts stop using it.
const DefaultStopAt = 98

// RoutingConfig is a workflow file's routing: block: which of the owner's
// installed agent CLIs starts each worker, lead or job run, and which to
// try when that one's subscription is resting (internal/routing.Pick).
// No block means DefaultAgent with no fallback.
type RoutingConfig struct {
	// Default is the agent when no rule matches; "" means DefaultAgent.
	Default string `yaml:"default,omitempty" desc:"Agent CLI when no rule matches (default claude)"`
	// Fallback is the order to try when the picked agent is resting.
	Fallback []string `yaml:"fallback,omitempty" desc:"Agents to try, in order, when the picked one's subscription is resting"`
	// StopAt is the percent of each agent's weekly limit at which it rests.
	StopAt map[string]int `yaml:"stop_at,omitempty" desc:"Percent of an agent's weekly limit at which it rests like one at its limit, per agent (default 98, 100 is off); read from Product's workflow.yaml"`
	// Rules are tried in order; the first that matches picks the agent.
	Rules []RoutingRule `yaml:"rules,omitempty" desc:"First match wins: type, label, status, department, lead → agent"`
	// Codex is how a Codex start may run; nil leaves Codex's own defaults.
	Codex *CodexSandbox `yaml:"codex,omitempty" desc:"Codex's sandbox for a start: writable (issues, git) and approval (ask, auto-review, never); unset keeps Codex's own defaults (see docs/agent-dispatch.md, Codex's sandbox)"`
}

// The values of routing: codex:. Anything else adds nothing at launch and
// lint names it (internal/lint/routing.go).
const (
	CodexWritableIssues = "issues"
	CodexWritableGit    = "git"

	CodexApprovalAsk        = "ask"
	CodexApprovalAutoReview = "auto-review"
	CodexApprovalNever      = "never"
)

var (
	// CodexWritableValues are the folders writable: may name.
	CodexWritableValues = []string{CodexWritableIssues, CodexWritableGit}
	// CodexApprovalValues are the policies approval: may name.
	CodexApprovalValues = []string{CodexApprovalAsk, CodexApprovalAutoReview, CodexApprovalNever}
)

// CodexSandbox is a workflow file's routing: codex: block. It names folders
// and policies by word, never a path or a flag, so a workflow file can't
// reach Codex's bypass flag or open the network. The owner picks the values.
type CodexSandbox struct {
	Writable []string `yaml:"writable,omitempty" desc:"Extra folders a Codex start may write: issues (the project's issue dir), git (the git common dir and the worktree's own git dir)"`
	Approval string   `yaml:"approval,omitempty" desc:"Who answers Codex's approval requests: ask (a person, the default), auto-review (a reviewer agent), never (the action fails back to the agent)"`
}

// RoutingRule picks Agent for a start whose subject matches every key the
// rule names. A rule naming no key matches everything.
type RoutingRule struct {
	Type   string `yaml:"type,omitempty"`
	Label  string `yaml:"label,omitempty"`
	Status string `yaml:"status,omitempty"`
	// Department is a box key (DepartmentKey) or a department name.
	Department string `yaml:"department,omitempty"`
	// Lead matches leads (true) or everything but leads (false).
	Lead  *bool  `yaml:"lead,omitempty"`
	Agent string `yaml:"agent"`
}

// CatchAll reports whether the rule names no match key.
func (r RoutingRule) CatchAll() bool {
	return r.Type == "" && r.Label == "" && r.Status == "" && r.Department == "" && r.Lead == nil
}

// Describe is the rule's match in words: "type docs, label design".
func (r RoutingRule) Describe() string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+" "+v)
		}
	}
	add("type", r.Type)
	add("label", r.Label)
	add("status", r.Status)
	add("department", r.Department)
	if r.Lead != nil {
		if *r.Lead {
			parts = append(parts, "leads")
		} else {
			parts = append(parts, "not leads")
		}
	}
	if len(parts) == 0 {
		return "every start"
	}
	return strings.Join(parts, ", ")
}

// DefaultAgentName is the block's default, or DefaultAgent.
func (c *RoutingConfig) DefaultAgentName() string {
	if c == nil || strings.TrimSpace(c.Default) == "" {
		return DefaultAgent
	}
	return strings.TrimSpace(c.Default)
}

// StopPoint is agent's stop_at:, or DefaultStopAt. A nil block has the default.
func (c *RoutingConfig) StopPoint(agent string) int {
	if c != nil {
		if p, ok := c.StopAt[agent]; ok {
			return p
		}
	}
	return DefaultStopAt
}

func cloneRouting(in *RoutingConfig) *RoutingConfig {
	if in == nil {
		return nil
	}
	out := *in
	if in.StopAt != nil {
		out.StopAt = make(map[string]int, len(in.StopAt))
		for a, p := range in.StopAt {
			out.StopAt[a] = p
		}
	}
	out.Fallback = append([]string(nil), in.Fallback...)
	out.Rules = make([]RoutingRule, len(in.Rules))
	for i, r := range in.Rules {
		if r.Lead != nil {
			v := *r.Lead
			r.Lead = &v
		}
		out.Rules[i] = r
	}
	if in.Codex != nil {
		c := *in.Codex
		c.Writable = slices.Clone(in.Codex.Writable)
		out.Codex = &c
	}
	return &out
}
