package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestSidecarBytesUnchanged: the stats and data sidecars and a goal job's
// metrics file are written byte for byte as before they moved onto
// jsonfile.Write (the goldens were written by the old code), at 0644.
// UPDATE_GOLDEN=1 rewrites the goldens.
func TestSidecarBytesUnchanged(t *testing.T) {
	p := salesProject(t)
	issue := filepath.Join(p.IssueDir, "Factory", "x.md")
	if err := os.MkdirAll(filepath.Dir(issue), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC)
	tokens := 1200
	if err := tracker.SaveStats(issue, tracker.StatsStore{Transitions: []tracker.TransitionStat{
		{From: "idea", To: "in progress", TS: ts, StaticTokens: 10, DynamicTokens: 20, ActualTokens: &tokens},
		{From: "in progress", To: "testing", TS: ts.Add(time.Hour), BranchHead: "0fdbaaf1"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SaveData(issue, tracker.DataStore{
		NextID:     3,
		Entries:    []tracker.DataEntry{{ID: 1, Description: "a <b> & \"c\"", Status: "pass", Comment: "ok"}, {ID: 2, Description: "é", Status: "todo", Tier: "t2"}},
		Experiment: json.RawMessage(`{"a":1,"b":[2,3]}`),
	}); err != nil {
		t.Fatal(err)
	}
	jw, err := WriteMetrics(p, salesJob(t, p), Write{Date: "2026-11-15", Today: "2026-11-15", Metrics: []Metric{{ID: "visits", Value: 410}, {ID: "signups", Value: 12.5}}})
	if err != nil {
		t.Fatal(err)
	}
	for golden, path := range map[string]string{
		"stats.json": tracker.StatsSidecarPath(issue),
		"data.json":  tracker.SidecarPath(issue),
		"job.json":   jw.Path,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join("testdata", "sidecar-bytes", golden)
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(want, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		wantBytes, err := os.ReadFile(want)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(wantBytes) {
			t.Errorf("%s changed:\n got %q\nwant %q", golden, got, wantBytes)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode %v, %v; want 0644", golden, info.Mode().Perm(), err)
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".json" && filepath.Ext(e.Name()) != ".md" {
				t.Errorf("left behind beside %s: %s", golden, e.Name())
			}
		}
	}
}
