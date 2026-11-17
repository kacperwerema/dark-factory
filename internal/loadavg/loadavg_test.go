package loadavg

import (
	"runtime"
	"testing"
)

func TestParseSysctl_macOS(t *testing.T) {
	for in, want := range map[string][2]float64{"{ 4.28 5.00 7.63 }\n": {4.28, 5}, "{ 16.02 12.10 9.00 }": {16.02, 12.10}, "0.10 0.20 0.30": {0.10, 0.20}} {
		got, err := ParseSysctl(in)
		if err != nil || got != want {
			t.Errorf("ParseSysctl(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "{ }", "{ 4.28 }", "{ x 1 2 }", "{ 1 x 2 }", "{ -1 0 0 }", "{ 1 -1 0 }"} {
		if _, err := ParseSysctl(bad); err == nil {
			t.Errorf("ParseSysctl(%q): want an error", bad)
		}
	}
}

func TestParseProc_Linux(t *testing.T) {
	got, err := ParseProc("0.52 0.58 0.59 1/389 12345\n")
	if err != nil || got != [2]float64{0.52, 0.58} {
		t.Errorf("ParseProc = %v, %v; want [0.52 0.58]", got, err)
	}
	for _, bad := range []string{"", "\n", "0.52", "abc 1 2 1/2 3", "1 abc 2 1/2 3"} {
		if _, err := ParseProc(bad); err == nil {
			t.Errorf("ParseProc(%q): want an error", bad)
		}
	}
}

// The real reader gives a reading on macOS and Linux; its value is never
// asserted (the machine's load varies).
func TestRead_ThisMachine(t *testing.T) {
	_, _, ok := Read()
	if want := runtime.GOOS == "darwin" || runtime.GOOS == "linux"; ok != want {
		t.Errorf("Read ok = %v on %s, want %v", ok, runtime.GOOS, want)
	}
}
