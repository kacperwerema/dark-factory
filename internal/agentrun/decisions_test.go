package agentrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The dispatch and lead specs carry the injected event their briefing's
// block writes at launch, and none when nothing is in force.
func TestSpecsCarryInjectedDecisions(t *testing.T) {
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	for rel, body := range map[string]string{
		"Ops/rules.md": "---\ntitle: \"rules\"\nstatus: \"done\"\nsystem: \"Ops\"\n---\n\n## Decisions\n\n- **D-1** · 2026-10-01 · chat: Project rule {scope: project}\n- **D-2** · 2026-10-02 · chat: UI rule {scope: system:UI}\n",
		"UI/work.md":   "---\ntitle: \"work\"\nstatus: \"in progress\"\nsystem: \"UI\"\n---\n\nBody.\n",
	} {
		p := filepath.Join(issues, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &tracker.Project{Slug: "acme", IssueDir: issues, WorkDir: root}
	all, _ := tracker.LoadIssues(issues)
	var work *tracker.Issue
	for _, i := range all {
		if i.Slug == "ui/work" {
			work = i
		}
	}
	spec, refused := DispatchSpec(proj, work, "claude", root, true)
	if refused != nil {
		t.Fatalf("refused: %+v", refused)
	}
	if d := spec.Decisions; d == nil || d.Briefing != tracker.BriefingDispatch || d.Issue != "ui/work" || len(d.Refs) != 2 || d.Actor.Name != "agent-ui-work" {
		t.Errorf("dispatch spec decisions %+v", spec.Decisions)
	}
	lead := LeadSpec(proj, nil, root, time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local))
	if d := lead.Decisions; d == nil || d.Briefing != tracker.BriefingLead || len(d.Refs) != 1 || d.Refs[0] != "ops/rules#D-1" {
		t.Errorf("lead spec decisions %+v", lead.Decisions)
	}
	if workerInjected(tracker.BriefingDispatch, "ui/x", nil) != nil {
		t.Error("an empty block should write nothing")
	}
}
