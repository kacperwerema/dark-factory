package ci

import (
	"reflect"
	"strings"
	"testing"
)

// make validate's `validate: <step> <seconds>s` lines become the command's
// steps; the total, the red so far line and the verdict are not steps.
func TestParseSteps(t *testing.T) {
	out := "[fmt-check] ok\nvalidate: fmt-check 1s\nvalidate: red so far: vet\nvalidate: vet 12s\n" +
		"ok  pkg 3.2s\nvalidate: test 244s\nvalidate: cover-cli 1.5s\nvalidate: total 400s\nvalidate: FAIL (vet)\n"
	want := []Step{{"fmt-check", 1000}, {"vet", 12000}, {"test", 244000}, {"cover-cli", 1500}}
	if got := ParseSteps(out); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSteps = %+v, want %+v", got, want)
	}
	if got := ParseSteps("hello\n"); got != nil {
		t.Errorf("no step lines: %+v", got)
	}
}

// A command that times its steps keeps them in .ci/runs/<sha>.json, and the
// slowest one of the newest default-tier run gets its line.
func TestExecuteRecordsStepsAndTheSlowestShows(t *testing.T) {
	root := newRepo(t)
	c := cmd("echo 'validate: vet 3s'; echo 'validate: test 9s'; echo 'validate: total 12s'; echo 'validate: PASS'")
	runs, _, err := Execute(Options{Root: root, Config: cfg(c, cmd("echo plain")), Out: &strings.Builder{}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
	recorded := RecentRuns(root, 1)
	if len(recorded) != 1 || len(recorded[0].Commands) != 2 {
		t.Fatalf("recorded = %+v", recorded)
	}
	if got, want := recorded[0].Commands[0].Steps, []Step{{"vet", 3000}, {"test", 9000}}; !reflect.DeepEqual(got, want) {
		t.Errorf("steps = %+v, want %+v", got, want)
	}
	if got := recorded[0].Commands[1].Steps; got != nil {
		t.Errorf("a command with no step lines has no steps: %+v", got)
	}
	line := SlowestStepLine(recorded)
	if want := "Slowest step at " + recorded[0].Sha[:7] + ": test 9s ("; !strings.HasPrefix(line, want) {
		t.Errorf("SlowestStepLine = %q, want prefix %q", line, want)
	}

	slow := Run{Sha: "bbbbbbbbbb", Tier: "slow", Commands: []CommandResult{{Steps: []Step{{"ux", 99000}}}}}
	if got := SlowestStepLine(append([]Run{slow}, recorded...)); got != line {
		t.Errorf("a slow-tier run must not hide the default tier's: %q", got)
	}
	plain := Run{Sha: "cccccccccc", Commands: []CommandResult{{Cmd: "x"}}}
	if got := SlowestStepLine(append([]Run{plain}, recorded...)); got != "" {
		t.Errorf("the newest default run timed no steps, an older one's must not show: %q", got)
	}
}
