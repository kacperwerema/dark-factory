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
)

// makeHandoffGateProject is the YOLO test project as a git repo whose
// product workflow names the legal check on in progress → human-testing:
// handoff_answered: legal with paths. The issue "work" is at in progress;
// its branch work/work changes changed (a file path), and its dispatch
// recorded the base.
func makeHandoffGateProject(t *testing.T, changed string) *tracker.Project {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	proj := makeYOLOProject(t)
	root := proj.WorkDir
	wf := strings.Replace(yoloTestWorkflow, "  - from: \"in progress\"\n    to: \"human-testing\"\n",
		"  - from: \"in progress\"\n    to: \"human-testing\"\n    actions:\n      - type: \"validate\"\n        rule: \"handoff_answered: legal\"\n        paths: [\"server/**\", \"src/copy/**\", \"package.json\"]\n", 1)
	if wf == yoloTestWorkflow {
		t.Fatal("yoloTestWorkflow changed: update makeHandoffGateProject")
	}
	mustWrite(t, proj.WorkflowFile, wf)
	work := filepath.Join(proj.IssueDir, "work.md")
	mustWrite(t, work, "---\ntitle: \"Work\"\nstatus: \"in progress\"\n---\n\nThe signup form.\n")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "work")

	mustWrite(t, filepath.Join(root, ".gitignore"), "issues/\n.yolo/\n.worktrees/\n.git-critique/\n")
	for _, f := range []string{"server/signup.go", "web/style.css"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(root, f), "// v1\n")
	}
	respectedGit(t, root, "init", "-q", "-b", "main")
	respectedGit(t, root, "add", "-A")
	respectedGit(t, root, "commit", "-q", "-m", "init")
	base := respectedGit(t, root, "rev-parse", "HEAD")
	respectedGit(t, root, "checkout", "-q", "-b", "work/work")
	mustWrite(t, filepath.Join(root, changed), "// v2\n")
	respectedGit(t, root, "commit", "-q", "-am", "feat: change "+changed)
	respectedGit(t, root, "checkout", "-q", "main")

	st := tracker.StatsStore{Dispatches: []tracker.Dispatch{{At: time.Now(), Agent: "claude", BaseBranch: "main", BaseSHA: base}}}
	data, _ := json.Marshal(st)
	mustWrite(t, tracker.StatsSidecarPath(work), string(data))
	return proj
}

// writeLegalHandoff files Legal's answer to "work" as a handoff issue at
// status, with comment (a handoff: declined line, or "").
func writeLegalHandoff(t *testing.T, proj *tracker.Project, name, status, comment string) {
	t.Helper()
	path := filepath.Join(proj.IssueDir, name+".md")
	body := "---\ntitle: \"Legal check: " + name + "\"\nstatus: \"" + status + "\"\ndepartment: \"Legal\"\ntype: \"legal-check\"\nrequested_by: \"product\"\nhandoff_route: \"product → legal\"\nhandoff_for: \"work\"\n---\n\n## Handoff\n\nThe signup form stores a birth date.\n"
	mustWrite(t, path, body)
	if comment != "" {
		if err := tracker.AddComment(path, 0, comment, "test"); err != nil {
			t.Fatal(err)
		}
	}
}

// AC2: a product workflow naming handoff_answered: legal holds a branch
// that touches one of its paths until Legal's handoff is done; an open,
// declined or parked handoff doesn't answer it.
func TestHandoffAnsweredHolds(t *testing.T) {
	now := time.Now()
	proj := makeHandoffGateProject(t, "server/signup.go")

	_, _, err := runYOLOTransition(t, proj, now, "work", "--to", "human-testing")
	if err == nil {
		t.Fatal("passed with no handoff, want a hold")
	}
	for _, w := range []string{"handoff_answered: legal hasn't answered work yet", "the branch touches server/signup.go", `issue-cli handoff legal --for work --title "..." --why "..."`} {
		assertContains(t, err.Error(), w)
	}

	writeLegalHandoff(t, proj, "declined", "parked", "handoff: declined by lead-proj-legal: out of scope")
	writeLegalHandoff(t, proj, "open", "research", "")
	_, _, err = runYOLOTransition(t, proj, now, "work", "--to", "human-testing")
	if err == nil {
		t.Fatal("passed with an open and a declined handoff, want a hold")
	}
	assertContains(t, err.Error(), "Waiting on: legal-check-open (at research)")
	if s := loadLeadIssue(t, proj, "work").Status; s != "in progress" {
		t.Fatalf("status moved to %q", s)
	}

	// YOLO doesn't pass it: the answer is Legal's done issue.
	yoloOn(t, proj, now, time.Hour)
	if _, _, err = runYOLOTransition(t, proj, now, "work", "--to", "human-testing"); err == nil {
		t.Fatal("YOLO passed handoff_answered, want a hold")
	}

	writeLegalHandoff(t, proj, "open", "done", "")
	out, _, err := runYOLOTransition(t, proj, now, "work", "--to", "human-testing")
	if err != nil {
		t.Fatalf("answered: %v\n%s", err, out)
	}
	if s := loadLeadIssue(t, proj, "work").Status; s != "human-testing" {
		t.Fatalf("status = %q, want human-testing", s)
	}
}

// A branch whose changes match none of the rule's paths passes without
// asking Legal.
func TestHandoffAnsweredSkipsUntouchedPaths(t *testing.T) {
	proj := makeHandoffGateProject(t, "web/style.css")
	if _, _, err := runYOLOTransition(t, proj, time.Now(), "work", "--to", "human-testing"); err != nil {
		t.Fatalf("untouched paths: %v", err)
	}
}

// With no recorded base the branch can't be read: the rule holds as if a
// path matched, and says why.
func TestHandoffAnsweredHoldsWithoutABase(t *testing.T) {
	proj := makeHandoffGateProject(t, "web/style.css")
	if err := os.Remove(tracker.StatsSidecarPath(filepath.Join(proj.IssueDir, "work.md"))); err != nil {
		t.Fatal(err)
	}
	_, _, err := runYOLOTransition(t, proj, time.Now(), "work", "--to", "human-testing")
	if err == nil {
		t.Fatal("passed with no base, want a hold")
	}
	assertContains(t, err.Error(), "the branch's changes can't be read")
}
