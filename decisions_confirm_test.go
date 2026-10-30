package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
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
