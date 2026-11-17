package viewer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The stats page's two slow inputs, cached per project so the page renders
// in well under 300 ms on a big project (Kestrel: the 30-day aggregate
// alone is ~500 ms, the CLI usage report ~300 ms).
//
// The aggregate is kept for the widest window asked for and filtered down
// for narrower ones. Once there is one, a request never builds it: when the
// done issues changed (their fingerprint: on a busy board several times an
// hour, so a warm-up alone goes stale) or the range asks for more than it
// covers, the page gets the last one marked updating, and the new one is
// built in the background. With none at all, before the warm-up lands, a
// request starts the build in the background and waits for it at most
// statsColdWait: a small project still gets its numbers on the first
// visit, a big one gets the page marked Building at once. Older than statsAggregateTTL it is
// refreshed in the background too, unmarked: clilogs move on without
// touching an issue file. The CLI usage report never blocks: it is served
// from the cache and refreshed in the background.

const (
	statsAggregateTTL = 15 * time.Minute
	statsUsageTTL     = 5 * time.Minute
	// statsWarmWindow is what the server computes at start: the 90-day
	// range plus the 30 days a trend point looks back, so it covers the
	// default 30-day view too (TestWarmStatsCoversTheDefaultView).
	statsWarmWindow = 120 * 24 * time.Hour
	// statsColdWait is how long a request with no aggregate at all waits
	// for the first build before it answers marked Building.
	statsColdWait = 500 * time.Millisecond
	// statsBuildingRefresh is the Refresh header's seconds on a Building
	// page: the browser reloads it until the numbers are in.
	statsBuildingRefresh = "3"
)

type statsAggregate struct {
	fp      string
	window  time.Duration
	at      time.Time
	rep     timeline.Report
	ms      int64
	running bool
}

type statsUsage struct {
	at      time.Time
	rep     *telemetry.Report
	notice  string
	running bool
}

type statsCache struct {
	mu    sync.Mutex
	agg   map[string]*statsAggregate
	usage map[string]*statsUsage
	// buildMu serializes aggregate builds per project, so two requests on
	// a cold cache build it once.
	buildMu map[string]*sync.Mutex
	// pending is the first background build per project, while it runs:
	// closed when it returns, so cold requests share it.
	pending map[string]chan struct{}
	// coldWait is statsColdWait; tests set it to 0.
	coldWait time.Duration
	// compute builds the report; tests stand in for timeline.Aggregate.
	compute func(proj *tracker.Project, wf *tracker.WorkflowConfig, issues []*tracker.Issue, now time.Time, window time.Duration) timeline.Report
}

func newStatsCache() *statsCache {
	return &statsCache{
		agg: map[string]*statsAggregate{}, usage: map[string]*statsUsage{}, buildMu: map[string]*sync.Mutex{},
		pending: map[string]chan struct{}{}, coldWait: statsColdWait,
		compute: func(proj *tracker.Project, wf *tracker.WorkflowConfig, issues []*tracker.Issue, now time.Time, window time.Duration) timeline.Report {
			rep := timeline.Aggregate(context.Background(), proj, wf, issues, now, window)
			// The By model table's tokens: worker transcripts, read in this
			// background build only once some done issue has a model.
			if rep.HasModels() {
				sp := spend.Load(proj, issues, now, spend.Options{})
				rep = rep.WithTokens(sp.IssueTokens(), sp.Since)
			}
			return rep
		},
	}
}

// statsRead is what a request gets from the cache.
type statsRead struct {
	Rep timeline.Report
	MS  int64
	// Updating: Rep is the last aggregate, not one for this board and
	// range; the new one is being built in the background.
	Updating bool
	// Building: there is no aggregate yet and the first one is being
	// built in the background; Rep is empty.
	Building bool
	// Built is when Rep was built, Covers the window it covers. A request
	// wider than Covers sees only that much until the rebuild lands.
	Built  time.Time
	Covers time.Duration
}

