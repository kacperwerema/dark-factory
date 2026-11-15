package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

func TestRunWorkflowScanDryRunWritesNothing(t *testing.T) {
	proj := writeUpgradeProject(t, oldDevelopmentWorkflow)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"scan", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"== Workflow scan ==", "Mode:   dry run", "✓ human-approval-gates", "transitions[0].actions[0].type", "· worktree", "+ installed human-approval-gates@", "Record after this scan:", "features:"} {
		assertContains(t, out, want)
	}
	if got, _ := os.ReadFile(proj.WorkflowFile); string(got) != oldDevelopmentWorkflow {
		t.Fatal("scan --dry-run modified workflow.yaml")
	}
}

func TestRunWorkflowScanWritesRecordAndIsIdempotent(t *testing.T) {
	proj := writeUpgradeProject(t, oldDevelopmentWorkflow)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"scan", "--declined", "scoring,worktree"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Mode:   wrote the features: record")
	got, _ := os.ReadFile(proj.WorkflowFile)
	if !strings.HasSuffix(string(got), oldDevelopmentWorkflow) {
		t.Fatalf("scan changed more than the features: block:\n%s", got)
	}
	rec, ok := workflowupgrade.ReadRecord(got)
	if !ok || strings.Join(rec.Declined, ",") != "scoring,worktree" || len(rec.Installed) == 0 {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := tracker.LoadWorkflow(proj.WorkflowFile); err != nil {
		t.Fatalf("LoadWorkflow after scan: %v", err)
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"scan"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "record already up to date, nothing written")
	assertContains(t, stdout.String(), "✗ scoring")
	again, _ := os.ReadFile(proj.WorkflowFile)
	if string(again) != string(got) {
		t.Fatal("second scan changed the file")
	}
}

func TestRunWorkflowScanJSON(t *testing.T) {
	proj := writeUpgradeProject(t, oldDevelopmentWorkflow)
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runWorkflowScan(ctx, []string{"--dry-run"}); err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout.String())
	}
	for _, key := range []string{"workflow_file", "dry_run", "written", "had_record", "features", "record", "block", "changes", "notes"} {
		if _, ok := res[key]; !ok {
			t.Errorf("JSON missing %q", key)
		}
	}
	if res["written"] != false || res["dry_run"] != true {
		t.Errorf("dry_run/written = %v/%v", res["dry_run"], res["written"])
	}
}

func TestRunWorkflowScanRejectsArgs(t *testing.T) {
	proj := writeUpgradeProject(t, oldDevelopmentWorkflow)
	ctx, _, _ := newTestContext(proj, false)
	if err := runWorkflowScan(ctx, []string{"extra"}); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunWorkflowUpgradeListsDeclinedAndRecord(t *testing.T) {
	proj := writeUpgradeProject(t, "features:\n  declined: [scoring]\nworktree: true\n"+oldDevelopmentWorkflow)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	assertContains(t, out, "Record:   features: (0 installed, 1 declined)")
	assertContains(t, out, "Declined (not proposed; re-open with `issue-cli workflow scan --forget <id>`): scoring")
	assertContains(t, out, "Detected but not in the features: record (run `issue-cli workflow scan`): ")
	assertContains(t, out, "worktree\n")

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"upgrade", "--all"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "declined earlier")
}

// Each category heading prints once, however the catalog orders entries.
func TestRunWorkflowScanPrintsEachCategoryOnce(t *testing.T) {
	proj := writeUpgradeProject(t, oldDevelopmentWorkflow)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"scan", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, c := range workflowupgrade.Categories {
		if n := strings.Count(out, "\n  "+c+"\n"); n != 1 {
			t.Errorf("heading %q printed %d times:\n%s", c, n, out)
		}
	}
}

