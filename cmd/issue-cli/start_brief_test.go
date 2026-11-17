package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/briefing"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// dispatchedProject is critiqueProject with decisions in force, a pinned
// memory, a status memory and the briefing a dispatch wrote an hour ago
// (its prompt file and its sidecar record). After the briefing: a new
// decision and a comment.
func dispatchedProject(t *testing.T) (proj *tracker.Project, slug, pinned, atStatus string) {
	t.Helper()
	proj, slug, issuePath, _ := critiqueProject(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	rules := filepath.Join(proj.IssueDir, "Ops", "rules.md")
	if err := os.MkdirAll(filepath.Dir(rules), 0o755); err != nil {
		t.Fatal(err)
	}
	decisions := "---\ntitle: \"rules\"\nstatus: \"done\"\nsystem: \"Ops\"\n---\n\n## Decisions\n\n" +
		"- **D-1** · 2026-10-01 · chat: Project rule, \"yes\" {scope: project; hard}\n" +
		"- **D-2** · 2026-10-02 · chat: Factory rule {scope: system:Factory}\n"
	mustWrite(t, rules, decisions)
	store := memory.ProjectStore(proj.IssueDir)
	add := func(text, at string) string {
		res, err := store.Add(memory.Draft{Text: text, Scope: memory.ScopeProject, At: at}, memory.AddOptions{Mode: memory.ModeNew}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return res.Memory.ID
	}
	pinned = add("Pinned fact every worker reads in the briefing.", "")
	if err := store.Pin(pinned, "owner", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	atStatus = add("At in design, read the critic's notes before the gate.", "in design")

	issue := loadLeadIssue(t, proj, slug)
	wf := proj.LoadWorkflowForIssue(issue)
	dp := briefing.BuildDispatchPrompt(proj, issue, wf, proj.WorkDir, nil)
	path := dispatchPromptPath(proj, slug)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, dp.Prompt)
	written := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	b := tracker.NewBriefing(tracker.BriefingDispatch, issue.Status, tracker.AgentSessionName(slug), dp.Prompt, dp.MemoryIDs, dp.DecisionRefs, written)
	if err := tracker.RecordBriefing(issuePath, b); err != nil {
		t.Fatal(err)
	}
	if len(dp.DecisionRefs) != 2 || !strings.Contains(dp.Prompt, pinned) {
		t.Fatalf("fixture: the briefing carries %v and lacks pin %s", dp.DecisionRefs, pinned)
	}

	mustWrite(t, rules, decisions+"- **D-3** · 2026-10-03 · chat: Rule made after the dispatch {scope: project}\n")
	if err := tracker.AddCommentBy(issuePath, 0, "lead: also cover the reprompt path", "cli", "lead-proj"); err != nil {
		t.Fatal(err)
	}
	return proj, slug, pinned, atStatus
}

func startOut(t *testing.T, proj *tracker.Project, args ...string) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local) }
	if err := runStart(ctx, args); err != nil {
		t.Fatal(err)
	}
	return stdout.String()
}

// AC1: in its dispatched session, start leaves out the decisions, guidance
// and memories the briefing holds, prints what came since, and stays
// under 4k chars.
func TestDispatchedStartPrintsOnlyWhatTheBriefingLacks(t *testing.T) {
	proj, slug, pinned, atStatus := dispatchedProject(t)
	prompt, err := os.ReadFile(dispatchPromptPath(proj, slug))
	if err != nil {
		t.Fatal(err)
	}
	out := startOut(t, proj, slug)
	for _, never := range []string{"== Decisions in force ==", "ops/rules#D-1", "ops/rules#D-2", "== Memories ==", pinned, "Box text wrong?"} {
		if strings.Contains(out, never) {
			t.Errorf("start repeats %q from the briefing:\n%s", never, out)
		}
	}
	if strings.Contains(out, "== Current Status Guidance ==") {
		t.Errorf("start repeats guidance the briefing holds:\n%s", out)
	}
	if !strings.Contains(normSpace(string(prompt)), "Current status guidance") {
		t.Fatal("fixture: the briefing has no guidance section")
	}
	for _, want := range []string{
		"== Comments since your briefing ==", "lead: also cover the reprompt path",
		"== Decisions since your briefing ==", "- ops/rules#D-3 · soft · project · Rule made after the dispatch",
		"== Memories your briefing lacks", atStatus,
		"== Next ==", "start <slug> --full prints it all",
	} {
		assertContains(t, out, want)
	}
	if len(out) >= 4000 {
		t.Errorf("start printed %d chars, want under 4000:\n%s", len(out), out)
	}
}

// AC2: --full, and start outside the dispatched session, print the full
// output: every block, the same text.
func TestStartFullAndUndispatchedPrintEverything(t *testing.T) {
	proj, slug, pinned, _ := dispatchedProject(t)
	full := startOut(t, proj, slug, "--full")
	for _, want := range []string{"== Decisions in force ==", "ops/rules#D-1", "ops/rules#D-3", "== Memories ==", "Pinned:", pinned, "== Workflow lifecycle =="} {
		assertContains(t, full, want)
	}
	if strings.Contains(full, "since your briefing") {
		t.Errorf("--full printed the dispatched form:\n%s", full)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	if outside := startOut(t, proj, slug); outside != full {
		t.Errorf("outside the session start differs from --full:\n--- outside\n%s\n--- full\n%s", outside, full)
	}
}

// AC3: the worker briefing doesn't send the worker to show before it
// starts: it already holds the body; show is for re-reading.
func TestWorkerBriefingDropsTheRunShowStep(t *testing.T) {
	tmpl := tracker.AgentDispatchPromptTemplate
	if strings.Contains(tmpl, "2. Run: issue-cli show") {
		t.Error("the briefing's step 2 still runs show")
	}
	i := strings.Index(tmpl, "\n2. ")
	j := strings.Index(tmpl, "\n3. ")
	if i < 0 || j < i {
		t.Fatal("no step 2 in the briefing")
	}
	step := tmpl[i:j]
	for _, want := range []string{"The brief below is the body", "show re-reads it"} {
		if !strings.Contains(step, want) {
			t.Errorf("step 2 lacks %q:\n%s", want, step)
		}
	}
}
