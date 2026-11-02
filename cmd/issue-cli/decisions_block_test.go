package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var blockRefRe = regexp.MustCompile(`(?m)^- ([a-z0-9/-]+#D-\d+) · `)

// blockRefs is the refs a == Decisions in force == block printed, sorted.
func blockRefs(t *testing.T, out string) []string {
	t.Helper()
	i := strings.Index(out, "== Decisions in force ==")
	if i < 0 {
		t.Fatalf("no block:\n%s", out)
	}
	block := out[i:]
	if j := strings.Index(block[3:], "\n== "); j >= 0 {
		block = block[:j+3]
	}
	var refs []string
	for _, m := range blockRefRe.FindAllStringSubmatch(block, -1) {
		refs = append(refs, m[1])
	}
	sort.Strings(refs)
	return refs
}

func inForceRefs(t *testing.T, proj *tracker.Project, slug string) []string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runDecisions(ctx, []string{"--in-force", slug}); err != nil {
		t.Fatal(err)
	}
	var rows []inForceRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, r := range rows {
		refs = append(refs, r.Ref)
	}
	sort.Strings(refs)
	return refs
}

func readLog(t *testing.T, proj *tracker.Project) string {
	t.Helper()
	b, err := os.ReadFile(tracker.DecisionLogPath(proj.IssueDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// AC4, AC7: start and show print the set decisions --in-force lists; show
// and the critic's brief leave the log byte-identical, start adds one
// injected event.
func TestDecisionsBlockInStartShowAndCritic(t *testing.T) {
	proj, slug, _, _ := critiqueProject(t)
	rules := filepath.Join(proj.IssueDir, "Ops", "rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, rules, "---\ntitle: \"rules\"\nstatus: \"done\"\nsystem: \"Ops\"\n---\n\n## Decisions\n\n"+
		"- **D-1** · 2026-10-01 · chat: Project rule, \"yes\" {scope: project; hard}\n"+
		"- **D-2** · 2026-10-02 · chat: Factory rule {scope: system:Factory}\n"+
		"- **D-3** · 2026-10-03 · chat: Combat rule {scope: system:Combat}\n"+
		"- **D-4** · 2026-10-04 · chat: Design-only rule {scope: project; at: in design}\n")
	// Something already in the log, so "byte-identical" is not "empty".
	if err := tracker.AppendDecisionEvents(proj.IssueDir, []tracker.DecisionEvent{{Write: "w0", Op: tracker.DecisionOpDone, Event: tracker.DecisionRecorded, Ref: "ops/rules#D-1"}}); err != nil {
		t.Fatal(err)
	}
	want := inForceRefs(t, proj, slug)
	if strings.Join(want, ",") != "ops/rules#D-1,ops/rules#D-2,ops/rules#D-4" {
		t.Fatalf("fixture: in force %v", want)
	}
	before := readLog(t, proj)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	if got := blockRefs(t, stdout.String()); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("show: %v, decisions --in-force: %v", got, want)
	}
	assertContains(t, stdout.String(), "- ops/rules#D-1 · hard · project · Project rule — \"yes\"")
	if readLog(t, proj) != before {
		t.Error("show wrote the decisions log")
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--brief"}); err != nil {
		t.Fatal(err)
	}
	brief := stdout.String()
	assertContains(t, brief, "- ops/rules#D-1 · hard · project — in "+rules)
	assertContains(t, brief, rules+"  (holds decisions in force)")
	for _, never := range []string{"Project rule", "Factory rule", "Combat", "\"yes\""} {
		if strings.Contains(brief, never) {
			t.Errorf("the critic's brief carries %q", never)
		}
	}
	if readLog(t, proj) != before {
		t.Error("critique --brief wrote the decisions log")
	}

	ctx, stdout, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local) }
	if err := runStart(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if got, want := blockRefs(t, out), inForceRefs(t, proj, slug); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("start: %v, decisions --in-force: %v", got, want)
	}
	if strings.Index(out, "== Decisions in force ==") > strings.Index(out, "== Memories ==") {
		t.Error("the block should come before == Memories ==")
	}
	after := readLog(t, proj)
	if !strings.HasPrefix(after, before) {
		t.Fatal("start rewrote the log")
	}
	added := strings.Split(strings.TrimSpace(strings.TrimPrefix(after, before)), "\n")
	if len(added) != 1 {
		t.Fatalf("start added %d lines:\n%s", len(added), strings.Join(added, "\n"))
	}
	var ev tracker.DecisionEvent
	if err := json.Unmarshal([]byte(added[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Event != tracker.DecisionInjected || ev.Briefing != tracker.BriefingStart || ev.Issue != slug || ev.Op != tracker.DecisionOpDone || len(ev.Refs) != 3 {
		t.Errorf("event %+v", ev)
	}
}

// AC6 on the command line: an issue with none prints the empty line with
// the command, and start writes nothing.
func TestDecisionsBlockEmptyInShowAndStart(t *testing.T) {
	proj, slug, _, _ := critiqueProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "== Decisions in force ==\nNo decisions in force for this issue: issue-cli --project proj decisions --in-force "+slug)
	ctx, _, _ = newTestContext(proj, false)
	if err := runStart(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tracker.DecisionLogDir(proj.IssueDir)); !os.IsNotExist(err) {
		t.Errorf("start with nothing in force made the log: %v", err)
	}
}

// decisions --lead: the CEO's set, refused for an unknown department.
func TestDecisionsLeadCommand(t *testing.T) {
	proj, _, _, _ := critiqueProject(t)
	mustWrite(t, filepath.Join(proj.IssueDir, "Factory", "rules.md"), "---\ntitle: \"rules\"\nstatus: \"done\"\nsystem: \"Factory\"\n---\n\n## Decisions\n\n"+
		"- **D-1** · 2026-10-01 · chat: Project rule {scope: project}\n- **D-2** · 2026-10-02 · chat: Factory rule {scope: system:Factory}\n")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"--lead", "ceo"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "== Decisions in force for the CEO (project scope) (1: 0 hard) ==\nfactory/rules#D-1 · soft · project")
	ctx, _, _ = newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"--lead", "nope"}); err == nil || !strings.Contains(err.Error(), "want ceo or a department key") {
		t.Errorf("unknown department: %v", err)
	}
}
