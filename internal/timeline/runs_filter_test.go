package timeline

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Without a cache, LoadPlaytestRuns parses only the results.yaml files that
// name the issue: the checkout holds every issue's runs, and parsing them all
// made each prove call slow. The issue's own runs still come back, a broken
// one of its own is still reported, and a cache still reads every run.
func TestPlaytestRunsWithoutACacheParseOnlyTheIssuesFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(session, run, body string) string {
		p := filepath.Join(dir, ".playtest", "runs", session, run, "results.yaml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var others []string
	for i := 0; i < 20; i++ {
		others = append(others, write("other", "r"+strconv.Itoa(i), "session: other\nissue: cli/other\nmode: auto\nstarted: \"2026-11-01T10:00:00Z\"\nsteps:\n  - id: TP1\n    status: pass\n"))
	}
	write("mine", "r1", "session: mine\nissue: \"cli/mine\"\nmode: auto\nstarted: \"2026-11-02T10:00:00Z\"\nsteps:\n  - id: TP1\n    status: pass\n")
	write("mine", "r2", "session: mine\nissue: cli/mine\nstarted: [\n")

	runs, problems := LoadPlaytestRuns(nil, "cli/mine", dir)
	if len(runs) != 1 || runs[0].Session != "mine" || len(runs[0].Steps) != 1 {
		t.Fatalf("runs = %+v, want the one good run of cli/mine", runs)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "r2") {
		t.Errorf("problems = %q, want the broken run of cli/mine", problems)
	}
	parsedResults.Lock()
	for _, p := range others {
		if _, ok := parsedResults.m[p]; ok {
			t.Errorf("%s was parsed: it names another issue", p)
		}
	}
	parsedResults.Unlock()

	if got := NewCache().results(dir); len(got) != 22 {
		t.Errorf("a cache read %d runs, want all 22", len(got))
	}
	if runs, _ := LoadPlaytestRuns(nil, "cli/other", dir); len(runs) != 1 {
		t.Errorf("cli/other after the cache read them: %d runs, want 1 (they share a started:)", len(runs))
	}
}