// A stamp at the bundled version with a stale record: upgrade doesn't say
// up to date, and process workflow warns.
func TestStaleRecordIsReportedByUpgradeAndProcess(t *testing.T) {
	data, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	proj := writeUpgradeProject(t, string(data)+"\nci:\n  run:\n    - cmd: go test ./...\n")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	assertContains(t, out, "Status:   "+workflowupgrade.StatusStaleRecord)
	assertContains(t, out, "issue-cli workflow scan")
	if strings.Contains(out, "up_to_date") {
		t.Errorf("upgrade calls a stale record up to date:\n%s", out)
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runProcess(ctx, []string{"workflow"}); err != nil {
		t.Fatal(err)
	}
	out = stdout.String()
	assertContains(t, out, "== Warnings (workflow.yaml) ==")
	assertContains(t, out, "features: record")
	assertContains(t, out, "local-ci")

	// Undecided proposals alone are the owner's call: upgrade says so,
	// process workflow stays quiet.
	proj = writeUpgradeProject(t, string(data))
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Status:   "+workflowupgrade.StatusUndecided)
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runProcess(ctx, []string{"workflow"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "features: record") {
		t.Errorf("process workflow warns about undecided proposals:\n%s", stdout.String())
	}
}

// scanWorktreeRepo makes a repo whose main holds workflow.yaml (committed in
// a second commit) and two linked worktrees under .worktrees/: "x" on main's
// head and "old" on the first commit, which has no workflow.yaml. The project
// points at the primary checkout's file, as projects.yaml does.
func scanWorktreeRepo(t *testing.T) (proj *tracker.Project, primary string) {
	t.Helper()
	primary = t.TempDir() // not resolved: on a Mac it is under /var, a link
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
			"-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "core.hooksPath=/dev/null"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(primary, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(primary, ".gitignore"), []byte(".worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(primary, "add", "-A")
	git(primary, "commit", "-q", "-m", "first")
	if err := os.WriteFile(filepath.Join(primary, "workflow.yaml"), []byte(oldDevelopmentWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	git(primary, "add", "-A")
	git(primary, "commit", "-q", "-m", "workflow")
	git(primary, "worktree", "add", "-q", "-b", "work/x", filepath.Join(primary, ".worktrees", "x"))
	git(primary, "worktree", "add", "-q", "-b", "work/old", filepath.Join(primary, ".worktrees", "old"), "HEAD~1")
	return &tracker.Project{Name: "test", Slug: "test", IssueDir: filepath.Join(primary, "issues"), WorkflowFile: filepath.Join(primary, "workflow.yaml")}, primary
}

// Run from a worker's worktree, scan writes the worktree's workflow.yaml,
// not the primary checkout's (main's working tree); a worktree without the
// file is refused, and from the primary checkout scan still writes there.
func TestWorkflowScanFromAWorktreeWritesTheWorktreesFile(t *testing.T) {
	proj, primary := scanWorktreeRepo(t)
	wt := filepath.Join(primary, ".worktrees", "x")
	chdir(t, wt)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"scan"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Mode:   wrote the features: record")
	if got, _ := os.ReadFile(filepath.Join(primary, "workflow.yaml")); string(got) != oldDevelopmentWorkflow {
		t.Fatal("scan from a worktree wrote the primary checkout's workflow.yaml")
	}
	got, _ := os.ReadFile(filepath.Join(wt, "workflow.yaml"))
	if _, ok := workflowupgrade.ReadRecord(got); !ok {
		t.Fatalf("the worktree's workflow.yaml has no record:\n%s", got)
	}

	// upgrade reads the same file scan writes: the worktree's, with its record.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Record:   features: (")

	chdir(t, filepath.Join(primary, ".worktrees", "old"))
	ctx, _, _ = newTestContext(proj, false)
	err := runWorkflow(ctx, []string{"scan"})
	if err == nil || !strings.Contains(err.Error(), "has no workflow.yaml") {
		t.Fatalf("scan in a worktree without the file: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(primary, "workflow.yaml")); string(got) != oldDevelopmentWorkflow {
		t.Fatal("the refused scan wrote the primary checkout's workflow.yaml")
	}

	chdir(t, primary)
	ctx, _, _ = newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"scan"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(primary, "workflow.yaml")); string(got) == oldDevelopmentWorkflow {
		t.Fatal("scan from the primary checkout no longer writes its workflow.yaml")
	}
}
