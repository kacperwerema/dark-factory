package autostart

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 30, 14, 0, 0, 0, time.UTC)

func onState() *State {
	return &State{On: true, Words: "start one when memory is low", By: "lead-p", Below: 50, MaxWorkers: 2, GapMinutes: 5}
}

func okMemory(used int) Memory { return Memory{Found: true, Used: used, CanStart: true} }

func TestDecide_HoldsWithAReasonForEachRule(t *testing.T) {
	ready := []Candidate{{Slug: "a/one"}}
	paused := onState()
	paused.Paused, paused.PauseReason = true, "golf plan till Friday"
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"off", Input{State: &State{}}, "auto-start is off"},
		{"nil state", Input{}, "auto-start is off"},
		{"paused", Input{State: paused, Memory: okMemory(10), Candidates: ready}, "auto-start is paused (golf plan till Friday)"},
		{"no mem-guard", Input{State: onState(), Candidates: ready}, "mem-guard not found"},
		{"mem-guard says no", Input{State: onState(), Memory: Memory{Found: true, Used: 20, Note: "BLOCKED: memory used 80%"}, Candidates: ready}, "mem-guard says no: BLOCKED: memory used 80%"},
		{"memory at below", Input{State: onState(), Memory: okMemory(50), Candidates: ready}, "memory 50% ≥ 50%"},
		{"cap", Input{State: onState(), Memory: okMemory(20), Workers: 2, Candidates: ready}, "2 workers live (cap 2)"},
		{"gap", Input{State: onState(), Memory: okMemory(20), Last: &Start{Project: "golf", Slug: "g/x", At: t0.Add(-40 * time.Second)}, Now: t0, Candidates: ready}, "golf started g/x 40s ago (gap 5m)"},
		{"nothing ready", Input{State: onState(), Memory: okMemory(20), Now: t0, Candidates: []Candidate{{Slug: "a/one", Hold: "waiting on in progress"}}}, "nothing ready to start (first: a/one, waiting on in progress)"},
		{"empty backlog", Input{State: onState(), Memory: okMemory(20), Now: t0}, "nothing ready to start"},
	}
	for _, c := range cases {
		v := Decide(c.in)
		if v.Start || !strings.HasPrefix(v.Reason, c.want) {
			t.Errorf("%s: got start=%v reason %q, want hold %q", c.name, v.Start, v.Reason, c.want)
		}
	}
}

func TestDecide_StartsTheFirstReadyCandidate(t *testing.T) {
	v := Decide(Input{
		State: onState(), Memory: okMemory(31), Workers: 1, Now: t0,
		Last:       &Start{Project: "golf", Slug: "g/x", At: t0.Add(-6 * time.Minute)},
		Candidates: []Candidate{{Slug: "a/live", Hold: "worker live"}, {Slug: "a/two"}, {Slug: "a/three"}},
	})
	if !v.Start || v.Slug != "a/two" || v.Memory != 31 || v.Below != 50 || v.Workers != 1 || v.Cap != 2 {
		t.Fatalf("verdict = %+v, want start a/two with the reading", v)
	}
}

func TestState_OnPauseResumeOff(t *testing.T) {
	root := t.TempDir()
	if st, _ := Load(root, "p"); st.On {
		t.Fatal("a missing file must read as off")
	}
	if err := TurnOn(root, "p", State{By: "lead-p"}, t0); err == nil {
		t.Fatal("on without words must fail")
	}
	if err := TurnOn(root, "p", State{By: "lead-p", Words: "go", How: "lead chat"}, t0); err != nil {
		t.Fatal(err)
	}
	st, _ := Load(root, "p")
	if !st.On || st.Below != DefaultBelow || st.MaxWorkers != DefaultMaxWorkers || st.GapMinutes != DefaultGapMinutes || !st.Since.Equal(t0) || st.Words != "go" {
		t.Fatalf("state after on = %+v", st)
	}
	if err := TurnOn(root, "p", State{Words: "again"}, t0); err == nil {
		t.Fatal("on twice must fail")
	}
	if st, err := Pause(root, "p", "plan", t0); err != nil || !st.Paused {
		t.Fatalf("pause: %+v %v", st, err)
	}
	if st, err := Resume(root, "p"); err != nil || st.Paused {
		t.Fatalf("resume: %+v %v", st, err)
	}
	st, _ = Load(root, "p")
	if err := TurnOff(root, "p", st, t0.Add(time.Hour), "turned off by owner"); err != nil {
		t.Fatal(err)
	}
	if st, _ := Load(root, "p"); st.On || st.EndReason != "turned off by owner" {
		t.Fatalf("state after off = %+v", st)
	}
	if h := History(root, "p"); len(h) != 1 || h[0].Words != "go" {
		t.Fatalf("history = %+v", h)
	}
	if _, err := Pause(root, "p", "", t0); err == nil {
		t.Fatal("pause while off must fail")
	}
}

// Two projects asking at once: one start between them, and the second sees
// the first's.
func TestWithBudget_OneStartAtATime(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	started := 0
	var wg sync.WaitGroup
	for _, p := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(project string) {
			defer wg.Done()
			err := WithBudget(dir, func(last *Start) (*Start, error) {
				v := Decide(Input{State: onState(), Memory: okMemory(10), Last: last, Now: time.Now(), Candidates: []Candidate{{Slug: project + "/x"}}})
				if !v.Start {
					return nil, nil
				}
				mu.Lock()
				started++
				mu.Unlock()
				time.Sleep(50 * time.Millisecond)
				return &Start{Project: project, Slug: v.Slug, At: time.Now(), Memory: 10}, nil
			})
			if err != nil {
				t.Error(err)
			}
		}(p)
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("started %d workers, want 1", started)
	}
	last, err := LastStart(dir)
	if err != nil || last == nil || last.Memory != 10 {
		t.Fatalf("last = %+v, %v", last, err)
	}
}

func fakeMemGuard(t *testing.T, used string, checkExit int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n  used) echo " + used + " ;;\n  check) echo 'BLOCKED: memory used " + used + "%'; exit " + strconv.Itoa(checkExit) + " ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "mem-guard"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestReadMemGuard(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if m := readMemGuard(); m.Found {
		t.Fatalf("no mem-guard on PATH: %+v", m)
	}
	fakeMemGuard(t, "31", 0)
	if m := readMemGuard(); !m.Found || m.Used != 31 || !m.CanStart {
		t.Fatalf("mem-guard 31%%, check ok: %+v", m)
	}
	fakeMemGuard(t, "80", 1)
	if m := readMemGuard(); !m.Found || m.Used != 80 || m.CanStart || m.Note != "BLOCKED: memory used 80%" {
		t.Fatalf("mem-guard 80%%, check refuses: %+v", m)
	}
}
