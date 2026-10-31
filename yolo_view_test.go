package main

import (
	"encoding/json"
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
	page := pageGet(t, ts.URL+"/p/yp/board?classic=1")
	for _, want := range []string{`data-testid="nav.yolo"`, "1 after the fact", `data-testid="nav.yolo.row.shop"`, "shop</a> · 1 gate", `href="/p/yp/yolo/review" data-testid="nav.yolo.review_all"`} {
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
	page = pageGet(t, ts.URL+"/p/yp/board?classic=1")
	for _, want := range []string{`class="sidebar-yolo on"`, "<strong>YOLO</strong> until", "no releases", `data-testid="nav.yolo.off"`} {
		if !strings.Contains(page, want) {
			t.Errorf("sidebar band lacks %q", want)
		}
	}
	// The Workshop Board pages (the Report here) carry the hazard band.
	page = pageGet(t, ts.URL+"/p/yp/status")
	for _, want := range []string{`data-testid="yolo.band"`, "Gates pass without you", "no releases", `data-testid="yolo.band.off"`} {
		if !strings.Contains(page, want) {
			t.Errorf("hazard band lacks %q", want)
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

// A box only the owner can check that a YOLO gate passed without waits on
// the Station, the company's queue, Needs you and the sidebar by name, and
// the owner's review settles it like a pass.
func TestYOLOOwnerCheckOnThePages(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	path := filepath.Join(proj.IssueDir, "shop.md")
	if err := tracker.AddComment(path, 0, tracker.YOLOReviewComment("agreed", "ok", "viewer", "2026-10-29", "backlog"), "app"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ntitle: \"Shop\"\nstatus: \"backlog\"\n---\n\n## Acceptance Criteria\n- [ ] The shop opens\n- [ ] The owner likes the colours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := tracker.YOLOOwnerCheckComment(tracker.YOLOOwnerCheck{ID: "AC2", Why: "playtest judgement step TP9", Text: "The owner likes the colours", From: "in design"})
	if err := tracker.AddComment(path, 0, check, "cli"); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	assertContains(t, pageGet(t, ts.URL+"/p/yp/issue/shop"),
		`data-status="AC2"`, `data-testid="station.yolo.owner_check"`, `<b>AC2: owner check, not done</b> · The owner likes the colours`, `(playtest judgement step TP9)`, `Back to in design`)
	assertContains(t, pageGet(t, ts.URL+"/p/yp/company?classic=1"), `YOLO · 1 owner check`)
	assertContains(t, pageGet(t, ts.URL+"/p/yp/needs-you"), `<span class="wb-muted">1 owner check</span>`)
	assertContains(t, pageGet(t, ts.URL+"/p/yp/board?classic=1"), `shop</a> · 1 owner check`)
	assertContains(t, pageGet(t, ts.URL+"/p/yp/yolo/review"), `data-key="AC2" data-text="The owner likes the colours"`, `(playtest judgement step TP9)`)

	if code, out := postJSON(t, ts.URL+"/p/yp/issue/shop/yolo-review", `{"verdict":"agree","status":"AC2","words":"saw it, fine"}`); code != 200 {
		t.Fatalf("agree: %d %v", code, out)
	}
	comments, _ := tracker.LoadComments(path)
	if last := comments[len(comments)-1].Text; !strings.HasSuffix(last, "→ AC2") {
		t.Fatalf("review comment = %q", last)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	if q := yolo.Queue(issues); len(q) != 0 {
		t.Fatalf("queue after the review = %+v", q)
	}
}

// An owner_only gate with YOLO on: the issue waits in Needs you as with
// YOLO off, and the bands and yolo.json list the gates YOLO never passes.
func TestYOLOOwnerOnlyGateOnThePages(t *testing.T) {
	proj := setupYOLOProject(t)
	raw, _ := os.ReadFile(proj.WorkflowFile)
	wf := strings.Replace(string(raw), "        status: \"backlog\"\n", "        status: \"backlog\"\n        owner_only: true\n", 1)
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "launch.md"), []byte("---\ntitle: \"Launch\"\nstatus: \"in design\"\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := yolo.TurnOn(proj.WorkDir, proj.Slug, yolo.State{Until: now.Add(time.Hour), By: "owner", Words: "go", How: "viewer"}, now); err != nil {
		t.Fatal(err)
	}
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, js := getResp(t, ts.URL+"/p/yp/needs-you.json")
	var ny NeedsYouJSON
	if err := json.Unmarshal([]byte(js), &ny); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range ny.Rows {
		found = found || r.Slug == "launch"
	}
	if !found {
		t.Fatalf("owner_only gate not in Needs you: %+v", ny.Rows)
	}

	_, js = getResp(t, ts.URL+"/p/yp/yolo.json")
	var band YOLOBand
	if err := json.Unmarshal([]byte(js), &band); err != nil {
		t.Fatal(err)
	}
	if len(band.Never) == 0 || band.Never[0].From != "in design" || band.Never[0].To != "backlog" {
		t.Fatalf("yolo.json never = %+v", band.Never)
	}
	page := pageGet(t, ts.URL+"/p/yp/board?classic=1")
	if !strings.Contains(page, `data-testid="nav.yolo.never"`) || !strings.Contains(page, "in design → backlog") {
		t.Error("sidebar band lacks the never-pass gates")
	}
	page = pageGet(t, ts.URL+"/p/yp/status")
	if !strings.Contains(page, `data-testid="yolo.band.never"`) {
		t.Error("hazard band lacks the never-pass count")
	}
}