// doneFingerprint changes when an issue is done or leaves done, or a done
// issue's file changes: what the aggregate reads besides the clilogs.
func doneFingerprint(p *tracker.Project, issues []*tracker.Issue) string {
	h := sha256.New()
	for _, is := range issues {
		if p.IssueDone(is) {
			fmt.Fprintf(h, "%s:%d\n", is.Slug, is.ModTime.UnixNano())
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (c *statsCache) lockFor(slug string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.buildMu[slug]
	if !ok {
		m = &sync.Mutex{}
		c.buildMu[slug] = m
	}
	return m
}

// aggregate is the project report over at least window and how long it took
// to build. bg runs the background builds. With bg nil, or with no aggregate
// yet, a miss builds inline; otherwise a miss answers with the last aggregate
// marked Updating. With none yet and bg set, the build runs in the background
// and the request waits for it at most coldWait, then answers Building.
func (c *statsCache) aggregate(proj *tracker.Project, wf *tracker.WorkflowConfig, issues []*tracker.Issue, window time.Duration, now time.Time, bg func(func())) statsRead {
	fp := doneFingerprint(proj, issues)
	c.mu.Lock()
	a := c.agg[proj.Slug]
	if a != nil && (bg != nil || a.fp == fp && a.window >= window) {
		read := statsRead{Rep: a.rep, MS: a.ms, Built: a.at, Covers: a.window}
		read.Updating = a.fp != fp || a.window < window
		if (read.Updating || now.Sub(a.at) > statsAggregateTTL) && !a.running && bg != nil {
			a.running = true
			wide := max(a.window, window)
			bg(func() { c.build(proj, wf, issues, fp, wide, time.Now()) })
		}
		c.mu.Unlock()
		return read
	}
	if a == nil && bg != nil {
		done, ok := c.pending[proj.Slug]
		if !ok {
			done = make(chan struct{})
			c.pending[proj.Slug] = done
			bg(func() {
				defer func() {
					c.mu.Lock()
					delete(c.pending, proj.Slug)
					c.mu.Unlock()
					close(done)
				}()
				c.build(proj, wf, issues, fp, window, time.Now())
			})
		}
		wait := c.coldWait
		c.mu.Unlock()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-done:
			c.mu.Lock()
			landed := c.agg[proj.Slug] != nil
			c.mu.Unlock()
			if landed {
				return c.aggregate(proj, wf, issues, window, now, bg)
			}
			// Nothing landed: the next request starts another build.
			return statsRead{Building: true, Built: now}
		case <-timer.C:
			return statsRead{Building: true, Built: now}
		}
	}
	c.mu.Unlock()
	if a != nil {
		window = max(a.window, window)
	}
	return c.build(proj, wf, issues, fp, window, now)
}

func (c *statsCache) build(proj *tracker.Project, wf *tracker.WorkflowConfig, issues []*tracker.Issue, fp string, window time.Duration, now time.Time) statsRead {
	lock := c.lockFor(proj.Slug)
	lock.Lock()
	defer lock.Unlock()
	c.mu.Lock()
	if a := c.agg[proj.Slug]; a != nil && a.fp == fp && a.window >= window && now.Sub(a.at) <= statsAggregateTTL {
		c.mu.Unlock()
		return statsRead{Rep: a.rep, MS: a.ms, Built: a.at, Covers: a.window}
	}
	c.mu.Unlock()
	start := time.Now()
	rep := c.compute(proj, wf, issues, now, window)
	ms := time.Since(start).Milliseconds()
	c.mu.Lock()
	c.agg[proj.Slug] = &statsAggregate{fp: fp, window: window, at: now, rep: rep, ms: ms}
	c.mu.Unlock()
	return statsRead{Rep: rep, MS: ms, Built: now, Covers: window}
}

// statsUpdatingNote is the page's line for a read marked Updating: how old
// the numbers are, and how far they reach when the range asks for more.
func statsUpdatingNote(read statsRead, window time.Duration) string {
	if read.Building {
		return "Building these numbers for the first time since the viewer started; this page reloads by itself when they are ready"
	}
	if !read.Updating {
		return ""
	}
	note := "Updating: these numbers were built " + read.Built.Format("2 Jan 15:04") + "; reload in a few seconds"
	if read.Covers < window {
		note += fmt.Sprintf(". Until then they reach back only %d days", int(read.Covers/(24*time.Hour)))
	}
	return note
}

// cliUsage is the cached CLI usage report and its notice. It never waits
// for the report: a missing or stale one is refreshed through bg, and
// loading is true until the first one lands.
func (c *statsCache) cliUsage(proj *tracker.Project, now time.Time, bg func(func())) (rep *telemetry.Report, notice string, loading bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := c.usage[proj.Slug]
	if u == nil {
		u = &statsUsage{}
		c.usage[proj.Slug] = u
	}
	if (u.at.IsZero() || now.Sub(u.at) > statsUsageTTL) && !u.running {
		u.running = true
		refresh := func() {
			r, n := loadCLIUsage(proj)
			c.mu.Lock()
			u.rep, u.notice, u.at, u.running = r, n, time.Now(), false
			c.mu.Unlock()
		}
		if bg == nil {
			c.mu.Unlock()
			refresh()
			c.mu.Lock()
		} else {
			bg(refresh)
		}
	}
	return u.rep, u.notice, u.at.IsZero()
}

// warmStats builds each project's aggregate and CLI usage in the
// background after start, so the first stats page is fast too.
// One project at a time, so start-up doesn't load every core.
func (s *Server) warmStats(stop <-chan struct{}) {
	for i := range s.projects {
		select {
		case <-stop:
			return
		default:
		}
		proj := &s.projects[i]
		issues, err := proj.LoadIssues()
		if err != nil {
			log.Printf("stats: %s: %v", proj.Slug, err)
			continue
		}
		s.stats.aggregate(proj, proj.LoadWorkflow(), issues, statsWarmWindow, time.Now(), nil)
		s.stats.cliUsage(proj, time.Now(), nil)
	}
}

// statsBG runs fn on the server's background runner.
func (s *Server) statsBG(name string) func(func()) {
	return func(fn func()) { s.bg.Go(name, fn) }
}
