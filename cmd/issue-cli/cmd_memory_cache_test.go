package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/briefing"
	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Memory as a cache (docs/memory.md): usefulness from the traces, the
// durable-facts rule on add.

// writeTraceSpans writes issue slug's trace with one closed in-design
// span per entry of firstTry, an hour each from t0, the way internal/trace
// writes it.
func writeTraceSpans(t *testing.T, issueDir, slug, status string, t0 time.Time, firstTry ...bool) {
	t.Helper()
	path := filepath.Join(issueDir, memory.TraceDir, filepath.FromSlash(slug)+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i, ft := range firstTry {
		from := t0.Add(time.Duration(i) * time.Hour)
		line, _ := json.Marshal(map[string]any{"status": status, "from": from, "to": from.Add(time.Hour), "wall_sec": 3600,
			"tokens": map[string]any{"known": false}, "gate": map[string]any{"to": "backlog", "tries": 1, "first_try": ft}})
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// start records each memory it shows with the issue and the status.
func TestStartRecordsTheStageOfEachUse(t *testing.T) {
	memoryAs(t, "ceo")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "SEO pass: check robots.txt first", "--kind", "howto", "--scope", "project"}); err != nil {
		t.Fatal(err)
	}
	var w strings.Builder
	printStartMemories(&w, ctx, &tracker.Issue{Slug: "seo-pass", Title: "SEO pass", System: "SEO", Status: "idea"})
	data, err := os.ReadFile(memory.ProjectPath(proj.IssueDir))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), `"op":"use"`)
	assertContains(t, string(data), `"status":"idea","issue":"seo-pass"`)
}

