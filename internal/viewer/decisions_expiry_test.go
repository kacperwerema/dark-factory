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
)

// expiryProject is setupDecisionsProject plus sim/rules: D-1 hard and
// expired yesterday, D-2 hard expiring today, D-3 soft and expired, D-4
// hard and expired but let go; and sim/later replacing an expired hard one.
func expiryProject(t *testing.T) (tracker.Project, string) {
	t.Helper()
	proj := setupDecisionsProject(t)
	day := func(d int) string { return time.Now().AddDate(0, 0, d).Format("2006-01-02") }
	path := filepath.Join(proj.IssueDir, "Sim", "rules.md")
	body := "---\ntitle: \"rules\"\nstatus: \"done\"\nsystem: \"Sim\"\n---\n\n## Decisions\n\n" +
		"- **D-1** · 2026-10-01 · chat: Freeze the map, \"freeze it\" {scope: project; hard; expires: " + day(-1) + "}\n" +
		"- **D-2** · 2026-10-02 · chat: Two lanes only, \"two lanes\" {scope: project; hard; expires: " + day(0) + "}\n" +
		"- **D-3** · 2026-10-03 · chat: Soft trial {scope: project; expires: " + day(-5) + "}\n" +
		"- **D-4** · 2026-10-04 · chat: Old rule, \"old\" {scope: project; hard; expires: " + day(-9) + "; ended: " + day(-2) + "}\n" +
		"- **D-5** · 2026-10-05 · chat: Replaced rule, \"r\" {scope: project; hard; expires: " + day(-3) + "}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	later := "---\ntitle: \"later\"\nstatus: \"backlog\"\nsystem: \"Sim\"\n---\n\n## Decisions\n\n" +
		"- **D-1** · 2026-10-06 · chat: New rule, \"new\" (replaces sim/rules#D-5) {scope: project; hard}\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "Sim", "later.md"), []byte(later), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj, path
}

func decisionRows(t *testing.T, proj tracker.Project) []WbQueueCard {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []WbQueueCard
	for _, r := range needsYouRows(&proj, "", "/p/"+proj.Slug, issues, time.Now()) {
		if r.Kind == "decision" {
			out = append(out, r)
		}
	}
	return out
}

// TestNeedsYouExpiredDecisionRow: exactly one row, for the expired hard
// decision nobody renewed or let go (not today's, not the soft one, not the
// let-go one, not the replaced one); renew or let go removes it (AC2).
func TestNeedsYouExpiredDecisionRow(t *testing.T) {
	for _, act := range []struct{ path, body string }{
		{"renew", `{"expires":"` + time.Now().AddDate(0, 1, 0).Format("2006-01-02") + `","text":"keep it"}`},
		{"let-go", `{"text":"let it go"}`},
	} {
		t.Run(act.path, func(t *testing.T) {
			proj, path := expiryProject(t)
			withMockTmuxSessions(t, nil)
			rows := decisionRows(t, proj)
			if len(rows) != 1 || rows[0].Slug != "sim/rules#D-1" || rows[0].Label != "Decision expired" ||
				rows[0].ExpiryURL != "/p/wb/decisions/sim/rules/D-1" || rows[0].RenewDefault != time.Now().AddDate(0, 0, renewDefaultDays).Format("2006-01-02") {
				t.Fatalf("rows = %+v", rows)
			}
			ts := newTestServer(t, []tracker.Project{proj})
			defer ts.Close()
			_, html := getBody(t, ts.URL+"/p/wb/needs-you")
			assertContains(t, html, `data-kind="decision" data-slug="sim/rules#D-1"`, `data-ny-expiry="/p/wb/decisions/sim/rules/D-1"`,
				`data-testid="needs_you.expiry.renew"`, `data-testid="needs_you.expiry.let_go"`)

			if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
				t.Fatal("rendering Needs you wrote the decisions log")
			}
			code, out := postJSON(t, ts.URL+"/p/wb/decisions/sim/rules/D-1/"+act.path, act.body)
			if code != 200 || out["ref"] != "sim/rules#D-1" {
				t.Fatalf("%s: %d %v", act.path, code, out)
			}
			if rows := decisionRows(t, proj); len(rows) != 0 {
				t.Fatalf("after %s rows = %+v", act.path, rows)
			}
			cs, _ := tracker.LoadComments(path)
			if len(cs) != 1 || cs[0].Author != tracker.OwnerAuthor || !strings.HasSuffix(cs[0].Text, "— the owner") {
				t.Fatalf("comments = %+v", cs)
			}
			evs, _ := tracker.ReadDecisionLog(proj.IssueDir)
			want := tracker.DecisionRenewedEvent
			if act.path == "let-go" {
				want = tracker.DecisionExpiredEvent
			}
			if len(evs) != 1 || evs[0].Event != want || evs[0].Role != tracker.ActorOwnerBrowser {
				t.Fatalf("log = %+v", evs)
			}
			// Again: renew moves nothing (409 once let go); let go twice is 409.
			if act.path == "let-go" {
				if code, _ := postJSON(t, ts.URL+"/p/wb/decisions/sim/rules/D-1/let-go", `{}`); code != http.StatusConflict {
					t.Fatalf("let go twice: %d", code)
				}
			}
		})
	}
}

// TestDecisionExpiryRoutesOwnerOnly: Renew and Let go without the owner's
// sign-in get 403 and write nothing (AC3).
func TestDecisionExpiryRoutesOwnerOnly(t *testing.T) {
	proj, _ := expiryProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	before := snapshotIssues(t, proj.IssueDir)
	for _, path := range []string{"/p/wb/decisions/sim/rules/D-1/renew", "/p/wb/decisions/sim/rules/D-1/let-go"} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(`{"expires":"2099-01-01"}`))
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
		t.Fatal("a refused act changed an issue")
	}
	if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); err == nil {
		t.Fatal("a refused act wrote the log")
	}
}

// TestDecisionExpiryRefusals: a past date, a decision not expired, one
// replaced and one with no expiry are refused with a reason.
func TestDecisionExpiryRefusals(t *testing.T) {
	proj, _ := expiryProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	for _, c := range []struct {
		path, body string
		code       int
		want       string
	}{
		{"sim/rules/D-1/renew", `{"expires":"2001-01-01"}`, 400, "in the past"},
		{"sim/rules/D-1/renew", `{"expires":"soon"}`, 400, "YYYY-MM-DD"},
		{"sim/rules/D-2/let-go", `{}`, 409, "has not expired"},
		{"sim/rules/D-5/let-go", `{}`, 409, "replaced by sim/later#D-1"},
		{"sim/rules/D-4/renew", `{"expires":"2099-01-01"}`, 409, "was let go"},
		{"sim/later/D-1/renew", `{"expires":"2099-01-01"}`, 409, "no expires: date"},
		{"sim/rules/D-9/renew", `{"expires":"2099-01-01"}`, 404, "has no D-9"},
	} {
		code, out := postJSON(t, ts.URL+"/p/wb/decisions/"+c.path, c.body)
		if code != c.code || !strings.Contains(out["error"].(string), c.want) {
			t.Errorf("%s: %d %v, want %d %q", c.path, code, out, c.code, c.want)
		}
	}
}
