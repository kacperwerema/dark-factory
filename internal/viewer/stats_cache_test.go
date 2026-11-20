package viewer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/timing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// statsCacheServer is a server with one done issue whose aggregate builds
// are counted; gate, while non-nil, holds each build until it is closed.
type statsCacheServer struct {
	*Server
	done   string
	builds atomic.Int32
	window atomic.Int64
	gate   atomic.Pointer[chan struct{}]
}

func newStatsCacheServer(t *testing.T) *statsCacheServer {
	t.Helper()
	withStubTelemetryReport(t, func(*tracker.Project) ([]byte, error) { return nil, errors.New("no report") })
	proj, _ := setupTestProject(t)
	done := filepath.Join(proj.IssueDir, "shipped.md")
	if err := os.WriteFile(done, []byte("---\ntitle: \"Shipped\"\nstatus: \"done\"\n---\n\nDone.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	s := &statsCacheServer{Server: srv, done: done}
	srv.stats.compute = func(p *tracker.Project, wf *tracker.WorkflowConfig, issues []*tracker.Issue, now time.Time, window time.Duration) timeline.Report {
		s.builds.Add(1)
		s.window.Store(int64(window))
		if g := s.gate.Load(); g != nil {
			<-*g
		}
		return timeline.Aggregate(context.Background(), p, wf, issues, now, window)
	}
	t.Cleanup(func() { s.finishBackground(t) })
	return s
}

// finishBackground waits for the background builds to land.
func (s *statsCacheServer) finishBackground(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if left := s.bg.Wait(ctx); len(left) > 0 {
		t.Fatalf("still running: %v", left)
	}
}

// page is the stats page; it fails when a held build keeps it waiting, which
// is what a build in the request would do.
func (s *statsCacheServer) page(t *testing.T, query string) string {
	t.Helper()
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("GET", "/p/test-project/stats"+query, nil)
		req.Host = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		out <- rec
	}()
	select {
	case rec := <-out:
		if rec.Code != http.StatusOK {
			t.Fatalf("stats page = %d", rec.Code)
		}
		return rec.Body.String()
	case <-time.After(20 * time.Second):
		t.Fatal("the stats page waited for the aggregate build")
		return ""
	}
}

// touchDone changes the done issue's file time: what a comment or a late
// edit does to the fingerprint.
func (s *statsCacheServer) touchDone(t *testing.T) {
	t.Helper()
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(s.done, later, later); err != nil {
		t.Fatal(err)
	}
}

func (s *statsCacheServer) holdBuilds() (release func()) {
	g := make(chan struct{})
	s.gate.Store(&g)
	return func() { s.gate.Store(nil); close(g) }
}

// After the warm-up the default view is a hit: no build in the request, no
// updating line.
func TestWarmStatsCoversTheDefaultView(t *testing.T) {
	s := newStatsCacheServer(t)
	if def := parseStatsOpts(nil).window(); statsWarmWindow < def {
		t.Fatalf("statsWarmWindow %s doesn't cover the default view's %s", statsWarmWindow, def)
	}
	s.warmStats(make(chan struct{}))
	if n := s.builds.Load(); n != 1 {
		t.Fatalf("warm-up built %d times, want 1", n)
	}
	body := s.page(t, "")
	s.finishBackground(t)
	if n := s.builds.Load(); n != 1 {
		t.Errorf("the default view built again: %d builds, want the warm-up's 1", n)
	}
	if strings.Contains(body, `data-testid="stats.updating"`) {
		t.Error("a warm default view says it is updating")
	}
}

// A done issue changing after the warm-up is how the audit's 7.4 s visit
// happened. The request now gets the last aggregate marked updating, the
// rebuild runs once in the background, and the next visit is current.
func TestStatsMissServesTheLastAggregateMarkedUpdating(t *testing.T) {
	for _, tc := range []struct{ name, query string }{{"workshop", ""}, {"classic", "?classic=1"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStatsCacheServer(t)
			s.warmStats(make(chan struct{}))
			s.touchDone(t)
			release := s.holdBuilds()
			defer func() {
				if s.gate.Load() != nil {
					release()
				}
			}()
			for range 3 {
				body := s.page(t, tc.query)
				if !strings.Contains(body, `data-testid="stats.updating"`) || !strings.Contains(body, "reload in a few seconds") {
					t.Fatal("a miss must show the last aggregate marked updating")
				}
			}
			release()
			s.finishBackground(t)
			if n := s.builds.Load(); n != 2 {
				t.Errorf("%d builds, want the warm-up's and one rebuild", n)
			}
			if body := s.page(t, tc.query); strings.Contains(body, `data-testid="stats.updating"`) {
				t.Error("still updating after the rebuild landed")
			}
		})
	}
}

