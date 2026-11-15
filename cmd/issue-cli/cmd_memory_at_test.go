package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const memAtWorkflow = `statuses:
  - name: "idea"
  - name: "in design"
  - name: "in progress"
  - name: "testing"
  - name: "shipping"
  - name: "done"
transitions:
  - from: "idea"
    to: "in design"
  - from: "in design"
    to: "in progress"
  - from: "in progress"
    to: "testing"
  - from: "testing"
    to: "shipping"
  - from: "shipping"
    to: "done"
`

// newTestProject is a project on memAtWorkflow with one issue, "plain".
func newTestProject(t *testing.T) *tracker.Project {
	t.Helper()
	root := realTempDir(t)
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "workflow.yaml"), memAtWorkflow)
	mustWrite(t, filepath.Join(issues, "plain.md"), "---\ntitle: \"Plain\"\nstatus: \"idea\"\n---\nBody.\n")
	return &tracker.Project{Name: "acme", Slug: "acme", IssueDir: issues, WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

func testContext(proj *tracker.Project) (*Context, *bytes.Buffer, *bytes.Buffer) {
	return newTestContext(proj, false)
}

func memAt(t *testing.T, proj *tracker.Project, text, status string) memory.Memory {
	t.Helper()
	res, err := memory.ProjectStore(proj.IssueDir).Add(memory.Draft{Text: text, Scope: memory.ScopeProject, At: status}, memory.AddOptions{Mode: memory.ModeNew}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return res.Memory
}

func TestMemoryAddAtStatus(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	ctx, out, _ := testContext(proj)
	if err := runMemory(ctx, []string{"add", "merge main again right before the ff", "--at", "In Progress"}); err != nil {
		t.Fatal(err)
	}
	if m := onlyMemory(t, proj); m.At != "in progress" {
		t.Fatalf("stored as %q, want the workflow's spelling", m.At)
	}
	if s := out.String(); !strings.Contains(s, "on entering in progress") {
		t.Errorf("add's closing line: %s", s)
	}
	ctx, out, _ = testContext(proj)
	if err := runMemory(ctx, []string{"list", "--at", "in progress"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "· at in progress") {
		t.Errorf("list: %s", s)
	}
	ctx, out, _ = testContext(proj)
	if err := runMemory(ctx, []string{"list", "--at", "done"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Contains(s, "merge main") {
		t.Errorf("the filter let it through: %s", s)
	}
}

// AC2's refusal: a typo writes nothing and names the statuses.
func TestMemoryAtRefusesAnUnknownStatus(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	ctx, _, _ := testContext(proj)
	err := runMemory(ctx, []string{"add", "a fact about a step", "--at", "shiping"})
	if err == nil || !strings.Contains(err.Error(), "no such status") || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("got %v", err)
	}
	if all := memSet(t, proj).All(); len(all) != 0 {
		t.Fatalf("written anyway: %+v", all)
	}
	m := memAt(t, proj, "a fact about a step", "")
	ctx, _, _ = testContext(proj)
	if err := runMemory(ctx, []string{"edit", m.ID, "--at", "shiping", "--text", "changed words here"}); err == nil {
		t.Fatal("edit took a status no workflow has")
	}
	if got := onlyMemory(t, proj); got.Text != m.Text {
		t.Fatalf("a refused edit changed the text: %q", got.Text)
	}
}

// AC2: an existing memory takes a status and gives it up.
func TestMemoryEditSetsAndClearsTheStatus(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	m := memAt(t, proj, "a fact about a step", "")
	ctx, out, _ := testContext(proj)
	if err := runMemory(ctx, []string{"edit", m.ID, "--at", "testing"}); err != nil {
		t.Fatal(err)
	}
	if got := onlyMemory(t, proj); got.At != "testing" || got.Text != m.Text {
		t.Fatalf("%+v", got)
	}
	if s := out.String(); !strings.Contains(s, "on entering testing") {
		t.Errorf("edit: %s", s)
	}
	ctx, out, _ = testContext(proj)
	if err := runMemory(ctx, []string{"show", m.ID}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "set-at") || !strings.Contains(s, ": at testing") {
		t.Errorf("history: %s", s)
	}
	ctx, _, _ = testContext(proj)
	if err := runMemory(ctx, []string{"edit", m.ID, "--at", "none"}); err != nil {
		t.Fatal(err)
	}
	if got := onlyMemory(t, proj); got.At != "" {
		t.Fatalf("not cleared: %+v", got)
	}
	ctx, _, _ = testContext(proj)
	if err := runMemory(ctx, []string{"edit", m.ID}); err == nil {
		t.Fatal("edit with nothing to change")
	}
}

func transitionOut(t *testing.T, proj *tracker.Project, slug, to string) string {
	t.Helper()
	ctx, out, _ := testContext(proj)
	if err := runTransition(ctx, []string{slug, "--to", to}); err != nil {
		t.Fatalf("transition: %v\n%s", err, out.String())
	}
	return out.String()
}

// The What and AC3: entering a status prints its memories, whatever the
// issue is about, and records the use.
func TestTransitionPrintsTheMemoriesOfTheStatusItEnters(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	here := memAt(t, proj, "fast forward right after the merge of main", "in design")
	memAt(t, proj, "a note for a later status entirely", "done")
	memAt(t, proj, "a memory with no status at all", "")

	out := transitionOut(t, proj, "plain", "in design")
	if !strings.Contains(out, "== Memories for in design ==") || !strings.Contains(out, here.ID+" (fact) fast forward") {
		t.Fatalf("no block:\n%s", out)
	}
	if strings.Contains(out, "later status") || strings.Contains(out, "no status at all") {
		t.Errorf("a memory of no such status printed:\n%s", out)
	}
	if got := memSet(t, proj).Get(here.ID); got.Uses != 1 {
		t.Errorf("uses %d, want 1", got.Uses)
	}
}

// AC3: no memory, no lines at all.
func TestTransitionWithNoMemoryAtTheStatusAddsNoLines(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	memAt(t, proj, "a note for shipping only", "shipping")
	out := transitionOut(t, proj, "plain", "in design")
	if strings.Contains(out, "Memories") || strings.Contains(out, "shipping only") {
		t.Fatalf("extra lines:\n%s", out)
	}
}

// AC3: the cap, and the line that finds the rest.
func TestTransitionCapsTheMemories(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	for _, w := range []string{"alpha one", "bravo two", "charlie three", "delta four", "echo five"} {
		memAt(t, proj, w, "in design")
	}
	out := transitionOut(t, proj, "plain", "in design")
	if n := strings.Count(out, " (fact) "); n != 3 {
		t.Fatalf("%d memories printed, want 3:\n%s", n, out)
	}
	if !strings.Contains(out, "+2 more:") || !strings.Contains(out, `memory list --at 'in design'`) {
		t.Fatalf("no more line:\n%s", out)
	}
}

// A department's memory stays with the department; a pin stays in the
// briefing; --json stays as it was.
func TestTransitionMemoriesKeepScopesAndPins(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	store := memory.ProjectStore(proj.IssueDir)
	now := time.Now()
	if _, err := store.Add(memory.Draft{Text: "only the marketing folk read this", Scope: memory.DepartmentScope("marketing"), At: "in design"}, memory.AddOptions{Mode: memory.ModeNew}, now); err != nil {
		t.Fatal(err)
	}
	pin := memAt(t, proj, "pinned and in every briefing", "in design")
	if err := store.Pin(pin.ID, "owner", true, now); err != nil {
		t.Fatal(err)
	}
	if out := transitionOut(t, proj, "plain", "in design"); strings.Contains(out, "Memories for") {
		t.Fatalf("printed:\n%s", out)
	}
}

func TestTransitionJSONCarriesNoMemoryBlock(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	memAt(t, proj, "a step note here", "in design")
	ctx, out, _ := testContext(proj)
	ctx.JSONOutput = true
	if err := runTransition(ctx, []string{"plain", "--to", "in design"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "a step note") {
		t.Fatalf("in the JSON: %s", out.String())
	}
}

// A worker resuming at a status, or moved there by start, reads them under
// == Memories ==, once.
func TestStartPrintsTheMemoriesOfTheCurrentStatusOnce(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	m := memAt(t, proj, "status words about the idea step", "idea")
	ctx, out, _ := testContext(proj)
	if err := runStart(ctx, []string{"plain"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "At idea:") {
		t.Fatalf("no At line:\n%s", s)
	}
	if n := strings.Count(s, m.ID); n != 1 {
		t.Fatalf("printed %d times:\n%s", n, s)
	}
}
