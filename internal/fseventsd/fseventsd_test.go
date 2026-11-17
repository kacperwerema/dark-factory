package fseventsd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

func TestMain(m *testing.M) { tmuxguard.Main(m) }

// 36 GB, the owner's Mac.
const memMB = 36 * 1024.0

var t0 = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseTopMemReadsTheFixtures(t *testing.T) {
	if mb, ok := ParseTopMem(fixture(t, "top-quiet.txt"), 72567); !ok || mb < 3.3 || mb > 3.4 {
		t.Errorf("quiet = %v %v, want 3424K", mb, ok)
	}
	if mb, ok := ParseTopMem(fixture(t, "top-runaway.txt"), 312); !ok || mb != 13*1024 {
		t.Errorf("runaway = %v %v, want 13G", mb, ok)
	}
	if _, ok := ParseTopMem(fixture(t, "top-runaway.txt"), 999); ok {
		t.Error("another pid's line must not count")
	}
	for in, want := range map[string]float64{"512M": 512, "1024B": 1.0 / 1024, "2048K": 2, "1T": 1024 * 1024} {
		if got, ok := parseSize(in); !ok || got != want {
			t.Errorf("parseSize(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
}

func TestParsePS(t *testing.T) {
	r, ok := ParsePS("  312  99.6 13840000 fseventsd       \n")
	if !ok || r.PID != 312 || r.CPU != 99.6 || r.FootprintMB < 13515 || r.FootprintMB > 13516 {
		t.Errorf("ParsePS = %+v %v", r, ok)
	}
	if _, ok := ParsePS("  1  0.4 21616 launchd\n"); ok {
		t.Error("launchd is not fseventsd")
	}
}

// The footprint line: over 2 GB on the newest reading alone.
func TestFootprintOverTwoGB(t *testing.T) {
	under := Reading{At: t0, PID: 312, CPU: 5, FootprintMB: FootprintLimitMB, MemMB: memMB}
	if v := Check([]Reading{under}, t0); v.Over {
		t.Errorf("exactly 2 GB is not over: %+v", v)
	}
	over := Reading{At: t0, PID: 312, CPU: 5, FootprintMB: 13.2 * 1024, MemMB: memMB}
	v := Check([]Reading{over}, t0.Add(time.Minute))
	if !v.Over || !strings.Contains(v.Why, "footprint is over 2.0 GB") {
		t.Fatalf("13.2 GB = %+v", v)
	}
	if want := "fseventsd (the macOS file-event daemon) uses 13.2 GB, 37% of memory, at 5% CPU"; v.Line != want {
		t.Errorf("line = %q\nwant  %q", v.Line, want)
	}
	// A reading the viewer took long ago says nothing about now.
	if v := Check([]Reading{over}, t0.Add(PairMax+time.Minute)); v.Over {
		t.Error("a stale reading must not hold a row")
	}
}

// The CPU line: over 50% on two readings of one daemon about 10 minutes apart.
func TestCPUOverFiftyAcrossTwoReadings(t *testing.T) {
	at := func(min int, pid int, cpu float64) Reading {
		return Reading{At: t0.Add(time.Duration(min) * time.Minute), PID: pid, CPU: cpu, FootprintMB: 40, MemMB: memMB}
	}
	for _, c := range []struct {
		name string
		rs   []Reading
		over bool
	}{
		{"one reading", []Reading{at(0, 312, 99)}, false},
		{"two high, 10 min apart", []Reading{at(0, 312, 99), at(10, 312, 107)}, true},
		{"two high, 2 min apart", []Reading{at(8, 312, 99), at(10, 312, 99)}, false},
		{"high then an earlier pair", []Reading{at(0, 312, 99), at(8, 312, 20), at(10, 312, 99)}, true},
		{"one high one low", []Reading{at(0, 312, 30), at(10, 312, 99)}, false},
		{"exactly 50", []Reading{at(0, 312, 50), at(10, 312, 50)}, false},
		{"restarted between", []Reading{at(0, 312, 99), at(10, 900, 99)}, false},
		{"too far apart", []Reading{at(0, 312, 99), at(40, 312, 99)}, false},
	} {
		now := c.rs[len(c.rs)-1].At
		v := Check(c.rs, now)
		if v.Over != c.over {
			t.Errorf("%s: over = %v, want %v (%+v)", c.name, v.Over, c.over, v)
		}
		if v.Over && v.Why != "its CPU stayed over 50% for 10 minutes" {
			t.Errorf("%s: why = %q", c.name, v.Why)
		}
	}
}

func TestAdviceNamesTheOwnersCommand(t *testing.T) {
	if a := Advice(); !strings.Contains(a, "sudo killall fseventsd") || !strings.Contains(a, "nothing here runs it") {
		t.Errorf("advice = %q", a)
	}
}

// Nothing in the package restarts the daemon: the command is the owner's.
func TestNothingRestartsFseventsd(t *testing.T) {
	var calls []string
	stubRun(t, func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch name {
		case "pgrep":
			return "312\n", nil
		case "ps":
			return "  312  99.6 13840000 fseventsd\n", nil
		case "top":
			return fixture(t, "top-runaway.txt"), nil
		case "sysctl":
			return "38654705664\n", nil
		}
		return "", errors.New("unexpected " + name)
	})
	r, err := Read(t0)
	if err != nil {
		t.Fatal(err)
	}
	if r.PID != 312 || r.CPU != 99.6 || r.FootprintMB != 13*1024 || r.MemMB != memMB {
		t.Errorf("reading = %+v", r)
	}
	for _, c := range calls {
		if strings.Contains(c, "kill") || strings.Contains(c, "sudo") || strings.Contains(c, "launchctl") {
			t.Errorf("the package ran %q", c)
		}
	}
	src, err := os.ReadFile("fseventsd.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"killall"`, `"sudo"`, `"launchctl"`, `"kill"`} {
		if strings.Contains(string(src), bad) {
			t.Errorf("fseventsd.go starts %s", bad)
		}
	}
}

// No fseventsd (Linux, CI): no reading and nothing written.
func TestNoDaemonIsNoReading(t *testing.T) {
	stubRun(t, func(name string, args ...string) (string, error) {
		return "", errors.New("exit status 1")
	})
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := Record(t0); !errors.Is(err, ErrNone) {
		t.Errorf("err = %v, want ErrNone", err)
	}
	if _, err := os.Stat(Path()); !os.IsNotExist(err) {
		t.Errorf("a file was written: %v", err)
	}
	if v := Current(t0); v.Over || v.Line != "" {
		t.Errorf("verdict = %+v", v)
	}
}

func TestAppendKeepsTheLastReadings(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for i := 0; i < Keep+3; i++ {
		if err := Append(Path(), Reading{At: t0.Add(time.Duration(i) * 10 * time.Minute), PID: 312, CPU: 99, FootprintMB: 40}); err != nil {
			t.Fatal(err)
		}
	}
	rs := Load(Path())
	if len(rs) != Keep || !rs[len(rs)-1].At.Equal(t0.Add(time.Duration(Keep+2)*10*time.Minute)) {
		t.Fatalf("readings = %+v", rs)
	}
	if v := Current(rs[len(rs)-1].At); !v.Over {
		t.Errorf("two high readings in the file: %+v", v)
	}
}

func stubRun(t *testing.T, f func(string, ...string) (string, error)) {
	t.Helper()
	prev := run
	run = f
	t.Cleanup(func() { run = prev })
}
