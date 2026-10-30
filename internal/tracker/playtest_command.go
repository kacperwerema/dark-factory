package tracker

import "strings"

// Default playtest commands: this repo's runner (cmd/playtest via make).
const (
	DefaultPlaytestCommand = "make playtest ISSUE={slug}"
	DefaultPlaytestReport  = "make playtest-report ISSUE={slug} APPLY=1"
)

// PlaytestCommandFor is the command the owner's playtest of slug runs:
// playtest_command, or make playtest.
func (wf *WorkflowConfig) PlaytestCommandFor(slug string) string {
	cmd := DefaultPlaytestCommand
	if wf != nil && strings.TrimSpace(wf.PlaytestCommand) != "" {
		cmd = strings.TrimSpace(wf.PlaytestCommand)
	}
	return strings.ReplaceAll(cmd, "{slug}", slug)
}

// PlaytestReportFor is the command that ticks the boxes slug's playtest
// passed: playtest_report, or make playtest-report APPLY=1.
func (wf *WorkflowConfig) PlaytestReportFor(slug string) string {
	cmd := DefaultPlaytestReport
	if wf != nil && strings.TrimSpace(wf.PlaytestReport) != "" {
		cmd = strings.TrimSpace(wf.PlaytestReport)
	}
	return strings.ReplaceAll(cmd, "{slug}", slug)
}
