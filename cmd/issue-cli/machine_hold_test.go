package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/queue"
)

// The boundary of the one machine hold, through every caller: memory at the
// line holds, the load at the limit holds, and the owner's max_load is the
// limit wherever it is set.
func TestMachineHoldBoundaryThroughEveryCaller(t *testing.T) {
	below := autostart.DefaultBelow
	cases := []struct {
		name    string
		mem     int
		load    float64
		maxLoad float64
		want    string // "": may start
	}{
		{name: "under both", mem: below - 1, load: 13.9},
		{name: "memory at the line", mem: below, load: 4, want: fmt.Sprintf("memory %d%% ≥ %d%%", below, below)},
		{name: "load at the cores", mem: 31, load: 14, want: "load 14.0 on 14 cores (max 14)"},
		{name: "load over the owner's max_load", mem: 31, load: 12, maxLoad: 10.5, want: "load 12.0 on 14 cores (max 10.5)"},
		{name: "load over the cores, under the owner's max_load", mem: 31, load: 15, maxLoad: 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for name, run := range map[string]func(t *testing.T) string{ // each returns the hold, "" when it would start
				"auto-start": func(t *testing.T) string {
					proj := makeAutostartProject(t)
					machineState(t, c.mem, c.load, c.maxLoad, yoloRoot(proj), proj.Slug)
					ctx, stdout, _ := newTestContext(proj, false)
					if err := runAutostart(ctx, []string{"next"}); err != nil {
						t.Log(err)
					}
					return holdOf(stdout.String())
				},
				"queue": func(t *testing.T) string {
					proj := makeAutostartProject(t)
					machineState(t, c.mem, c.load, c.maxLoad, yoloRoot(proj), proj.Slug)
					mustQ(t, proj, "add", "high", "--quote", queueWords)
					out, err := runQ(t, proj, "next")
					if err != nil {
						return err.Error()
					}
					return holdOf(out)
				},
				"jobs": func(t *testing.T) string {
					proj := makeJobProject(t)
					machineState(t, c.mem, c.load, c.maxLoad, yoloRoot(proj), proj.Slug)
					if _, err := runJobCmd(t, proj, "auto", "on", "marketing/weekly-seo-pass", "--quote", jobWords); err != nil {
						t.Fatal(err)
					}
					out, _ := runJobCmd(t, proj, "due")
					if i := strings.Index(out, "hold: "); i >= 0 {
						return out[i:]
					}
					return ""
				},
				"nightly": func(t *testing.T) string {
					proj := makeAutostartProject(t)
					machineState(t, c.mem, c.load, c.maxLoad, yoloRoot(proj), proj.Slug)
					return nightlyMachine(proj)
				},
			} {
				got := run(t)
				if c.want == "" && got != "" {
					t.Errorf("%s holds: %s", name, got)
				}
				if c.want != "" && !strings.Contains(got, c.want) {
					t.Errorf("%s: got %q, want %q", name, got, c.want)
				}
			}
		})
	}
}

// machineState stubs the readings and opens an auto-start window with the
// owner's max_load, the state every caller reads it from.
func machineState(t *testing.T, mem int, load, maxLoad float64, root, slug string) {
	t.Helper()
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: mem, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: load, Cores: 14} }
	st := autostart.State{Words: "start them", How: "test", By: "owner", MaxLoad: maxLoad}
	if err := autostart.TurnOn(root, slug, st, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// holdOf is the verdict's reason from `hold: …` output, "" on a start.
func holdOf(out string) string {
	s := strings.TrimSpace(out)
	if i := strings.Index(s, "hold: "); i >= 0 {
		return s[i:]
	}
	return ""
}

// The lines the docs and templates state: each is the source's number in
// the sentence that states it, and the old "over the cores" wording stays
// gone, so a changed line or a new boundary shows here.
func TestDocsStateTheMachineLines(t *testing.T) {
	below, quiet := autostart.DefaultBelow, memguard.ResumeAt
	if memory.GuardLimit != quiet {
		t.Errorf("memory.GuardLimit = %d, memguard.ResumeAt = %d", memory.GuardLimit, quiet)
	}
	if queue.DefaultBelow != below {
		t.Errorf("queue.DefaultBelow = %d, autostart.DefaultBelow = %d", queue.DefaultBelow, below)
	}
	root := filepath.Join("..", "..")
	states := []struct{ file, want string }{
		{"docs/agent-dispatch.md", fmt.Sprintf("`mem-guard used` is at or above `--below` (default %d%%)", below)},
		{"docs/agent-dispatch.md", fmt.Sprintf("memory under %d%% and **no worker cap** by default", below)},
		{"docs/departments.md", fmt.Sprintf("memory is under %d%%;", below)},
		{"docs/benchmark.md", fmt.Sprintf("Memory under %d%% (mem-guard's reading)", below)},
		{"docs/benchmark.md", fmt.Sprintf("BLOCKED, or a reading of %d%% or more", quiet)},
		{"docs/memory.md", fmt.Sprintf("At %d%% or more, when mem-guard isn't on PATH", quiet)},
		{"docs/CLI/overview.md", fmt.Sprintf("Held while memory is ≥ %d%%", below)},
		{"cmd/issue-cli/templates/jobs/nightly-improve.md", fmt.Sprintf("holds itself while memory is %d%% or more", below)},
		{"cmd/issue-cli/templates/workflow-features/memory-embedder.yaml", fmt.Sprintf("at %d%% or more", quiet)},
		{"CLAUDE.md", fmt.Sprintf("check` is blocked, stops at %d%%)", quiet)},
		{"scripts/under-load.sh", fmt.Sprintf("reports this much used (default %d)", quiet)},
	}
	banned := []string{"at most the core count", "is over the core count", "load over the cores"}
	seen := map[string]string{}
	read := func(rel string) string {
		if s, ok := seen[rel]; ok {
			return s
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		seen[rel] = string(data)
		return seen[rel]
	}
	for _, s := range states {
		if !strings.Contains(read(s.file), s.want) {
			t.Errorf("%s no longer says %q", s.file, s.want)
		}
	}
	for rel, s := range seen {
		for _, b := range banned {
			if strings.Contains(s, b) {
				t.Errorf("%s says %q: a start holds at the limit, the load must be under it", rel, b)
			}
		}
	}
}

// The help text is built from the constants.
func TestHelpStatesTheLine(t *testing.T) {
	want := fmt.Sprintf("--below %d", autostart.DefaultBelow)
	if !strings.Contains(autostartCommand.LongHelp, want) {
		t.Errorf("autostart help lost %q", want)
	}
	want = fmt.Sprintf("memory is %d%% or more", autostart.DefaultBelow)
	if !strings.Contains(strings.Join(strings.Fields(benchCommand.LongHelp), " "), want) {
		t.Errorf("bench help lost %q", want)
	}
}
