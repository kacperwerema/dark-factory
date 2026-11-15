package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// makeDriftProject is a git repo with a board: a hard decision on
// internal/store/**, a replaced one cited by an open issue and by the
// docs' Settled rules, and a direct commit on main touching the store.
func makeDriftProject(t *testing.T) *tracker.Project {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	proj := &tracker.Project{Name: "proj", Slug: "proj", WorkDir: root, IssueDir: filepath.Join(root, "issues"), WorkflowFile: filepath.Join(root, "workflow.yaml")}
	for _, d := range []string{proj.IssueDir, filepath.Join(root, "docs"), filepath.Join(root, "internal", "store")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, proj.WorkflowFile, "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n")
	mustWrite(t, filepath.Join(proj.IssueDir, "rules.md"), "---\ntitle: \"Rules\"\nstatus: \"done\"\n---\n\n## Decisions\n\n"+
		`- **D-1** · 2026-10-01 · chat: The store is append-only, "append only" {scope: project; hard; paths: internal/store/**}`+"\n"+
		`- **D-2** · 2026-10-01 · chat: Pages are plain, "plain"`+"\n"+
		`- **D-3** · 2026-10-02 · chat: Pages may use one script, "one script" (replaces rules#D-2)`+"\n")
	mustWrite(t, filepath.Join(proj.IssueDir, "open.md"), "---\ntitle: \"Open\"\nstatus: \"in progress\"\n---\n\nFollows rules#D-2.\n")
	mustWrite(t, filepath.Join(root, "docs", "departments.md"), "# Departments\n\n## Settled rules\n\n- **Pages.** Plain. Source: rules#D-2.\n")
	mustWrite(t, filepath.Join(root, ".gitignore"), "issues/\n")
	mustWrite(t, filepath.Join(root, "README.md"), "hi\n")
	respectedGit(t, root, "init", "-q", "-b", "main")
	respectedGit(t, root, "add", "-A")
	respectedGit(t, root, "commit", "-q", "-m", "chore: init")
	mustWrite(t, filepath.Join(root, "internal", "store", "db.go"), "package store\n")
	respectedGit(t, root, "add", "-A")
	respectedGit(t, root, "commit", "-q", "-m", "fix: store straight on main")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	return proj
}

func runDrift(t *testing.T, proj *tracker.Project, jsonOut bool, args ...string) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, jsonOut)
	if err := runDecisions(ctx, append([]string{"drift"}, args...)); err != nil {
		t.Fatalf("decisions drift %v: %v", args, err)
	}
	return stdout.String()
}

