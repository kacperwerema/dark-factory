package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// sweepProject is a project whose issues dir holds one issue per status.
func sweepProject(t *testing.T, slug string, issues map[string]string) tracker.Project {
	t.Helper()
	root := t.TempDir()
	for name, status := range issues {
		path := filepath.Join(root, "issues", name+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\ntitle: \""+name+"\"\nstatus: \""+status+"\"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tracker.Project{Slug: slug, Name: slug, IssueDir: filepath.Join(root, "issues"), WorkDir: root}
}

// The viewer's sweep closes what each project's sweep finds, once, and keeps
// a closed issue's session when another project's open issue has its name;
// a dead pane goes whoever holds the name.
func TestSweepSessions(t *testing.T) {
	a := sweepProject(t, "a", map[string]string{"shared": "done", "only-a": "done", "dead": "done"})
	b := sweepProject(t, "b", map[string]string{"shared": "in progress", "dead": "in progress"})
	origFind, origClose, origSnap := finishedSessions, closeSession, takeSnapshot
	t.Cleanup(func() { finishedSessions, closeSession, takeSnapshot = origFind, origClose, origSnap })
	takeSnapshot = func() *agentrun.Snapshot { return nil }
	finishedSessions = func(proj *tracker.Project, _ string, in agentrun.SweepInput) []agentrun.Finished {
		if proj.Slug != "a" {
			return nil
		}
		return []agentrun.Finished{
			{Name: "agent-shared", Slug: "shared", Why: "shared is done"},
			{Name: "agent-only-a", Slug: "only-a", Why: "only-a is done"},
			{Name: "agent-only-a", Slug: "only-a", Why: "only-a is done"},
			{Name: "agent-dead", Slug: "dead", Why: "only a shell: its agent exited", Dead: true},
		}
	}
	var closed []string
	closeSession = func(proj *tracker.Project, name string) error {
		closed = append(closed, proj.Slug+"/"+name)
		return nil
	}
	got := sweepSessions([]tracker.Project{a, b})
	if strings.Join(closed, ",") != "a/agent-only-a,a/agent-dead" || len(got) != 2 {
		t.Fatalf("closed %v, returned %v", closed, got)
	}
}
