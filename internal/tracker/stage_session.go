package tracker

import (
	"fmt"
	"strings"
)

// The stage contract: a status says whether its worker keeps the session it
// has (continue, the default) or meets it in a new one (fresh). A worker
// never starts an agent, so transition into a fresh status only marks it
// (StageMarkComment) and tells the worker to write HANDOFF.md and end its
// turn; the lead's tick starts the new session with dispatch --fresh on the
// issue's own dispatch words, as a takeover does. docs/workflow.md, "Fresh
// session per stage".
const (
	SessionContinue = "continue"
	SessionFresh    = "fresh"
)

// DefaultBriefingBudget is a stage briefing's cap in estimated tokens when
// its status sets no briefing_budget.
const DefaultBriefingBudget = 3000

// MinBriefingBudget is the smallest budget lint takes without a warning:
// under it the parts that never fold fill it alone.
const MinBriefingBudget = 500

// stageMarkPrefix starts the comment transition posts on entering a fresh
// status with a live worker. It is locked like dispatch: (cmd_comment.go),
// so a worker can't forge or edit one.
const stageMarkPrefix = "stage: fresh session at "

// freshHow starts the how of dispatch --fresh's dispatch: line.
const freshHow = "fresh session at "

// SessionFor is the session: of status, SessionContinue when unset or
// unknown.
func (w *WorkflowConfig) SessionFor(status string) string {
	if w == nil {
		return SessionContinue
	}
	if s := w.GetStatus(status); s != nil && strings.TrimSpace(s.Session) == SessionFresh {
		return SessionFresh
	}
	return SessionContinue
}

// IsFreshStatus reports whether status starts a fresh session.
func (w *WorkflowConfig) IsFreshStatus(status string) bool {
	return w.SessionFor(status) == SessionFresh
}

// BriefingBudgetFor is status's stage briefing cap in estimated tokens.
func (w *WorkflowConfig) BriefingBudgetFor(status string) int {
	if w != nil {
		if s := w.GetStatus(status); s != nil && s.BriefingBudget > 0 {
			return s.BriefingBudget
		}
	}
	return DefaultBriefingBudget
}

// StageMarkComment is the comment transition posts when the issue enters
// the fresh status with its worker live.
func StageMarkComment(status string) string {
	return stageMarkPrefix + status
}

// FreshDispatchHow is the how of the dispatch: line dispatch --fresh writes.
func FreshDispatchHow(status string) string {
	return freshHow + status
}

// FreshPending is the status of the newest stage: mark that no dispatch
// --fresh answered yet; ok is false when none waits. A newer dispatch: line
// of any kind answers it: a plain dispatch at a fresh status builds the
// stage briefing too.
func FreshPending(comments []Comment) (status string, ok bool) {
	for i := len(comments) - 1; i >= 0; i-- {
		text := strings.TrimSpace(comments[i].Text)
		if _, _, isDispatch := parseDispatchComment(text); isDispatch {
			return "", false
		}
		if rest, found := strings.CutPrefix(text, stageMarkPrefix); found {
			if j := strings.Index(rest, recordedBy); j >= 0 {
				rest = rest[:j]
			}
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// isFreshHow reports whether a dispatch: line's how is dispatch --fresh's.
func isFreshHow(how string) bool {
	return strings.HasPrefix(how, freshHow)
}

// FreshSessionLines are what transition prints to the worker on entering a
// fresh status: write the handoff, stop, and who starts the next session.
func FreshSessionLines(slug, status string) []string {
	return []string{
		status + " starts a fresh session (session: fresh in the workflow): the next agent reads only paths, not this chat.",
		"Write " + HandoffFile + " at your worktree's root now (what's done, the next step, open questions), then end your turn. Don't start the " + status + " work here.",
		"Your lead starts the new session: issue-cli dispatch " + slug + " --fresh",
	}
}

// lintSessions is Lint's part on session: and briefing_budget:: a value
// the engine reads as continue, and a budget too small to hold the parts
// that never fold.
func (w *WorkflowConfig) lintSessions() []string {
	var out []string
	for _, s := range w.Statuses {
		switch v := strings.TrimSpace(s.Session); v {
		case "", SessionContinue, SessionFresh:
		default:
			out = append(out, fmt.Sprintf("status %q has session: %q, which is read as continue: use fresh or continue", s.Name, v))
		}
		switch {
		case s.BriefingBudget < 0 || (s.BriefingBudget > 0 && s.BriefingBudget < MinBriefingBudget):
			out = append(out, fmt.Sprintf("status %q has briefing_budget: %d: under %d estimated tokens the parts that never fold fill it alone (raise it, or remove it for the default %d)", s.Name, s.BriefingBudget, MinBriefingBudget, DefaultBriefingBudget))
		case s.BriefingBudget > 0 && strings.TrimSpace(s.Session) != SessionFresh:
			out = append(out, fmt.Sprintf("status %q has briefing_budget: but no session: fresh: only a fresh session's briefing has a budget", s.Name))
		}
	}
	return out
}
