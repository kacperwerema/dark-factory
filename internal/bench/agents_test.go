package bench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// codexWorker is a stand-in Codex: it leaves Add wrong, writes a rollout file
// in its own home with a --json style line on stdout that must not be read.
const codexWorker = `cat > /dev/null; d="$HOME/.codex/sessions/2026/11/05"; mkdir -p "$d"; f="$d/rollout-1.jsonl"; ` +
	`printf '{"type":"session_meta","payload":{"id":"c1","cwd":"%s","cli_version":"0.163.0"}}\n' "$PWD" > "$f"; ` +
	`echo '{"type":"turn_context","payload":{"model":"gpt-stand-in"}}' >> "$f"; ` +
	`echo '{"type":"response_item","payload":{"type":"message","content":"never read"}}' >> "$f"; ` +
	`echo '{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":60,"cache_write_input_tokens":5,"output_tokens":10}}}}' >> "$f"; ` +
	`echo '{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"cache_write_input_tokens":5,"output_tokens":50}}}}' >> "$f"; ` +
	`echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'`

func (f *fixture) twoAgents(t *testing.T) *Benchmark {
	t.Helper()
	b := f.benchmark("", f.task)
	b.Workers = map[string]string{AgentClaude: fixWorker, AgentCodex: codexWorker}
	return b
}

var bothAgents = Options{Agents: []string{AgentClaude, AgentCodex}, Setup: Setup{Agent: "claude,codex"}}

