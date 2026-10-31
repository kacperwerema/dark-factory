package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

const yoloTestWorkflow = `statuses:
  - name: "in design"
  - name: "backlog"
  - name: "in progress"
  - name: "human-testing"
  - name: "documentation"
  - name: "done"
  - name: "parked"
    optional: true
    global: true
transitions:
  - from: "in design"
    to: "backlog"
    actions:
      - type: "require_human_approval"
        status: "backlog"
  - from: "backlog"
    to: "in progress"
    actions:
      - type: "require_human_approval"
        status: "in progress"
  - from: "in progress"
    to: "human-testing"
  - from: "human-testing"
    to: "documentation"
    actions:
      - type: "require_human_approval"
        status: "documentation"
  - from: "documentation"
    to: "done"
  - from: "*"
    to: "parked"
    actions:
      - type: "require_human_approval"
        status: "parked"
`

// makeYOLOProject is a project with gated transitions and one issue per
// status the tests need. The temp dir is no git repo, so .yolo/ lands in it.
func makeYOLOProject(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	issue := func(title, status string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\n---\n\nBody of " + title + ".\n"
	}
	write("issues/design.md", issue("Design", "in design"))
	write("issues/testing.md", issue("Testing", "human-testing"))
	write("workflow.yaml", yoloTestWorkflow)
	// Run as the design issue's worker.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "design")
	return &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

func yoloOn(t *testing.T, proj *tracker.Project, now time.Time, until time.Duration) {
	t.Helper()
	st := yolo.State{Until: now.Add(until), By: "owner", Words: "go", How: "viewer", Valves: yolo.Valves{CIRedMinutes: 30}}
	if err := yolo.TurnOn(proj.WorkDir, proj.Slug, st, now); err != nil {
		t.Fatal(err)
	}
}

func runYOLOTransition(t *testing.T, proj *tracker.Project, now time.Time, args ...string) (string, string, error) {
	t.Helper()
	ctx, out, errOut := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	err := runTransition(ctx, args)
	return out.String(), errOut.String(), err
}

func TestYOLOGatePassesAndMarks(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()

	// Off: the gate waits as today.
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err == nil {
		t.Fatal("transition passed the gate with YOLO off")
	}

	yoloOn(t, proj, now, time.Hour)
	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err != nil {
		t.Fatalf("YOLO on: %v", err)
	}
	if !strings.Contains(out, "YOLO: gate in design → backlog passed without the owner") {
		t.Fatalf("output lacks the YOLO line:\n%s", out)
	}
	issue := loadLeadIssue(t, proj, "design")
	if issue.Status != "backlog" {
		t.Fatalf("status = %q", issue.Status)
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Text, `approval: "YOLO" (yolo · agent-design · from in design, `) || !strings.HasSuffix(comments[0].Text, "→ backlog") {
		t.Fatalf("comments = %+v", comments)
	}
	q := yolo.Unreviewed(issue, comments)
	if len(q) != 1 || q[0].Status != "backlog" || q[0].From != "in design" || q[0].By != "agent-design" {
		t.Fatalf("queue = %+v", q)
	}

	// Next step says to run it, not to stop.
	if !strings.Contains(out, "YOLO is on until") || !strings.Contains(out, `issue-cli transition design --to "in progress"`) {
		t.Fatalf("next step not rewritten for YOLO:\n%s", out)
	}
}

func TestYOLONeverPasses(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	// Optional / side status: parked still needs the owner.
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "parked"); err == nil {
		t.Fatal("YOLO passed the gate to parked")
	}
	issue := loadLeadIssue(t, proj, "design")
	if comments, _ := tracker.LoadComments(issue.FilePath); len(comments) != 0 {
		t.Fatalf("a refused transition left comments: %+v", comments)
	}

	wf := proj.LoadWorkflowForIssue(issue)
	if yoloEligible(wf, nil, "backlog", "in design", "in design") {
		t.Fatal("backwards step eligible")
	}
	if yoloEligible(wf, nil, "in progress", "human-testing", "human-testing") {
		t.Fatal("Human Testing approval eligible")
	}
	if !yoloEligible(wf, nil, "human-testing", "documentation", "documentation") {
		t.Fatal("human-testing → documentation should pass in YOLO")
	}
}

