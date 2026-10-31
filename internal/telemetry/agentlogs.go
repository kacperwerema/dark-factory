package telemetry

import (
	"errors"
	"os"
	"path/filepath"
)

// AgentLogsDir is the per-project folder of agent and CLI logs.
const AgentLogsDir = ".agent-logs"

// agentLogsIgnore is the .gitignore written into .agent-logs/, so a project
// never has to list it: a newcomer's first issue-cli call left it untracked
// in git status.
const agentLogsIgnore = "# Agent and issue-cli logs (issue-cli, dispatch); never committed\n*\n"

// IgnoreAgentLogs writes <...>/.agent-logs/.gitignore, ignoring the whole
// folder, when path lies inside an .agent-logs folder that has none yet.
// Paths elsewhere (the global log under $XDG_STATE_HOME) are left alone.
// Errors are ignored: a log line matters more than its ignore file.
func IgnoreAgentLogs(path string) {
	dir := filepath.Clean(path)
	for {
		if filepath.Base(dir) == AgentLogsDir {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); !errors.Is(err, os.ErrNotExist) {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(ignore, []byte(agentLogsIgnore), 0o644)
}