func TestTwoAgentRunRecordsBothInRunJSON(t *testing.T) {
	parallel(t)
	f := newFixture(t)
	env := f.env(t)
	env.Keep = true
	run, err := Execute(context.Background(), f.twoAgents(t), env, bothAgents)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := LoadRun(run.Benchmark, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Setup.Agent != "claude,codex" || !slices.Equal(saved.Setup.Agents(), []string{AgentClaude, AgentCodex}) || len(saved.Tasks) != 2 {
		t.Fatalf("run.json = %+v", saved)
	}
	cl, cx := saved.Tasks[0], saved.Tasks[1]
	if cl.Agent != AgentClaude || cl.Outcome != OutcomePass || cl.Usage.Tokens() != 4200 || cl.Key() != "calc/calc/fix-add@claude" {
		t.Errorf("claude record = %+v", cl)
	}
	if cx.Agent != AgentCodex || cx.Outcome != OutcomeFail {
		t.Errorf("codex record = %+v", cx)
	}
	// From the rollout's last total, not the --json line; each agent its own sandbox.
	u := cx.Usage
	if !u.Known || u.Source != "rollout" || u.Input != 400 || u.CacheRead != 600 || u.CacheWrite != 5 || u.Output != 50 || u.Model != "gpt-stand-in" || u.ToolVersion != "0.163.0" || u.SessionID != "c1" {
		t.Errorf("codex usage = %+v", u)
	}
	if u.USDKnown || u.USD != 0 {
		t.Errorf("no prices: the cost must stay unknown: %+v", u)
	}
	for _, n := range []string{"01", "02"} {
		if _, err := os.Stat(filepath.Join(env.TmpRoot, run.ID, n, "repo")); err != nil {
			t.Errorf("sandbox %s: %v", n, err)
		}
	}
	if got := saved.CostText(); got != "≥ $0.25" {
		t.Errorf("CostText = %q", got)
	}
}

func TestTwoAgentRunPricesCodexFromPricesAlone(t *testing.T) {
	parallel(t)
	f := newFixture(t)
	b := f.twoAgents(t)
	b.Prices = map[string]Price{"gpt-stand": {In: 1, Out: 2, CacheRead: 0.5, CacheWrite: 1}}
	run, err := Execute(context.Background(), b, f.env(t), bothAgents)
	if err != nil {
		t.Fatal(err)
	}
	u := run.Tasks[1].Usage
	want := (400.0 + 100 + 300 + 5) / 1e6
	if !u.USDKnown || u.USD < want*0.999 || u.USD > want*1.001 {
		t.Errorf("usd = %v known %v, want %v", u.USD, u.USDKnown, want)
	}
}

func TestTwoAgentRunResumesEachAgentsMissingTask(t *testing.T) {
	parallel(t)
	f := newFixture(t)
	env := f.env(t)
	calls := 0
	env.MemGuard = func() (bool, string) { calls++; return calls > 1, "BLOCKED: used 90%" }
	paused, err := Execute(context.Background(), f.twoAgents(t), env, bothAgents)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Stop != StopPaused || len(paused.Tasks) != 1 || paused.Tasks[0].Agent != AgentClaude {
		t.Fatalf("paused = %+v", paused)
	}
	env.MemGuard = nil
	// The agents come from the run, not the options.
	resumed, err := Execute(context.Background(), f.twoAgents(t), env, Options{Resume: paused.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Stop != StopDone || len(resumed.Tasks) != 2 || resumed.Tasks[1].Agent != AgentCodex {
		t.Fatalf("resumed = %+v", resumed)
	}
}

func TestDefaultTokenCapCountsEveryAgent(t *testing.T) {
	parallel(t)
	f := newFixture(t)
	b := f.twoAgents(t)
	// Over one task's share: a cap counting tasks would stop before codex.
	b.Workers[AgentClaude] = `cat > /dev/null; echo '{"usage":{"input_tokens":30000000,"output_tokens":0}}'`
	run, err := Execute(context.Background(), b, f.env(t), bothAgents)
	if err != nil {
		t.Fatal(err)
	}
	if run.Stop != StopDone || run.Tasks[1].Outcome != OutcomeFail {
		t.Fatalf("stop %s, tasks %+v", run.Stop, run.Tasks)
	}
}

func TestBudgetStopRecordsEachAgentsRest(t *testing.T) {
	parallel(t)
	f := newFixture(t)
	b := f.twoAgents(t)
	b.Budget.Tokens = 100
	run, err := Execute(context.Background(), b, f.env(t), bothAgents)
	if err != nil {
		t.Fatal(err)
	}
	if run.Stop != StopBudget || len(run.Tasks) != 2 || run.Tasks[1].Agent != AgentCodex || run.Tasks[1].Outcome != OutcomeBudget {
		t.Fatalf("stop %s, tasks %+v", run.Stop, run.Tasks)
	}
}

func TestOneAgentRunRecordsNoAgentOnItsTasks(t *testing.T) {
	parallel(t)
	f := newFixture(t)
	run, err := Execute(context.Background(), f.benchmark(fixWorker, f.task), f.env(t), Options{Setup: Setup{Agent: AgentClaude}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].Agent != "" || run.Tasks[0].Key() != run.Tasks[0].Task {
		t.Fatalf("record = %+v", run.Tasks[0])
	}
	data, _ := os.ReadFile(filepath.Join(RunDir(run.Benchmark, run.ID), "run.json"))
	var raw struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Tasks[0]["agent"]; ok {
		t.Errorf("run.json names an agent on a one-agent run's task: %s", data)
	}
}

func TestACodexWorkerThatNeverRanIsNoLostTask(t *testing.T) {
	parallel(t)
	f := newFixture(t)
	b := f.twoAgents(t)
	b.Workers[AgentCodex] = `cat > /dev/null; echo 'stream disconnected' >&2; exit 1`
	run, err := Execute(context.Background(), b, f.env(t), bothAgents)
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[1]
	if r.Outcome != OutcomeError || !strings.Contains(r.Reason, "stream disconnected") {
		t.Fatalf("record = %+v", r)
	}
	row, _ := ByAgent(run).Row(AgentCodex, AllTypes)
	if row.Scored != 0 || row.NotScored != 1 {
		t.Fatalf("row = %+v", row)
	}
}

func TestReadCodexSessionsTakesOnlyTheSandboxsSessions(t *testing.T) {
	dir := t.TempDir()
	mine, other := filepath.Join(t.TempDir(), "sb"), filepath.Join(t.TempDir(), "elsewhere")
	line := func(cwd string, in int) string {
		meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "x", "cwd": cwd}})
		count, _ := json.Marshal(map[string]any{"type": "event_msg", "payload": map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": map[string]any{"input_tokens": in, "cached_input_tokens": 1, "output_tokens": 2}}}})
		return string(meta) + "\n" + string(count) + "\n"
	}
	write(t, filepath.Join(dir, "2026", "rollout-a.jsonl"), line(filepath.Join(mine, "repo"), 10))
	write(t, filepath.Join(dir, "2026", "rollout-b.jsonl"), line(mine, 20))
	write(t, filepath.Join(dir, "2026", "rollout-c.jsonl"), line(other, 1000))
	write(t, filepath.Join(dir, "2026", "notes.jsonl"), line(mine, 1000))
	tr := ReadCodexSessions(dir, time.Time{}, mine)
	if !tr.Usage.Known || tr.Usage.Input != 28 || tr.Usage.CacheRead != 2 || tr.Usage.Output != 4 {
		t.Fatalf("usage = %+v", tr.Usage)
	}
	if got := ReadCodexSessions(dir, time.Now().Add(time.Hour), mine); got.Usage.Known {
		t.Errorf("files before since were read: %+v", got.Usage)
	}
	if got := ReadCodexSessions(filepath.Join(dir, "none"), time.Time{}, mine); got.Usage.Known {
		t.Errorf("missing dir: %+v", got.Usage)
	}
}

func TestDefaultCodexWorkerNeverBypassesCodexsSandbox(t *testing.T) {
	if strings.Contains(DefaultCodexWorker, "bypass") || strings.Contains(DefaultCodexWorker, "danger") {
		t.Errorf("default worker: %s", DefaultCodexWorker)
	}
	for _, want := range []string{"workspace-write", "$BENCH_SANDBOX", "$BENCH_GOCACHE"} {
		if !strings.Contains(DefaultCodexWorker, want) {
			t.Errorf("default worker lacks %s: %s", want, DefaultCodexWorker)
		}
	}
	b := New("x")
	if w, _ := b.WorkerFor(AgentCodex); w != DefaultCodexWorker {
		t.Errorf("codex default = %s", w)
	}
	b.Worker = "/opt/claude -p"
	if w, _ := b.WorkerFor(AgentClaude); w != b.Worker {
		t.Errorf("worker: on that CLI = %s", w)
	}
	b.Workers = map[string]string{AgentClaude: "stand-in"}
	if w, _ := b.WorkerFor(AgentClaude); w != "stand-in" {
		t.Errorf("workers: wins = %s", w)
	}
	if _, err := b.WorkerFor("gemini"); err == nil {
		t.Error("an unknown agent needs workers:")
	}
}

// rec is a finished record on agent.
func rec(task, typ, agent, outcome string, wall float64, tok int64, usd float64) TaskRecord {
	return TaskRecord{Task: "p/" + task, Type: typ, Agent: agent, Outcome: outcome, Judge: -1, WallSec: wall,
		Usage: Usage{Known: true, Input: tok, USD: usd, USDKnown: usd > 0, Model: agent + "-model"}}
}

func twoAgentRun(id string, records ...TaskRecord) *Run {
	r := &Run{ID: id, Benchmark: "ab", Stop: StopDone, Ended: time.Now(), Setup: Setup{Agent: "claude,codex"}}
	for _, t := range records {
		if !t.Holdout {
			r.Tasks = append(r.Tasks, t)
		}
	}
	return r
}

func TestByAgentIsPerAgentAndTypeAndLeavesTheUncountableOut(t *testing.T) {
	r := twoAgentRun("r",
		rec("a", "bugfix", "claude", OutcomePass, 60, 100, 1),
		rec("a", "bugfix", "codex", OutcomeFail, 120, 300, 0),
		rec("b", "feature", "claude", OutcomeTimeout, 300, 100, 3),
		rec("b", "feature", "codex", OutcomeError, 1, 0, 0),
		rec("c", "feature", "claude", OutcomeBroken, 1, 0, 0),
	)
	tb := ByAgent(r)
	if tb == nil || !slices.Equal(tb.Agents, []string{"claude", "codex"}) || !slices.Equal(tb.Types, []string{"bugfix", "feature"}) {
		t.Fatalf("table = %+v", tb)
	}
	if len(tb.Rows) != 6 {
		t.Fatalf("rows = %+v", tb.Rows)
	}
	all, _ := tb.Row("claude", AllTypes)
	if all.Scored != 2 || all.Passed != 1 || all.PassRate != 0.5 || all.WallSec != 180 || all.Tokens != 100 || all.USD != 2 {
		t.Errorf("all = %+v", all)
	}
	cx, _ := tb.Row("codex", "feature")
	if cx.Scored != 0 || cx.NotScored != 1 || cx.PassText() != "–" {
		t.Errorf("codex feature = %+v", cx)
	}
	bug, _ := tb.Row("codex", "bugfix")
	if bug.CostText() != "cost unknown" || bug.PassText() != "0/1 0%" {
		t.Errorf("codex bugfix: %s %s", bug.CostText(), bug.PassText())
	}
	if !slices.Equal(tb.Unpriced, []string{"codex-model"}) {
		t.Errorf("unpriced = %v", tb.Unpriced)
	}
	partly := AgentRow{Scored: 3, Priced: 2, USD: 1.5}
	if partly.CostText() != "$1.50 (2 of 3 priced)" {
		t.Errorf("partly priced: %s", partly.CostText())
	}
}

func TestByAgentNeedsTwoAgentsAndTakesThemAcrossRuns(t *testing.T) {
	one := func(id, agent string) *Run {
		return &Run{ID: id, Setup: Setup{Agent: agent}, Tasks: []TaskRecord{{Task: "p/a", Type: "tweak", Outcome: OutcomePass}}}
	}
	if ByAgent(one("a", "claude")) != nil {
		t.Error("one agent makes no table")
	}
	if ByAgent(one("a", "claude"), one("b", "claude")) != nil {
		t.Error("two runs of one agent make no table")
	}
	tb := ByAgent(one("a", "claude"), one("b", "codex"))
	if tb == nil || !slices.Equal(tb.Agents, []string{"claude", "codex"}) {
		t.Fatalf("table = %+v", tb)
	}
	old := &Run{ID: "old", Tasks: []TaskRecord{{Task: "p/a", Outcome: OutcomePass}}}
	if got := ByAgent(old, one("b", "codex")); got != nil {
		t.Errorf("a run with no agent recorded: %+v", got)
	}
}

var routed = &tracker.RoutingConfig{Default: "claude", Rules: []tracker.RoutingRule{
	{Status: "in design", Agent: "claude"},
	{Type: "bugfix", Agent: "claude"},
	{Type: "tweak", Agent: "codex"},
}}

func pairs(typ string, n int, claude, codex string, wallCl, wallCx float64, tokCl, tokCx int64) []TaskRecord {
	var out []TaskRecord
	for i := range n {
		id := typ + string(rune('a'+i))
		out = append(out, rec(id, typ, "claude", claude, wallCl, tokCl, 0), rec(id, typ, "codex", codex, wallCx, tokCx, 0))
	}
	return out
}

func TestRoutingLineNamesARuleTheNumbersContradict(t *testing.T) {
	r := pairs("bugfix", 3, OutcomePass, OutcomePass, 60, 60, 100, 100)
	r[0].Outcome, r[2].Outcome = OutcomeFail, OutcomeFail // claude 1/3, codex 3/3
	checks := CheckRouting(routed, ByAgent(twoAgentRun("r", r...)))
	if len(checks) != 1 {
		t.Fatalf("checks = %+v", checks)
	}
	c := checks[0]
	if c.Verdict != RoutingContradicted || c.Rule != "type bugfix → claude" || c.Agent != "claude" || c.Other != "codex" {
		t.Fatalf("check = %+v", c)
	}
	if got := c.Line(); got != "type bugfix → claude contradicted: codex passed 3/3, claude 1/3" {
		t.Errorf("line = %q", got)
	}
}

func TestRoutingLineVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records []TaskRecord
		verdict string
		why     string
	}{
		{"the routed agent passes more", func() []TaskRecord {
			r := pairs("bugfix", 3, OutcomePass, OutcomeFail, 60, 60, 100, 100)
			return r
		}(), RoutingSupported, "codex passed 0/3, claude 3/3"},
		{"same passes, the other far cheaper", pairs("bugfix", 3, OutcomePass, OutcomePass, 60, 60, 100, 50), RoutingContradicted, "both passed 3/3, codex used 50% less tokens"},
		{"same passes, cheaper but far slower", pairs("bugfix", 3, OutcomePass, OutcomePass, 60, 90, 100, 50), RoutingNoDifference, "both passed 3/3"},
		{"same passes, the routed one cheaper", pairs("bugfix", 3, OutcomePass, OutcomePass, 60, 60, 50, 100), RoutingSupported, "both passed 3/3, claude used 50% less tokens"},
		{"same passes, close", pairs("bugfix", 3, OutcomePass, OutcomePass, 60, 60, 100, 95), RoutingNoDifference, "both passed 3/3"},
		{"two tasks say nothing", pairs("bugfix", 2, OutcomeFail, OutcomePass, 60, 60, 100, 100), RoutingTooFew, "2 both ran, 3 needed"},
		{"a task only one agent ran doesn't count", append(pairs("bugfix", 2, OutcomeFail, OutcomePass, 60, 60, 100, 100),
			rec("x", "bugfix", "claude", OutcomeFail, 60, 100, 0), rec("x", "bugfix", "codex", OutcomeError, 1, 0, 0)), RoutingTooFew, "2 both ran, 3 needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := CheckRouting(routed, ByAgent(twoAgentRun("r", tc.records...)))
			if len(checks) != 1 || checks[0].Verdict != tc.verdict || checks[0].Why != tc.why {
				t.Fatalf("got %+v, want %s: %s", checks, tc.verdict, tc.why)
			}
		})
	}
}

