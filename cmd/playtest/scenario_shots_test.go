package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// scenarioShotsAgeOut says why a scenario's committed playtest runs would be
// rewritten by its own viewer, "" when they won't. The daily health pass
// shrinks the shots of issues done 30 days ago, and a fixture's date only
// gets older: a page that links its .png passes until the day the run ages
// out. A scenario holding runs turns the shrink off.
func scenarioShotsAgeOut(dir string) (string, error) {
	var runs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "results.yaml" || !strings.Contains(filepath.ToSlash(p), "/.playtest/runs/") {
			return nil
		}
		runs = append(runs, p)
		return nil
	})
	if err != nil || len(runs) == 0 {
		return "", err
	}
	spec, err := LoadScenarioSpec(dir)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, spec.Workflow))
	if err != nil {
		return "", err
	}
	var wf struct {
		Health *tracker.HealthConfig `yaml:"health"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return "", err
	}
	if after, on := wf.Health.ShrinkShotsAfter(); on {
		return filepath.Base(dir) + ": " + spec.Workflow + " shrinks runs after " + after.String() + " (add health: shrink_shots_after_days: 0): " + runs[0], nil
	}
	return "", nil
}

// TestScenarioShotsDontAgeOut: every scenario with dated .playtest/runs keeps
// them as committed, so a session never starts failing with the calendar.
func TestScenarioShotsDontAgeOut(t *testing.T) {
	root := filepath.Join(repoDir(t), "playtest", "scenarios")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		why, err := scenarioShotsAgeOut(filepath.Join(root, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		} else if why != "" {
			t.Error(why)
		}
	}
}

// TestScenarioShotsAgeOutSpotsARun pins what the guard above looks at, on
// fixtures of its own: the live scenarios all pass once they're fixed.
func TestScenarioShotsAgeOutSpotsARun(t *testing.T) {
	cases := []struct {
		name, workflow string
		root           string // the folder holding .playtest/runs
		flagged        bool
	}{
		{name: "no health block", workflow: "statuses: []\n", root: "files", flagged: true},
		{name: "a longer shrink", workflow: "health:\n  shrink_shots_after_days: 300\n", root: "files", flagged: true},
		{name: "a run in a commit's overlay", workflow: "statuses: []\n", root: "second", flagged: true},
		{name: "shrink off", workflow: "health:\n  shrink_shots_after_days: 0\n", root: "files"},
		{name: "no runs", workflow: "statuses: []\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(rel, body string) {
				t.Helper()
				p := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("scenario.yaml", "title: t\nworkflow: flow.yaml\n")
			write("flow.yaml", c.workflow)
			if c.root != "" {
				write(filepath.Join(c.root, ".playtest", "runs", "s", "20261001-100000.000-auto", "results.yaml"), "session: s\n")
			}
			why, err := scenarioShotsAgeOut(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := why != ""; got != c.flagged {
				t.Errorf("flagged = %v, want %v (%q)", got, c.flagged, why)
			}
			if c.flagged && !strings.Contains(why, "shrink_shots_after_days: 0") {
				t.Errorf("the failure doesn't name the fix: %q", why)
			}
		})
	}
}
