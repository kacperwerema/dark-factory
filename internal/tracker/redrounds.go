package tracker

import (
	"strings"
	"time"
)

// DefaultMaxRedRounds is the red-round cap when workflow.yaml sets none: the
// owner chose four (factory/stop-after-four-red-rounds-…, D-4 of the
// spec-flow epic).
const DefaultMaxRedRounds = 4

// RedRoundCap is max_red_rounds, or DefaultMaxRedRounds when it is unset or
// not positive (lint warns on a value below 1).
func (w *WorkflowConfig) RedRoundCap() int {
	if w == nil || w.MaxRedRounds < 1 {
		return DefaultMaxRedRounds
	}
	return w.MaxRedRounds
}

// RoundResult is one outcome of one check on an issue, kept in the stats
// sidecar's rounds: list: a gate's check run by a transition, a check the
// worker says it cannot fix, later a critic block. internal/redrounds counts
// them into streaks.
type RoundResult struct {
	// Check names what was checked: gate:<rule>[:<command>], ci:<command>,
	// critic:<finding>.
	Check string    `json:"check"`
	Red   bool      `json:"red"`
	At    time.Time `json:"at"`
	// Head is the work branch head when it ran: reds at one head are one
	// round. Empty without a branch.
	Head string `json:"head,omitempty"`
	// Why is the failure's first line, or the worker's reason for
	// --cannot-fix.
	Why string `json:"why,omitempty"`
	// CannotFix marks the worker's "I cannot fix this": the check is a
	// question at once, whatever the count.
	CannotFix bool `json:"cannot_fix,omitempty"`
}

// AppendRoundResults adds results to the issue's stats sidecar under the
// issue lock.
func AppendRoundResults(issuePath string, results ...RoundResult) error {
	if len(results) == 0 {
		return nil
	}
	return withIssueLock(issuePath, func() error {
		store, err := LoadStats(issuePath)
		if err != nil {
			return err
		}
		store.Rounds = append(store.Rounds, results...)
		return SaveStats(issuePath, store)
	})
}

// CheckKey names a validate action that is a check, one that runs something
// and can go red on its own (a shell command, the proof check), as opposed
// to a form the worker fills in (boxes, sections). ok is false for those.
func CheckKey(action WorkflowAction) (key string, ok bool) {
	rule := strings.TrimSpace(action.Rule)
	name, _, _ := strings.Cut(rule, ":")
	switch strings.TrimSpace(name) {
	case "command_succeeds":
		cmd := strings.Join(strings.Fields(action.Command), " ")
		if len(cmd) > 80 {
			cmd = cmd[:80]
		}
		return "gate:command_succeeds:" + cmd, true
	case criteriaProvenRule:
		return "gate:" + criteriaProvenRule, true
	}
	return "", false
}

// WorkBranchHead is the work/<slug> branch head in the project's checkout,
// "" without one.
func (w *WorkflowConfig) WorkBranchHead(filePath, slug string) string {
	head, _ := branchShas(w.RepoRoot, folderSlug(w.IssuesRoot, filePath, slug), "")
	return head
}
