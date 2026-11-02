package viewer

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// The fixture is setupDecisionsProject's: shop#D-2 has no words,
// sim/phases#D-1 is superseded, sim/pivot#D-1 has the owner's words.

func TestDecisionConfirmRecordsTheOwnersWordsOnTheSameLine(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil) // no CEO: Confirm does not need it
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := postJSON(t, ts.URL+"/p/wb/decisions/shop/D-2/confirm", `{"text":"yes, later"}`)
	if code != 200 || out["ref"] != "shop#D-2" || out["quote"] != "yes, later" {
		t.Fatalf("confirm: %d %v", code, out)
	}
	date := time.Now().Format("2006-01-02")
	want := `- **D-2** · 2026-10-04 · deferred · chat, confirmed by owner ` + date + `: Balance later, "yes, later"`
	if out["line"] != want {
		t.Fatalf("line = %q, want %q", out["line"], want)
	}
	data, _ := os.ReadFile(filepath.Join(proj.IssueDir, "shop.md"))
	if !strings.Contains(string(data), want) {
		t.Fatalf("issue not rewritten:\n%s", data)
	}

	// The row now shows the owner's words and no box.
	_, html := getBody(t, ts.URL+"/p/wb/decisions")
	assertContains(t, html, `data-testid="decisions.row.shop.d-2.quote"`, `<blockquote>“yes, later”</blockquote>`)
	if strings.Contains(html, `data-testid="decisions.row.shop.d-2.confirm"`) {
		t.Error("confirmed decision still asks for the owner's words")
	}

	code, out = postJSON(t, ts.URL+"/p/wb/decisions/shop/D-2/confirm", `{}`)
	if code != http.StatusConflict || !strings.Contains(out["error"].(string), "already has your words") {
		t.Fatalf("second confirm: %d %v", code, out)
	}
}

func TestDecisionConfirmWithoutWordsAndProvisional(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	writeWorkbenchIssue(t, proj.IssueDir, "map", "backlog", "", "## Decisions\n"+
		"- **D-1** · 2026-10-29 · lead, YOLO: (provisional) Keep the lots square, \"lots are fine\"")
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	// A provisional decision with a relayed quote still gets the box.
	_, html := getBody(t, ts.URL+"/p/wb/decisions")
	assertContains(t, html, `data-confirm="/p/wb/decisions/map/D-1/confirm"`, "Provisional — not confirmed by you yet.")

	code, out := postJSON(t, ts.URL+"/p/wb/decisions/map/D-1/confirm", "")
	if code != 200 || out["quote"] != "lots are fine" || strings.Contains(out["line"].(string), "provisional") || strings.Contains(out["line"].(string), "YOLO") {
		t.Fatalf("confirm: %d %v", code, out)
	}
}

func TestDecisionConfirmRefuses(t *testing.T) {
	withMockTmuxSessions(t, nil)
	for _, tc := range []struct {
		name, path string
		code       int
	}{
		{"no issue", "/p/wb/decisions/nope/D-1/confirm", 404},
		{"no decision", "/p/wb/decisions/shop/D-9/confirm", 404},
		{"superseded", "/p/wb/decisions/sim/phases/D-1/confirm", 409},
		{"owner's words already", "/p/wb/decisions/sim/pivot/D-1/confirm", 409},
	} {
		proj := setupDecisionsProject(t)
		before := snapshotIssues(t, proj.IssueDir)
		ts := newTestServer(t, []tracker.Project{proj})
		code, out := postJSON(t, ts.URL+tc.path, `{"text":"x"}`)
		ts.Close()
		if code != tc.code {
			t.Errorf("%s: %d %v, want %d", tc.name, code, out, tc.code)
		}
		if after := snapshotIssues(t, proj.IssueDir); !bytes.Equal(before, after) {
			t.Errorf("%s: an issue changed", tc.name)
		}
	}
}

func TestDecisionConfirmCrossOriginIsRefused(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	before := snapshotIssues(t, proj.IssueDir)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/wb/decisions/shop/D-2/confirm", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin confirm: %d", resp.StatusCode)
	}
	if after := snapshotIssues(t, proj.IssueDir); !bytes.Equal(before, after) {
		t.Fatal("an issue changed")
	}
}

// snapshotIssues is every issue file's bytes, in walk order.
func snapshotIssues(t *testing.T, dir string) []byte {
	t.Helper()
	var all []byte
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".md") {
			data, _ := os.ReadFile(path)
			all = append(append(all, path...), data...)
		}
		return nil
	})
	return all
}

