package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/clilog"
	"github.com/michal-franc/issue-viewer/internal/memory"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// memoryAs isolates the machine store and makes the session the owner's
// shell ("" ), a worker on slug ("worker:<slug>"), the CEO ("ceo") or a
// department lead ("lead:<key>").
func memoryAs(t *testing.T, who string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("AGENT_NAME", "")
	t.Setenv("TMUX", "")
	oldWd := workingDir
	workingDir = func() (string, error) { return "/home/owner/project", nil }
	t.Cleanup(func() { workingDir = oldWd })
	switch {
	case who == "ceo":
		t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", tracker.LeadScopeProject)
	case strings.HasPrefix(who, "lead:"):
		t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", tracker.LeadScopeDepartmentPrefix+strings.TrimPrefix(who, "lead:"))
	case strings.HasPrefix(who, "worker:"):
		t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", strings.TrimPrefix(who, "worker:"))
	}
}

func memSet(t *testing.T, proj *tracker.Project) *memory.Set {
	t.Helper()
	set, err := memory.ProjectStore(proj.IssueDir).Load()
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func onlyMemory(t *testing.T, proj *tracker.Project) memory.Memory {
	t.Helper()
	all := memSet(t, proj).All()
	if len(all) != 1 {
		t.Fatalf("%d memories, want 1: %+v", len(all), all)
	}
	return all[0]
}

// A department worker's memory: department scope, its issue as source,
// its session as author (D-14, P3, P5).
func TestMemoryAddByDepartmentWorker(t *testing.T) {
	memoryAs(t, "worker:seo-pass")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "SEO pages need a canonical link before publishing", "--kind", "pitfall", "--commit", "abc1234", "--tag", "seo,links"}); err != nil {
		t.Fatal(err)
	}
	m := onlyMemory(t, proj)
	if m.Scope != "department:marketing" || m.Kind != "pitfall" || m.Source.Issue != "seo-pass" || m.Source.Commit != "abc1234" ||
		m.Author != "agent-seo-pass" || m.Department != "Marketing" || m.System != "SEO" || m.Project != "acme" || strings.Join(m.Tags, ",") != "seo,links" {
		t.Fatalf("stored %+v", m)
	}
	assertContains(t, out.String(), "✓ Added "+m.ID+" (pitfall, department:marketing) · issue seo-pass · commit abc1234")
}

// Product's worker and the owner write to the project by default.
func TestMemoryAddDefaultsToProject(t *testing.T) {
	memoryAs(t, "worker:engine")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "the engine builds with go build"}); err != nil {
		t.Fatal(err)
	}
	if m := onlyMemory(t, proj); m.Scope != memory.ScopeProject || m.Author != "agent-engine" {
		t.Fatalf("stored %+v", m)
	}
}