// usefulFixture: m (helps) reached 8 design stages that all passed first
// try; n (no help) reached 16 that all failed; other issues' design stages
// pass half the time. It returns the two ids.
func usefulFixture(t *testing.T, proj *tracker.Project, ctx *Context) (good, bad string) {
	t.Helper()
	for _, args := range [][]string{
		{"add", "The design gate wants every hard decision cited", "--kind", "pitfall", "--scope", "project"},
		{"add", "Playtest shots live under the project root's playtest runs", "--kind", "fact", "--scope", "project"},
	} {
		if err := runMemory(ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range memSet(t, proj).All() {
		if strings.HasPrefix(m.Text, "The design gate") {
			good = m.ID
		} else {
			bad = m.ID
		}
	}
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	store := memory.ProjectStore(proj.IssueDir)
	for i := 0; i < 16; i++ {
		day := t0.Add(time.Duration(i) * 24 * time.Hour)
		gs, bs := fmt.Sprintf("ui/good-%02d", i), fmt.Sprintf("ui/bad-%02d", i)
		if i < 8 {
			writeTraceSpans(t, proj.IssueDir, gs, "in design", day, true)
			if err := store.RecordUse([]string{good}, "agent-x", gs, "in design", day.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		writeTraceSpans(t, proj.IssueDir, bs, "in design", day, false)
		if err := store.RecordUse([]string{bad}, "agent-x", bs, "in design", day.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		writeTraceSpans(t, proj.IssueDir, fmt.Sprintf("ui/base-%02d", i), "in design", day, i%2 == 0, true)
	}
	return good, bad
}

// memory show, list --json and the consolidate pack show the score; an
// unhelpful memory is proposed to the owner and nothing is forgotten.
func TestMemoryShowUsefulness(t *testing.T) {
	memoryAs(t, "ceo")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	good, bad := usefulFixture(t, proj, ctx)

	out.Reset()
	if err := runMemory(ctx, []string{"show", good}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Usefulness: +")
	assertContains(t, s, "over 8 (helps)")
	assertContains(t, s, "in design +")

	out.Reset()
	if err := runMemory(ctx, []string{"show", bad}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "(no help)")
	assertContains(t, out.String(), "the next memory consolidate proposes forgetting it to the owner")

	jctx, jout := goalsCtx(t, proj, true)
	if err := runMemory(jctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	var mems []memory.Memory
	if err := json.Unmarshal(jout.Bytes(), &mems); err != nil {
		t.Fatal(err)
	}
	for _, m := range mems {
		if m.Usefulness == nil || !m.Usefulness.Measured() {
			t.Fatalf("list --json without usefulness: %+v", m)
		}
	}

	before, _ := os.ReadFile(memory.ProjectPath(proj.IssueDir))
	out.Reset()
	if err := runMemory(ctx, []string{"consolidate"}); err != nil {
		t.Fatal(err)
	}
	s = out.String()
	assertContains(t, s, "Unhelpful: shown in 15+ traced stages")
	assertContains(t, s, bad)
	assertContains(t, s, "    stages: scored: in design 16\n")
	assertContains(t, s, "propose forgetting to the owner, never forget them in this run")
	after, _ := os.ReadFile(memory.ProjectPath(proj.IssueDir))
	if !bytes.Equal(before, after) {
		t.Fatal("consolidate wrote to the log")
	}
	if !memSet(t, proj).Get(bad).Valid(ctx.now()) {
		t.Fatal("a score forgot a memory")
	}

	// Pinned: never proposed.
	if err := runMemory(ctx, []string{"pin", bad}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runMemory(ctx, []string{"consolidate"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "not pinned (0)")
}

// A worker's add of a note on its own issue is refused with where it
// belongs; general facts pass; a lead gets a warning only.
func TestMemoryAddRefusesIssueSpecific(t *testing.T) {
	memoryAs(t, "worker:engine")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	for _, text := range []string{
		"On this issue the gate needs the Design boxes first",
		"Pushed my branch before the trace merged",
		"work/factory/memory-as-a-cache-usefulness-from-traces has the trace merged",
	} {
		err := runMemory(ctx, []string{"add", text})
		if err == nil {
			t.Fatalf("%q was added", text)
		}
		for _, want := range []string{"refused: this reads as a note on one issue", "HANDOFF.md", "post-mortem", "memory consolidate"} {
			assertContains(t, err.Error(), want)
		}
	}
	for _, text := range []string{
		"The engine package builds with go build",
		"internal/memory/usefulness.go reads the traces under .traces",
		"The testing status runs make validate in the background",
	} {
		if err := runMemory(ctx, []string{"add", text, "--new"}); err != nil {
			t.Fatalf("%q refused: %v", text, err)
		}
	}

	memoryAs(t, "ceo")
	lctx, _, errOut := newTestContext(proj, false)
	if err := runMemory(lctx, []string{"add", "this issue shows the lead only gets a warning", "--new"}); err != nil {
		t.Fatalf("a lead was refused: %v", err)
	}
	assertContains(t, errOut.String(), "warning: this reads as a note on one issue")
}

// The briefing's ## Memory and start's add line say what memory is for.
func TestMemoryHintsSayDurableFacts(t *testing.T) {
	memoryAs(t, "ceo")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	var w strings.Builder
	printStartMemories(&w, ctx, &tracker.Issue{Slug: "seo-pass", Title: "SEO pass", Status: "idea"})
	assertContains(t, w.String(), "durable project fact")
	assertContains(t, briefing.MemoryPromptSection(proj, &tracker.Issue{Slug: "seo-pass"}), "durable project facts")
}

// docs/memory.md describes the layers and the rules with their numbers.
func TestMemoryDocsLayers(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start := strings.Index(doc, "## Memory as a cache")
	if start < 0 {
		t.Fatal(`docs/memory.md has no "## Memory as a cache" section`)
	}
	sec := doc[start:]
	if end := strings.Index(sec[3:], "\n## "); end >= 0 {
		sec = sec[:end+3]
	}
	for _, want := range []string{
		"Working context", "HANDOFF.md", "Long-term memory", "Rules and decisions", "Procedures",
		"first try", fmt.Sprintf("%d", memory.UsefulMin), fmt.Sprintf("%d", memory.UsefulWindow), fmt.Sprintf("%d", memory.ForgetMin),
		"--at", "pinned", "post-mortem", "memory consolidate",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Memory as a cache does not say %q", want)
		}
	}
}

// Stages at a status with no room to lift are counted, not scored: the
// line says so, so "not measured" isn't read as "never reached a stage".
func TestUsefulnessLineNamesNoRoomStages(t *testing.T) {
	line := usefulnessLine(memory.Usefulness{Injections: 20, NoRoom: 20})
	assertContains(t, line, "not measured yet (0 of 8")
	assertContains(t, line, "20 not scored (statuses that nearly always pass first try)")
	mixed := usefulnessLine(memory.Usefulness{Injections: 20, NoRoom: 20, NoRoomAt: map[string]int{"testing": 2, "idea": 18}})
	assertContains(t, mixed, "20 not scored (statuses that nearly always pass first try: idea 18, testing 2)")
	if l := usefulnessLine(memory.Usefulness{Injections: 3, Score: memory.Score{Known: 3}}); strings.Contains(l, "not scored") {
		t.Fatalf("no no-room stages: %s", l)
	}
}
