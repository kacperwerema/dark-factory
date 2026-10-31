package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// ownerChecksSandbox is project Acme with an epic and three children:
// "child" has a judgement step box TP1 with a shot, a worker's box TP2 and
// a Human Testing box; "plain" has nothing for the owner; "other" belongs
// to another epic.
func ownerChecksSandbox(t *testing.T) (tracker.Project, string) {
	t.Helper()
	_, proj := deptSandbox(t)
	root := proj.Root()
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("issues/epic.md", "---\ntitle: \"The epic\"\nslug: \"epic\"\nstatus: \"in progress\"\n---\n\n## Acceptance criteria\n- [ ] Every page reads like the board (owner)\n")
	write("issues/child.md", "---\ntitle: \"Child page\"\nslug: \"child\"\nstatus: \"done\"\nparent: \"epic\"\n---\n\n## Test Plan\n- [ ] TP1 The page reads like the Workshop Board (owner's look)\n- [ ] TP2 A worker's box\n\n## Human Testing\n- [ ] Human verification complete if required\n")
	write("issues/plain.md", "---\ntitle: \"Plain\"\nslug: \"plain\"\nstatus: \"done\"\nparent: \"epic\"\n---\n\n## Test Plan\n- [x] TP1 Done\n")
	write("issues/other.md", "---\ntitle: \"Other\"\nslug: \"other\"\nstatus: \"done\"\nparent: \"elsewhere\"\n---\n\n## Human Testing\n- [ ] Human verification complete if required\n")
	write("issues/elsewhere.md", "---\ntitle: \"Elsewhere\"\nslug: \"elsewhere\"\nstatus: \"idea\"\n---\n")
	run := ".playtest/runs/child-page/20261031-1000-auto/"
	write(run+"results.yaml", "session: child-page\nissue: child\nmode: auto\nstarted: \"2026-10-31T10:00:00Z\"\nsteps:\n  - id: TP1\n    status: pass\n    judgement_step: true\n    viewports:\n      - viewport: 1440x900\n        status: pass\n        screenshot: shots/TP1-1440x900.png\n  - id: TP2\n    status: pass\n")
	write(run+"shots/TP1-1440x900.png", "png")
	ts := newTestServer(t, []tracker.Project{proj})
	t.Cleanup(ts.Close)
	return proj, ts.URL
}

