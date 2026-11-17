package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/leaks"
)

func stubLeaks(t *testing.T, rep leaks.Report) *leaks.Options {
	t.Helper()
	var got leaks.Options
	prev := findLeaks
	findLeaks = func(o leaks.Options) leaks.Report { got = o; return rep }
	t.Cleanup(func() { findLeaks = prev })
	return &got
}

// TestDoctorLeaksListsPidAgeAndStop: each finding prints its pid, age and
// stop command, and nothing is stopped without --stop.
func TestDoctorLeaksListsPidAgeAndStop(t *testing.T) {
	stubLeaks(t, leaks.Report{
		Leaks:  []leaks.Finding{{Kind: leaks.KindAgentHome, PID: 69322, Age: 10 * 24 * time.Hour, What: "codex app-server-daemon (CODEX_HOME=/x/scratchpad/real/chome)", Why: "CODEX_HOME is a temp folder and its parent is gone (pid 1)", StopCommand: "kill 69322"}},
		Traced: []leaks.Finding{{Kind: "child", PID: 7, Age: time.Hour, What: "child (ci runner)", Why: "started by issue-cli ci run", StopCommand: "kill 7", Traced: true}},
	})
	var out bytes.Buffer
	ctx := &Context{Stdout: &out, Stderr: &out, Now: time.Now}
	if err := runDoctor(ctx, []string{"leaks"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"doctor leaks: 1 left running (nothing stopped; --stop stops them)", "pid 69322", "10d0h", "stop: kill 69322", "From the trace", "pid 7"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorLeaksQuietAndUnder(t *testing.T) {
	got := stubLeaks(t, leaks.Report{})
	var out bytes.Buffer
	ctx := &Context{Stdout: &out, Stderr: &out, Now: time.Now}
	if err := runDoctor(ctx, []string{"leaks", "--under", "/tmp/ck-1", "--stop", "--quiet"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || got.Under != "/tmp/ck-1" {
		t.Fatalf("out %q, under %q", out.String(), got.Under)
	}
	if err := runDoctor(ctx, []string{"nope"}); err == nil {
		t.Fatal("doctor nope passed")
	}
}

// TestLeadTickRunsDoctorLeaksOnceADay: the first tick of a day looks and
// says what is left running in one line; later ticks that day don't look.
func TestLeadTickRunsDoctorLeaksOnceADay(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	calls := 0
	prev := findLeaks
	findLeaks = func(leaks.Options) leaks.Report {
		calls++
		return leaks.Report{Leaks: []leaks.Finding{
			{Kind: leaks.KindChrome, PID: 50180, What: "Chrome on /tmp/chromedp-runner1"},
			{Kind: leaks.KindChrome, PID: 80030, What: "Chrome on /tmp/chromedp-runner2"},
			{Kind: leaks.KindAgentHome, PID: 69322, What: "codex app-server-daemon"},
		}}
	}
	t.Cleanup(func() { findLeaks = prev })
	out := tick(t, proj)
	assertContains(t, out, "leaks: 3 things left running (2 chrome, 1 agent-home); see issue-cli doctor leaks, stop with --stop")
	out = tick(t, proj)
	assertNotContains(t, out, "leaks:")
	if calls != 1 {
		t.Fatalf("doctor leaks ran %d times in one day, want 1", calls)
	}
}
