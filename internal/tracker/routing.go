package tracker

import "strings"

// DefaultAgent is the agent CLI a start uses when nothing names one: no
// routing: block, no rule and no --agent.
const DefaultAgent = "claude"

// RoutingConfig is a workflow file's routing: block: which of the owner's
// installed agent CLIs starts each worker, lead or job run, and which to
// try when that one's subscription is resting (internal/routing.Pick).
// No block means DefaultAgent with no fallback.
type RoutingConfig struct {
	// Default is the agent when no rule matches; "" means DefaultAgent.
	Default string `yaml:"default,omitempty" desc:"Agent CLI when no rule matches (default claude)"`
	// Fallback is the order to try when the picked agent is resting.
	Fallback []string `yaml:"fallback,omitempty" desc:"Agents to try, in order, when the picked one's subscription is resting"`
	// Rules are tried in order; the first that matches picks the agent.
	Rules []RoutingRule `yaml:"rules,omitempty" desc:"First match wins: type, label, status, department, lead → agent"`
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

func cloneRouting(in *RoutingConfig) *RoutingConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.Fallback = append([]string(nil), in.Fallback...)
	out.Rules = make([]RoutingRule, len(in.Rules))
	for i, r := range in.Rules {
		if r.Lead != nil {
			v := *r.Lead
			r.Lead = &v
		}
		out.Rules[i] = r
	}
	return &out
}