// The update step through the CLI: a near-duplicate is refused with the
// candidates; --updates, --supersedes and --new go on.
func TestMemoryUpdateStepCLI(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "The playtest runner needs Chrome for Testing on macOS"}); err != nil {
		t.Fatal(err)
	}
	first := onlyMemory(t, proj)
	out.Reset()
	if err := runMemory(ctx, []string{"add", "the playtest runner needs chrome for testing on macOS"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Already remembered as "+first.ID)

	err := runMemory(ctx, []string{"add", "The playtest runner needs Chrome for Testing on macOS and Linux"})
	var sim *memory.SimilarError
	if !errors.As(err, &sim) {
		t.Fatalf("near duplicate: %v", err)
	}
	for _, want := range []string{first.ID, "--updates " + first.ID, "--supersedes " + first.ID, "--new"} {
		assertContains(t, err.Error(), want)
	}
	out.Reset()
	if err := runMemory(ctx, []string{"add", "The playtest runner finds Chrome for Testing itself", "--updates", first.ID}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Updated "+first.ID)
	out.Reset()
	if err := runMemory(ctx, []string{"add", "Playtests run headless Chrome everywhere", "--supersedes", first.ID}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "supersedes "+first.ID)
	if err := runMemory(ctx, []string{"add", "x y z words", "--updates", "a", "--new"}); err == nil {
		t.Error("two decisions at once accepted")
	}

	out.Reset()
	if err := runMemory(ctx, []string{"show", first.ID}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"superseded", "== History ==", "add ", "update ", "supersede "} {
		assertContains(t, s, want)
	}
}

// AC4: secrets refused through the CLI, the text never printed or logged.
func TestMemoryRefusesSecrets(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	secret := "xoxb-" + "1234567890-abcdefghijkl"
	args := []string{"add", "the slack bot posts with " + secret}
	err := runMemory(ctx, args)
	if err == nil || !strings.Contains(err.Error(), "Slack token") || strings.Contains(err.Error()+out.String(), secret) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(memory.ProjectPath(proj.IssueDir)); !os.IsNotExist(statErr) {
		t.Error("a refused memory wrote the log")
	}
	if logged := strings.Join(clilog.RedactCall(append([]string{"memory"}, args...), nil), " "); strings.Contains(logged, secret) {
		t.Errorf("the clilog record keeps the text: %s", logged)
	}
}

// AC4: per project by default; machine memories reach every project; a
// department's stay in it; a private project can't write machine.
func TestMemoryScopesAcrossProjectsAndDepartments(t *testing.T) {
	memoryAs(t, "")
	a, b := goalsProject(t), goalsProject(t)
	b.Name, b.Slug = "beta", "beta"
	ctxA, _ := goalsCtx(t, a, false)
	for _, args := range [][]string{
		{"add", "acme deploys from the release branch"},
		{"add", "--scope", "machine", "every project cuts release candidates as rc tags"},
		{"add", "--scope", "department", "--issue", "seo-pass", "marketing release notes go to the blog"},
	} {
		if err := runMemory(ctxA, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	ctxB, outB := goalsCtx(t, b, false)
	if err := runMemory(ctxB, []string{"search", "release"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, outB.String(), "release candidates")
	if strings.Contains(outB.String(), "release branch") || strings.Contains(outB.String(), "blog") {
		t.Errorf("project B sees project A's own memories:\n%s", outB.String())
	}

	memoryAsKeep(t, "worker:engine")
	ctxA, outA := goalsCtx(t, a, false)
	if err := runMemory(ctxA, []string{"search", "release"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(outA.String(), "blog") {
		t.Errorf("a Product worker sees Marketing's memory:\n%s", outA.String())
	}
	memoryAsKeep(t, "worker:seo-pass")
	outA.Reset()
	if err := runMemory(ctxA, []string{"search", "release"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, outA.String(), "blog")

	priv := goalsProject(t)
	wf := filepath.Join(priv.WorkDir, "workflow.yaml")
	mustWrite(t, wf, "privacy:\n  work_files: tracker\n")
	priv.WorkflowFile = wf
	ctxP, _ := goalsCtx(t, priv, false)
	if err := runMemory(ctxP, []string{"add", "--scope", "machine", "the client roster lives in the tracker"}); err == nil || !strings.Contains(err.Error(), "keeps its work private") {
		t.Fatalf("private machine add: %v", err)
	}
}

// memoryAsKeep switches the session without a fresh machine store.
func memoryAsKeep(t *testing.T, who string) {
	t.Helper()
	data := os.Getenv("XDG_DATA_HOME")
	memoryAs(t, who)
	t.Setenv("XDG_DATA_HOME", data)
}

// Pins: leads and the owner only (D-16); a worker suggests.
func TestMemoryPinRules(t *testing.T) {
	memoryAs(t, "worker:engine")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "never push from a worker", "--kind", "convention", "--suggest-pin"}); err != nil {
		t.Fatal(err)
	}
	m := onlyMemory(t, proj)
	if !m.SuggestPin {
		t.Error("suggest-pin not stored")
	}
	if err := runMemory(ctx, []string{"pin", m.ID}); err == nil || !strings.Contains(err.Error(), "leads and the owner") {
		t.Fatalf("worker pin: %v", err)
	}
	memoryAsKeep(t, "ceo")
	if err := runMemory(ctx, []string{"pin", m.ID}); err != nil {
		t.Fatal(err)
	}
	if m := onlyMemory(t, proj); !m.Pinned || m.PinnedBy != "lead-acme" {
		t.Fatalf("after pin %+v", m)
	}
	out.Reset()
	if err := runMemory(ctx, []string{"list", "--pinned"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "never push from a worker")
	if err := runMemory(ctx, []string{"unpin", m.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryEditForgetListJSON(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "staging resets nightly", "--expires", "2026-12-31"}); err != nil {
		t.Fatal(err)
	}
	m := onlyMemory(t, proj)
	if err := runMemory(ctx, []string{"edit", m.ID, "--text", "staging resets every night at 02:00"}); err != nil {
		t.Fatal(err)
	}
	if err := runMemory(ctx, []string{"forget", m.ID}); err == nil {
		t.Error("forget without --reason accepted")
	}
	if err := runMemory(ctx, []string{"forget", m.ID, "--reason", "staging was retired"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runMemory(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No memories yet")
	out.Reset()
	ctx.JSONOutput = true
	if err := runMemory(ctx, []string{"list", "--all"}); err != nil {
		t.Fatal(err)
	}
	var list []memory.Memory
	if err := json.Unmarshal([]byte(out.String()), &list); err != nil || len(list) != 1 || list[0].InvalidReason != "staging was retired" || list[0].Text != "staging resets every night at 02:00" {
		t.Fatalf("list --all --json %s (%v)", out.String(), err)
	}
	ctx.JSONOutput = false
	for _, args := range [][]string{nil, {"bogus"}, {"add"}, {"search", "a"}, {"edit", "m-1"}, {"show"}, {"pin", "zz"}, {"add", "a fact", "--kind", "gossip"}, {"list", "--scope", "planet"}} {
		if err := runMemory(ctx, args); err == nil {
			t.Errorf("memory %v: no error", args)
		}
	}
}

// Injection at start: pins, then the matches for the issue, recorded as use.
func TestStartPrintsPinsAndMatches(t *testing.T) {
	memoryAs(t, "ceo")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	issue := &tracker.Issue{Slug: "seo-pass", Title: "SEO pass", System: "SEO"}
	var w strings.Builder
	printStartMemories(&w, ctx, issue)
	assertContains(t, w.String(), "== Memories ==\nNone yet on this project.")
	assertContains(t, w.String(), `issue-cli memory add "<fact>"`)

	for _, args := range [][]string{
		{"add", "SEO pass: check robots.txt first", "--kind", "howto"},
		{"add", "the board's tests use a sandbox"},
		{"add", "never push from a worker", "--kind", "convention"},
	} {
		if err := runMemory(ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	var pin string
	for _, m := range memSet(t, proj).All() {
		if strings.HasPrefix(m.Text, "never push") {
			pin = m.ID
		}
	}
	if err := runMemory(ctx, []string{"pin", pin}); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	printStartMemories(&w, ctx, issue)
	s := w.String()
	assertContains(t, s, "Pinned:\n")
	assertContains(t, s, "never push from a worker")
	assertContains(t, s, "SEO pass: check robots.txt first")
	if strings.Contains(s, "sandbox") {
		t.Errorf("an unrelated memory shown:\n%s", s)
	}
	for _, m := range memSet(t, proj).All() {
		if strings.HasPrefix(m.Text, "SEO pass") && m.Uses != 1 {
			t.Errorf("use not recorded: %+v", m)
		}
	}
}

// Reflection hint at done / retro: the add line and similar memories.
func TestMemoryReflectionHint(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "SEO pass: check robots.txt first"}); err != nil {
		t.Fatal(err)
	}
	var w strings.Builder
	printMemoryReflection(&w, ctx, &tracker.Issue{Slug: "seo-pass", Title: "SEO pass", System: "SEO"})
	assertContains(t, w.String(), "Anything the next agent should know?")
	assertContains(t, w.String(), "--issue seo-pass")
	assertContains(t, w.String(), "SEO pass: check robots.txt first")
}

// Phase 4: entities in text and tags; search @x / #t filters by them,
// memory entities lists who knows what, add shows the entities and
// hints a who memory without an @name and a #topic.
func TestMemoryEntitiesSearchAndIndex(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	for _, a := range [][]string{
		{"add", "@lead-dark-factory knows the #playtest runner and #chrome", "--kind", "who"},
		{"add", "The playtest runner finds Chrome for Testing by itself", "--tag", "playtest,ci"},
		{"add", "Ask @owner before a release goes out", "--tag", "release"},
	} {
		if err := runMemory(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	assertContains(t, out.String(), "Entities: @owner #release (issue-cli memory search @owner)")

	search := func(args ...string) string {
		t.Helper()
		out.Reset()
		if err := runMemory(ctx, append([]string{"search"}, args...)); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	s := search("@lead-dark-factory")
	assertContains(t, s, "knows the #playtest runner")
	if strings.Contains(s, "Ask @owner") || strings.Contains(s, "by itself") {
		t.Fatalf("@lead-dark-factory found others:\n%s", s)
	}
	s = search("#playtest")
	if !strings.Contains(s, "knows the #playtest") || !strings.Contains(s, "by itself") || strings.Contains(s, "Ask @owner") {
		t.Fatalf("#playtest:\n%s", s)
	}
	s = search("#playtest", "chrome", "testing")
	assertContains(t, s, "by itself")
	assertContains(t, search("#nothing"), "memory entities")
	if err := runMemory(ctx, []string{"search", "#42", "@"}); err == nil {
		t.Error("search with no word or entity accepted")
	}

	out.Reset()
	if err := runMemory(ctx, []string{"entities"}); err != nil {
		t.Fatal(err)
	}
	s = out.String()
	if !strings.HasPrefix(s, "#playtest") {
		t.Fatalf("most memories first:\n%s", s)
	}
	for _, want := range []string{"2 memories (fact 1, who 1) · known by @lead-dark-factory", "@owner", "#ci", "issue-cli memory search #playtest"} {
		assertContains(t, s, want)
	}
	out.Reset()
	if err := runMemory(ctx, []string{"entities", "--only", "agent"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Contains(s, "#playtest ") || !strings.Contains(s, "@owner") {
		t.Fatalf("--only agent:\n%s", s)
	}
	out.Reset()
	if err := runMemory(ctx, []string{"add", "the lead knows deployment best", "--kind", "who"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Hint: a who memory links @<agent or person> to #<topic>")
}

// A department's memories stay out of the index for others (D-14 scopes).
func TestMemoryEntitiesRespectScopes(t *testing.T) {
	memoryAs(t, "worker:seo-pass")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "@seo-lead owns #canonical links", "--kind", "who"}); err != nil {
		t.Fatal(err)
	}
	memoryAs(t, "worker:engine")
	ctx, out := goalsCtx(t, proj, true)
	if err := runMemory(ctx, []string{"entities"}); err != nil {
		t.Fatal(err)
	}
	var idx []memory.Entity
	if err := json.Unmarshal(out.Bytes(), &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx) != 0 {
		t.Fatalf("another department's entities leaked: %+v", idx)
	}
	memoryAs(t, "ceo")
	ctx, out = goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"entities"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "#canonical")
}
