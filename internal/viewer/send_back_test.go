package viewer

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// setupSendBackProject runs on this repo's workflow.yaml (no backward
// edges) with one feature issue at human-testing, stamped and assigned.
func setupSendBackProject(t *testing.T, status string) (tracker.Project, string) {
	t.Helper()
	root := t.TempDir()
	issueDir := filepath.Join(root, "issues")
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf, err := os.ReadFile(repoPath("workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), wf, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(issueDir, "shop.md")
	issue := "---\ntitle: \"Shop\"\ntype: \"feature\"\nstatus: \"" + status + "\"\nassignee: \"agent-shop\"\nhuman_approval: \"documentation\"\n---\n\nBody.\n"
	if err := os.WriteFile(path, []byte(issue), 0o644); err != nil {
		t.Fatal(err)
	}
	return tracker.Project{Name: "Shop", Slug: "sp", IssueDir: issueDir, WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}, path
}

func loadShop(t *testing.T, proj tracker.Project) *tracker.Issue {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	return tracker.FindIssue(issues, "shop")
}

func TestSendBackMovesTheIssueBackAndTellsTheWorker(t *testing.T) {
	proj, path := setupSendBackProject(t, "human-testing")
	withMockTmuxSessions(t, []AgentSession{{Name: "agent-shop"}})
	var gotTarget string
	var gotLines []string
	withMockTmuxSendKeys(t, func(target string, lines []string) error {
		gotTarget, gotLines = target, lines
		return nil
	})
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := postJSON(t, ts.URL+"/p/sp/issue/shop/send-back", `{"text":"the label is cut off"}`)
	if code != 200 || out["back_to"] != "testing" || out["from"] != "human-testing" || out["notified_session"] != "agent-shop" {
		t.Fatalf("send-back: %d %v", code, out)
	}
	shop := loadShop(t, proj)
	if shop.Status != "testing" || shop.HumanApproval != "" || shop.Assignee != "agent-shop" {
		t.Fatalf("after send-back: status %q approval %q assignee %q", shop.Status, shop.HumanApproval, shop.Assignee)
	}
	comments, _ := tracker.LoadComments(path)
	if last := comments[len(comments)-1].Text; !strings.HasPrefix(last, `rejected: "the label is cut off" (viewer, `) || !strings.HasSuffix(last, "→ back to testing") {
		t.Fatalf("comment = %q", last)
	}
	if gotTarget != "agent-shop" || len(gotLines) != 1 || !strings.Contains(gotLines[0], `Sent back to testing for fixes by the owner: "the label is cut off"`) || !strings.Contains(gotLines[0], "issue-cli start shop") {
		t.Fatalf("nudge to %q: %q", gotTarget, gotLines)
	}
	store, _ := tracker.LoadStats(path)
	wf := proj.LoadWorkflowForIssue(shop)
	if len(store.Transitions) != 1 || !timeline.Backward(wf, store.Transitions[0]) {
		t.Fatalf("stats = %+v, want one backward row", store.Transitions)
	}
}

func TestSendBackWithoutAWorkerStillMoves(t *testing.T) {
	proj, _ := setupSendBackProject(t, "human-testing")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := postJSON(t, ts.URL+"/p/sp/issue/shop/send-back", `{"text":"again","to":"in progress"}`)
	if code != 200 || out["back_to"] != "in progress" || out["notified_session"] != nil || !strings.Contains(out["notification_error"].(string), "no worker is live") {
		t.Fatalf("send-back: %d %v", code, out)
	}
	if shop := loadShop(t, proj); shop.Status != "in progress" {
		t.Fatalf("status = %q", shop.Status)
	}
}

func TestSendBackRefuses(t *testing.T) {
	withMockTmuxSessions(t, nil)
	for _, tc := range []struct {
		name, status, body string
		code               int
	}{
		{"no words", "human-testing", `{"text":"  "}`, 400},
		{"later status", "human-testing", `{"text":"x","to":"shipping"}`, 400},
		{"first status", "idea", `{"text":"x"}`, 409},
		{"done", "done", `{"text":"x"}`, 409},
		{"parked", "parked", `{"text":"x"}`, 409},
	} {
		proj, path := setupSendBackProject(t, tc.status)
		before, _ := os.ReadFile(path)
		ts := newTestServer(t, []tracker.Project{proj})
		code, out := postJSON(t, ts.URL+"/p/sp/issue/shop/send-back", tc.body)
		ts.Close()
		if code != tc.code {
			t.Errorf("%s: %d %v, want %d", tc.name, code, out, tc.code)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Errorf("%s: the issue changed:\n%s", tc.name, after)
		}
	}
}

func TestSendBackCrossOriginIsRefused(t *testing.T) {
	proj, path := setupSendBackProject(t, "human-testing")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	before, _ := os.ReadFile(path)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/sp/issue/shop/send-back", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin send-back: %d", resp.StatusCode)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatalf("the issue changed:\n%s", after)
	}
}

func TestYOLOBackWithoutADeclaredBackEdge(t *testing.T) {
	proj, path := setupSendBackProject(t, "backlog")
	if err := tracker.AddComment(path, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-shop", "in design"), "2026-10-29", "backlog"), "cli"); err != nil {
		t.Fatal(err)
	}
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	if code, out := postJSON(t, ts.URL+"/p/sp/issue/shop/yolo-review", `{"verdict":"back","words":"not settled"}`); code != 200 {
		t.Fatalf("back: %d %v", code, out)
	}
	if shop := loadShop(t, proj); shop.Status != "in design" {
		t.Fatalf("status = %q", shop.Status)
	}
}

func TestStationSendBackButtonPostsToTheEndpoint(t *testing.T) {
	proj, _ := setupSendBackProject(t, "human-testing")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/sp/issue/shop")
	if !strings.Contains(page, `data-send-back="/p/sp/issue/shop/send-back"`) || !strings.Contains(page, `data-testid="station.stamp.back"`) {
		t.Fatal("the Station's stamp card has no send-back URL")
	}
	js, err := os.ReadFile("static/station.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "post(form.getAttribute('data-send-back'), { text: text })") {
		t.Fatal("station.js does not post to data-send-back")
	}
}
