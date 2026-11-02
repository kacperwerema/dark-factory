package viewer

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

const clarifyViewBody = "## Clarify\n\n" +
	"### Q1. Where does the owner answer?\n" +
	"1. **On the Station** (recommended) needs_you.go keeps one row. Cost: one card.\n" +
	"2. **In each row** Cost: a form per row.\n" +
	"**Answer:** —\n\n" +
	"### Q2. Keep comments?\n" +
	"1. **Keep** (recommended) Cost: nothing.\n" +
	"2. **Drop** Cost: a script.\n" +
	"**Answer:** —\n"

// clarifySandbox is project Acme with issue "q" in design, its two
// clarify questions open.
func clarifySandbox(t *testing.T) (string, string) {
	t.Helper()
	_, proj := deptSandbox(t)
	path := filepath.Join(proj.Root(), "issues", "q.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ntitle: \"Questions\"\nslug: \"q\"\nstatus: \"in design\"\n---\n\n"+clarifyViewBody), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	t.Cleanup(ts.Close)
	return path, ts.URL
}

func postClarify(t *testing.T, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/p/acme/issue/q/clarify", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

// AC3: the Station shows the round as one form, Needs you one row for the
// issue, and only the owner's browser records answers, each a decision
// with the owner's words.
func TestClarifyFormOnTheStationIsTheOwnersOnly(t *testing.T) {
	path, url := clarifySandbox(t)

	_, page := getResp(t, url+"/p/acme/issue/q")
	for _, want := range []string{
		`data-clarify-url="/p/acme/issue/q/clarify" data-testid="station.clarify"`,
		`data-clarify-q="Q1" data-text="Where does the owner answer?"`,
		`data-clarify-q="Q2"`,
		`name="clarify-Q1" value="1" checked`,
		`<span class="wb-cl-cost">Cost: one card</span>`,
		`recommended</span>`,
		`2 wait for your answer`,
		`data-testid="station.clarify.send">Answer all 2</button>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("station lacks %q", want)
		}
	}
	if strings.Count(page, `data-testid="station.clarify.q"`) != 2 {
		t.Error("the form doesn't hold both questions")
	}

	_, ny := getResp(t, url+"/p/acme/needs-you")
	if !strings.Contains(ny, "2 questions to answer") || !strings.Contains(ny, `/p/acme/issue/q#clarify`) {
		t.Error("Needs you has no Clarify row for q")
	}

	answer := `{"answers":[{"id":"Q1","option":2,"words":"In the row, please.","text":"Where does the owner answer?"},{"id":"Q2","option":1,"text":"Keep the old comments?"}]}`
	before, _ := os.ReadFile(path)
	for name, headers := range map[string]map[string]string{
		"no owner session (curl, agent)": {strangerHeader: "1"},
		"forged same-origin":             {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
		"cross-site":                     {"Sec-Fetch-Site": "cross-site"},
	} {
		code, out := postClarify(t, url, answer, headers)
		if code != http.StatusForbidden || (headers[strangerHeader] != "" && !strings.Contains(errText(out), "only the owner")) {
			t.Errorf("%s: status %d %v, want 403", name, code, out)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused answer changed the issue")
	}

	// The owner: Q1 recorded with their words; Q2's text changed under the
	// page, so it is refused and stays open.
	code, out := postClarify(t, url, answer, sameOrigin)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("owner's answers: %d %v", code, out)
	}
	results, _ := out["results"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["decision"] != "D-1" || !strings.Contains(results[1].(map[string]any)["error"].(string), "changed since this page loaded") {
		t.Fatalf("results = %v", results)
	}
	body := string(mustRead(t, path))
	today := time.Now().Format("2006-01-02")
	for _, want := range []string{
		`**Answer:** 2 · ` + today + ` · owner: "In the row, please." (D-1)`,
		`- **D-1** · ` + today + ` · clarify round: Q1 Where does the owner answer → In each row, "In the row, please."`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("issue lacks %q:\n%s", want, body)
		}
	}
	comments, _ := tracker.LoadComments(path)
	if len(comments) != 1 || comments[0].Author != tracker.OwnerAuthor || comments[0].Text != "clarify: the owner answered Q1 (D-1) on the Station" {
		t.Errorf("comments = %+v", comments)
	}
	ds := tracker.ParseDecisions("q", body)
	if len(ds) != 1 || ds[0].Quote != "In the row, please." || tracker.NeedsOwnerWords(ds[0]) {
		t.Errorf("decisions = %+v", ds)
	}

	// Answered: Q1 leaves the form, a second answer is refused, Needs you
	// counts one.
	if code, out := postClarify(t, url, `{"answers":[{"id":"Q1","option":1}]}`, sameOrigin); code != http.StatusConflict || !strings.Contains(out["results"].([]any)[0].(map[string]any)["error"].(string), "already answered") {
		t.Errorf("second answer: %d %v", code, out)
	}
	_, page = getResp(t, url+"/p/acme/issue/q")
	if !strings.Contains(page, `data-clarify-q="Q1" data-text="Where does the owner answer?" disabled`) || !strings.Contains(page, "Answer the question</button>") {
		t.Error("the answered question is still in the form")
	}
	_, ny = getResp(t, url+"/p/acme/needs-you")
	if !strings.Contains(ny, "1 question to answer") {
		t.Error("Needs you doesn't count the one left")
	}
}

// An assumed answer held by the review after YOLO is not counted on Needs
// you: it waits in the review instead, never twice.
func TestClarifyWaitingSkipsAssumedAnswersTheYOLOReviewHolds(t *testing.T) {
	issue := &tracker.Issue{Slug: "q", BodyRaw: strings.Replace(clarifyViewBody, "**Answer:** —", "**Answer:** assumed — YOLO: took the recommendation", 1)}
	if n := len(clarifyWaiting(issue, nil)); n != 2 {
		t.Fatalf("no YOLO pass: waiting = %d, want 2 (assumed waits for the owner too)", n)
	}
	pass := []tracker.Comment{{ID: 1, Date: "2026-10-31 10:00", Text: tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-q", "in design"), "2026-10-31", "backlog")}}
	if n := len(clarifyWaiting(issue, pass)); n != 1 {
		t.Fatalf("after a YOLO pass: waiting = %d, want 1 (Q1 is in the review)", n)
	}
	if qs := yolo.Questions(issue, pass); len(qs) != 1 || qs[0].Question != "Q1" || qs[0].Label() != "Q1: assumed answer" {
		t.Fatalf("review items = %+v", qs)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
