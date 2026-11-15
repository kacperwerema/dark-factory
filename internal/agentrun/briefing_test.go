package agentrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// AC3: a dispatch spec carries its briefing record (the prompt's size, the
// decision refs it carried), and recordBriefing puts it in the sidecar,
// which Start and a delivered Reprompt call; building the spec writes none.
func TestDispatchSpecCarriesBriefing(t *testing.T) {
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	for rel, body := range map[string]string{
		"Ops/rules.md": "---\ntitle: \"rules\"\nstatus: \"done\"\nsystem: \"Ops\"\n---\n\n## Decisions\n\n- **D-1** · 2026-10-01 · chat: Project rule {scope: project}\n",
		"UI/work.md":   "---\ntitle: \"work\"\nstatus: \"in progress\"\nsystem: \"UI\"\n---\n\nBody.\n",
	} {
		p := filepath.Join(issues, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &tracker.Project{Slug: "acme", IssueDir: issues, WorkDir: root}
	work := tracker.FindIssue(mustLoad(t, issues), "ui/work")
	spec, refused := DispatchSpec(proj, work, "claude", root, true)
	if refused != nil {
		t.Fatalf("refused: %+v", refused)
	}
	b := spec.Briefing
	if b == nil || b.Kind != tracker.BriefingDispatch || b.Status != "in progress" || b.Bytes != len(spec.Prompt) || b.Session != "agent-ui-work" ||
		len(b.Decisions) != 1 || b.Decisions[0] != "ops/rules#D-1" {
		t.Fatalf("spec briefing %+v (prompt %d bytes)", b, len(spec.Prompt))
	}
	if stats, _ := tracker.LoadStats(work.FilePath); len(stats.Briefings) != 0 {
		t.Fatalf("building the spec recorded a briefing: %+v", stats.Briefings)
	}
	rec := *b
	rec.At = time.Now()
	var steps []Step
	recordBriefing(proj, "ui/work", rec, &steps)
	stats, _ := tracker.LoadStats(work.FilePath)
	if len(steps) != 0 || len(stats.Briefings) != 1 || stats.Briefings[0].Bytes != len(spec.Prompt) {
		t.Errorf("recorded %+v, steps %+v", stats.Briefings, steps)
	}
}

func mustLoad(t *testing.T, dir string) []*tracker.Issue {
	t.Helper()
	all, err := tracker.LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	return all
}
