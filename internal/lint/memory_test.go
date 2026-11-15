package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
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
