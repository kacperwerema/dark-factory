package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// respectedGit runs git in dir with no user config, hooks or background
// maintenance.
func respectedGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
		"-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeRespectedProject is the YOLO test project as a git repo, with
// decisions_respected on in progress → human-testing, a hard project
// decision guarding internal/store/**, and the issue "work" at in progress
// whose branch work/work changes internal/store/db.go. body is the work
// issue's body.
func makeRespectedProject(t *testing.T, body string) *tracker.Project {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	proj := makeYOLOProject(t)
	root := proj.WorkDir
	wf := strings.Replace(yoloTestWorkflow, "  - from: \"in progress\"\n    to: \"human-testing\"\n",
		"  - from: \"in progress\"\n    to: \"human-testing\"\n    actions:\n      - type: \"validate\"\n        rule: \"decisions_respected\"\n", 1)
	if wf == yoloTestWorkflow {
		t.Fatal("yoloTestWorkflow changed: update makeRespectedProject")
	}
	mustWrite(t, proj.WorkflowFile, wf)
	mustWrite(t, filepath.Join(proj.IssueDir, "rules.md"), "---\ntitle: \"Rules\"\nstatus: \"done\"\n---\n\n## Decisions\n\n"+
		`- **D-1** · 2026-10-01 · chat: The store is append-only, "append only" {scope: project; hard; paths: internal/store/**}`+"\n")
	work := filepath.Join(proj.IssueDir, "work.md")
	mustWrite(t, work, "---\ntitle: \"Work\"\nstatus: \"in progress\"\n---\n\n"+body+"\n")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "work")

	mustWrite(t, filepath.Join(root, ".gitignore"), "issues/\n.yolo/\n.worktrees/\n.git-critique/\n")
	if err := os.MkdirAll(filepath.Join(root, "internal", "store"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "internal", "store", "db.go"), "package store\n")
	respectedGit(t, root, "init", "-q", "-b", "main")
	respectedGit(t, root, "add", "-A")
	respectedGit(t, root, "commit", "-q", "-m", "init")
	base := respectedGit(t, root, "rev-parse", "HEAD")
	respectedGit(t, root, "checkout", "-q", "-b", "work/work")
	mustWrite(t, filepath.Join(root, "internal", "store", "db.go"), "package store\n// rewritten\n")
	respectedGit(t, root, "commit", "-q", "-am", "feat: rewrite the store")
	respectedGit(t, root, "checkout", "-q", "main")

	st := tracker.StatsStore{Dispatches: []tracker.Dispatch{{At: time.Now(), Agent: "claude", BaseBranch: "main", BaseSHA: base}}}
	data, _ := json.Marshal(st)
	mustWrite(t, tracker.StatsSidecarPath(work), string(data))
	return proj
}

// AC1 through issue-cli: the uncited touched decision refuses the gate with
// its ref, the file and the fixes; citing it passes.
func TestTransitionDecisionsRespected(t *testing.T) {
	now := time.Now()
	proj := makeRespectedProject(t, "We change the store.")
	_, _, err := runYOLOTransition(t, proj, now, "work", "--to", "human-testing")
	if err == nil {
		t.Fatal("passed, want a refusal")
	}
	for _, w := range []string{"decisions_respected", "rules#D-1", "internal/store/db.go", `issue-cli append work --section Design --body "Follows rules#D-1: …"`} {
		assertContains(t, err.Error(), w)
	}
	if s := loadLeadIssue(t, proj, "work").Status; s != "in progress" {
		t.Fatalf("status moved to %q", s)
	}

	ctx, out, _ := newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"--touched", "work"}); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"rules#D-1 · NOT cited", "files: internal/store/db.go  (paths: internal/store/**)", "decisions_respected holds 1"} {
		assertContains(t, out.String(), w)
	}

	proj = makeRespectedProject(t, "Follows rules#D-1: we only append.")
	if _, _, err := runYOLOTransition(t, proj, now, "work", "--to", "human-testing"); err != nil {
		t.Fatalf("cited: %v", err)
	}
}

