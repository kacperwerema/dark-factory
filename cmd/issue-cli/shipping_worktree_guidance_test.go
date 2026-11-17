package main

import (
	"strings"
	"testing"
)

// The shipping prompt is read at every ship, and the worker briefing says the
// engine removes an issue's worktree once its branch is on main. The two must
// not contradict each other.
func TestShippingGuidanceMatchesWorktreeRemoval(t *testing.T) {
	wf := engineWorkflow(t)
	for _, s := range wf.Statuses {
		if s.Name != "shipping" {
			continue
		}
		if strings.Contains(s.Prompt, "nobody has to remove") {
			t.Error("shipping prompt still says nobody removes the worktree; the engine does")
		}
		if !strings.Contains(s.Prompt, "The engine removes it once your branch is on main") {
			t.Errorf("shipping prompt doesn't say the engine removes the worktree once the branch is on main:\n%s", s.Prompt)
		}
		if !strings.Contains(s.Prompt, "do not remove it yourself") {
			t.Error("shipping prompt doesn't tell the worker to leave the worktree to the engine")
		}
		return
	}
	t.Fatal("no shipping status in workflow.yaml")
}
