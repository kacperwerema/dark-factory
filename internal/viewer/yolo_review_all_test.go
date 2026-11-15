package viewer

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

var agreeAllItemsRe = regexp.MustCompile(`data-items="([^"]*)"`)

// agreeAllItems is the list the review page carries for Agree all, as the
// owner's browser loaded it.
func agreeAllItems(t *testing.T, url string) string {
	t.Helper()
	page := pageGet(t, url+"/p/yp/yolo/review")
	m := agreeAllItemsRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the owner's page carries no Agree all list")
	}
	return html.UnescapeString(m[1])
}

func postAgreeAll(t *testing.T, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/p/yp/yolo/review/all", strings.NewReader(body))
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

func yoloQueueNow(t *testing.T, proj tracker.Project) []yolo.Entry {
	t.Helper()
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	return yolo.QueueEntries(issues)
}

// The button and its confirm are rendered only for the owner's signed-in
// browser; the confirm names the counts.
func TestYOLOReviewAgreeAllOnlyForOwner(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	owner := pageGet(t, url+"/p/yp/yolo/review")
	assertContains(t, owner, `data-testid="yolo_review.agree_all"`)
	assertContains(t, owner, `data-testid="yolo_review.agree_all.confirm"`)
	assertContains(t, owner, "2 issues · 2 gates · 1 owner check")

	req, _ := http.NewRequest(http.MethodGet, url+"/p/yp/yolo/review", nil)
	req.Header.Set(strangerHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if strings.Contains(b.String(), "yolo_review.agree_all") || strings.Contains(b.String(), "data-items=") {
		t.Fatal("a signed-out browser sees Agree all")
	}
}

// AC1: signed-out, agent and cross-site requests get 403 and change nothing.
func TestAgreeAllRefusesNonOwner(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	items := agreeAllItems(t, url)
	cart := filepath.Join(proj.IssueDir, "cart.md")
	shop := filepath.Join(proj.IssueDir, "shop.md")
	beforeCart, _ := os.ReadFile(cart)
	beforeShop, _ := os.ReadFile(shop)
	for name, headers := range map[string]map[string]string{
		"no session (curl, agent)": {strangerHeader: "1"},
		"forged same-origin":       {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
		"cross-site":               {"Sec-Fetch-Site": "cross-site"},
	} {
		if code, out := postAgreeAll(t, url, `{"items":`+items+`,"words":"agent says so"}`, headers); code != http.StatusForbidden {
			t.Errorf("%s: status %d %v, want 403", name, code, out)
		}
	}
	afterCart, _ := os.ReadFile(cart)
	afterShop, _ := os.ReadFile(shop)
	if string(afterCart) != string(beforeCart) || string(afterShop) != string(beforeShop) {
		t.Fatal("a refused Agree all changed an issue")
	}
	if q := yoloQueueNow(t, proj); len(q) != 2 {
		t.Fatalf("queue after refused Agree all = %+v", q)
	}
}

// AC2: Agree all agrees exactly what the page showed (gates, owner checks)
// with the owner as author, and the queue is empty after it.
func TestAgreeAllAgreesWhatThePageShowed(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	items := agreeAllItems(t, url)
	code, out := postAgreeAll(t, url, `{"items":`+items+`}`, nil)
	if code != http.StatusOK || out["agreed"] != float64(3) || out["issues"] != float64(2) {
		t.Fatalf("agree all: %d %v", code, out)
	}
	if skipped, _ := out["skipped"].([]any); len(skipped) != 0 {
		t.Fatalf("skipped = %v", skipped)
	}
	if q := yoloQueueNow(t, proj); len(q) != 0 {
		t.Fatalf("queue after Agree all = %+v", q)
	}
	comments, _ := tracker.LoadComments(filepath.Join(proj.IssueDir, "cart.md"))
	reviews := 0
	for _, c := range comments {
		if _, ok := tracker.ParseYOLOReviewComment(c.Text); ok {
			reviews++
			if c.Author != tracker.OwnerAuthor || !strings.Contains(c.Text, "Agree all on the YOLO review page, ") {
				t.Errorf("review %q by %q", c.Text, c.Author)
			}
		}
	}
	if reviews != 2 {
		t.Fatalf("cart has %d reviews, want its gate and its owner check", reviews)
	}
	page := pageGet(t, url+"/p/yp/yolo/review?agreed=3&issues=2&skipped=0")
	assertContains(t, page, `data-testid="yolo_review.empty"`)
	assertContains(t, page, "Agreed 3 items on 2 issues ✓ · 0 skipped")
}

// Owner checks are agreed without a tick: the box stays open.
func TestAgreeAllLeavesOwnerBoxesOpen(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	if code, out := postAgreeAll(t, url, `{"items":`+agreeAllItems(t, url)+`,"words":"all good"}`, nil); code != http.StatusOK {
		t.Fatalf("agree all: %d %v", code, out)
	}
	raw, _ := os.ReadFile(filepath.Join(proj.IssueDir, "cart.md"))
	if !strings.Contains(string(raw), "- [ ] AC1") {
		t.Fatal("Agree all ticked the owner box")
	}
}

// An item added after the page loaded is not agreed: a new issue's pass is
// not in the list, and a new pass under a key the page showed leaves that
// key waiting (one review would settle both).
func TestAgreeAllSkipsItemsAddedAfterLoad(t *testing.T) {
	proj := setupYOLOReviewProject(t)
	url := yoloReviewServer(t, proj)
	items := agreeAllItems(t, url)

	// After the page loaded: a second backlog pass on shop, and a new issue.
	shop := filepath.Join(proj.IssueDir, "shop.md")
	if err := tracker.AddComment(shop, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-shop", "in design"), "2026-11-01", "backlog"), "cli"); err != nil {
		t.Fatal(err)
	}
	late := filepath.Join(proj.IssueDir, "late.md")
	if err := os.WriteFile(late, []byte("---\ntitle: \"Late\"\nstatus: \"backlog\"\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AddComment(late, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-late", "in design"), "2026-11-01", "backlog"), "cli"); err != nil {
		t.Fatal(err)
	}

	code, out := postAgreeAll(t, url, `{"items":`+items+`}`, nil)
	if code != http.StatusOK || out["agreed"] != float64(2) || out["issues"] != float64(1) {
		t.Fatalf("agree all: %d %v", code, out)
	}
	skipped, _ := out["skipped"].([]any)
	if len(skipped) != 1 || !strings.Contains(toJSON(skipped[0]), `"slug":"shop"`) || !strings.Contains(toJSON(skipped[0]), yoloAllChanged) {
		t.Fatalf("skipped = %v, want shop's backlog as changed", skipped)
	}
	var slugs []string
	for _, e := range yoloQueueNow(t, proj) {
		slugs = append(slugs, e.Slug)
	}
	if strings.Join(slugs, ",") != "late,shop" && strings.Join(slugs, ",") != "shop,late" {
		t.Fatalf("queue after = %v, want shop and late left", slugs)
	}
	// Posting the same list again: nothing it named still waits unchanged.
	code, out = postAgreeAll(t, url, `{"items":`+items+`}`, nil)
	if code != http.StatusOK || out["agreed"] != float64(0) {
		t.Fatalf("second agree all: %d %v", code, out)
	}
	if code, _ := postAgreeAll(t, url, `{"items":[]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("empty list: %d, want 400", code)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
