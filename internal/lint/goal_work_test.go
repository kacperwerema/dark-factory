package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func goalProject(t *testing.T, goals []tracker.Goal, files map[string]string) *Env {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "issues")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, front := range files {
		body := "---\ntitle: \"" + name + "\"\ncreated: \"2026-11-01\"\n" + front + "---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &tracker.Project{Slug: "orchard", IssueDir: dir, WorkDir: root}
	if goals != nil {
		proj.Company = &tracker.Company{Goals: goals}
	}
	env, err := Load(proj, time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// AC1: two goals, one with an open issue and one whose only issue is
// closed: lint warns once, naming the second.
func TestLintGoalWithoutWork(t *testing.T) {
	env := goalProject(t, []tracker.Goal{{ID: "clients", Name: "Paying clients"}, {ID: "revenue", Name: "Revenue"}}, map[string]string{
		"case-studies": "status: backlog\ngoal: hq/clients\n",
		"old-pricing":  "status: done\ngoal: hq/revenue\n",
	})
	rep, err := Run(env, "goal-without-work")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Warnings != 1 || len(rep.Findings) != 1 {
		t.Fatalf("findings = %+v, want one", rep.Findings)
	}
	f := rep.Findings[0]
	if !strings.Contains(f.Message, "hq/revenue (Revenue)") || strings.Contains(f.Message, "clients") {
		t.Errorf("message = %q, want it to name hq/revenue only", f.Message)
	}
	if !strings.Contains(f.Fix, `issue-cli --project orchard create --title "…" --goal hq/revenue`) {
		t.Errorf("fix = %q, want the create --goal command", f.Fix)
	}
}

// AC4: no goals anywhere is one line, not one per owner.
func TestLintProjectWithNoGoals(t *testing.T) {
	env := goalProject(t, nil, map[string]string{"some-work": "status: backlog\n"})
	env.Project.Departments = []tracker.Department{{Name: "Marketing"}, {Name: "Sales"}}
	rep, err := Run(env, "goal-without-work")
	if err != nil {
		t.Fatal(err)
	}
	// A note, not a warning: a new project from the templates starts
	// clean (TestBundledTemplatesHaveNoFindings).
	if rep.Warnings != 0 || len(rep.Findings) != 0 || len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0].Message, "no goals in projects.yaml") {
		t.Fatalf("findings = %+v, notes = %+v, want one no-goals note", rep.Findings, rep.Notes)
	}
	if !strings.Contains(rep.Notes[0].Fix, "issue-cli --project orchard department update hq --goal") {
		t.Errorf("fix = %q", rep.Notes[0].Fix)
	}
}
