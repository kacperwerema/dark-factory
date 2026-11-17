package tracker

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkersConfig is a workflow file's workers: block: what an issue's Claude
// Code worker starts with (docs/agent-dispatch.md "Worker setup"). No block
// means the lean setup with nothing added: the department's connectors only,
// no plugin, no personal or project skill, no claude.ai connector.
type WorkersConfig struct {
	// Setup is "lean" (the default) or "full": the owner's whole Claude
	// Code setup, as a worker had before workers: existed.
	Setup string `yaml:"setup,omitempty" desc:"lean (default): a worker loads only the MCP servers, plugins and skills named here plus its department's connectors; full: the owner's whole Claude Code setup"`
	// MCP are servers from the owner's Claude Code config a worker gets
	// when its issue matches.
	MCP []WorkerMCP `yaml:"mcp,omitempty" desc:"MCP servers from the owner's Claude Code config (~/.claude.json, the project's .mcp.json) by name, each for the issues its systems/types/labels match (none listed: every worker)"`
	// Plugins are plugin ids (name@marketplace) a worker keeps on.
	Plugins []string `yaml:"plugins,omitempty" desc:"Plugin ids (name@marketplace) a lean worker keeps on; every other plugin the owner has on is off"`
	// Skills are skill names a worker keeps on: a bare name for every
	// worker, or an item for the issues its systems/types/labels match.
	Skills []WorkerSkill `yaml:"skills,omitempty" desc:"Skills a lean worker keeps on, by the name Claude Code lists (personal, project, bundled, or anthropic-skills:<name> synced from claude.ai): a name for every worker, or {skill, systems, types, labels} for the issues they match; every other one is off"`
	// Tools are Claude Code tools a lean worker keeps that it otherwise
	// starts without: only Artifact so far.
	Tools []string `yaml:"tools,omitempty" desc:"Claude Code tools a lean worker keeps that it otherwise starts without (Artifact)"`
}

// WorkerSkill is one item of workers: skills:, written as a bare name
// (every worker) or a map like a workers: mcp: item.
type WorkerSkill struct {
	Skill   string   `yaml:"skill" desc:"Skill name as Claude Code lists it"`
	Systems []string `yaml:"systems,omitempty" desc:"Issue systems that keep the skill"`
	Types   []string `yaml:"types,omitempty" desc:"Issue types that keep the skill"`
	Labels  []string `yaml:"labels,omitempty" desc:"Issue labels that keep the skill"`
}

// UnmarshalYAML takes a bare name or a map.
func (s *WorkerSkill) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = WorkerSkill{Skill: n.Value}
		return nil
	}
	type plain WorkerSkill
	return n.Decode((*plain)(s))
}

// MarshalYAML writes an item with no match as its bare name.
func (s WorkerSkill) MarshalYAML() (any, error) {
	if len(s.Systems) == 0 && len(s.Types) == 0 && len(s.Labels) == 0 {
		return s.Skill, nil
	}
	type plain WorkerSkill
	return plain(s), nil
}

// WorkerToolArtifact is the one tool workers: tools: knows: Claude Code's
// Artifact tool, off for a lean worker unless kept.
const WorkerToolArtifact = "Artifact"

// WorkerMCP is one server of workers: mcp:. Any of Systems, Types and
// Labels matching an issue gives its worker the server; none set gives it
// to every worker.
type WorkerMCP struct {
	Server  string   `yaml:"server" desc:"Server name as the owner's Claude Code config names it (claude mcp list)"`
	Systems []string `yaml:"systems,omitempty" desc:"Issue systems that get the server"`
	Types   []string `yaml:"types,omitempty" desc:"Issue types that get the server"`
	Labels  []string `yaml:"labels,omitempty" desc:"Issue labels that get the server"`
}

// Worker setup values.
const (
	WorkerSetupLean = "lean"
	WorkerSetupFull = "full"
)

// WorkerSetupFull reports whether wf's workers keep the owner's whole
// Claude Code setup (workers: setup: full).
func (wf *WorkflowConfig) WorkerSetupFull() bool {
	return wf != nil && wf.Workers != nil && wf.Workers.Setup == WorkerSetupFull
}

// WorkerMCPFor are the server names of workers: mcp: that issue's worker
// gets, in the file's order. issueType is the issue's type as resolved
// (type:, else default_type).
func (wf *WorkflowConfig) WorkerMCPFor(issue *Issue, issueType string) []string {
	if wf == nil || wf.Workers == nil || issue == nil {
		return nil
	}
	var out []string
	for _, m := range wf.Workers.MCP {
		if m.Server != "" && m.matches(issue, issueType) && !slices.Contains(out, m.Server) {
			out = append(out, m.Server)
		}
	}
	return out
}

