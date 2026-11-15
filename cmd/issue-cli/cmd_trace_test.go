package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const traceTestWorkflow = `statuses:
  - name: "in design"
  - name: "backlog"
  - name: "in progress"
  - name: "done"
transitions:
  - from: "in design"
    to: "backlog"
  - from: "backlog"
    to: "in progress"
  - from: "in progress"
    to: "done"
`

// makeTraceProject is one issue, "design", in design, run as its worker,
// with transcripts looked up in an empty folder.
func makeTraceProject(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "issues", "design.md"), []byte("---\ntitle: \"Design\"\nstatus: \"in design\"\n---\n\nBody of Design.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(traceTestWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "design")
	old := traceProjectsDir
	traceProjectsDir = t.TempDir()
	t.Cleanup(func() { traceProjectsDir = old })
	return &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

// AC1: a real transition rewrites the trace file, one span per visit, and
// trace --write writes the same file.
func TestTransitionWritesTrace(t *testing.T) {
	proj := makeTraceProject(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	ctx, out, errOut := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runTransition(ctx, []string{"design", "--to", "backlog"}); err != nil {
		t.Fatalf("transition: %v\n%s", err, errOut)
	}
	path := trace.Path(proj.IssueDir, "design")
	if !strings.Contains(out.String(), "Trace: "+path) || !strings.Contains(out.String(), "trace design") {
		t.Errorf("transition does not name the trace:\n%s", out)
	}
	spans, err := trace.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 || spans[0].Status != "in design" || spans[0].Gate == nil || spans[0].Gate.To != "backlog" || spans[1].Status != "backlog" || !spans[1].Open {
		t.Fatalf("spans %+v", spans)
	}
	written, _ := os.ReadFile(path)
	ctx, _, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runTrace(ctx, []string{"design", "--write"}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(written) {
		t.Errorf("trace --write differs from the transition's file:\n%s\n---\n%s", written, again)
	}
	// A dry run writes nothing new.
	os.Remove(path)
	ctx, _, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"design", "--to", "in progress", "--dry-run"}); err != nil {
		t.Logf("dry run: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a dry run wrote the trace")
	}
}

// AC4: trace prints a table by stage, --json the spans, and writes nothing.
func TestTraceCommand(t *testing.T) {
	proj := makeTraceProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runTrace(ctx, []string{"design"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"== Trace: Design ==", "STATUS", "in design", "Not read: Transcripts", "File: "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("trace lacks %q:\n%s", want, out)
		}
	}
	ctx, out, _ = newTestContext(proj, true)
	if err := runTrace(ctx, []string{"design", "--json"}); err != nil {
		t.Fatal(err)
	}
	var got traceOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if got.Slug != "design" || len(got.Spans) != 1 || got.Spans[0].Tokens.Known {
		t.Errorf("--json %+v", got)
	}
	if _, err := os.Stat(trace.Path(proj.IssueDir, "design")); err == nil {
		t.Error("trace without --write wrote the file")
	}
}

// AC3: start records its briefing (bytes of its output, the memory ids and
// decision refs it showed) in the stats sidecar.
func TestStartRecordsBriefing(t *testing.T) {
	proj := makeTraceProject(t)
	ctx, out, errOut := newTestContext(proj, false)
	if err := runStart(ctx, []string{"design"}); err != nil {
		t.Fatalf("start: %v\n%s", err, errOut)
	}
	stats, err := tracker.LoadStats(filepath.Join(proj.IssueDir, "design.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Briefings) != 1 {
		t.Fatalf("briefings %+v", stats.Briefings)
	}
	b := stats.Briefings[0]
	if b.Kind != tracker.BriefingStart || b.Status != "in design" || b.Bytes != out.Len() || b.Tokens != out.Len()/4 {
		t.Errorf("briefing %+v, output %d bytes", b, out.Len())
	}
}

// AC3: reads record no briefing.
func TestReadsRecordNoBriefing(t *testing.T) {
	proj := makeTraceProject(t)
	for name, run := range map[string]func(*Context, []string) error{"show": runShow, "dispatch-prompt": runDispatchPrompt, "trace": runTrace} {
		ctx, _, errOut := newTestContext(proj, false)
		if err := run(ctx, []string{"design"}); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, errOut)
		}
	}
	stats, _ := tracker.LoadStats(filepath.Join(proj.IssueDir, "design.md"))
	if len(stats.Briefings) != 0 {
		t.Errorf("a read recorded a briefing: %+v", stats.Briefings)
	}
}

// AC5: bench compare prints the By stage section when the runs are traced.
func TestBenchComparePrintsByStage(t *testing.T) {
	c := bench.Comparison{A: "b/a", B: "b/b", Comparable: true, Stages: []bench.StageDelta{
		{Status: "in design", A: bench.StageSide{Tasks: 2, Tokens: 2000, TokensKnown: 2, WallSec: 90, Calls: 5, FirstTry: 1},
			B: bench.StageSide{Tasks: 2, WallSec: 60, Calls: 3, FirstTry: -1}},
	}}
	var buf bytes.Buffer
	printComparison(&buf, c)
	for _, want := range []string{"By stage (mean per task that visited it, A → B):", "in design", "2k → —", "1m → 1m", "100% → —"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("compare lacks %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	c.Stages = nil
	printComparison(&buf, c)
	if strings.Contains(buf.String(), "By stage") {
		t.Error("an untraced comparison prints a By stage section")
	}
}
