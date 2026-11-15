package viewer

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// lintCacheProject is a board with one dated issue: no lint warnings.
func lintCacheProject(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	dir := t.TempDir()
	issues := filepath.Join(dir, "issues")
	if err := os.MkdirAll(issues, 0755); err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(wf, []byte("template: custom\nstatuses:\n  - name: idea\n    description: raw\ntransitions: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(issues, "a.md"), []byte("---\ntitle: \"A\"\nstatus: idea\ncreated: \"2026-11-01\"\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Slug: "lc", IssueDir: issues, WorkflowFile: wf}, issues
}

// A second request inside the window reuses the first run: lint runs once.
func TestLintCacheReusesARecentRun(t *testing.T) {
	proj, _ := lintCacheProject(t)
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	c := newLintCache()
	c.now = func() time.Time { return clock }
	runs := 0
	c.build = func(p *tracker.Project, now time.Time) *LintPanel { runs++; return lintPanel(p, now) }
	queued := 0
	bg := func(func()) { queued++ }
	c.panel(proj, bg)
	clock = clock.Add(lintCacheTTL - time.Second)
	c.panel(proj, bg)
	if runs != 1 || queued != 0 {
		t.Errorf("two requests within %s: lint ran %d times, %d rebuilds queued; want 1 and 0", lintCacheTTL, runs, queued)
	}
}

// An issue edited on disk shows its finding by the next request after the
// cache window: the request past it is served the last panel and starts the
// rebuild, the one after reads the new finding.
func TestLintCacheShowsAnEditAfterTheWindow(t *testing.T) {
	proj, issues := lintCacheProject(t)
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	c := newLintCache()
	c.now = func() time.Time { return clock }
	var queue []func()
	bg := func(fn func()) { queue = append(queue, fn) }

	if p := c.panel(proj, bg); p.Err != "" || p.Warnings != 0 {
		t.Fatalf("clean board: %d warnings, err %q", p.Warnings, p.Err)
	}
	// An undated issue: lint's undated check warns on it.
	if err := os.WriteFile(filepath.Join(issues, "b.md"), []byte("---\ntitle: \"B\"\nstatus: idea\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(10 * time.Second)
	if p := c.panel(proj, bg); p.Warnings != 0 || len(queue) != 0 {
		t.Fatalf("inside the window: %d warnings, %d rebuilds queued; want the cached 0 and none", p.Warnings, len(queue))
	}
	clock = clock.Add(lintCacheTTL)
	if p := c.panel(proj, bg); p.Warnings != 0 || len(queue) != 1 {
		t.Fatalf("past the window: %d warnings, %d rebuilds queued; want the last panel and one rebuild", p.Warnings, len(queue))
	}
	// A request while the rebuild runs starts no second one.
	if c.panel(proj, bg); len(queue) != 1 {
		t.Fatalf("a request during the rebuild queued another: %d", len(queue))
	}
	queue[0]()
	p := c.panel(proj, bg)
	if p.Warnings != 1 || len(p.Findings) != 1 || p.Findings[0].Check != "undated" {
		t.Errorf("after the rebuild: %d warnings %+v; want the undated finding", p.Warnings, p.Findings)
	}
}

// Without a background runner a stale panel is rebuilt inline, and a nil
// cache runs lint live.
func TestLintCacheWithoutBackgroundRebuildsInline(t *testing.T) {
	proj, issues := lintCacheProject(t)
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	c := newLintCache()
	c.now = func() time.Time { return clock }
	c.panel(proj, nil)
	if err := os.WriteFile(filepath.Join(issues, "b.md"), []byte("---\ntitle: \"B\"\nstatus: idea\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(lintCacheTTL)
	if p := c.panel(proj, nil); p.Warnings != 1 {
		t.Errorf("stale, no bg: %d warnings; want 1", p.Warnings)
	}
	var none *lintCache
	if p := none.panel(proj, nil); p.Warnings != 1 {
		t.Errorf("nil cache: %d warnings; want 1", p.Warnings)
	}
}

// Two projects with no slug never share a panel.
func TestLintCacheKeysByFiles(t *testing.T) {
	a, _ := lintCacheProject(t)
	b, issues := lintCacheProject(t)
	a.Slug, b.Slug = "", ""
	os.WriteFile(filepath.Join(issues, "b.md"), []byte("---\ntitle: \"B\"\nstatus: idea\n---\n"), 0644)
	c := newLintCache()
	if pa, pb := c.panel(a, nil), c.panel(b, nil); pa.Warnings != 0 || pb.Warnings != 1 {
		t.Errorf("a %d, b %d warnings; want 0 and 1", pa.Warnings, pb.Warnings)
	}
}

// The /health page goes through the server's cache: two loads in a row run
// lint once.
func TestHealthPageReusesTheLintRun(t *testing.T) {
	proj, _ := lintCacheProject(t)
	proj.Name = "LC"
	srv, err := NewServer([]tracker.Project{*proj})
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	srv.lint.build = func(p *tracker.Project, now time.Time) *LintPanel { runs++; return lintPanel(p, now) }
	ts := httptestServer(t, srv)
	for i := 0; i < 2; i++ {
		pageGet(t, ts+"/p/lc/health")
	}
	if runs != 1 {
		t.Errorf("two /health loads ran lint %d times; want 1", runs)
	}
}

func httptestServer(t *testing.T, srv *Server) string {
	t.Helper()
	ts := httptest.NewServer(asOwner(t, srv))
	t.Cleanup(ts.Close)
	return ts.URL
}

// A scenario viewer (recent.OffEnv) runs lint on every request, so a
// playtest step that edits an issue sees it on the reload.
func TestLintCacheOffInAScenarioViewer(t *testing.T) {
	t.Setenv(recent.OffEnv, "1")
	proj, _ := lintCacheProject(t)
	c := newLintCache()
	runs := 0
	c.build = func(p *tracker.Project, now time.Time) *LintPanel { runs++; return lintPanel(p, now) }
	c.panel(proj, func(func()) { t.Error("queued a rebuild") })
	c.panel(proj, func(func()) { t.Error("queued a rebuild") })
	if runs != 2 {
		t.Errorf("scenario viewer: lint ran %d times for two requests; want 2", runs)
	}
}
