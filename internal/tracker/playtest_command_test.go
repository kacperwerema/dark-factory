package tracker

import "testing"

func TestPlaytestCommands(t *testing.T) {
	var none *WorkflowConfig
	if got := none.PlaytestCommandFor("cli/lead-tab"); got != "make playtest ISSUE=cli/lead-tab" {
		t.Errorf("default command = %q", got)
	}
	if got := (&WorkflowConfig{}).PlaytestReportFor("cli/lead-tab"); got != "make playtest-report ISSUE=cli/lead-tab APPLY=1" {
		t.Errorf("default report = %q", got)
	}
	wf := &WorkflowConfig{PlaytestCommand: " ./play.sh {slug} ", PlaytestReport: "./report.sh --issue {slug}"}
	if got := wf.PlaytestCommandFor("game/jump"); got != "./play.sh game/jump" {
		t.Errorf("command = %q", got)
	}
	if got := wf.PlaytestReportFor("game/jump"); got != "./report.sh --issue game/jump" {
		t.Errorf("report = %q", got)
	}
}
