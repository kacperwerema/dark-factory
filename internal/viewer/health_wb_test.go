package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// withReviewLive stubs whether the review agent's tmux session runs.
func withReviewLive(t *testing.T, live bool) {
	t.Helper()
	orig := agentSessionLive
	agentSessionLive = func(_ *tracker.Project, name string) bool { return live && strings.HasPrefix(name, "review-") }
	t.Cleanup(func() { agentSessionLive = orig })
}

func healthSnapshotCount(t *testing.T, root string) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(health.SnapshotsDir(root), "*.json"))
	return len(files)
}

func TestHealthWbPageRenders(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withReviewLive(t, false)
	now := time.Now()
	proj, root := healthProject(t, now)
	before := healthSnapshotCount(t, root)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, page := getBody(t, ts.URL+"/p/test-project/health")
	for _, want := range []string{
		`data-testid="health.page"`, `data-testid="hall.tabs"`, `aria-current="page" data-testid="hall.tab.health"`,
		`href="/p/test-project/hall" data-testid="hall.tab.line"`, `href="/p/test-project/status" data-testid="hall.tab.report"`,
		`data-testid="health.range.30d"`, `href="/p/test-project/health?classic=1" data-testid="health.classic"`,
		`data-testid="health.score.value">86<`, `data-testid="health.score.bar"`,
		`data-testid="health.sensor_group.code"`, `data-testid="health.sensor_group.tests"`,
		`data-testid="health.sensor.big_files"`, "good 5.0%", "18.2%", `data-testid="health.sensor.flaky"`, "No data: Needs the playtest telemetry",
		`data-testid="health.opinion.value">78<`, "Move the Chrome tests to the e2e tier", `data-testid="health.file_recommendation"`,
		`action="/p/test-project/health/recommendation/R1/file"`, `action="/p/test-project/health/review"`, `data-testid="health.run_review"`,
		`data-testid="health.trend.scores"`, `data-testid="health.trend.unit_tests"`, "issue-cli ci run --tier slow",
		`data-testid="health.list"`, "static/style.css", `data-testid="health.setup"`, `data-testid="health.lint.cmd"`,
		"Product hall", "/static/workshop-health.js",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("health page lacks %s", want)
		}
	}
	// It is a Workshop Board page: no classic sidebar, no classic gauge.
	for _, unwanted := range []string{`data-testid="nav.tab.health"`, `data-testid="health.score_gauge"`} {
		if strings.Contains(page, unwanted) {
			t.Errorf("health page has %s", unwanted)
		}
	}

	_, classic := getBody(t, ts.URL+"/p/test-project/health?classic=1&range=7d")
	for _, want := range []string{`data-testid="health.score_gauge"`, `data-testid="nav.tab.health"`, `data-testid="health.component.big_files"`} {
		if !strings.Contains(classic, want) {
			t.Errorf("classic page lacks %s", want)
		}
	}

	_, hall := getBody(t, ts.URL+"/p/test-project/hall")
	if !strings.Contains(hall, `aria-current="page" data-testid="hall.tab.line"`) || !strings.Contains(hall, `href="/p/test-project/health" data-testid="hall.tab.health"`) {
		t.Error("the hall's header lacks the Line | Report | Health toggle")
	}

	if after := healthSnapshotCount(t, root); after != before {
		t.Errorf("rendering wrote snapshots: %d → %d", before, after)
	}
}

