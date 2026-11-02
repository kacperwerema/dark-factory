package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// setupYOLOReviewProject is setupYOLOProject's shop (one gate pass) plus
// cart: a gate pass and an owner check the gate left open.
func setupYOLOReviewProject(t *testing.T) tracker.Project {
	t.Helper()
	proj := setupYOLOProject(t)
	cart := filepath.Join(proj.IssueDir, "cart.md")
	body := "---\ntitle: \"Cart\"\nstatus: \"backlog\"\n---\n\n## Acceptance Criteria\n- [ ] AC1 The owner likes the cart\n"
	if err := os.WriteFile(cart, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AddComment(cart, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-cart", "in design"), "2026-10-30", "backlog"), "cli"); err != nil {
		t.Fatal(err)
	}
	check := tracker.YOLOOwnerCheckComment(tracker.YOLOOwnerCheck{ID: "AC1", Why: "marked owner", Text: "The owner likes the cart", From: "in design"})
	if err := tracker.AddComment(cart, 0, check, "cli"); err != nil {
		t.Fatal(err)
	}
	return proj
}

func yoloReviewServer(t *testing.T, proj tracker.Project) string {
	t.Helper()
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{proj})
	t.Cleanup(ts.Close)
	return ts.URL
}

func postIssueReview(t *testing.T, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/p/yp/yolo/review", strings.NewReader(body))
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
	out := map[string]any{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func needsYouJSON(t *testing.T, url string) NeedsYouJSON {
	t.Helper()
	_, js := getResp(t, url+"/p/yp/needs-you.json")
	var out NeedsYouJSON
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestYOLOReviewPageListsOneEntryPerIssue(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)

	page := pageGet(t, url+"/p/yp/yolo/review")
	assertContains(t, page,
		`data-testid="yolo_review.page"`, `data-testid="yolo_review.summary"`,
		`<b data-testid="yolo_review.issues">2</b>`,
		`data-slug="shop" data-review="/p/yp/issue/shop/yolo-review"`,
		`data-slug="cart" data-review="/p/yp/issue/cart/yolo-review"`,
		`data-testid="yolo_review.group.detail">1 gate, 1 owner check`,
		`data-key="AC1" data-text="The owner likes the cart"`,
		`AC1 · owner check</span> The owner likes the cart`, `Tick AC1`, `Agree without it`,
		`Back to in design`, `Agree the gate`,
		`data-testid="yolo_review.group.evidence"`, `No shots`,
		`data-issue-url="/p/yp/yolo/review" data-tick-url="/p/yp/owner-checks/tick"`, `/static/yolo-review.js`,
	)
	if n := strings.Count(page, `data-testid="yolo_review.group"`); n != 2 {
		t.Errorf("%d groups, want one per issue (2)", n)
	}
	if n := strings.Count(page, `data-testid="yolo_review.gate"`); n != 2 {
		t.Errorf("%d gates, want 2", n)
	}
	// One Agree per issue: gates carry no buttons of their own.
	if n := strings.Count(page, `data-testid="yolo_review.agree"`); n != 2 {
		t.Errorf("%d Agree buttons, want one per issue (2)", n)
	}
	// The script CSP: no inline handlers.
	if strings.Contains(page, "onclick=") {
		t.Error("the page has an inline handler")
	}

	// Needs you: issues, gates and owner checks, the oldest age, the link.
	ny := pageGet(t, url+"/p/yp/needs-you")
	assertContains(t, ny, `data-testid="needs_you.after.summary">2 issues · 2 gates, 1 owner check · oldest `,
		`href="/p/yp/yolo/review" data-testid="needs_you.after.review_all"`, `1 gate, 1 owner check`)
	out := needsYouJSON(t, url)
	if out.AfterCount != 2 || out.AfterGates != 2 || out.AfterChecks != 1 || out.AfterOldest == "" || out.AfterURL != "/p/yp/yolo/review" || len(out.After) != 2 {
		t.Errorf("needs-you.json after = %d issues %d gates %d checks %q %q rows %d", out.AfterCount, out.AfterGates, out.AfterChecks, out.AfterOldest, out.AfterURL, len(out.After))
	}
	// The header's count and the company's list link here, counting issues.
	assertContains(t, pageGet(t, url+"/p/yp/company?classic=1"), `href="/p/yp/yolo/review" title="Issues YOLO moved without you`, `2 issues to review after the fact`, `data-testid="company.yolo_queue.review_all"`, `YOLO · 1 gate, 1 owner check`)
	// The floor: the header's count, the After YOLO tab and its rows.
	assertContains(t, pageGet(t, url+"/p/yp/company"), `href="/p/yp/yolo/review" title="Issues YOLO moved without you`, `2 after the fact`, `After YOLO · 2`, `data-testid="floor.after_yolo.all"`, `1 gate, 1 owner check`)
}

// Regression (the owner, 2026-10-31: "342 to review while delivered are
// 1XX issues"): an issue with 3 passed gates and 2 open owner boxes is one
// entry everywhere it is counted.
func TestYOLOQueueCountsIssuesNotGates(t *testing.T) {
	proj := setupYOLOProject(t)
	path := filepath.Join(proj.IssueDir, "big.md")
	body := "---\ntitle: \"Big\"\nstatus: \"done\"\n---\n\n## Acceptance Criteria\n- [ ] AC1 (owner) Looks right\n- [ ] AC2 (owner) Reads right\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{{"in design", "backlog"}, {"backlog", "in progress"}, {"testing", "done"}} {
		if err := tracker.AddComment(path, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-big", step[0]), "2026-10-30", step[1]), "cli"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"AC1", "AC2"} {
		c := tracker.YOLOOwnerCheckComment(tracker.YOLOOwnerCheck{ID: id, Why: "marked owner", Text: "x", From: "testing"})
		if err := tracker.AddComment(path, 0, c, "cli"); err != nil {
			t.Fatal(err)
		}
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	big := tracker.FindIssue(issues, "big")
	entries := yolo.QueueEntries([]*tracker.Issue{big})
	if len(entries) != 1 || len(entries[0].Gates) != 3 || len(entries[0].Checks) != 2 || entries[0].Detail() != "3 gates, 2 owner checks" {
		t.Fatalf("entries = %+v, want one entry with 3 gates and 2 owner checks", entries)
	}

	url := yoloReviewServer(t, proj)
	// shop (1 gate) and big: two issues.
	_, js := getResp(t, url+"/p/yp/yolo.json")
	var band YOLOBand
	if err := json.Unmarshal([]byte(js), &band); err != nil {
		t.Fatal(err)
	}
	if len(band.Queue) != 2 {
		t.Errorf("yolo.json queue = %d entries, want 2 issues", len(band.Queue))
	}
	if out := needsYouJSON(t, url); out.AfterCount != 2 || out.AfterGates != 4 || out.AfterChecks != 2 {
		t.Errorf("needs-you.json after = %d issues, %d gates, %d checks; want 2, 4, 2", out.AfterCount, out.AfterGates, out.AfterChecks)
	}
	assertContains(t, pageGet(t, url+"/p/yp/status"), `2 issues to review after the fact`)
	assertContains(t, pageGet(t, url+"/p/yp/board?classic=1"), `2 after the fact`, `big</a> · 3 gates, 2 owner checks`)
	assertContains(t, pageGet(t, url+"/p/yp/yolo/review"), `<b data-testid="yolo_review.issues">2</b>`, `3 gates, 2 owner checks`, `Agree all 3 gates`)
}

func TestYOLOReviewPageEmpty(t *testing.T) {
	proj := setupYOLOProject(t)
	path := filepath.Join(proj.IssueDir, "shop.md")
	if err := tracker.AddComment(path, 0, tracker.YOLOReviewComment("agreed", "ok", "viewer", "2026-10-29", "backlog"), "app"); err != nil {
		t.Fatal(err)
	}
	url := yoloReviewServer(t, proj)
	page := pageGet(t, url+"/p/yp/yolo/review")
	assertContains(t, page, `data-testid="yolo_review.empty"`)
	if strings.Contains(page, `data-testid="yolo_review.group"`) {
		t.Error("an empty queue lists a group")
	}
}

// One Agree on an issue covers all its gates and leaves its owner checks;
// each owner check is ticked or agreed on its own; Back to sends the issue
// back once and settles its gates and checks.
func TestYOLOReviewAgreeAndBackPerIssue(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	cart := filepath.Join(proj.IssueDir, "cart.md")
	// A second gate on cart: one Agree covers both.
	if err := tracker.AddComment(cart, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-cart", "backlog"), "2026-10-31", "done"), "cli"); err != nil {
		t.Fatal(err)
	}

	if code, _ := postIssueReview(t, url, `{"verdict":"agree"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("no slug: %d", code)
	}
	if code, _ := postIssueReview(t, url, `{"slug":"nope","verdict":"agree"}`, nil); code != http.StatusNotFound {
		t.Fatalf("unknown issue: %d", code)
	}
	if code, _ := postIssueReview(t, url, `{"slug":"cart","verdict":"maybe"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("bad verdict: %d", code)
	}

	code, out := postIssueReview(t, url, `{"slug":"cart","verdict":"agree","words":"all fine"}`, nil)
	if code != http.StatusOK || out["settled"] != float64(2) || out["checks_left"] != float64(1) {
		t.Fatalf("agree: %d %v", code, out)
	}
	comments, _ := tracker.LoadComments(cart)
	var reviews []string
	for _, c := range comments {
		if _, ok := tracker.ParseYOLOReviewComment(c.Text); ok {
			if c.Author != tracker.OwnerAuthor {
				t.Errorf("review by %q, want the owner", c.Author)
			}
			reviews = append(reviews, c.Text)
		}
	}
	if len(reviews) != 2 || !strings.Contains(reviews[0], `agreed "all fine"`) || !strings.HasSuffix(reviews[0], "→ backlog") || !strings.HasSuffix(reviews[1], "→ done") {
		t.Fatalf("reviews = %q", reviews)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	entries := yolo.QueueEntries(issues)
	var cartEntry *yolo.Entry
	for i := range entries {
		if entries[i].Slug == "cart" {
			cartEntry = &entries[i]
		}
	}
	if len(entries) != 2 || cartEntry == nil || len(cartEntry.Gates) != 0 || len(cartEntry.Checks) != 1 {
		t.Fatalf("entries after agree = %+v, want shop, and cart with its owner check only", entries)
	}
	// Agree again: no gate waits, the owner check is its own.
	if code, out := postIssueReview(t, url, `{"slug":"cart","verdict":"agree"}`, nil); code != http.StatusConflict || !strings.Contains(errText(out), "tick or agree each owner check") {
		t.Fatalf("second agree: %d %v", code, out)
	}
	// The owner check, on its own: agreed without it through the station's endpoint.
	if code, out := postJSON(t, url+"/p/yp/issue/cart/yolo-review", `{"status":"AC1","verdict":"agree","words":"fine without"}`); code != 200 {
		t.Fatalf("agree without the check: %d %v", code, out)
	}
	if raw, _ := os.ReadFile(cart); !strings.Contains(string(raw), "- [ ] AC1") {
		t.Error("agreeing without the check ticked the box")
	}

	// Back to on shop: words required, then the issue moves once.
	if code, _ := postIssueReview(t, url, `{"slug":"shop","verdict":"back"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("back without words: %d", code)
	}
	code, out = postIssueReview(t, url, `{"slug":"shop","verdict":"back","words":"not settled"}`, nil)
	if code != http.StatusOK || out["to"] != "in design" || out["settled"] != float64(1) {
		t.Fatalf("back: %d %v", code, out)
	}
	issues, _ = tracker.LoadIssues(proj.IssueDir)
	if shop := tracker.FindIssue(issues, "shop"); shop.Status != "in design" {
		t.Fatalf("shop after back = %q", shop.Status)
	}
	if q := yolo.QueueEntries(issues); len(q) != 0 {
		t.Fatalf("queue after all reviews = %+v", q)
	}
	assertContains(t, pageGet(t, url+"/p/yp/yolo/review"), `data-testid="yolo_review.empty"`)
}

// Back to on an issue with owner checks settles them too: the issue leaves
// the queue in one step.
func TestYOLOReviewBackSettlesOwnerChecks(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	if code, out := postIssueReview(t, url, `{"slug":"cart","verdict":"back","words":"redo the cart"}`, nil); code != 200 || out["settled"] != float64(2) {
		t.Fatalf("back: %d %v", code, out)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, e := range yolo.QueueEntries(issues) {
		if e.Slug == "cart" {
			t.Fatalf("cart still waits after Back: %+v", e)
		}
	}
}

// The issue's Agree and Back are the owner's: curl, an agent's script and a
// forged header are refused and nothing is written.
func TestYOLOIssueReviewIsTheOwnersOnly(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	cart := filepath.Join(proj.IssueDir, "cart.md")
	before, _ := os.ReadFile(cart)
	for name, headers := range map[string]map[string]string{
		"no session (curl, agent)": {strangerHeader: "1"},
		"forged same-origin":       {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
		"cross-site":               {"Sec-Fetch-Site": "cross-site"},
	} {
		for _, verdict := range []string{"agree", "back"} {
			body := fmt.Sprintf(`{"slug":"cart","verdict":%q,"words":"agent says so"}`, verdict)
			if code, out := postIssueReview(t, url, body, headers); code != http.StatusForbidden {
				t.Errorf("%s %s: status %d %v, want 403", name, verdict, code, out)
			}
		}
	}
	// The per-check endpoints the page uses are the owner's too.
	for _, req := range []struct{ path, body string }{
		{"/p/yp/issue/cart/yolo-review", `{"status":"AC1","verdict":"agree"}`},
		{"/p/yp/owner-checks/tick", `{"slug":"cart","id":"AC1"}`},
	} {
		r, _ := http.NewRequest(http.MethodPost, url+req.path, strings.NewReader(req.body))
		r.Header.Set(strangerHeader, "1")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s without a session: %d, want 403", req.path, resp.StatusCode)
		}
	}
	if after, _ := os.ReadFile(cart); string(after) != string(before) {
		t.Fatal("a refused review changed the issue")
	}
}

// Many issues: 15 a page, the oldest first; Needs you lists 8 issues and
// points at the page for the rest.
func TestYOLOReviewPagesAndNeedsYouCap(t *testing.T) {
	proj := setupYOLOProject(t)
	for i := 0; i < 17; i++ {
		path := filepath.Join(proj.IssueDir, fmt.Sprintf("job-%02d.md", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("---\ntitle: \"Job %02d\"\nstatus: \"backlog\"\n---\n\nBody.\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := tracker.AddComment(path, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent", "in design"), "2026-10-30", "backlog"), "cli"); err != nil {
			t.Fatal(err)
		}
	}
	url := yoloReviewServer(t, proj)
	page := pageGet(t, url+"/p/yp/yolo/review")
	if n := strings.Count(page, `data-testid="yolo_review.group"`); n != yoloReviewPageSize {
		t.Errorf("page 1 has %d groups, want %d", n, yoloReviewPageSize)
	}
	assertContains(t, page, `data-testid="yolo_review.pager"`, `href="/p/yp/yolo/review?page=2"`, "page 1 of 2")
	page2 := pageGet(t, url+"/p/yp/yolo/review?page=2")
	if n := strings.Count(page2, `data-testid="yolo_review.group"`); n != 3 {
		t.Errorf("page 2 has %d groups, want 3", n)
	}
	assertContains(t, page2, `href="/p/yp/yolo/review?page=1"`)

	ny := pageGet(t, url+"/p/yp/needs-you")
	if n := strings.Count(ny, `data-testid="needs_you.after.row"`); n != needsYouAfterRows {
		t.Errorf("Needs you lists %d after-the-fact rows, want %d", n, needsYouAfterRows)
	}
	assertContains(t, ny, "… 10 more issues on the review page", `data-testid="needs_you.after.more"`, "18 issues · 18 gates, 0 owner checks")
}

func TestYOLOReviewGroupsOldestFirst(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	entries := yolo.Entries([]yolo.Pass{
		{Slug: "b", Status: "backlog", At: "2026-10-31T08:00:00Z"},
		{Slug: "a", Status: "backlog", At: "2026-10-31T07:00:00Z"},
		{Slug: "b", Check: "AC1", At: "2026-10-31T05:00:00Z"},
		{Slug: "c", Status: "done", At: "not a date"},
	})
	groups := yoloReviewGroups(entries, now)
	if len(groups) != 3 || groups[0].Slug != "b" || groups[1].Slug != "a" || groups[2].Slug != "c" {
		t.Fatalf("groups = %+v, want b (05:00), a (07:00), c (no date)", groups)
	}
	if len(groups[0].Gates) != 1 || len(groups[0].Checks) != 1 || groups[0].Oldest != shortDuration(4*time.Hour) || groups[0].Detail != "1 gate, 1 owner check" {
		t.Errorf("b = %+v", groups[0])
	}
	if oldest, ok := yoloQueueOldest(entries); !ok || !oldest.Equal(time.Date(2026, 10, 31, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("oldest = %v %v", oldest, ok)
	}
}
