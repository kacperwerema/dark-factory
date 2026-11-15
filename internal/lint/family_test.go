package lint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestLintFamilyCountsFindings(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "workflow.yaml")
	os.WriteFile(wf, []byte("template: custom\nstatuses:\n  - name: idea\n    description: raw\ntransitions: []\nbord: {}\n"), 0644)
	issues := filepath.Join(dir, "issues")
	os.MkdirAll(issues, 0755)
	os.WriteFile(filepath.Join(issues, "a.md"), []byte("---\ntitle: \"A\"\nstatus: idea\n---\n"), 0644)
	proj := &tracker.Project{Slug: "t", WorkflowFile: wf, IssueDir: issues}

	ms, err := lintFamily(context.Background(), health.Env{Root: dir, Project: proj, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, m := range ms {
		got[m.Name+" "+m.Labels["check"]] = m.Value
	}
	want := map[string]float64{
		"lint.warnings ":        2,
		"lint.findings schema":  1,
		"lint.findings undated": 1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (all: %v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("metrics = %v, want only %v", got, want)
	}

	if _, err := lintFamily(context.Background(), health.Env{Root: dir, Project: proj, Now: time.Now(), Backfill: true}); !errors.Is(err, health.ErrNoPast) {
		t.Errorf("backfill: err = %v, want ErrNoPast", err)
	}
}

// TestLintRegistersItsFamily: importing lint is what puts the lint family
// on the health registry; health itself does not know it.
func TestLintRegistersItsFamily(t *testing.T) {
	if !slices.Contains(health.Families(), "lint") {
		t.Fatalf("health families = %v, want lint registered by package lint", health.Families())
	}
}
