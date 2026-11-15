package tracker

import (
	"fmt"
	"strings"
)

// PostmortemConfig is a workflow file's postmortem: block: a fresh agent
// that writes the post-mortem when an issue reaches At (issue-cli
// postmortem, docs/workflow.md "Post-mortem"). No block means no
// post-mortem.
type PostmortemConfig struct {
	// At is the status whose transition starts it; "" means the last status
	// of the workflow's main path.
	At string `yaml:"at,omitempty" desc:"Status whose transition starts the post-mortem (default: the workflow's last status, e.g. done)"`
	// Start is "auto" (the transition starts the agent) or "ask" (the
	// default: it prints the command, the lead starts it on the owner's words).
	Start string `yaml:"start,omitempty" desc:"auto: the transition starts the post-mortem agent; ask (default): it prints issue-cli postmortem start <slug>"`
	// AutoTypes, with start: auto, are the issue types whose post-mortem
	// starts by itself; any other type prints the start command as ask
	// does. Empty: every type but a chore starts by itself.
	AutoTypes []string `yaml:"auto_types,omitempty" desc:"With start: auto, the issue types whose post-mortem starts by itself (e.g. [feature, epic]); any other type prints issue-cli postmortem start <slug>. Unset: every type but chore"`
	// Agent names the agent CLI; "" means routing picks it as for a worker.
	Agent string `yaml:"agent,omitempty" desc:"Agent CLI for the post-mortem (claude, codex); default: routing: as for a worker"`
}

// Post-mortem start modes.
const (
	PostmortemAuto = "auto"
	PostmortemAsk  = "ask"
)

// PostmortemAt is the status that starts the post-mortem, "" when the
// workflow has none.
func (wf *WorkflowConfig) PostmortemAt() string {
	if wf == nil || wf.Postmortem == nil {
		return ""
	}
	if wf.Postmortem.At != "" {
		return wf.Postmortem.At
	}
	last := ""
	for _, s := range wf.Statuses {
		if !wf.IsSideStatus(s.Name) {
			last = s.Name
		}
	}
	return last
}

// PostmortemAutoStart reports whether the transition starts the agent itself.
func (wf *WorkflowConfig) PostmortemAutoStart() bool {
	return wf != nil && wf.Postmortem != nil && wf.Postmortem.Start == PostmortemAuto
}

// PostmortemAutoFor reports whether the transition starts the agent itself
// for an issue of type issueType: start: auto and, when auto_types: is set,
// the type (as written, or as default_type resolves it) is listed. Without
// auto_types every type but a chore starts.
func (wf *WorkflowConfig) PostmortemAutoFor(issueType string) bool {
	if !wf.PostmortemAutoStart() {
		return false
	}
	if len(wf.Postmortem.AutoTypes) == 0 {
		return issueType != ChoreType
	}
	return wf.PostmortemListsType(issueType)
}

// PostmortemListsType reports whether auto_types: names issueType, as
// written or as default_type resolves it.
func (wf *WorkflowConfig) PostmortemListsType(issueType string) bool {
	if wf == nil || wf.Postmortem == nil {
		return false
	}
	resolved, _ := wf.ResolveType(issueType)
	for _, t := range wf.Postmortem.AutoTypes {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.EqualFold(t, strings.TrimSpace(issueType)) || (resolved != "" && strings.EqualFold(t, resolved)) {
			return true
		}
	}
	return false
}

// CheckPostmortem is what's wrong with the block, or nil.
func (wf *WorkflowConfig) CheckPostmortem() error {
	if wf == nil || wf.Postmortem == nil {
		return nil
	}
	p := wf.Postmortem
	switch p.Start {
	case "", PostmortemAuto, PostmortemAsk:
	default:
		return fmt.Errorf("postmortem: start: %q is not auto or ask", p.Start)
	}
	if p.At != "" && wf.GetStatus(p.At) == nil {
		return fmt.Errorf("postmortem: at: %q is not a status of the workflow", p.At)
	}
	for _, t := range p.AutoTypes {
		if _, ok := wf.Types[strings.TrimSpace(t)]; ok {
			continue
		}
		if !wf.HasTypes() {
			return fmt.Errorf("postmortem: auto_types: %q is not a type of the workflow: it has no types: (remove auto_types, or define types:)", t)
		}
		return fmt.Errorf("postmortem: auto_types: %q is not a type of the workflow (types: %s)", t, strings.Join(wf.TypeNames(), ", "))
	}
	return nil
}
