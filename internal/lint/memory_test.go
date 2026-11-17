package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func memoryFindings(t *testing.T, raw string) []string {
	t.Helper()
	got, err := File("/nowhere/workflow.yaml", []byte(routingBase+raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range got {
		if f.Check == "memory" {
			out = append(out, f.Message)
		}
	}
	return out
}

func TestMemoryLint(t *testing.T) {
	if got := memoryFindings(t, ""); len(got) != 0 {
		t.Fatalf("no memory: block: %v", got)
	}
	ok := "memory:\n  embedder:\n    url: http://127.0.0.1:11434/api/embed\n    model: nomic-embed-text\n    timeout: 3s\n"
	if got := memoryFindings(t, ok); len(got) != 0 {
		t.Fatalf("local embedder: %v", got)
	}
	cmd := "memory:\n  embedder:\n    command: [swift, scripts/memory-embed-macos.swift]\n    model: nl-sentence-en\n"
	if got := memoryFindings(t, cmd); len(got) != 0 {
		t.Fatalf("command embedder: %v", got)
	}
	hosted := "memory:\n  embedder:\n    url: https://api.example.com/v1/embed\n    model: big\n"
	if got := strings.Join(memoryFindings(t, hosted), "\n"); !strings.Contains(got, "leaves this machine") || !strings.Contains(got, "owner") {
		t.Fatalf("hosted embedder not flagged: %q", got)
	}
	bad := "memory:\n  embedder:\n    url: http://localhost:11434/api/embed\n    command: [x]\n    timeout: soon\n    min_similarity: 2\n"
	got := strings.Join(memoryFindings(t, bad), "\n")
	for _, want := range []string{"not both", "needs model", "timeout \"soon\"", "outside 0-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A status renamed after the memory was written would go silent: lint says.
func TestMemoryLintFindsAStatusGone(t *testing.T) {
	root := t.TempDir()
	proj := &tracker.Project{Slug: "acme", Name: "Acme", IssueDir: filepath.Join(root, "issues"), WorkflowFile: filepath.Join(root, "workflow.yaml")}
	if err := os.WriteFile(proj.WorkflowFile, []byte(routingBase), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store := memory.ProjectStore(proj.IssueDir)
	statuses := proj.AllStatuses()
	if len(statuses) == 0 {
		t.Fatal("no statuses in the fixture")
	}
	ok, err := store.Add(memory.Draft{Text: "at a status that exists", Scope: memory.ScopeProject, At: strings.ToUpper(statuses[0])}, memory.AddOptions{}, now)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := store.Add(memory.Draft{Text: "tied to something long renamed", Scope: memory.ScopeProject, At: "qa"}, memory.AddOptions{}, now)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(NewEnv(proj, nil, now), "memory")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings %+v", report.Findings)
	}
	f := report.Findings[0]
	if !strings.Contains(f.Message, gone.Memory.ID) || !strings.Contains(f.Message, `"qa"`) || !strings.Contains(f.Fix, "memory edit "+gone.Memory.ID) {
		t.Fatalf("%+v", f)
	}
	if strings.Contains(f.Message, ok.Memory.ID) {
		t.Fatalf("%+v", f)
	}
}

// A memory that reads as a note on one issue is a warning with its fix; a
// general fact and a decision citation are not.
func TestMemoryLintIssueSpecific(t *testing.T) {
	root := t.TempDir()
	proj := &tracker.Project{Slug: "acme", Name: "Acme", IssueDir: filepath.Join(root, "issues"), WorkflowFile: filepath.Join(root, "workflow.yaml")}
	if err := os.WriteFile(proj.WorkflowFile, []byte(routingBase), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	store := memory.ProjectStore(proj.IssueDir)
	add := func(text string, src string) string {
		t.Helper()
		r, err := store.Add(memory.Draft{Text: text, Scope: memory.ScopeProject, Source: memory.Source{Issue: src}}, memory.AddOptions{Mode: memory.ModeNew}, now)
		if err != nil {
			t.Fatal(err)
		}
		return r.Memory.ID
	}
	note := add("On this issue the gate waited for the owner", "ui/a")
	other := add("Fixed on main (factory/a-long-issue-slug-for-lint-test): the scan reads the worktree", "ui/b")
	add("The design gate wants every hard decision cited", "ui/c")
	add("Decision factory/a-long-issue-slug-for-lint-test#D-2 sets the memory policy", "ui/d")
	issues := []*tracker.Issue{{Slug: "factory/a-long-issue-slug-for-lint-test"}}
	report, err := Run(NewEnv(proj, issues, now), "memory")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings %+v", report.Findings)
	}
	got := report.Findings[0].Message + "\n" + report.Findings[1].Message
	for _, want := range []string{note, other, "reads as a note on one issue", `"this issue"`, "names the issue factory/a-long-issue-slug-for-lint-test"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if !strings.Contains(report.Findings[0].Fix, "memory edit ") || !strings.Contains(report.Findings[0].Fix, "memory forget ") {
		t.Fatalf("fix %q", report.Findings[0].Fix)
	}
}

const memoryStepsWorkflow = `statuses: [{name: idea}, {name: in design}, {name: in progress}, {name: testing}, {name: shipping}, {name: done}]
transitions: [{from: idea, to: in design}, {from: in design, to: in progress}, {from: in progress, to: testing}, {from: testing, to: shipping}, {from: shipping, to: done}]
`

// memoryLintProject is a project on memoryStepsWorkflow and an add helper
// for its memory log.
func memoryLintProject(t *testing.T, now time.Time) (*tracker.Project, func(memory.Draft) string) {
	t.Helper()
	root := t.TempDir()
	proj := &tracker.Project{Slug: "acme", Name: "Acme", IssueDir: filepath.Join(root, "issues"), WorkflowFile: filepath.Join(root, "workflow.yaml")}
	if err := os.WriteFile(proj.WorkflowFile, []byte(memoryStepsWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	store := memory.ProjectStore(proj.IssueDir)
	return proj, func(d memory.Draft) string {
		t.Helper()
		if d.Scope == "" {
			d.Scope = memory.ScopeProject
		}
		if d.Kind == "" {
			d.Kind = memory.KindPitfall
		}
		r, err := store.Add(d, memory.AddOptions{Mode: memory.ModeNew}, now)
		if err != nil {
			t.Fatal(err)
		}
		return r.Memory.ID
	}
}

func memoryLintRun(t *testing.T, proj *tracker.Project, now time.Time, want string) []Finding {
	t.Helper()
	report, err := Run(NewEnv(proj, nil, now), "memory")
	if err != nil {
		t.Fatal(err)
	}
	var out []Finding
	for _, f := range report.Findings {
		if strings.Contains(f.Message, want) {
			out = append(out, f)
		}
	}
	return out
}

// Three retellings of one fact are one cluster, named by id with the merge
// as its fix; a pair and an unrelated memory are not, and no text is shown.
func TestMemoryLintFlagsANearDuplicateCluster(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	proj, add := memoryLintProject(t, now)
	a := add(memory.Draft{Text: "The leak scan fails on a private project name copied from a brief into a test comment or changelog line: write a neutral name like atlas instead."})
	b := add(memory.Draft{Text: "make leak-check refuses a private project name in tracked files such as the changelog or a test comment: describe the project instead of naming it."})
	c := add(memory.Draft{Text: "Tracked files must not carry a private project name from a brief (changelog line, test comment, fixture): the leak scan fails, use a neutral name."})
	pair1 := add(memory.Draft{Text: "Rebuild the worktree issue-cli after a code change before running check-script yes, or the guided check tests old code."})
	pair2 := add(memory.Draft{Text: "check-script yes runs the worktree issue-cli build, so rebuild it after a code change or the guided check tests the old code."})
	other := add(memory.Draft{Text: "Viewer pages are timed with a throwaway test that loads the real config and serves one project through httptest."})
	got := memoryLintRun(t, proj, now, "share most of their words")
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %+v", got)
	}
	f := got[0]
	for _, id := range []string{a, b, c} {
		if !strings.Contains(f.Message, id) {
			t.Errorf("cluster misses %s: %s", id, f.Message)
		}
	}
	for _, id := range []string{pair1, pair2, other} {
		if strings.Contains(f.Message, id) {
			t.Errorf("cluster holds %s: %s", id, f.Message)
		}
	}
	if !strings.Contains(f.Message, "(3, project scope)") || strings.Contains(f.Message, "atlas") {
		t.Errorf("message %q", f.Message)
	}
	if !strings.Contains(f.Fix, "issue-cli memory edit "+f.Where+" --text") || !strings.Contains(f.Fix, "memory forget") {
		t.Errorf("fix %q", f.Fix)
	}
}

// A pitfall or howto about a step with no --at is named with the --at to
// set; tied ones, facts, prose words, closed statuses and workflow edges
// are not.
func TestMemoryLintFlagsAPitfallThatNamesAStepWithoutAt(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	proj, add := memoryLintProject(t, now)
	ship := add(memory.Draft{Text: "Shipping on a busy night: main moves during validate, so merge again and fast-forward at once."})
	design := add(memory.Draft{Kind: memory.KindHowto, Text: "Ask the sandbox question in the Clarify round while the issue is in design."})
	progress := add(memory.Draft{Text: "Run the leak scan before each commit made at in progress."})
	add(memory.Draft{Text: "Shipping: tied already, so nothing to say.", At: "shipping"})
	add(memory.Draft{Kind: memory.KindFact, Text: "The board ships an in progress column."})
	add(memory.Draft{Text: "Headless Chrome for Testing plays H.264 video."})
	add(memory.Draft{Text: "Once done, close the session; when done the worktree goes."})
	add(memory.Draft{Text: "An overlay edge (idea → in progress) is added with no gates."})
	got := memoryLintRun(t, proj, now, "but has no --at")
	byID := map[string]Finding{}
	for _, f := range got {
		byID[f.Where] = f
	}
	if len(got) != 3 {
		t.Fatalf("want 3 findings, got %+v", got)
	}
	for id, st := range map[string]string{ship: "shipping", design: "in design", progress: "in progress"} {
		f, ok := byID[id]
		if !ok {
			t.Errorf("%s (%s) not flagged: %+v", id, st, got)
			continue
		}
		if !strings.Contains(f.Message, "the "+st+" step") || f.Fix != "issue-cli memory edit "+id+" --at "+shell.Quote(st) {
			t.Errorf("%s: %+v", id, f)
		}
	}
}