// WorkerSkillsFor are the skill names of workers: skills: that issue's
// worker keeps, in the file's order: each bare name, and each item whose
// systems/types/labels match. A nil issue keeps the bare names only.
func (wf *WorkflowConfig) WorkerSkillsFor(issue *Issue, issueType string) []string {
	if wf == nil || wf.Workers == nil {
		return nil
	}
	var out []string
	for _, s := range wf.Workers.Skills {
		name := strings.TrimSpace(s.Skill)
		if name == "" || slices.Contains(out, name) {
			continue
		}
		all := len(s.Systems) == 0 && len(s.Types) == 0 && len(s.Labels) == 0
		if all || (issue != nil && workerMatch(s.Systems, s.Types, s.Labels, issue, issueType)) {
			out = append(out, name)
		}
	}
	return out
}

// WorkerKeepsTool reports whether workers: tools: names tool.
func (wf *WorkflowConfig) WorkerKeepsTool(tool string) bool {
	return wf != nil && wf.Workers != nil && slices.ContainsFunc(wf.Workers.Tools, func(t string) bool { return strings.EqualFold(strings.TrimSpace(t), tool) })
}

func (m WorkerMCP) matches(issue *Issue, issueType string) bool {
	if len(m.Systems) == 0 && len(m.Types) == 0 && len(m.Labels) == 0 {
		return true
	}
	return workerMatch(m.Systems, m.Types, m.Labels, issue, issueType)
}

func workerMatch(systems, types, labels []string, issue *Issue, issueType string) bool {
	fold := func(list []string, v string) bool {
		return v != "" && slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, v) })
	}
	if fold(systems, issue.System) || fold(types, issueType) {
		return true
	}
	return slices.ContainsFunc(issue.Labels, func(l string) bool { return fold(labels, l) })
}

// WorkersProblems are workers: values lint warns about: an unknown setup:,
// a server with no name, a type the workflow does not define.
func (wf *WorkflowConfig) WorkersProblems() []string {
	if wf == nil || wf.Workers == nil {
		return nil
	}
	var out []string
	w := wf.Workers
	if w.Setup != "" && w.Setup != WorkerSetupLean && w.Setup != WorkerSetupFull {
		out = append(out, fmt.Sprintf("workers: setup: %q is neither lean nor full", w.Setup))
	}
	for i, m := range w.MCP {
		if strings.TrimSpace(m.Server) == "" {
			out = append(out, fmt.Sprintf("workers: mcp: item %d names no server:", i+1))
		}
		for _, t := range m.Types {
			if len(wf.Types) > 0 {
				if _, ok := wf.Types[t]; !ok {
					out = append(out, fmt.Sprintf("workers: mcp: %s names type %q, which types: does not define", m.Server, t))
				}
			}
		}
	}
	for i, s := range w.Skills {
		if strings.TrimSpace(s.Skill) == "" {
			out = append(out, fmt.Sprintf("workers: skills: item %d names no skill:", i+1))
		}
		for _, t := range s.Types {
			if len(wf.Types) > 0 {
				if _, ok := wf.Types[t]; !ok {
					out = append(out, fmt.Sprintf("workers: skills: %s names type %q, which types: does not define", s.Skill, t))
				}
			}
		}
	}
	for _, t := range w.Tools {
		if !strings.EqualFold(strings.TrimSpace(t), WorkerToolArtifact) {
			out = append(out, fmt.Sprintf("workers: tools: %q is not a tool workers: can keep (%s)", t, WorkerToolArtifact))
		}
	}
	for _, p := range w.Plugins {
		if !strings.Contains(p, "@") {
			out = append(out, fmt.Sprintf("workers: plugins: %q is not a plugin id (name@marketplace, claude plugin list)", p))
		}
	}
	return out
}

// clone is a deep copy of w (nil stays nil).
func (w *WorkersConfig) clone() *WorkersConfig {
	if w == nil {
		return nil
	}
	c := *w
	c.Plugins = slices.Clone(w.Plugins)
	c.Tools = slices.Clone(w.Tools)
	c.Skills = make([]WorkerSkill, len(w.Skills))
	for i, s := range w.Skills {
		c.Skills[i] = WorkerSkill{Skill: s.Skill, Systems: slices.Clone(s.Systems), Types: slices.Clone(s.Types), Labels: slices.Clone(s.Labels)}
	}
	c.MCP = make([]WorkerMCP, len(w.MCP))
	for i, m := range w.MCP {
		c.MCP[i] = WorkerMCP{Server: m.Server, Systems: slices.Clone(m.Systems), Types: slices.Clone(m.Types), Labels: slices.Clone(m.Labels)}
	}
	return &c
}
