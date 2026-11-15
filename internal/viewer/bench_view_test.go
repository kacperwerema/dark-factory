package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestReportBenchCardShowsTheLastRunAgainstThePreviousOne(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	proj := &tracker.Project{Slug: "orchard"}
	if got := reportBench(proj, time.Now()); got != nil {
		t.Fatalf("no benchmark: card = %+v", got)
	}
	b := bench.New("factory-v1")
	b.Tasks = []bench.Task{{Project: "orchard", Slug: "a"}, {Project: "orchard", Slug: "b"}, {Project: "orchard", Slug: "secret-one", Holdout: true}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	other := bench.New("golf")
	other.Tasks = []bench.Task{{Project: "other-game", Slug: "x"}}
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}
	card := reportBench(proj, time.Now())
	if card == nil || len(card.Rows) != 1 || !card.Rows[0].NoRun || card.Rows[0].Cmd != "issue-cli bench run factory-v1" {
		t.Fatalf("no runs yet: card = %+v", card)
	}

	rec := func(task, outcome string, holdout bool) bench.TaskRecord {
		return bench.TaskRecord{Task: task, Outcome: outcome, Holdout: holdout, Judge: -1, Usage: bench.Usage{Known: true, Input: 1500, USD: 0.5}}
	}
	start := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	for i, records := range [][]bench.TaskRecord{
		{rec("orchard/a", bench.OutcomePass, false), rec("orchard/b", bench.OutcomePass, false), rec("orchard/secret-one", bench.OutcomePass, true)},
		{rec("orchard/a", bench.OutcomePass, false), rec("orchard/b", bench.OutcomeFail, false), rec("orchard/secret-one", bench.OutcomeFail, true)},
	} {
		at := start.Add(time.Duration(i) * time.Hour)
		r := &bench.Run{ID: at.Format("20060102-150405"), Benchmark: "factory-v1", Started: at, Ended: at.Add(30 * time.Minute), Stop: bench.StopDone, SpentTokens: 4500, SpentUSD: 1.5}
		if i == 1 {
			r.Setup.Label = "new-prompts"
		}
		if err := bench.Save(r, records); err != nil {
			t.Fatal(err)
		}
	}
	card = reportBench(proj, start.Add(3*time.Hour))
	row := card.Rows[0]
	if row.NoRun || row.Pass != "1/2 passed" || row.Holdout != "holdout 0/1" || row.Label != "new-prompts" || row.When != "2h ago" {
		t.Fatalf("row = %+v", row)
	}
	if row.Tone != "red" || !strings.Contains(row.Delta, "-50 points vs 20261030-080000") || !strings.Contains(row.Delta, "1 regression") {
		t.Fatalf("delta %q tone %q", row.Delta, row.Tone)
	}

	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&out, "wb/bench", card); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`data-testid="report.bench"`, "1/2 passed · holdout 0/1", "wb-chip--you", "issue-cli bench run factory-v1"} {
		if !strings.Contains(html, want) {
			t.Errorf("card lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "secret-one") {
		t.Errorf("the card names a holdout task:\n%s", html)
	}
	u := unlockBench(WbUnlock{Total: 1, Done: 1, Items: []WbUnlockItem{{Key: "review", On: true}}}, false)
	if u.Total != 2 || u.Done != 1 || u.Items[0].Key != "benchmark" || u.Items[0].Code != "issue-cli bench candidates" {
		t.Errorf("To unlock = %+v", u)
	}
}

func TestBenchSeriesIsOneLinePerBenchmarkFromTheSnapshots(t *testing.T) {
	snap := func(date string, rows ...health.Row) *health.Snapshot {
		return &health.Snapshot{Run: health.RunInfo{Date: date}, Metrics: rows}
	}
	row := func(bench string, v float64) health.Row {
		return health.Row{Metric: "bench.pass_rate", Labels: map[string]string{"benchmark": bench, "setup": "baseline"}, Value: v}
	}
	got := benchSeries([]*health.Snapshot{
		snap("2026-10-29T10:00", row("factory-v1", 0.5)),
		snap("2026-10-30T10:00", row("factory-v1", 0.75), row("app", 1)),
		snap("2026-10-30T11:00"),
	})
	if len(got) != 2 || got[0].Name != "app" || got[1].Name != "factory-v1" || len(got[1].Points) != 2 || got[1].Points[1].Value != 0.75 || got[1].Points[0].Note != "baseline" {
		t.Fatalf("series = %+v", got)
	}
	if benchSeries([]*health.Snapshot{snap("2026-10-30T10:00")}) != nil {
		t.Error("no bench rows must give no series (no card)")
	}
}

func TestReportBenchCardShowsTheNewestTwoAgentRunAndTheRuleItContradicts(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	wf := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(wf, []byte("statuses:\n  - name: backlog\n  - name: done\nrouting:\n  default: claude\n  rules:\n    - {type: bugfix, agent: claude}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "orchard", IssueDir: filepath.Join(dir, "issues"), WorkflowFile: wf}
	b := bench.New("ab")
	b.Tasks = []bench.Task{{Project: "orchard", Slug: "a"}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	rec := func(task, agent, outcome string, usd float64) bench.TaskRecord {
		return bench.TaskRecord{Task: "orchard/" + task, Type: "bugfix", Agent: agent, Outcome: outcome, Judge: -1, WallSec: 60,
			Usage: bench.Usage{Known: true, Input: 2000, USD: usd, USDKnown: usd > 0, Model: agent + "-model"}}
	}
	at := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	var records []bench.TaskRecord
	for _, task := range []string{"a", "b", "c"} {
		records = append(records, rec(task, "claude", bench.OutcomeFail, 0.5), rec(task, "codex", bench.OutcomePass, 0))
	}
	records = append(records, bench.TaskRecord{Task: "orchard/secret-one", Type: "bugfix", Agent: "codex", Holdout: true, Outcome: bench.OutcomePass, Judge: -1})
	if err := bench.Save(&bench.Run{ID: "ab-run", Benchmark: "ab", Started: at, Ended: at.Add(time.Hour), Stop: bench.StopDone, Setup: bench.Setup{Agent: "claude,codex"}}, records); err != nil {
		t.Fatal(err)
	}
	// A later one-agent run doesn't hide it.
	later := at.Add(5 * time.Hour)
	if err := bench.Save(&bench.Run{ID: "nightly", Benchmark: "ab", Started: later, Ended: later.Add(time.Hour), Stop: bench.StopDone, Setup: bench.Setup{Agent: "claude"}}, []bench.TaskRecord{{Task: "orchard/a", Outcome: bench.OutcomePass}}); err != nil {
		t.Fatal(err)
	}
	card := reportBench(proj, at.Add(8*time.Hour))
	a := card.Rows[0].Agents
	if a == nil || a.RunID != "ab-run" || len(a.Rows) != 4 {
		t.Fatalf("agents = %+v", a)
	}
	if len(a.Routing) != 1 || a.Routing[0].Tone != "red" || !strings.Contains(a.Routing[0].Text, "type bugfix → claude contradicted: codex passed 3/3, claude 0/3") {
		t.Fatalf("routing = %+v", a.Routing)
	}
	if !strings.Contains(a.Prices, "no prices.yaml") || !strings.Contains(a.Prices, "codex-model") {
		t.Fatalf("prices = %q", a.Prices)
	}
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&out, "wb/bench", card); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`data-testid="report.bench.agents"`, `data-testid="report.bench.agent.bugfix.codex"`, `data-testid="report.bench.routing"`, `data-testid="report.bench.prices"`, "3/3 100%", "cost unknown", "$0.50", "wb-chip--you"} {
		if !strings.Contains(html, want) {
			t.Errorf("card lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "secret-one") {
		t.Errorf("the card names a holdout task:\n%s", html)
	}
}
