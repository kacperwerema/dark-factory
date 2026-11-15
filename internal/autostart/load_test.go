package autostart

import (
	"runtime"
	"strings"
	"testing"
)

func load(avg float64, cores int) CPU { return CPU{Found: true, Avg1: avg, Cores: cores} }

func TestDecide_HoldsWhileLoadIsAtOrOverTheLimit(t *testing.T) {
	ready := []Candidate{{Slug: "a/one"}}
	custom := onState()
	custom.MaxLoad = 10.5
	cases := []struct {
		name  string
		st    *State
		load  CPU
		start bool
		want  string
	}{
		{"over the cores", onState(), load(16, 14), false, "load 16.0 on 14 cores (max 14)"},
		{"at the cores", onState(), load(14, 14), false, "load 14.0 on 14 cores (max 14)"},
		{"under the cores", onState(), load(4.28, 14), true, ""},
		{"over max_load", custom, load(11, 14), false, "load 11.0 on 14 cores (max 10.5)"},
		{"under max_load", custom, load(10.4, 14), true, ""},
		{"no reading", onState(), CPU{Cores: 14}, true, ""},
	}
	for _, c := range cases {
		v := Decide(Input{State: c.st, Memory: okMemory(20), CPU: c.load, Now: t0, Candidates: ready})
		if v.Start != c.start || v.Reason != c.want {
			t.Errorf("%s: got start=%v reason %q, want start=%v %q", c.name, v.Start, v.Reason, c.start, c.want)
		}
		if v.Cores != c.load.Cores || v.LoadFound != c.load.Found || v.MaxLoad != c.st.LoadLimit(c.load.Cores) {
			t.Errorf("%s: verdict reading = %+v", c.name, v)
		}
	}
}

func TestDecide_LoadComesAfterMemory(t *testing.T) {
	v := Decide(Input{State: onState(), Memory: okMemory(60), CPU: load(30, 14), Candidates: []Candidate{{Slug: "a/one"}}})
	if !strings.HasPrefix(v.Reason, "memory 60%") {
		t.Fatalf("reason = %q, want the memory hold first", v.Reason)
	}
}

func TestVerdict_LoadPart(t *testing.T) {
	v := Decide(Input{State: onState(), Memory: okMemory(20), CPU: load(4.28, 14), Now: t0, Candidates: []Candidate{{Slug: "a/one"}}})
	if got := v.LoadPart(); got != "load 4.3 < 14 on 14 cores" {
		t.Errorf("LoadPart = %q", got)
	}
	if got := (Verdict{}).LoadPart(); got != "load unknown" {
		t.Errorf("LoadPart without a reading = %q", got)
	}
}

