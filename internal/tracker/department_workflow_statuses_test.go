package tracker

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A department's own status is one a project-wide reference can name, and
// a status both files have counts once.
func TestAllStatusesTakesTheDepartmentsFiles(t *testing.T) {
	root := t.TempDir()
	write := func(name, raw string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("workflow.yaml", "statuses: [{name: idea}, {name: done}]\ntransitions: [{from: idea, to: done}]\n")
	write("workflows/marketing.yaml", "statuses: [{name: Idea}, {name: draft}, {name: done}]\ntransitions: [{from: Idea, to: draft}, {from: draft, to: done}]\n")
	p := &Project{Slug: "acme", Name: "Acme", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml"),
		Departments: []Department{{Name: "Marketing", Workflow: "workflows/marketing.yaml"}}}
	got := p.AllStatuses()
	if want := []string{"idea", "done", "draft"}; !slices.Equal(got, want) {
		t.Fatalf("AllStatuses = %v, want %v", got, want)
	}
}
