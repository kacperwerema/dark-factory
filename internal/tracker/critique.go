package tracker

import (
	"fmt"
	"strings"
)

// CritiqueConfig is a workflow file's critique: block: how `issue-cli
// critique` starts the fresh critic that the `critique` gate reads. Every
// key is optional (docs/Workflow/critique.md).
type CritiqueConfig struct {
	Agent   string `yaml:"agent,omitempty" desc:"Agent CLI the critic runs on (claude or codex); default: routing's pick, switched to the other installed agent when there is one"`
	Command string `yaml:"command,omitempty" desc:"Command that runs the critic instead of the agent's own (sh -c, the brief on stdin, findings on stdout); a stand-in in tests"`
	Timeout string `yaml:"timeout,omitempty" desc:"How long a critic may run (Go duration, default 15m)"`
}

// CritiqueRule is the validate rule that holds a transition until the
// newest critique run from the current status has no blocks finding.
const CritiqueRule = "critique"

// critiqueChecker runs the critique gate, registered by internal/critique.
var critiqueChecker func(w *WorkflowConfig, issue *Issue) error

// SetCritiqueChecker registers the critique gate.
func SetCritiqueChecker(fn func(w *WorkflowConfig, issue *Issue) error) {
	critiqueChecker = fn
}

func (w *WorkflowConfig) checkCritique(issue *Issue) error {
	if critiqueChecker == nil {
		return fmt.Errorf("the critique gate needs the critic's run log, which this program doesn't include — run the transition with issue-cli")
	}
	return critiqueChecker(w, issue)
}

// CritiqueEdge is the edge out of status from whose actions run the
// critique rule: its target, and whether there is one. w should be the
// issue's workflow (ForIssue).
func (w *WorkflowConfig) CritiqueEdge(from string) (to string, ok bool) {
	if w == nil {
		return "", false
	}
	for _, t := range w.Transitions {
		if !strings.EqualFold(t.From, from) {
			continue
		}
		if TransitionHasRule(t.Actions, CritiqueRule) {
			return t.To, true
		}
	}
	return "", false
}

// CritiqueEdges lists every from → to edge whose actions run the critique
// rule.
func (w *WorkflowConfig) CritiqueEdges() []WorkflowTransition {
	if w == nil {
		return nil
	}
	var out []WorkflowTransition
	for _, t := range w.Transitions {
		if TransitionHasRule(t.Actions, CritiqueRule) {
			out = append(out, t)
		}
	}
	return out
}

// TransitionHasRule reports whether actions validate rule (by name, any
// argument).
func TransitionHasRule(actions []WorkflowAction, rule string) bool {
	for _, a := range actions {
		if a.Type != "validate" {
			continue
		}
		name, _, _ := strings.Cut(a.Rule, ":")
		if strings.TrimSpace(name) == rule {
			return true
		}
	}
	return false
}

func cloneCritique(c *CritiqueConfig) *CritiqueConfig {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}
