package viewer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestNeedsYouListsAGateHeldOnlyByAnOwnerBox: 15.11, in another project. A YOLO worker
// moved the issue into human-testing and left HT1 (the Human Testing box,
// the owner's) unticked with a yolo-owner-check: comment. The approval into
// documentation is all the next move needs besides that box, so the issue
// is a ready gate on the Needs you list, not left out of it.
func TestNeedsYouListsAGateHeldOnlyByAnOwnerBox(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("workflow.yaml", `statuses:
  - name: "testing"
  - name: "human-testing"
  - name: "documentation"
transitions:
  - from: "testing"
    to: "human-testing"
    actions:
      - type: "append_section"
        title: "Human Testing"
        body: "- [ ] Human verification complete if required"
  - from: "human-testing"
    to: "documentation"
    actions:
      - type: "require_human_approval"
        status: "documentation"
`)
	write("issues/Plan/favourites.md", "---\ntitle: \"Favourites\"\nstatus: \"human-testing\"\n---\n\n## Testing\n- [x] Tests pass\n\n## Human Testing\n- [ ] Human verification complete if required\n"+
		"<!-- issue-viewer-comments\n{\"id\":1,\"block\":0,\"date\":\"2026-11-15T18:30:47+01:00\",\"text\":\"yolo-owner-check: HT1: owner check, not done (guided check favourites step MV2: needs your eyes) \\\"Human verification complete if required\\\" · from testing\",\"status\":\"open\",\"source\":\"cli\"}\n-->\n")
	proj := tracker.Project{Name: "Atlas", Slug: "atlas", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %d, %v", len(issues), err)
	}
	var gate *WbQueueCard
	rows := needsYouRows(&proj, "", "/p/atlas", issues, time.Now())
	for i := range rows {
		if rows[i].Kind == "gate" && rows[i].Slug == issues[0].Slug {
			gate = &rows[i]
		}
	}
	if gate == nil {
		t.Fatalf("no gate row for %s: rows = %+v", issues[0].Slug, rows)
	}
}