func TestHealthWbReviewRunning(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withReviewLive(t, true)
	now := time.Now()
	proj, _ := healthProject(t, now)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, page := getBody(t, ts.URL+"/p/test-project/health?review=review-test-project")
	for _, want := range []string{`data-testid="health.opinion.tmux"`, "tmux attach -t review-test-project", "reviewing the code", `data-testid="health.flash"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page, `data-testid="health.run_review"`) {
		t.Error("a running review offers no second one")
	}
}

func TestBuildHealthWbWithoutScore(t *testing.T) {
	now := time.Now()
	proj, root := setupTestProject(t)
	in := healthWbIn{healthIn: healthIn{Proj: &proj, Prefix: "/p/x", Range: "30d", Root: root, Now: now}, Review: "review-x",
		CEO: WbMessageBox{Session: "lead-x", SendURL: "/p/x/lead/send"}}

	// No snapshot at all: honest empty states, never a number.
	p := buildHealthWb(in, buildHealthPage(in.healthIn))
	if p.Score.Empty == "" || p.Score.Value != "" || len(p.Groups) != 0 || p.Look.Empty == "" {
		t.Errorf("no snapshot: score %+v, groups %d, look %+v", p.Score, len(p.Groups), p.Look)
	}
	if p.Opinion.Has || p.Opinion.Empty == "" || p.Opinion.ReviewURL != "/p/x/health/review" {
		t.Errorf("no review: %+v", p.Opinion)
	}
	for _, tr := range p.Trends {
		if !tr.Locked || tr.Cmd == "" {
			t.Errorf("trend without data must be locked with its command: %+v", tr)
		}
	}

	// Under 50% of the weight measured: the share, the bar and the way on.
	s := healthSnap(now.Add(-time.Hour), map[string]float64{
		"score.coverage":                       0.364,
		"score.component{component=big_files}": 27, "score.component.value{component=big_files}": 0.232, "score.component.weight{component=big_files}": 15,
		"score.component{component=lint}": 100, "score.component.value{component=lint}": 0, "score.component.weight{component=lint}": 10,
		"score.component{component=long_funcs}": 74, "score.component.value{component=long_funcs}": 0.041, "score.component.weight{component=long_funcs}": 10,
		"score.component{component=hotspots}": 66, "score.component.value{component=hotspots}": 0.236, "score.component.weight{component=hotspots}": 5,
		"score.component.weight{component=unit_wait}": 20, "score.component.weight{component=flaky}": 10,
		"score.component.weight{component=e2e_wait}": 10, "score.component.weight{component=e2e_coverage}": 10,
		"score.component.weight{component=unit_cost_trend}": 10, "score.component.weight{component=test_balance}": 10,
		"shape.hotspot{path=web/Plan.tsx}":                   23900,
		"shape.hotspot{path=web/styles.css}":                 246000,
		"shape.long_func.lines{func=Plan,path=web/Plan.tsx}": 1000,
		"shape.long_func.lines{func=Chat,path=web/Chat.tsx}": 399,
	})
	in.Snaps, in.Snapshot = []*health.Snapshot{s}, s
	p = buildHealthWb(in, buildHealthPage(in.healthIn))
	sc := p.Score
	if sc.Has || sc.Value != "36%" || sc.Measured != 4 || sc.Sensors != 10 || len(sc.Bar.Segs) != 10 {
		t.Fatalf("score = %+v", sc)
	}
	if sc.Bar.Segs[0].Tone != "bad" || sc.Bar.Segs[1].Tone != "good" || sc.Bar.Segs[9].Tone != "none" {
		t.Errorf("segments: measured first, coloured by sub-score, then no data: %+v", sc.Bar.Segs)
	}
	if h := sc.Hint; h == nil || h.Sensor != "unit_wait" || h.Reach != 55 || h.Eyebrow != "SHORTEST WAY TO A SCORE" || !strings.Contains(h.Give.Text, "unit_wait") || h.Give.Live {
		t.Errorf("hint = %+v", sc.Hint)
	}
	if len(p.Groups) != 2 || p.Groups[0].Name != "Code" || p.Groups[0].Tag.Text != "4/4 measured" || p.Groups[1].Tag.Text != "0/6 — no data" || p.Groups[1].Weight != "weight 70 of 110" {
		t.Errorf("groups = %+v", p.Groups)
	}
	big := p.Groups[0].Rows[0]
	if big.Name != "big_files" || big.Value != "23.2%" || big.Score != "27" || big.Tone != "bad" || big.Pos != "71.0%" || big.Good != "5.0%" {
		t.Errorf("big_files = %+v", big)
	}
	if p.Look.Both != "web/Plan.tsx" || p.Look.Refactor == nil || p.Look.Refactor.Base != "Plan.tsx" || !p.Look.Lists[0].Rows[1].Hot || p.Look.Lists[0].Rows[0].Hot {
		t.Errorf("look = %+v", p.Look)
	}
	if !strings.Contains(p.Look.Refactor.Body, "Plan (1k lines)") {
		t.Errorf("refactor body = %q", p.Look.Refactor.Body)
	}
}

func TestHealthWbComponentsStandAlone(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, data any) string {
		t.Helper()
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return b.String()
	}
	for _, c := range []struct {
		name string
		data any
		want []string
	}{
		{"wb/hall-tabs", wbHallTabs("/p/x", "report"), []string{`aria-current="page" data-testid="hall.tab.report">Report<`, `href="/p/x/hall"`}},
		{"wb/sensor", WbSensor{Name: "lint", Help: "Lint warnings", Has: true, Value: "0", Pos: "4.0%", Good: "0", Bad: "20", Score: "100", Tone: "good", Weight: "10"},
			[]string{`data-testid="health.sensor.lint"`, "left:4.0%", "wb-score--good", "good 0"}},
		{"wb/sensor", WbSensor{Name: "flaky", Range: "0.0% → 5.0%", Why: "Needs telemetry.", Score: "—", Tone: "none", Weight: "10"},
			[]string{"wb-fog", "No data: Needs telemetry.", "0.0% → 5.0%"}},
		{"wb/weight-bar", WbWeightBar{Threshold: 50, Label: "36%", Segs: []WbWeightSeg{{Name: "a", Tone: "good", Weight: 15}, {Name: "b", Tone: "none", Weight: 20}}},
			[]string{"threshold 50%", "wb-wbar-seg--none", "flex:15"}},
		{"wb/trend-card", WbTrendCard{Key: "scores", Title: "Health score", Locked: true, Need: "Compute it backwards.", Cmd: "issue-cli health backfill"},
			[]string{`data-testid="health.trend.scores"`, "wb-trend--locked", "issue-cli health backfill"}},
		{"wb/give-agent", WbGiveAgent{Label: "Give it to an agent", Text: "do it", SendURL: "/p/x/lead/send", Session: "lead-x"},
			[]string{`data-send="/p/x/lead/send"`, `value="do it"`, "disabled", "CEO not running"}},
		{"wb/copy-cmd", "tmux attach -t review-x", []string{`data-copy="tmux attach -t review-x"`}},
		{"wb/refactor", &WbRefactor{Base: "a.go", CreateURL: "/p/x/issues/create", Title: "Refactor a.go", Body: "b"}, []string{`data-create-issue="/p/x/issues/create"`, "File a refactor issue for a.go"}},
		{"wb/refactor", (*WbRefactor)(nil), nil},
	} {
		out := render(c.name, c.data)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s lacks %q:\n%s", c.name, w, out)
			}
		}
	}
}

func TestHealthWbNoSnapshotPage(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withReviewLive(t, false)
	proj, root := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, page := getBody(t, ts.URL+"/p/test-project/health")
	if code != 200 || !strings.Contains(page, `data-testid="health.score.empty"`) || !strings.Contains(page, `data-testid="health.opinion.empty"`) {
		t.Errorf("no snapshot: %d\n%s", code, page)
	}
	if _, err := os.Stat(health.SnapshotsDir(root)); err == nil {
		t.Error("rendering must not take a snapshot")
	}
}
