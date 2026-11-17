package bench

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A control that passes, or never ran, turns the smoke red: a green run
// with scoring that passes undone work proves nothing.
func TestSmokeControlMustFail(t *testing.T) {
	task := Task{Project: "bench-fixture", Slug: "tally/lines"}
	for name, c := range map[string]struct {
		run  *Run
		err  error
		ok   bool
		want string
	}{
		"fails":    {run: &Run{Tasks: []TaskRecord{{Outcome: OutcomeFail, Reason: "hidden tests 0/1"}}}, ok: true, want: "fails tally/lines (hidden tests 0/1)"},
		"passes":   {run: &Run{Tasks: []TaskRecord{{Outcome: OutcomePass}}}, want: "came back pass, not fail"},
		"error":    {run: &Run{Tasks: []TaskRecord{{Outcome: OutcomeError}}}, want: "came back error"},
		"no run":   {err: errors.New("sandbox: boom"), want: "sandbox: boom"},
		"no tasks": {run: &Run{}, want: "0 records for one task"},
	} {
		ok, detail := smokeControlVerdict(task, c.run, c.err)
		if ok != c.ok || !strings.Contains(detail, c.want) {
			t.Errorf("%s: ok %v %q, want ok %v with %q", name, ok, detail, c.ok, c.want)
		}
	}
}

// A task the stand-in could not solve names what the stand-in said.
func TestSmokeRunCheckQuotesTheStandIn(t *testing.T) {
	logs := t.TempDir()
	write(t, logs+"/tally_lines.log", "start requires <slug>\n\nExample:\n")
	task := Task{Project: "bench-fixture", Slug: "tally/lines", Start: "in progress"}
	run := &Run{Stop: StopDone, Tasks: []TaskRecord{{Task: task.ID(), Outcome: OutcomeFail, Reason: "hidden tests 0/1", HiddenTotal: 1}}}
	var got []SmokeCheck
	checkSmokeRun(run, []Task{task}, logs, func(name string, ok bool, format string, a ...any) {
		got = append(got, SmokeCheck{Name: name, OK: ok})
		if name == SmokeRun {
			if detail := fmt.Sprintf(format, a...); !strings.Contains(detail, "the stand-in worker said: start requires <slug>") {
				t.Errorf("run detail = %q", detail)
			}
		}
	})
	if len(got) == 0 || got[0].Name != SmokeRun || got[0].OK {
		t.Errorf("checks = %+v", got)
	}
}
