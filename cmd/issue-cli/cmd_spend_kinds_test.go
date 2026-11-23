package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/spend"
)

func TestSpendKindsCommand(t *testing.T) {
	proj := goalsProject(t)
	spendFixture(t, proj)
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	ctx, out := goalsCtx(t, proj, false)
	if err := runSpend(ctx, []string{"--kinds", "--days", "3"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Spend by project and kind of session, 2026-10-26 00:00 → 2026-10-29 12:00 (3.5 days)")
	for _, want := range []string{"worker", "lead", "owner's own", "Workers by type and stage", "untyped · idea",
		"claude-test", "assumed, from " + spend.PricesPath(), "add confirmed: true",
		"Weekly limit: not set, so % WEEK shows —", "weekly_limit: {usd: N}", "issue-cli spend --kinds --json --since <T0> --until <T1>"} {
		assertContains(t, s, want)
	}

	// The weekly limit and a confirmed price, from --prices.
	prices := filepath.Join(t.TempDir(), "p.yaml")
	mustWrite(t, prices, "prices:\n  claude-test: {in: 2, out: 10, confirmed: true}\nweekly_limit: {usd: 10}\n")
	out.Reset()
	if err := runSpend(ctx, []string{"--kinds", "--since", "2026-10-28", "--until", "2026-10-28", "--prices", prices}); err != nil {
		t.Fatal(err)
	}
	s = out.String()
	assertContains(t, s, "(1.0 days)")
	assertContains(t, s, "confirmed, from "+prices)
	assertContains(t, s, "Weekly limit: $10.00 a week")
	// $3.00 of the worker against $10 × 1/7: 210%.
	assertContains(t, s, "210.0%")

	jctx, jout := goalsCtx(t, proj, true)
	if err := runSpend(jctx, []string{"--all-projects", "--days", "3"}); err != nil {
		t.Fatal(err)
	}
	var rep spend.KindsReport
	if err := json.Unmarshal(jout.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Total.Turns != 3 || len(rep.Rows) == 0 || rep.LimitNote == "" {
		t.Errorf("json: %+v", rep)
	}

	for _, bad := range [][]string{
		{"--kinds", "--turns"}, {"--kinds", "--context"}, {"--turns", "--all-projects"},
		{"--prices", "x.yaml"}, {"--since", "2026-10-01"}, {"--kinds", "--since", "yesterday"},
		{"--kinds", "--days", "2", "--since", "2026-10-01"}, {"--kinds", "--since", "2026-10-29", "--until", "2026-10-28"},
		{"--kinds", "marketing"},
	} {
		if err := runSpend(ctx, bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestSpendTurnsCommand(t *testing.T) {
	proj := goalsProject(t)
	spendFixture(t, proj)
	spend.ResetTurnsCache()
	folder := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", spend.FolderName(filepath.Join(proj.Root(), ".worktrees", "seo-pass")))
	mustWrite(t, filepath.Join(folder, "t.jsonl"), `{"type":"assistant","timestamp":"2026-10-28T09:05:00Z","message":{"id":"t1","usage":{"input_tokens":10},"content":[{"type":"tool_use","id":"u1","name":"Bash","input":{"command":"issue-cli transition seo-pass --to x"}}]}}`+"\n"+
		`{"type":"user","timestamp":"2026-10-28T09:05:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"u1","content":"Error: failed to transition"}]}}`+"\n")
	ctx, out := goalsCtx(t, proj, false)
	if err := runSpend(ctx, []string{"--turns", "--days", "3"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Worker turns by cause, 2026-10-26 00:00 → 2026-10-29 12:00")
	assertContains(t, s, "seo-pass")
	assertContains(t, s, "1 (1)")
	assertContains(t, s, "All issues: transition 50.0% of turns")
	assertContains(t, s, "Only counts are kept")
	assertContains(t, s, "DRY RUNS")
	assertContains(t, s, "FOLLOW-ON READS")
	assertContains(t, s, "Turns a batch would save: 0 dry runs (0.00 per issue), 0 follow-on reads (0.00 per issue), 0 follow-on read-only turns (")
	assertContains(t, s, "FOLLOW-ON READ-ONLY")

	jctx, jout := goalsCtx(t, proj, true)
	if err := runSpend(jctx, []string{"--turns", "seo-pass"}); err != nil {
		t.Fatal(err)
	}
	var rep spend.TurnsReport
	if err := json.Unmarshal(jout.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Issues) != 1 || rep.Issues[0].Refused != 1 || rep.Issues[0].By[spend.CauseTransition].Turns != 1 {
		t.Errorf("json: %+v", rep.Issues)
	}
	if strings.Contains(jout.String(), "failed to transition") {
		t.Error("result text in the JSON")
	}

	out.Reset()
	if err := runSpend(ctx, []string{"--turns", "--since", "2026-10-29"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No worker turns in the window.")
}