// TestDecisionConfirmHardNeedsOwner: the confirm endpoint's Hard option is
// refused (403) without the owner's sign-in and changes nothing; from the
// owner's browser it confirms and makes the decision hard in one write, and
// makes a decision that already has the owner's words hard (AC7).
func TestDecisionConfirmHardNeedsOwner(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	before := snapshotIssues(t, proj.IssueDir)
	for _, path := range []string{"/p/wb/decisions/shop/D-2/confirm", "/p/wb/decisions/sim/pivot/D-1/confirm"} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(`{"text":"x","hard":true}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(strangerHeader, "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s without sign-in: %d, want 403", path, resp.StatusCode)
		}
	}
	if after := snapshotIssues(t, proj.IssueDir); !bytes.Equal(before, after) {
		t.Fatal("a refused Hard changed an issue")
	}
	if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
		t.Fatal("a refused Hard wrote the log")
	}

	// The Make hard button shows on a soft decision with the owner's words.
	_, html := getBody(t, ts.URL+"/p/wb/decisions")
	assertContains(t, html, `data-testid="decisions.row.sim/pivot.d-1.make_hard"`, `data-testid="decisions.row.shop.d-2.hard_box"`)

	code, out := postJSON(t, ts.URL+"/p/wb/decisions/shop/D-2/confirm", `{"text":"keep it","hard":true}`)
	if code != 200 || out["hard"] != true || !strings.HasSuffix(out["line"].(string), `"keep it" {hard}`) {
		t.Fatalf("confirm + hard: %d %v", code, out)
	}
	code, out = postJSON(t, ts.URL+"/p/wb/decisions/sim/pivot/D-1/confirm", `{"hard":true}`)
	if code != 200 || out["hard"] != true {
		t.Fatalf("make hard: %d %v", code, out)
	}
	code, _ = postJSON(t, ts.URL+"/p/wb/decisions/sim/pivot/D-1/confirm", `{"hard":true}`)
	if code != http.StatusConflict {
		t.Fatalf("hard twice: %d", code)
	}
	evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
	if len(evs) != 2 || evs[0].Event != tracker.DecisionConfirmed || evs[1].Event != tracker.DecisionAmended || evs[1].Role != tracker.ActorOwnerBrowser {
		t.Fatalf("log = %+v", evs)
	}
	_, html = getBody(t, ts.URL+"/p/wb/decisions")
	assertContains(t, html, `data-testid="decisions.row.sim/pivot.d-1.hard"`)
}

// TestYOLOReviewListsRelayedDecision: a lead's relayed hard-decision act
// under YOLO is on the review page, and the issue's Agree settles it.
func TestYOLOReviewListsRelayedDecision(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	file := filepath.Join(proj.IssueDir, "shop.md")
	text := tracker.DecisionRelayComment("shop#D-2", "made hard {hard}", tracker.DecisionRelay{Lead: "lead-wb", Quote: "make it a rule", YOLO: true})
	if err := tracker.AddCommentBy(file, 0, text, "cli", "lead-wb"); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/wb/yolo/review")
	assertContains(t, html, `data-testid="yolo_review.decision"`, "shop#D-2", "relayed by lead-wb", "Agree the relayed decision")
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	res, code, msg := reviewYOLOIssue(&proj, tracker.FindIssue(issues, "shop"), "agree", "fine")
	if code != 0 || res.Settled != 1 {
		t.Fatalf("agree: %d %s %+v", code, msg, res)
	}
	if q := yolo.QueueEntries(issues); len(q) != 0 {
		t.Fatalf("still queued: %+v", q)
	}
}

// TestYOLOReviewListsDecisionHold: a decision hold a gate passed in YOLO
// (a yolo-hold: comment) is on the review page and the Station as
// "<ref>: <rule> passed in YOLO", and the issue's Agree settles it.
func TestYOLOReviewListsDecisionHold(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	file := filepath.Join(proj.IssueDir, "shop.md")
	text := tracker.YOLOHoldComment(tracker.DecisionHold{Rule: tracker.DecisionsCitedRule, Ref: "sim/pivot#D-1", Why: "not cited in ## Design"}, "in design")
	if err := tracker.AddCommentBy(file, 0, text, "cli", "agent-shop"); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/wb/yolo/review")
	assertContains(t, html, `data-testid="yolo_review.decision"`, "sim/pivot#D-1", "decisions_cited passed in YOLO: not cited in ## Design", "Agree the decision hold")
	_, html = getBody(t, ts.URL+"/p/wb/issue/shop")
	assertContains(t, html, `data-testid="station.yolo.hold"`, "sim/pivot#D-1: decisions_cited passed in YOLO")
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	res, code, msg := reviewYOLOIssue(&proj, tracker.FindIssue(issues, "shop"), "agree", "fine")
	if code != 0 || res.Settled != 1 {
		t.Fatalf("agree: %d %s %+v", code, msg, res)
	}
	if q := yolo.QueueEntries(issues); len(q) != 0 {
		t.Fatalf("queue after agree: %+v", q)
	}
}
