package tracker

import (
	"fmt"
	"strconv"
	"strings"
)

// HandoffFile is the note a worker keeps at its worktree's root for the agent
// that may take its branch over. It is never committed
// (agentrun.ExcludeHandoff).
const HandoffFile = "HANDOFF.md"

// LimitedSessionPrefix starts the name of a worker session left running after
// a takeover. It is no managed prefix on purpose: the pane is not scanned for
// limits or counted as a worker, and nothing is typed into it.
const LimitedSessionPrefix = "limited-"

// takeoverHow starts the how of a takeover's dispatch: line.
const takeoverHow = "takeover from "

// LimitedSessionName is what issue slug's worker session is renamed to when
// the other agent takes the branch over.
func LimitedSessionName(slug string) string {
	return LimitedSessionPrefix + strings.TrimPrefix(AgentSessionName(slug), "agent-")
}

// TakeoverComment is the dispatch: line of a takeover: the words that started
// the worker it replaces, and the agent that stopped. It is also the record
// that the issue was taken over (TakenOver).
func TakeoverComment(words, from, date string) string {
	return DispatchComment(words, takeoverHow+from+" at its usage limit", date)
}

// parseDispatchComment splits a dispatch: line into its words and how. The
// prefix is locked (only dispatch writes one), so a worker can't forge it.
func parseDispatchComment(text string) (words, how string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(text), "dispatch: ")
	if !found {
		return "", "", false
	}
	quoted, err := strconv.QuotedPrefix(rest)
	if err != nil {
		return "", "", false
	}
	words, err = strconv.Unquote(quoted)
	if err != nil {
		return "", "", false
	}
	return words, strings.TrimPrefix(strings.TrimSpace(rest[len(quoted):]), "("), true
}

// DispatchWords is the newest words that started the issue's worker, "" when
// no dispatch: line carries any. A takeover's or a fresh session's own line
// is not a start: it quotes these words again.
func DispatchWords(comments []Comment) string {
	for i := len(comments) - 1; i >= 0; i-- {
		words, how, ok := parseDispatchComment(comments[i].Text)
		if ok && words != "" && !strings.HasPrefix(how, takeoverHow) && !isFreshHow(how) {
			return words
		}
	}
	return ""
}

// TakenOver reports whether the issue already had its one takeover.
func TakenOver(comments []Comment) bool {
	for _, c := range comments {
		if _, how, ok := parseDispatchComment(c.Text); ok && strings.HasPrefix(how, takeoverHow) {
			return true
		}
	}
	return false
}

// HandoffPromptSection is the worker briefing's part on HANDOFF.md. Every
// issue keeps one: the other agent takes the branch over from it at a usage
// limit (routing:), and a fresh session reads it at a session: fresh status.
// A type that builds nothing (BuildsNothing: a report, a chore) has no
// worktree to keep it in, so its handoff goes in progress: comments.
func HandoffPromptSection(wf *WorkflowConfig) string {
	if wf.BuildsNothing() {
		return "\n\n## Handoff\n\n" +
			"This type of work builds nothing, so there is no worktree to keep " + HandoffFile + " in. Post a `progress:` comment on the issue at each step instead: what's done, the next step, open questions. Three short lists are enough. " + handoffWhyTrail(wf) + "\n"
	}
	return "\n\n## Handoff\n\n" +
		"Keep " + HandoffFile + " current at the root of your worktree, at each step: what's done, the next step, open questions. Three short lists are enough, and it is never committed. " + handoffWhy(wf) + "\n"
}

// handoffWhy is who reads HANDOFF.md after the worker.
func handoffWhy(wf *WorkflowConfig) string {
	if wf != nil && wf.Routing != nil {
		return "When you hit your usage limit you can't write it any more: the other agent takes your branch over from this file, and a fresh session at a new stage starts from it."
	}
	return "The next session on this issue starts from it: a fresh session at a new stage, or the worker that resumes after a restart."
}

// handoffWhyTrail is who reads the progress: comments after the worker.
func handoffWhyTrail(wf *WorkflowConfig) string {
	if wf != nil && wf.Routing != nil {
		return "When you hit your usage limit you can't write any more: the other agent takes your work over from the issue trail, and a fresh session at a new stage starts from it."
	}
	return "The next session on this issue starts from the issue trail: a fresh session at a new stage, or the worker that resumes after a restart."
}

// HandoffReminder is transition's line to a worker.
func HandoffReminder(wf *WorkflowConfig) string {
	if wf.BuildsNothing() {
		return "Post a progress: comment: what's done, the next step, open questions."
	}
	return "Update " + HandoffFile + " at your worktree's root: what's done, the next step, open questions."
}

// TakeoverPromptSection opens a takeover's briefing: the normal briefing
// follows it.
func TakeoverPromptSection(from, dir, branch string) string {
	var b strings.Builder
	b.WriteString("## You are taking this branch over\n\n")
	fmt.Fprintf(&b, "The %s worker on this issue hit its usage limit mid-task. Its session is left running and nothing reaches it, so you are the only one writing here.\n\n", from)
	fmt.Fprintf(&b, "- Read %s/%s first: what's done, the next step, open questions. It may be missing or a step behind.\n", dir, HandoffFile)
	fmt.Fprintf(&b, "- Then `git status` and `git log` on %s: uncommitted changes in this worktree are the other worker's work, so keep them.\n", branch)
	b.WriteString("- Then the issue trail below. Don't start over: carry on from the next step, and keep " + HandoffFile + " current as you go.\n\n")
	return b.String()
}