// TestYOLOPassesDecisionsRespectedHold is AC3: with YOLO on the failing
// gate passes and leaves a yolo-hold: entry naming the decision and the
// files, listed in the review queue.
func TestYOLOPassesDecisionsRespectedHold(t *testing.T) {
	now := time.Now()
	proj := makeRespectedProject(t, "We change the store.")
	yoloOn(t, proj, now, time.Hour)
	out, _, err := runYOLOTransition(t, proj, now, "work", "--to", "human-testing")
	if err != nil {
		t.Fatalf("YOLO transition: %v", err)
	}
	assertContains(t, out, "YOLO: decisions_respected: rules#D-1 touched (internal/store/db.go), not cited — passed, in the after-the-fact queue")
	issue := loadLeadIssue(t, proj, "work")
	if issue.Status != "human-testing" {
		t.Fatalf("status = %q", issue.Status)
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	var holds []string
	for _, c := range comments {
		if strings.HasPrefix(c.Text, "yolo-hold:") {
			holds = append(holds, c.Text)
		}
	}
	want := "yolo-hold: decisions_respected rules#D-1 from in progress: touched (internal/store/db.go), not cited"
	if len(holds) != 1 || holds[0] != want {
		t.Fatalf("holds = %q, want %q", holds, want)
	}
	var found bool
	for _, e := range yolo.QueueEntries([]*tracker.Issue{issue}) {
		for _, d := range e.Decisions {
			if e.Slug == "work" && d.Decision == "rules#D-1" && d.Hold == "decisions_respected" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the hold is not in the YOLO review queue")
	}
}

// AC7 through issue-cli: no recorded base passes with a ⚠ line.
func TestTransitionDecisionsRespectedNoBaseWarns(t *testing.T) {
	now := time.Now()
	proj := makeRespectedProject(t, "We change the store.")
	if err := os.Remove(tracker.StatsSidecarPath(filepath.Join(proj.IssueDir, "work.md"))); err != nil {
		t.Fatal(err)
	}
	out, _, err := runYOLOTransition(t, proj, now, "work", "--to", "human-testing", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	assertContains(t, out, "⚠ decisions_respected: work has no recorded base")
	out, _, err = runYOLOTransition(t, proj, now, "work", "--to", "human-testing")
	if err != nil {
		t.Fatalf("no base: %v", err)
	}
	assertContains(t, out, "⚠ decisions_respected: work has no recorded base (it wasn't cut by a dispatch): the diff audit didn't run")
}

// TestCritiqueBriefListsTouchedDecisions is AC8: the critic's brief lists
// the hard decisions the branch touches, with the file holding each and
// the matching files, and the command; never the decision's text.
func TestCritiqueBriefListsTouchedDecisions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	proj, slug, _, _ := critiqueProject(t)
	root := proj.WorkDir
	rules := filepath.Join(proj.IssueDir, "rules.md")
	mustWrite(t, rules, "---\ntitle: \"Rules\"\nstatus: \"done\"\n---\n\n## Decisions\n\n"+
		`- **D-1** · 2026-10-01 · chat: TEXT-SENTINEL the store is append-only, "append only" {scope: system:Other; hard; paths: internal/store/**}`+"\n"+
		`- **D-2** · 2026-10-02 · chat: Pages stay plain {scope: project; paths: web/**}`+"\n")
	mustWrite(t, filepath.Join(root, ".gitignore"), "issues/\n.worktrees/\n")
	mustWrite(t, filepath.Join(root, "README.md"), "hi\n")
	respectedGit(t, root, "init", "-q", "-b", "main")
	respectedGit(t, root, "add", "-A")
	respectedGit(t, root, "commit", "-q", "-m", "init")
	wt := filepath.Join(root, ".worktrees", slug)
	respectedGit(t, root, "worktree", "add", "-q", "-b", "work/"+slug, wt)
	for _, f := range []string{"internal/store/db.go", "web/page.html"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(wt, f), "x\n")
	}
	respectedGit(t, wt, "add", "-A")
	respectedGit(t, wt, "commit", "-q", "-m", "work")

	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--brief"}); err != nil {
		t.Fatal(err)
	}
	brief := out.String()
	for _, w := range []string{"Hard decisions this branch touches", "- rules#D-1 — in " + rules + "; files: internal/store/db.go", "issue-cli decisions --touched " + slug} {
		assertContains(t, brief, w)
	}
	for _, never := range []string{"TEXT-SENTINEL", "rules#D-2 — in"} {
		if strings.Contains(brief, never) {
			t.Errorf("the brief carries %q:\n%s", never, brief)
		}
	}
}