// A range wider than the last aggregate says how far its numbers reach until
// the wider one lands, and the rebuild covers that range.
func TestStatsWiderRangeSaysWhatItCovers(t *testing.T) {
	s := newStatsCacheServer(t)
	s.warmStats(make(chan struct{}))
	release := s.holdBuilds()
	body := s.page(t, "?range=all")
	release()
	if !strings.Contains(body, "reach back only 120 days") {
		t.Error("the note must say the numbers cover 120 days")
	}
	s.finishBackground(t)
	if got, want := time.Duration(s.window.Load()), parseStatsOpts(map[string][]string{"range": {"all"}}).window(); got != want {
		t.Errorf("rebuilt over %s, want %s", got, want)
	}
	if body := s.page(t, "?range=all"); strings.Contains(body, "stats.updating") {
		t.Error("still updating after the wider aggregate landed")
	}
}

// With no aggregate at all there is nothing to serve: the request waits for
// the build, and concurrent ones share it.
func TestStatsNoAggregateYetBuildsOnce(t *testing.T) {
	s := newStatsCacheServer(t)
	done := make(chan string, 2)
	for range 2 {
		go func() { done <- s.page(t, "") }()
	}
	for range 2 {
		if body := <-done; strings.Contains(body, `data-testid="stats.updating"`) {
			t.Error("a first build has nothing old to mark")
		}
	}
	if n := s.builds.Load(); n != 1 {
		t.Errorf("%d builds, want 1", n)
	}
}

// pageWithHeaders is one stats request, how long it took and its Refresh
// header.
func (s *statsCacheServer) pageWithHeaders(t *testing.T, query string) (body, refresh string, took time.Duration) {
	t.Helper()
	req := httptest.NewRequest("GET", "/p/test-project/stats"+query, nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	start := time.Now()
	s.Routes().ServeHTTP(rec, req)
	took = time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats page = %d", rec.Code)
	}
	return rec.Body.String(), rec.Header().Get("Refresh"), took
}

// A visit before the first build lands (a fresh viewer, the warm-up still
// running: 21–27 s on the dark-factory board) answers at once, says the
// numbers are being built and reloads until they are in; then the page is
// the full one with no reload.
func TestStatsColdRequestDoesNotWait(t *testing.T) {
	for _, tc := range []struct{ name, query string }{{"workshop", ""}, {"classic", "?classic=1"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStatsCacheServer(t)
			release := s.holdBuilds()
			defer func() {
				if s.gate.Load() != nil {
					release()
				}
			}()
			// Every request while the build is held is a cold one: the
			// check takes the best of several, so a burst of load on one
			// does not fail it.
			cold := func() time.Duration {
				body, refresh, took := s.pageWithHeaders(t, tc.query)
				if !strings.Contains(body, `data-testid="stats.updating"`) || !strings.Contains(body, "Building these numbers") {
					t.Error("a cold request must say the numbers are being built")
				}
				if refresh != statsBuildingRefresh {
					t.Errorf("Refresh = %q, want %q so the page fills in by itself", refresh, statsBuildingRefresh)
				}
				return took
			}
			cold()
			if r := (timing.Check{Limit: time.Second}).Run(cold, nil); r.Slow {
				t.Errorf("a cold request: %v", r)
			}
			release()
			s.finishBackground(t)
			if n := s.builds.Load(); n != 1 {
				t.Errorf("%d builds, want one shared by the cold requests", n)
			}
			body, refresh, _ := s.pageWithHeaders(t, tc.query)
			if refresh != "" || strings.Contains(body, `data-testid="stats.updating"`) {
				t.Errorf("still building after the build landed (Refresh %q)", refresh)
			}
		})
	}
}
