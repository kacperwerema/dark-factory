package playtestruns

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeResults(t *testing.T, dir, session, name, body string) {
	t.Helper()
	p := filepath.Join(dir, session, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "results.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Only steps that ran on a ux-test run with a build count; a dirty build is
// its own build; a missing dir is no runs.
func TestFlakyCountsOnlyStepsThatRan(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	from, started := now.AddDate(0, 0, -14), `"2026-11-14T10:00:00+01:00"`
	a, b := t.TempDir(), t.TempDir()
	writeResults(t, a, "s", "1-auto", "session: s\nmode: auto\nbuild: abc\nstarted: "+started+"\nsteps:\n  - {id: TP1, status: fail}\n  - {id: TP2, status: fail}\n  - {id: TP3, status: skip}\n  - {id: TP4, status: fail}\n")
	// The second run is in another runs dir (local CI's): TP1 pairs across dirs.
	writeResults(t, b, "s", "2-ux-test", "session: s\nmode: ux-test\nbuild: abc\nstarted: "+started+"\nsteps:\n  - {id: TP1, status: pass}\n  - {id: TP2, status: pass, cached: .playtest/runs/s/1-auto}\n  - {id: TP3, status: pass}\n")
	writeResults(t, a, "s", "3-auto", "session: s\nmode: auto\nbuild: abc+1f2e\nstarted: "+started+"\nsteps:\n  - {id: TP4, status: pass}\n")
	writeResults(t, a, "s", "4-judge", "session: s\nmode: judge\nbuild: abc\nstarted: "+started+"\nsteps:\n  - {id: TP4, status: pass}\n")
	writeResults(t, a, "s", "5-auto", "session: s\nmode: auto\nbuild: unknown\nstarted: "+started+"\nsteps:\n  - {id: TP4, status: pass}\n  - {id: TP4, status: fail}\n")
	writeResults(t, a, "s", "6-auto", "session: s\nmode: auto\nbuild: abc\nstarted: \"2026-10-01T10:00:00Z\"\nsteps:\n  - {id: TP4, status: pass}\n")
	writeResults(t, a, "s", "7-auto", "not: [yaml")

	got := Flaky([]string{a, b, filepath.Join(a, "missing")}, from, now)
	want := FlakyCount{Runs: 3, Steps: 4, Names: []string{"s/TP1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Flaky = %+v, want %+v", got, want)
	}
}