func TestDecisionsDriftPlainRunWritesNothing(t *testing.T) {
	proj := makeDriftProject(t)
	out := runDrift(t, proj, false)
	for _, want := range []string{
		"Decision drift · main since ",
		"## Open issues citing replaced or expired decisions (1)\n\n- open cites rules#D-2, replaced by rules#D-3",
		"## Hard decisions' paths changed on main without a citation (1)",
		`"fix: store straight on main" (no issue) changed internal/store/db.go`,
		"## Settled rules pointing at replaced or expired decisions (1)\n\n- docs/departments.md:5 cites rules#D-2, replaced by rules#D-3",
		"fix: point docs/departments.md:5 at rules#D-3",
		"Not recorded: the next sweep reads the same window. The decision-drift job moves it: issue-cli --project proj decisions drift --record",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("drift misses %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(tracker.DecisionLogDir(proj.IssueDir)); !os.IsNotExist(err) {
		t.Fatalf("a plain run wrote %s (%v)", tracker.DecisionLogDir(proj.IssueDir), err)
	}
	if !isReadOnly(&invocation{cmd: decisionsCommand, args: []string{"drift"}}) || isReadOnly(&invocation{cmd: decisionsCommand, args: []string{"drift", "--record"}}) {
		t.Error("decisions drift reads; --record writes")
	}
}

func TestDecisionsDriftRecordMovesTheMark(t *testing.T) {
	proj := makeDriftProject(t)
	out := runDrift(t, proj, false, "--record")
	if !strings.Contains(out, "Recorded: the next sweep reads main after ") {
		t.Fatalf("record:\n%s", out)
	}
	mark, ok, err := tracker.LoadDriftMark(proj.IssueDir)
	head := respectedGit(t, proj.WorkDir, "rev-parse", "main")
	if err != nil || !ok || mark.Main != head || mark.By != "lead-proj" && mark.By != "owner" {
		t.Fatalf("mark %+v ok %v err %v (head %s)", mark, ok, err, head)
	}
	var got driftOutput
	if err := json.Unmarshal([]byte(runDrift(t, proj, true)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count(tracker.DriftPaths) != 0 || got.Window.From != head || got.Recorded || got.Count(tracker.DriftCitation) != 1 || got.Count(tracker.DriftDocs) != 1 {
		t.Fatalf("second run after --record: %+v", got)
	}
	if out := runDrift(t, proj, false, "--since", "main~1"); !strings.Contains(out, "(no issue) changed internal/store/db.go") {
		t.Errorf("--since reads the window given:\n%s", out)
	}
	ctx, _, _ := newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"drift", "--record", "--since", "main~1"}); err == nil {
		t.Error("--record with --since passed")
	}
}

func TestDecisionsDriftNoDrift(t *testing.T) {
	proj := makeDriftProject(t)
	mustWrite(t, filepath.Join(proj.IssueDir, "open.md"), "---\ntitle: \"Open\"\nstatus: \"in progress\"\n---\n\nFollows rules#D-3.\n")
	mustWrite(t, filepath.Join(proj.WorkDir, "docs", "departments.md"), "## Settled rules\n\n- Source: rules#D-3.\n")
	head := respectedGit(t, proj.WorkDir, "rev-parse", "main")
	if err := tracker.SaveDriftMark(proj.IssueDir, tracker.DriftMark{At: time.Now(), Main: head}); err != nil {
		t.Fatal(err)
	}
	if out := runDrift(t, proj, false); !strings.Contains(out, "No drift.") {
		t.Errorf("clean board:\n%s", out)
	}
}

// AC3: a project that adds the job under Product sees it due, waiting for
// the owner; its standing start is off until the owner turns it on.
func TestJob_DecisionDriftDueAndAutoOffByDefault(t *testing.T) {
	proj := makeJobProject(t)
	proj.Product = &tracker.Department{Name: tracker.ProductName, Jobs: []tracker.Job{{ID: "decision-drift", Every: "week", On: "mon", At: "07:00", Brief: "builtin:decision-drift"}}}
	started := stubStart(t)
	out, err := runJobCmd(t, proj, "due")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "product/decision-drift · 2026-W44 (due Mon 2026-10-26 07:00): waits for the owner") {
		t.Errorf("due misses the job:\n%s", out)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return jobNow }
	if err := runJobs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "standing start: off — the owner turns it on: issue-cli --project proj job auto on product/decision-drift") {
		t.Errorf("jobs:\n%s", stdout.String())
	}
	if _, err := runJobCmd(t, proj, "run", "product/decision-drift", "--scheduled"); err == nil || !strings.Contains(err.Error(), "has no standing start") || len(*started) != 0 {
		t.Fatalf("scheduled run without the owner's standing start: %v, started %v", err, *started)
	}
}

// The bundled brief sweeps with --record, files one issue and changes no
// decision, doc or standing start.
func TestDecisionDriftBriefChangesNothing(t *testing.T) {
	brief, err := jobs.BriefText(nil, tracker.Job{Brief: "builtin:decision-drift"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"issue-cli decisions drift --record", `issue-cli create --title "Decision drift ({{period}})"`, "never confirm, amend, replace, renew or let go of a decision", "never turn on a job's standing start"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief misses %q", want)
		}
	}
	for _, bad := range []string{"issue-cli decide", "job auto on", "git commit", "issue-cli transition", "issue-cli check "} {
		if strings.Contains(brief, bad) {
			t.Errorf("brief tells the run to %q", bad)
		}
	}
}