func TestYOLOExpiredAndValve(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()
	yoloOn(t, proj, now.Add(-2*time.Hour), time.Hour) // ended an hour ago

	_, errOut, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("expired YOLO passed the gate")
	}
	if !strings.Contains(errOut, "YOLO turned off at") || !strings.Contains(errOut, "expired") {
		t.Fatalf("refusal doesn't say why: %q", errOut)
	}
	if h := yolo.History(proj.WorkDir, proj.Slug); len(h) != 1 || h[0].EndReason != "expired" {
		t.Fatalf("history = %+v", h)
	}

	// CI red for 40 min trips the valve.
	yoloOn(t, proj, now, time.Hour)
	st := &ci.State{State: ci.Red, RedSince: "abc", RedAt: now.Add(-40 * time.Minute)}
	if err := ci.WriteState(proj.WorkDir, st); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil || !strings.Contains(errOut, "valve: CI red 40 min") {
		t.Fatalf("valve: err = %v, stderr = %q", err, errOut)
	}
}

func TestYOLODryRun(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)
	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ Ready") || !strings.Contains(out, "YOLO is on") {
		t.Fatalf("dry run:\n%s", out)
	}
}

func TestYOLOCommand(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Date(2026, 10, 29, 22, 10, 0, 0, time.Local)
	run := func(args ...string) (string, error) {
		ctx, out, _ := newTestContext(proj, false)
		ctx.Now = func() time.Time { return now }
		err := runYOLO(ctx, args)
		return out.String(), err
	}

	// Workers never turn it on.
	if _, err := run("on", "--until", "08:00", "--quote", "go"); err == nil || !strings.Contains(err.Error(), "workers never turn YOLO on") {
		t.Fatalf("worker: err = %v", err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")

	if _, err := run("on", "--quote", "go"); err == nil || !strings.Contains(err.Error(), "always has an end") {
		t.Fatalf("no end: err = %v", err)
	}
	if _, err := run("on", "--until", "08:00"); err == nil || !strings.Contains(err.Error(), "needs --quote") {
		t.Fatalf("no words: err = %v", err)
	}
	out, err := run("on", "--until", "morning", "--quote", "go yolo till morning")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "YOLO on until 08:00") || !strings.Contains(out, "passes: in design → backlog") || !strings.Contains(out, "no local CI") {
		t.Fatalf("on:\n%s", out)
	}
	st, _ := yolo.Load(proj.WorkDir, proj.Slug)
	if want := time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local); !st.Until.Equal(want) || st.Valves.CIRedMinutes != 0 {
		t.Fatalf("state = %+v", st)
	}
	if _, err := run("on", "--until", "4h", "--quote", "again"); err == nil || !strings.Contains(err.Error(), "already on") {
		t.Fatalf("twice: err = %v", err)
	}
	if out, err = run("extend", "--until", "off"); err != nil || !strings.Contains(out, "until turned off") {
		t.Fatalf("extend: %v %s", err, out)
	}
	if out, err = run("status"); err != nil || !strings.Contains(out, "YOLO: on until turned off") {
		t.Fatalf("status: %v %s", err, out)
	}

	// A pass, then the queue and the review.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "design")
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	if out, _ = run("queue"); !strings.Contains(out, "To review after YOLO: 1 issue (1 gate)") || !strings.Contains(out, "  design  (1 gate)\n      gate in design → backlog  by agent-design") {
		t.Fatalf("queue:\n%s", out)
	}
	if _, err := run("review", "design", "--quote", "fine"); err == nil {
		t.Fatal("review without --agree passed")
	}
	if _, err := run("review", "design", "--agree", "--quote", "fine"); err != nil {
		t.Fatal(err)
	}
	if out, _ = run("queue"); !strings.Contains(out, "Nothing to review") {
		t.Fatalf("queue after review:\n%s", out)
	}

	if out, err = run("off"); err != nil || !strings.Contains(out, "YOLO off (turned off by lead-proj)") {
		t.Fatalf("off: %v %s", err, out)
	}
}

func TestYOLOReleaseAndDecide(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	ctx, _, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runRelease(ctx, []string{"publish", "v0.1.0"}); err == nil || !strings.Contains(err.Error(), "YOLO is on: external releases wait for the owner") {
		t.Fatalf("release publish: err = %v", err)
	}

	ctx, _, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runDecide(ctx, []string{"design", "--text", "Group the list by aisle."}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(loadLeadIssue(t, proj, "design").FilePath)
	if !strings.Contains(string(body), "· YOLO: Group the list by aisle.") {
		t.Fatalf("decision not tagged YOLO:\n%s", body)
	}
}

func TestYOLOHeaderLine(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()
	show := func() string {
		ctx, out, _ := newTestContext(proj, false)
		ctx.Now = func() time.Time { return now }
		if err := runShow(ctx, []string{"design"}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if strings.Contains(show(), "YOLO:") {
		t.Fatal("YOLO line while off")
	}
	yoloOn(t, proj, now, time.Hour)
	if out := show(); !strings.Contains(out, "YOLO: on until ") || !strings.Contains(out, "issue-cli --project proj check-script <slug> --yes") {
		t.Fatalf("show lacks the YOLO line:\n%s", out)
	}
}
