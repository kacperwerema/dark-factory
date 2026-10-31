package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/spend"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// spendFixture writes a Marketing worker's transcript (in the seo-pass
// worktree) and the CEO's, under a fresh CLAUDE_CONFIG_DIR, and prices.
func spendFixture(t *testing.T, proj *tracker.Project) {
	t.Helper()
	spend.ResetCache()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	write := func(cwd, file string, lines ...string) {
		folder := bench.TranscriptFolders(filepath.Join(claude, "projects"), cwd)[0]
		os.MkdirAll(folder, 0o755)
		mustWrite(t, filepath.Join(folder, file), strings.Join(lines, "\n")+"\n")
	}
	line := func(id string, in int) string {
		return `{"type":"assistant","timestamp":"2026-10-28T09:00:00Z","message":{"id":"` + id + `","model":"claude-test","usage":{"input_tokens":` + strconv.Itoa(in) + `}}}`
	}
	write(filepath.Join(proj.Root(), ".worktrees", "seo-pass"), "w.jsonl", line("a", 1500000))
	write(proj.Root(), "ceo.jsonl", `{"type":"agent-name","agentName":"lead-acme","sessionId":"x"}`, line("b", 2000))
	write(proj.Root(), "me.jsonl", line("c", 30))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.MkdirAll(filepath.Dir(spend.PricesPath()), 0o755)
	mustWrite(t, spend.PricesPath(), "prices:\n  claude-test: {in: 2, out: 10}\n")
}


func TestSpendCommand(t *testing.T) {
	proj := goalsProject(t)
	proj.Departments[0].Budget = &tracker.Budget{USD: 2, Period: "week"}
	spendFixture(t, proj)
	ctx, out := goalsCtx(t, proj, false)
	if err := runSpend(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Spend by week since 2026-10-05 (estimates")
	assertContains(t, s, "== HQ · incl. product, marketing ==")
	assertContains(t, s, "== Marketing ==\nBudget: $3.00 of $2.00 this week (est.) · over (shown only, nothing is blocked)")
	assertContains(t, s, "this week (2026-10-26)   1.5M tokens · $3.00 est.")
	assertContains(t, s, "    seo-pass  1.5M tokens · $3.00 est.")
	assertContains(t, s, "Unattributed (sessions in the project folder with no agent name, yours): 30 tokens")

	out.Reset()
	if err := runSpend(ctx, []string{"marketing"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "== HQ") {
		t.Error("one box only")
	}
	if err := runSpend(ctx, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "boxes: hq, product, marketing") {
		t.Errorf("err = %v", err)
	}

	ctx, out = goalsCtx(t, proj, true)
	if err := runSpend(ctx, []string{"hq"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `"rolled_from": [`)
}

func TestSpendCommandNoPricesNoTranscripts(t *testing.T) {
	proj := goalsProject(t)
	spend.ResetCache()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ctx, out := goalsCtx(t, proj, false)
	if err := runSpend(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No spend recorded yet")

	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	folder := bench.TranscriptFolders(filepath.Join(claude, "projects"), proj.Root())[0]
	os.MkdirAll(folder, 0o755)
	mustWrite(t, filepath.Join(folder, "s.jsonl"), `{"type":"agent-name","agentName":"lead-acme-marketing"}`+"\n"+`{"type":"assistant","timestamp":"2026-10-28T09:00:00Z","message":{"id":"z","model":"claude-new","usage":{"output_tokens":5}}}`+"\n")
	out.Reset()
	if err := runSpend(ctx, []string{"marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "5 tokens · cost unknown")
	assertContains(t, out.String(), "No price for claude-new: cost is unknown for their tokens. Add them to "+spend.PricesPath())
}

func TestDepartmentShowSpendLine(t *testing.T) {
	proj := goalsProject(t)
	spendFixture(t, proj)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Spend: 1.5M tokens · $3.00 est. this week · 4 weeks: 1.5M / 0 / 0 / 0 (issue-cli spend marketing)")
	ctx, out = goalsCtx(t, proj, true)
	if err := runDepartment(ctx, []string{"show", "hq"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `"spend": {`)
}
