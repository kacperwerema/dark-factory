package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The hall is the board: /board and /workboard redirect to it, and the old
// pages answer at ?classic=1 (factory/retire-board-canvas-graph#D-1).
func TestOldBoardsRedirectToHall(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, path := range []string{"/board", "/workboard", "/board?system=Auth", "/workboard?group=epic"} {
		resp, err := noFollow.Get(ts.URL + "/p/test-project" + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/p/test-project/hall" {
			t.Errorf("%s: %d → %q, want 302 → /p/test-project/hall", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	for path, testid := range map[string]string{"/board?classic=1": "board.filters", "/workboard?classic=1": "workboard.group.status"} {
		code, html := getBody(t, ts.URL+"/p/test-project"+path)
		if code != http.StatusOK || !strings.Contains(html, `data-testid="`+testid+`"`) {
			t.Errorf("%s: status %d, want the old page (%s)", path, code, testid)
		}
	}
}

// Filtering a classic board stays on it: the forms carry classic=1 and so do
// the links the page builds.
func TestClassicBoardsKeepClassicInFilters(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, board := getBody(t, ts.URL+"/p/test-project/board?classic=1&system=Auth")
	assertContains(t, board, `<input type="hidden" name="classic" value="1">`, `href="/p/test-project/board?classic=1" class="clear-btn"`)

	_, wb := getBody(t, ts.URL+"/p/test-project/workboard?classic=1&system=Auth&group=system")
	assertContains(t, wb, `<input type="hidden" name="classic" value="1">`,
		`href="/p/test-project/workboard?classic=1&amp;system=Auth"`,
		`href="/p/test-project/workboard?classic=1&group=system" class="chip workboard-toggle active" data-testid="workboard.filter.system"`)
	for _, href := range []string{`href="/p/test-project/workboard?`, `href="/p/test-project/board?`} {
		for _, page := range []string{board, wb} {
			for _, rest := range strings.Split(page, href)[1:] {
				if !strings.HasPrefix(rest, "classic=1") {
					t.Errorf("a link drops classic=1: %s%s", href, rest[:min(40, len(rest))])
				}
			}
		}
	}
}

// The sidebar's Board opens the hall; the old boards sit under More at
// ?classic=1 next to Graph and Canvas.
func TestSidebarBoardOpensHall(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/test-project/graph")
	assertContains(t, html,
		`href="/p/test-project/hall"`,
		`href="/p/test-project/board?classic=1"`,
		`href="/p/test-project/workboard?classic=1"`,
		`href="/p/test-project/graph"`,
		`href="/p/test-project/canvas"`)
}

// Canvas and graph stay classic pages: they still serve, and no Workshop
// Board page links to them.
func TestCanvasAndGraphStayClassic(t *testing.T) {
	proj, _ := setupTestProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	for _, path := range []string{"/canvas", "/graph"} {
		if code, _ := getBody(t, ts.URL+"/p/test-project"+path); code != http.StatusOK {
			t.Errorf("%s: status %d", path, code)
		}
	}
	for _, path := range []string{"/", "/hall", "/company", "/status", "/agents", "/systems", "/decisions", "/docs", "/issue/bug-in-login"} {
		code, html := getBody(t, ts.URL+"/p/test-project"+path)
		if code != http.StatusOK {
			t.Errorf("%s: status %d", path, code)
			continue
		}
		if strings.Contains(html, `/p/test-project/canvas`) || strings.Contains(html, `/p/test-project/graph`) {
			t.Errorf("%s: a Workshop Board page links to canvas or graph", path)
		}
	}
}