func TestParseSysctlLoadavg_macOS(t *testing.T) {
	for in, want := range map[string]float64{"{ 4.28 5.00 7.63 }\n": 4.28, "{ 16.02 12.10 9.00 }": 16.02, "0.10 0.20 0.30": 0.10} {
		got, err := parseSysctlLoadavg(in)
		if err != nil || got != want {
			t.Errorf("parseSysctlLoadavg(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "{ }", "{ x 1 2 }", "{ -1 0 0 }"} {
		if _, err := parseSysctlLoadavg(bad); err == nil {
			t.Errorf("parseSysctlLoadavg(%q): want an error", bad)
		}
	}
}

func TestParseProcLoadavg_Linux(t *testing.T) {
	got, err := parseProcLoadavg("0.52 0.58 0.59 1/389 12345\n")
	if err != nil || got != 0.52 {
		t.Errorf("parseProcLoadavg = %v, %v; want 0.52", got, err)
	}
	for _, bad := range []string{"", "\n", "abc 1 2 1/2 3"} {
		if _, err := parseProcLoadavg(bad); err == nil {
			t.Errorf("parseProcLoadavg(%q): want an error", bad)
		}
	}
}

// The real reader on this OS returns the cores and, on macOS and Linux, a
// reading; its value is never asserted (the machine's load varies).
func TestReadLoad_ThisMachine(t *testing.T) {
	l := readLoad()
	if l.Cores != runtime.NumCPU() {
		t.Errorf("cores = %d, want %d", l.Cores, runtime.NumCPU())
	}
	if (runtime.GOOS == "darwin" || runtime.GOOS == "linux") && !l.Found {
		t.Errorf("no load reading on %s", runtime.GOOS)
	}
}

func TestLoadLimitAndLine(t *testing.T) {
	st := onState()
	if st.LoadLimit(8) != 8 {
		t.Errorf("default limit = %v, want the cores", st.LoadLimit(8))
	}
	st.MaxLoad = 6
	if st.LoadLimit(8) != 6 {
		t.Errorf("max_load limit = %v", st.LoadLimit(8))
	}
	if got := LoadLine(load(3.14, 8), 6); got != "load 3.1 on 8 cores (max 6)" {
		t.Errorf("LoadLine = %q", got)
	}
	if got := LoadLine(CPU{}, 6); got != "load unknown" {
		t.Errorf("LoadLine without a reading = %q", got)
	}
}

// The one hold's boundary: at the line holds, a step under doesn't.
func TestMachineHold(t *testing.T) {
	ok := Memory{Found: true, Used: 49, CanStart: true}
	cases := []struct {
		name    string
		mem     Memory
		cpu     CPU
		maxLoad float64
		want    string
	}{
		{"all under", ok, load(13.9, 14), 0, ""},
		{"no mem-guard", Memory{}, load(1, 14), 0, "mem-guard not found: memory is read from mem-guard"},
		{"check refuses, a note", Memory{Found: true, Used: 10, Note: "BLOCKED"}, load(1, 14), 0, "mem-guard says no: BLOCKED"},
		{"check refuses, no note", Memory{Found: true, Used: 10}, load(1, 14), 0, "mem-guard says no"},
		{"memory at the line", Memory{Found: true, Used: 50, CanStart: true}, load(1, 14), 0, "memory 50% ≥ 50%"},
		{"load at the cores", ok, load(14, 14), 0, "load 14.0 on 14 cores (max 14)"},
		{"load at max_load", ok, load(10.5, 14), 10.5, "load 10.5 on 14 cores (max 10.5)"},
		{"max_load over the cores", ok, load(15, 14), 20, ""},
		{"no load reading", ok, CPU{Cores: 14}, 0, ""},
		{"no cores known", ok, CPU{Found: true, Avg1: 3}, 0, ""},
	}
	for _, c := range cases {
		if got := MachineHold(c.mem, c.cpu, DefaultBelow, c.maxLoad); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// OwnerMaxLoad counts only while auto-start is on: a closed window's limit
// holds nothing.
func TestMaxLoadOf(t *testing.T) {
	root := t.TempDir()
	if got := MaxLoadOf(root, "p"); got != 0 {
		t.Fatalf("no state: %v", got)
	}
	if err := Save(root, "p", &State{On: true, MaxLoad: 7}); err != nil {
		t.Fatal(err)
	}
	if got := MaxLoadOf(root, "p"); got != 7 {
		t.Fatalf("on: %v", got)
	}
	if err := Save(root, "p", &State{MaxLoad: 7}); err != nil {
		t.Fatal(err)
	}
	if got := MaxLoadOf(root, "p"); got != 0 {
		t.Fatalf("off: %v", got)
	}
}

// The brief's sentence comes from the rules Decide runs: every rule but the
// pause (the brief states that one apart) has words, and a Decide that holds
// on a rule names something the sentence says.
func TestHoldClausesCoverEveryRule(t *testing.T) {
	for i, r := range holdRules {
		if r.hold == nil {
			t.Errorf("rule %d has no check", i)
		}
		if r.words == nil && i != 0 {
			t.Errorf("rule %d is missing from the lead brief's sentence: give it words", i)
		}
	}
	got := HoldSentence(HoldClauses(Limits{Below: 50, MaxWorkers: 3, MaxLoad: 10.5}))
	if want := "memory is at or above 50%, the 1-minute load is at or above 10.5, 3 workers are live, or another project just started one"; got != want {
		t.Errorf("sentence = %q", got)
	}
	got = HoldSentence(append(HoldClauses(Limits{}), CIRedClause))
	if want := "memory is at or above its limit, the 1-minute load is at or above the core count, the worker cap is reached, another project just started one, or local CI is red on main"; got != want {
		t.Errorf("queue sentence = %q", got)
	}
	if got := HoldSentence([]string{"one"}); got != "one" {
		t.Errorf("one clause = %q", got)
	}
}
