package viewer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/tracker/statustest"
)

func TestRenamedStatusesKeepViewerBehaviour(t *testing.T) {
	wf := statustest.Renamed()
	queue, work := statustest.Name(tracker.StatusRoleQueue), statustest.Name(tracker.StatusRoleWork)

	if !activeStatus(wf, work) || activeStatus(wf, queue) {
		t.Fatal("activeStatus didn't follow the rename")
	}
	if tr := findStartTransition(wf); tr == nil || tr.To != work {
		t.Fatalf("start transition = %+v", tr)
	}
	created, _ := createOptions(wf)
	if slices.Contains(created, queue) || slices.Contains(created, work) {
		t.Fatalf("createOptions = %v", created)
	}
	if got := gateTargets(wf, []string{queue, work}, Attention{}); got[1] != queue {
		t.Fatalf("the queue isn't last: %v", got)
	}
	if got := gateTitle(wf, "x", []string{work, queue}); got != "Approve x: start it now, or queue it in "+queue+"." {
		t.Fatalf("gateTitle = %q", got)
	}
	if then := approveThenFor(wf, []string{queue}); then[queue] != work {
		t.Fatalf("approveThenFor = %v", then)
	}
}

func TestRenamedOwnerTestKeepsAttention(t *testing.T) {
	root := t.TempDir()
	owner := statustest.Name(tracker.StatusRoleOwnerTest)
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("issues/CLI/lead-tab.md", "---\ntitle: \"Lead tab\"\nstatus: \""+owner+"\"\nsystem: \"CLI\"\n---\n\nBody\n")
	write(".worktrees/cli/lead-tab/scripts/checks/lead-tab.sh", "#!/usr/bin/env bash\n# issue: cli/lead-tab\n")
	file := filepath.Join(root, "workflow.yaml")
	statustest.Write(t, file, "")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	write("workflow.yaml", string(raw)+"worktree: true\n")
	proj := tracker.Project{Name: "P", Slug: "p", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: file}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	if a := ownerAttention(&proj, issues[0]); !a.ChecksDue {
		t.Fatalf("the renamed status owes the owner no checks: %+v", a)
	}
}