func TestRoutingLineComparesCostWhenEveryTaskIsPricedAndNamesTheDefault(t *testing.T) {
	r := pairs("feature", 3, OutcomePass, OutcomePass, 60, 60, 100, 500)
	for i := range r {
		r[i].Usage.USDKnown, r[i].Usage.USD = true, 2
		if r[i].Agent == "codex" {
			r[i].Usage.USD = 1
		}
	}
	checks := CheckRouting(routed, ByAgent(twoAgentRun("r", r...)))
	if len(checks) != 1 || checks[0].Verdict != RoutingContradicted || checks[0].Rule != "type feature (default → claude)" || !strings.Contains(checks[0].Why, "50% less cost") {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestRoutingLineSaysWhenTheRoutedAgentDidNotRun(t *testing.T) {
	r := []*Run{
		{ID: "a", Setup: Setup{Agent: "claude"}, Tasks: []TaskRecord{{Task: "p/a", Type: "tweak", Outcome: OutcomePass}}},
		{ID: "b", Setup: Setup{Agent: "gemini"}, Tasks: []TaskRecord{{Task: "p/a", Type: "tweak", Outcome: OutcomePass}}},
	}
	checks := CheckRouting(routed, ByAgent(r...))
	if len(checks) != 1 || checks[0].Verdict != RoutingNotRun || checks[0].Agent != "codex" {
		t.Fatalf("checks = %+v", checks)
	}
	if CheckRouting(routed, nil) != nil {
		t.Error("no table, no lines")
	}
}

func TestTwoAgentPairComparesTaskByTaskPerAgent(t *testing.T) {
	a := twoAgentRun("a", rec("x", "bugfix", "claude", OutcomePass, 1, 1, 1), rec("x", "bugfix", "codex", OutcomePass, 1, 1, 1))
	b := twoAgentRun("b", rec("x", "bugfix", "claude", OutcomePass, 1, 1, 1), rec("x", "bugfix", "codex", OutcomeFail, 1, 1, 1))
	a.Benchmark, b.Benchmark = "ab", "ab"
	c := Compare(a, b)
	if !c.Comparable || len(c.Tasks) != 2 || !slices.Equal(c.Regressions, []string{"p/x@codex"}) {
		t.Fatalf("compare = %+v", c)
	}
	if c.ByAgent == nil || len(c.Workflows) != 1 || c.Workflows[0].Verdict == NotComparable || c.Workflows[0].Tasks != 2 {
		t.Fatalf("by agent %+v, workflows %+v", c.ByAgent, c.Workflows)
	}
	// One agent's model changing is still not comparable.
	b.Tasks[0].Usage.Model = "another"
	if v := Compare(a, b).Workflows[0]; v.Verdict != NotComparable {
		t.Errorf("verdict = %+v", v)
	}
}

func TestTwoOneAgentRunsCompareAsBefore(t *testing.T) {
	mk := func(id, outcome string) *Run {
		r := &Run{ID: id, Benchmark: "ab", Stop: StopDone, Ended: time.Now(), Setup: Setup{Agent: "claude"},
			Tasks: []TaskRecord{{Task: "p/a", Outcome: outcome, Usage: Usage{Model: "m"}}}}
		return r
	}
	old := mk("old", OutcomePass)
	old.Setup.Agent = ""
	c := Compare(old, mk("new", OutcomeFail))
	if !c.Comparable || !slices.Equal(c.Regressions, []string{"p/a"}) || c.Tasks[0].Task != "p/a" || c.ByAgent != nil {
		t.Fatalf("compare = %+v", c)
	}
}

func TestCostTextNeverShowsZeroForUnpricedTokens(t *testing.T) {
	r := &Run{Tasks: []TaskRecord{{Usage: Usage{Known: true, Input: 10}}}}
	if got := r.CostText(); got != "cost unknown" {
		t.Errorf("CostText = %q", got)
	}
	r.Tasks = append(r.Tasks, TaskRecord{Usage: Usage{Known: true, USDKnown: true, USD: 1}})
	r.SpentUSD = 1
	if got := r.CostText(); got != "≥ $1.00" {
		t.Errorf("CostText = %q", got)
	}
	if got := (&Run{SpentUSD: 2}).CostText(); got != "$2.00" {
		t.Errorf("CostText = %q", got)
	}
}

func TestPickCandidatesPrefersNoRiskAndSpreadsTypes(t *testing.T) {
	c := func(slug, typ string, risks ...string) Candidate {
		return Candidate{Slug: slug, Type: typ, Risks: risks}
	}
	cands := []Candidate{
		c("f1", "feature"), c("f2", "feature"), c("f3", "feature"), c("b1", "bugfix"), c("t1", "tweak"),
		c("risky", "feature", "tmux"), {Slug: "left-out", Why: "no tests"},
	}
	var got []string
	for _, x := range PickCandidates(cands, 5) {
		got = append(got, x.Slug)
	}
	if !slices.Equal(got, []string{"f1", "b1", "t1", "f2", "f3"}) {
		t.Errorf("picked %v", got)
	}
	if n := len(PickCandidates(cands, 99)); n != 6 {
		t.Errorf("all proposed = %d", n)
	}
}

func TestNewestTwoAgentsSkipsALaterOneAgentRun(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	at := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	for i, agent := range []string{"claude,codex", "claude"} {
		s := at.Add(time.Duration(i) * time.Hour)
		r := &Run{ID: s.Format("150405"), Benchmark: "ab", Started: s, Ended: s, Stop: StopDone, Setup: Setup{Agent: agent}}
		if err := Save(r, nil); err != nil {
			t.Fatal(err)
		}
	}
	if r := NewestTwoAgents("ab"); r == nil || r.Setup.Agent != "claude,codex" {
		t.Fatalf("run = %+v", r)
	}
	if NewestTwoAgents("none") != nil {
		t.Error("no benchmark, no run")
	}
}
