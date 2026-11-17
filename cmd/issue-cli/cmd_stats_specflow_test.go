package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/specflow"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var specFlowNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)

// specFlowProject is a sandbox project with one record of each kind: two
// critique runs on one edge (held, then fixed), a clarify round (one owner
// answer, one assumed), a red-round question and a post-mortem (one action
// applied, one waiting). An old run sits 40 days back.
func specFlowProject(t *testing.T, withRecords bool) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "issues", "Factory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root}
	if !withRecords {
		mustWrite(t, filepath.Join(dir, "plain.md"), "---\ntitle: \"Plain\"\nstatus: \"backlog\"\ncreated: \"2026-11-15\"\n---\n\nNo records.\n")
		return proj
	}
	q, _ := json.Marshal(tracker.Comment{ID: 1, Date: specFlowNow.Add(-2 * time.Hour).Format(time.RFC3339), Text: "question: red rounds: make validate — red 4 of 4", Status: "open"})
	mustWrite(t, filepath.Join(dir, "widget.md"), "---\ntitle: \"Widget\"\nstatus: \"in progress\"\nsystem: \"Factory\"\ncreated: \"2026-11-14\"\n---\n\n## Clarify\n\n### Q1. Which?\n1. **A** (recommended) why. Cost: none.\n2. **B.** Cost: some.\n**Answer:** 2 · 2026-11-15 · owner: \"B\" (D-1)\n\n### Q2. Where?\n1. **Here** (recommended) why. Cost: none.\n**Answer:** assumed — took the recommendation\n\n<!-- issue-viewer-comments\n"+string(q)+"\n-->\n")
	held := &critique.Run{Slug: "factory/widget", From: "in design", To: "backlog", At: specFlowNow.Add(-3 * time.Hour), Agent: "codex", TookMS: 125_000,
		Report: critique.Report{Findings: []critique.Finding{{Severity: critique.Blocks, Path: "a.go", Text: "no test"}, {Severity: critique.Should, Path: "a.go", Text: "name"}}}}
	clear := &critique.Run{Slug: "factory/widget", From: "in design", To: "backlog", At: specFlowNow.Add(-time.Hour), Agent: "codex",
		Report: critique.Report{Findings: []critique.Finding{{Severity: critique.Note, Path: "a.go", Text: "fine"}}}}
	old := &critique.Run{Slug: "factory/widget", From: "testing", To: "shipping", At: specFlowNow.AddDate(0, 0, -40), Agent: "claude"}
	for _, r := range []*critique.Run{old, held, clear} {
		if err := critique.Record(proj.IssueDir, r); err != nil {
			t.Fatal(err)
		}
	}
	body := "## Proposed actions\n\n- [x] A1: Say it\n  Differently: would say it\n  Place: memory fact\n  Text: it\n  Applied: memory m-1\n- [ ] A2: Write it\n  Differently: would write it\n  Place: none docs/x.md#y\n"
	if _, err := postmortem.Save(filepath.Join(root, "retros"), postmortem.Meta{Issue: "factory/widget", Title: "Widget"}, body, specFlowNow.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return proj
}

// stats --spec-flow prints the four blocks with their totals; an empty
// block names the command that fills it.
func TestStatsSpecFlowCLI(t *testing.T) {
	proj := specFlowProject(t, true)
	ctx, out, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return specFlowNow }
	if err := runStats(ctx, []string{"--spec-flow"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"== Spec flow: since " + specFlowNow.AddDate(0, 0, -30).Format("2006-01-02") + " (30 days) ==",
		"Critique runs: 2 (2 gated, 0 by hand, 0 failed) · findings: 1 blocks, 1 should, 1 note",
		"Held a gate: 1 — fixed 1, overridden 0, held again 0, still holds 0 · took: median 2m5s (1 of 2 timed; tokens not recorded)",
		"in design → backlog", "2m5s", "fixed",
		"Clarify rounds: 1 designs (0 with nothing to ask) · 2 questions, 2.0 per design: 1 owner-answered, 1 assumed, 0 open",
		"Red-round questions: 1 raised on 1 issues · 1 open, 0 resolved",
		"make validate",
		"Post-mortem actions: 2 proposed in 1 post-mortems · 1 applied, 0 refused, 0 overtaken, 1 waiting",
	} {
		assertContains(t, got, want)
	}
	if strings.Contains(got, "testing → shipping") {
		t.Errorf("the run 40 days back is out of the 30-day range:\n%s", got)
	}

	ctx, out, _ = newTestContext(specFlowProject(t, false), false)
	ctx.Now = func() time.Time { return specFlowNow }
	if err := runStats(ctx, []string{"--spec-flow"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"issue-cli --project proj critique <slug>", "issue-cli --project proj clarify <slug>", "issue-cli --project proj rounds <slug>", "issue-cli --project proj postmortem list"} {
		assertContains(t, out.String(), want)
	}
}

// --json prints the report; --days and --all pick the range by calendar
// days.
func TestStatsSpecFlowJSONAndRange(t *testing.T) {
	proj := specFlowProject(t, true)
	read := func(args ...string) specflow.Report {
		t.Helper()
		ctx, out, _ := newTestContext(proj, true)
		ctx.Now = func() time.Time { return specFlowNow }
		if err := runStats(ctx, append([]string{"--spec-flow"}, args...)); err != nil {
			t.Fatal(err)
		}
		var rep specflow.Report
		if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		return rep
	}
	rep := read()
	if rep.Critique.Totals.Runs != 2 || rep.Clarify.Totals.Designs != 1 || rep.RedRounds.Totals.Raised != 1 || rep.PostMortem.Totals.Proposed != 2 {
		t.Errorf("30 days: %+v", rep)
	}
	if want := time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local); rep.From == nil || !rep.From.Equal(want) {
		t.Errorf("from = %v, want midnight 30 days back", rep.From)
	}
	if rep.Critique.Runs[0].Outcome != "" || rep.Critique.Runs[1].Outcome != specflow.Fixed {
		t.Errorf("outcomes: %+v", rep.Critique.Runs)
	}
	if rep := read("--all"); rep.Critique.Totals.Runs != 3 || rep.From != nil {
		t.Errorf("--all: %d runs, from %v", rep.Critique.Totals.Runs, rep.From)
	}
	// One day back is from yesterday's midnight: the clarify round dated
	// 2026-11-15 drops out, today's records stay.
	if rep := read("--days", "1"); rep.Critique.Totals.Runs != 2 || rep.Clarify.Totals.Designs != 0 || rep.PostMortem.Totals.PostMortems != 1 {
		t.Errorf("--days 1: %+v", rep)
	}
	ctx, _, _ := newTestContext(proj, false)
	if err := runStats(ctx, []string{"--all"}); err == nil || !strings.Contains(err.Error(), "--spec-flow") {
		t.Errorf("--all alone: %v", err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runStats(ctx, []string{"--spec-flow", "--days", "0"}); err == nil {
		t.Error("--days 0 accepted")
	}
}

// Plain stats ends with the Spec flow line when a record exists, and says
// nothing of it when none does.
func TestStatsPointsAtSpecFlow(t *testing.T) {
	ctx, out, _ := newTestContext(specFlowProject(t, true), false)
	if err := runStats(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Spec flow: 3 critique runs, 1 held a gate · 1 clarify rounds · 1 post-mortems — issue-cli --project proj stats --spec-flow")

	ctx, out, _ = newTestContext(specFlowProject(t, false), false)
	if err := runStats(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Spec flow") {
		t.Errorf("no records, yet:\n%s", out.String())
	}
}
