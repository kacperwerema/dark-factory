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
