package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// setupYOLOProject has one issue that passed in design → backlog in YOLO.
func setupYOLOProject(t *testing.T) tracker.Project {
	t.Helper()
	root := t.TempDir()
	issueDir := filepath.Join(root, "issues")
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := `statuses:
  - name: "in design"
  - name: "backlog"
  - name: "done"
transitions:
  - from: "in design"
    to: "backlog"
    actions:
      - type: "require_human_approval"
        status: "backlog"
  - from: "backlog"
    to: "in design"
  - from: "backlog"
    to: "done"
`
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	issue := "---\ntitle: \"Shop\"\nstatus: \"backlog\"\n---\n\nBody.\n"
	path := filepath.Join(issueDir, "shop.md")
	if err := os.WriteFile(path, []byte(issue), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AddComment(path, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-shop", "in design"), "2026-10-29", "backlog"), "cli"); err != nil {
		t.Fatal(err)
	}
	return tracker.Project{Name: "Yolo", Slug: "yp", IssueDir: issueDir, WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

func TestYOLOSwitchAndBand(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	// Off, but a pass waits: the sidebar shows the after-the-fact queue.
	page := pageGet(t, ts.URL+"/p/yp/board")
	for _, want := range []string{`data-testid="nav.yolo"`, "1 after the fact", `data-testid="nav.yolo.row.shop"`, "Back to in design"} {
		if !strings.Contains(page, want) {
			t.Errorf("sidebar lacks %q", want)
		}
	}
	if strings.Contains(page, `class="sidebar-yolo on"`) {
		t.Error("band drawn on while YOLO is off")
	}

	if code, out := postJSON(t, ts.URL+"/p/yp/yolo/on", `{"until":"4h"}`); code != 409 || !strings.Contains(out["error"].(string), "owner's words") {
		t.Fatalf("on without words: %d %v", code, out)
	}
	if code, out := postJSON(t, ts.URL+"/p/yp/yolo/on", `{"until":"","words":"go"}`); code != 400 {
		t.Fatalf("on without an end: %d %v", code, out)
	}
	if code, out := postJSON(t, ts.URL+"/p/yp/yolo/on", `{"until":"4h","words":"go yolo"}`); code != 200 || out["On"] != true {
		t.Fatalf("on: %d %v", code, out)
	}
	page = pageGet(t, ts.URL+"/p/yp/board")
	for _, want := range []string{`class="sidebar-yolo on"`, "<strong>YOLO</strong> until", "no releases", `data-testid="nav.yolo.off"`} {
		if !strings.Contains(page, want) {
			t.Errorf("band lacks %q", want)
		}
	}
	if code, out := postJSON(t, ts.URL+"/p/yp/yolo/off", ``); code != 200 || out["On"] != false || !strings.Contains(out["EndReason"].(string), "turned off by the owner") {
		t.Fatalf("off: %d %v", code, out)
	}
	if h := yolo.History(proj.WorkDir, proj.Slug); len(h) != 1 {
		t.Fatalf("history = %+v", h)
	}
}

func TestYOLOReview(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	path := filepath.Join(proj.IssueDir, "shop.md")

	if code, _ := postJSON(t, ts.URL+"/p/yp/issue/shop/yolo-review", `{"verdict":"back"}`); code != 400 {
		t.Fatalf("back without words: %d", code)
	}
	if code, out := postJSON(t, ts.URL+"/p/yp/issue/shop/yolo-review", `{"verdict":"back","words":"design is not settled"}`); code != 200 {
		t.Fatalf("back: %d %v", code, out)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	shop := tracker.FindIssue(issues, "shop")
	if shop.Status != "in design" {
		t.Fatalf("status after back = %q", shop.Status)
	}
	comments, _ := tracker.LoadComments(path)
	last := comments[len(comments)-1].Text
	if !strings.HasPrefix(last, `yolo-review: back to in design "design is not settled" (viewer, `) {
		t.Fatalf("review comment = %q", last)
	}
	if q := yolo.Queue(issues); len(q) != 0 {
		t.Fatalf("queue after review = %+v", q)
	}
	if code, _ := postJSON(t, ts.URL+"/p/yp/issue/shop/yolo-review", `{"verdict":"agree"}`); code != 409 {
		t.Fatalf("second review: %d", code)
	}
}

func TestYOLOBandValve(t *testing.T) {
	proj := setupYOLOProject(t)
	now := time.Now()
	st := yolo.State{Until: now.Add(time.Hour), Words: "go", Valves: yolo.Valves{CIRedMinutes: 30, Releases: true}}
	if err := yolo.TurnOn(proj.WorkDir, proj.Slug, st, now); err != nil {
		t.Fatal(err)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	b := yoloBand(&proj, issues, now)
	if !b.On || b.Valves != "CI guards · releases unlocked" || len(b.Queue) != 1 {
		t.Fatalf("band = %+v", b)
	}
	if b = yoloBand(&proj, issues, now.Add(2*time.Hour)); b.On || b.EndReason != "expired" || !b.Show() {
		t.Fatalf("band after the end = %+v", b)
	}
}
