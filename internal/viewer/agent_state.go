package viewer

import (
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// agentIdleAfter is how long a live agent may go without an issue-cli call
// before it reads as idle rather than working (D-7). A long build with no
// call reads as idle too; the footer says "no issue-cli call", not stopped.
// It is the Timeline's idle gap, so the canvas and the split agree.
const agentIdleAfter = timeline.IdleAfter

// lastCall is an agent's newest issue-cli call.
type lastCall struct {
	At  time.Time
	Cmd string // e.g. "check ui/foo D3", without --project
}

// lastCliCall is the newest call across the clilogs at paths for project
// projSlug (clilog.Newest).
func lastCliCall(projSlug string, paths ...string) (lastCall, bool) {
	rec, ok := clilog.Newest(projSlug, paths...)
	if !ok {
		return lastCall{}, false
	}
	return lastCall{At: rec.End(), Cmd: strings.Join(stripGlobalFlags(rec.Args), " ")}, true
}

// agentLogPaths are where a worker's clilog may be: the project work dir
// under the session name (dispatch writes it there since v0.54) and under the
// assignee, then its worktree, where dispatch wrote it before. lastCliCall
// takes the newest call across them.
func agentLogPaths(workDir, worktree, session, assignee string) []string {
	var paths []string
	if workDir != "" {
		paths = append(paths, clilog.AgentPath(workDir, session))
		if assignee != "" && assignee != session {
			paths = append(paths, clilog.AgentPath(workDir, assignee))
		}
	}
	if worktree != "" {
		paths = append(paths, clilog.AgentPath(worktree, session))
	}
	return paths
}

// workerState is a live worker's state, shared by the agents canvas and the
// board footer: an open question wins over a gate, a gate over idle, idle over
// working. gate is the pending approval's status, if any.
func workerState(wf *tracker.WorkflowConfig, issue *tracker.Issue, comments []tracker.Comment, call lastCall, known bool, now time.Time) (state, gate string) {
	gate = pendingGate(wf, issue)
	switch {
	case openQuestion(comments):
		return "question", gate
	case gate != "":
		return "at-gate", gate
	case known && now.Sub(call.At) >= agentIdleAfter:
		return "idle", ""
	default:
		return "working", ""
	}
}

// agoText is a short "N m/h/d" age for footers and chips.
func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " h"
	default:
		return strconv.Itoa(int(d/(24*time.Hour))) + " d"
	}
}

// flagValue is the value after flag in args, or "".
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