func postTick(t *testing.T, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/p/acme/owner-checks/tick", strings.NewReader(body))
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

// Proves: AC2 — one page lists every open owner box under an epic, with its
// shot and issue.
func TestOwnerChecksListsAnEpicsOwnerBoxes(t *testing.T) {
	_, url := ownerChecksSandbox(t)
	resp, body := getResp(t, url+"/p/acme/owner-checks?epic=epic")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`data-testid="owner_checks.page"`,
		`data-testid="owner_checks.count">3</span>`,
		`data-slug="epic"`, `data-id="AC1"`, `marked (owner)`,
		`data-slug="child"`, `data-id="TP1"`, `playtest judgement step TP1`,
		`child-page · TP1 judged pass`,
		`src="/p/acme/files/.playtest/runs/child-page/20261031-1000-auto/shots/TP1-1440x900.png"`,
		`data-id="HT1"`, `Human Testing box`, `data-testid="owner_checks.box.child.ht1.noshot"`,
		`href="/p/acme/issue/child?tab=evidence" data-testid="owner_checks.issue.child.evidence"`,
		`data-tick-url="/p/acme/owner-checks/tick"`,
		`<option value="epic" selected>The epic · 2 children</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("owner checks page missing %q", want)
		}
	}
	for _, not := range []string{`data-id="TP2"`, `data-slug="plain"`, `data-slug="other"`} {
		if strings.Contains(body, not) {
			t.Errorf("owner checks page must not list %q", not)
		}
	}
}

func TestOwnerChecksPickerAllAndUnknown(t *testing.T) {
	_, url := ownerChecksSandbox(t)
	_, body := getResp(t, url+"/p/acme/owner-checks")
	for _, want := range []string{`href="/p/acme/owner-checks?epic=epic" data-testid="owner_checks.epic"`, `href="/p/acme/owner-checks?epic=elsewhere"`, `href="?epic=all"`} {
		if !strings.Contains(body, want) {
			t.Errorf("picker missing %q", want)
		}
	}
	if strings.Contains(body, `data-oc-box`) {
		t.Error("the picker lists no boxes")
	}
	_, body = getResp(t, url+"/p/acme/owner-checks?epic=all")
	if !strings.Contains(body, `data-slug="other"`) || !strings.Contains(body, `data-testid="owner_checks.count">4</span>`) {
		t.Errorf("epic=all must list every issue's owner boxes (4)")
	}
	resp, body := getResp(t, url+"/p/acme/owner-checks?epic=nope")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "No issue nope in this project") {
		t.Errorf("unknown epic: status %d, want 404 with the empty note", resp.StatusCode)
	}
	_, body = getResp(t, url+"/p/acme/owner-checks?epic=plain")
	if !strings.Contains(body, `data-testid="owner_checks.empty"`) {
		t.Error("an epic with no owner box shows the empty note")
	}
}

// Proves: AC3 — ticking is the owner's only: refused for curl, agents and
// other sites, and only for a box still open and worded as shown.
func TestOwnerCheckTickIsTheOwnersOnly(t *testing.T) {
	proj, url := ownerChecksSandbox(t)
	path := filepath.Join(proj.Root(), "issues", "child.md")
	before, _ := os.ReadFile(path)
	tick := `{"slug":"child","id":"TP1","text":"The page reads like the Workshop Board (owner's look)"}`
	for name, headers := range map[string]map[string]string{
		"no headers (curl, agent)": {strangerHeader: "1"},
		"forged same-origin":       {strangerHeader: "1", "Sec-Fetch-Site": "same-origin"},
		"cross-site":               {"Sec-Fetch-Site": "cross-site"},
		"other origin":             {"Origin": "http://evil.example"},
	} {
		// A cross-site write stops at sameOriginOnly (plain text); the rest
		// at the owner gate (no owner session).
		code, out := postTick(t, url, tick, headers)
		if code != http.StatusForbidden || (headers[strangerHeader] != "" && !strings.Contains(errText(out), ownerRefused)) {
			t.Errorf("%s: status %d %v, want 403", name, code, out)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused tick changed the issue")
	}

	// Not an owner box, changed text, unknown issue.
	if code, _ := postTick(t, url, `{"slug":"child","id":"TP2","text":"A worker's box"}`, sameOrigin); code != http.StatusConflict {
		t.Errorf("worker's box: status %d, want 409", code)
	}
	if code, out := postTick(t, url, `{"slug":"child","id":"TP1","text":"Old words"}`, sameOrigin); code != http.StatusConflict || !strings.Contains(errText(out), "changed since this page loaded") {
		t.Errorf("changed text: status %d %v, want 409", code, out)
	}
	if code, _ := postTick(t, url, `{"slug":"nope","id":"TP1"}`, sameOrigin); code != http.StatusNotFound {
		t.Errorf("unknown issue: status %d, want 404", code)
	}

	code, out := postTick(t, url, tick, sameOrigin)
	if code != http.StatusOK || out["ok"] != true || out["id"] != "TP1" {
		t.Fatalf("owner's tick: status %d %v", code, out)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "- [x] TP1 The page reads like the Workshop Board (owner's look)") || !strings.Contains(string(after), "- [ ] TP2 A worker's box") {
		t.Errorf("only TP1 is ticked:\n%s", after)
	}
	comments, _ := tracker.LoadComments(path)
	if len(comments) != 1 || comments[0].Author != tracker.OwnerAuthor || !strings.HasPrefix(comments[0].Text, "owner-check: TP1 ticked on the Owner checks page") {
		t.Errorf("comments = %+v, want the owner's owner-check: comment", comments)
	}
	// Ticked: it leaves the page and a second tick is refused.
	if code, _ := postTick(t, url, tick, sameOrigin); code != http.StatusConflict {
		t.Errorf("second tick: status %d, want 409", code)
	}
	_, body := getResp(t, url+"/p/acme/owner-checks?epic=epic")
	if strings.Contains(body, `data-id="TP1"`) {
		t.Error("a ticked box is still listed")
	}
}

func TestNeedsYouLinksOwnerChecks(t *testing.T) {
	_, url := ownerChecksSandbox(t)
	_, body := getResp(t, url+"/p/acme/needs-you")
	if !strings.Contains(body, `href="/p/acme/owner-checks" data-testid="needs_you.owner_checks"`) {
		t.Error("Needs you does not link the Owner checks page")
	}
}

func errText(out map[string]any) string {
	s, _ := out["error"].(string)
	return s
}
