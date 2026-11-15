package viewer

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// lintPanelCap is how many warnings the Health view lists before pointing
// at issue-cli health lint for the rest.
const lintPanelCap = 50

// LintPanel is the Health view's Lint section: issue-cli health lint run
// live over workflow.yaml and the issues (cheap: one parse plus the
// issues' frontmatter). Every finding is a warning.
type LintPanel struct {
	Warnings int
	Findings []lint.Finding
	// More is how many warnings the list leaves out.
	More int
	// Command is what the owner or an agent runs for the full list.
	Command string
	Err     string
}

func lintPanel(proj *tracker.Project, now time.Time) *LintPanel {
	p := &LintPanel{Command: lintCommandFor(proj)}
	env, err := lint.Load(proj, now)
	if err != nil {
		p.Err = err.Error()
		return p
	}
	rep, err := lint.Run(env)
	if err != nil {
		p.Err = err.Error()
		return p
	}
	p.Warnings = rep.Warnings
	p.Findings = rep.Findings
	if len(p.Findings) > lintPanelCap {
		p.More = len(p.Findings) - lintPanelCap
		p.Findings = p.Findings[:lintPanelCap]
	}
	return p
}

// lintCacheTTL is how long a project's lint report stands before a request
// starts a rebuild. issue-cli health lint takes ~0.4s on the dark-factory
// board, so the Health page reuses a recent run (as stats_cache.go does).
const lintCacheTTL = 30 * time.Second

// lintCache keeps the last Lint panel per project. The first request builds
// it inline; one older than lintCacheTTL is still served while a rebuild
// runs through bg, so the next request sees what changed on disk. With bg
// nil a stale panel is rebuilt inline. A scenario viewer (recent.OffEnv)
// keeps nothing: a playtest step edits an issue and reloads at once.
type lintCache struct {
	// ttl is lintCacheTTL, or 0 to run lint on every request.
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]*lintEntry
	// now and build are the clock and the lint run; tests stand in for them.
	now   func() time.Time
	build func(proj *tracker.Project, now time.Time) *LintPanel
}

type lintEntry struct {
	at      time.Time
	panel   *LintPanel
	running bool
}

func newLintCache() *lintCache {
	ttl := lintCacheTTL
	if os.Getenv(recent.OffEnv) == "1" {
		ttl = 0
	}
	return &lintCache{ttl: ttl, entries: map[string]*lintEntry{}, now: time.Now, build: lintPanel}
}

// lintCacheKey names a project by the files lint reads, so two projects
// without a slug (tests, a one-project viewer) never share a panel.
func lintCacheKey(proj *tracker.Project) string {
	return proj.Slug + "\x00" + proj.IssueDir + "\x00" + proj.WorkflowFile
}

// panel is proj's Lint panel, at most ttl old unless a rebuild is
// running in the background.
func (c *lintCache) panel(proj *tracker.Project, bg func(func())) *LintPanel {
	if c == nil {
		return lintPanel(proj, time.Now())
	}
	if c.ttl <= 0 {
		return c.build(proj, c.now())
	}
	key := lintCacheKey(proj)
	now := c.now()
	c.mu.Lock()
	e := c.entries[key]
	fresh := e != nil && now.Sub(e.at) < c.ttl
	if fresh || e != nil && (e.running || bg != nil) {
		if !fresh && !e.running {
			e.running = true
			c.mu.Unlock()
			bg(func() { c.store(key, proj) })
			c.mu.Lock()
		}
		p := c.entries[key].panel
		c.mu.Unlock()
		return p
	}
	c.mu.Unlock()
	return c.store(key, proj)
}

// store runs lint for proj and keeps the panel under key.
func (c *lintCache) store(key string, proj *tracker.Project) *LintPanel {
	now := c.now()
	p := c.build(proj, now)
	c.mu.Lock()
	c.entries[key] = &lintEntry{at: now, panel: p}
	c.mu.Unlock()
	return p
}

func lintCommandFor(proj *tracker.Project) string {
	if proj.Slug != "" {
		return "issue-cli --project " + proj.Slug + " health lint"
	}
	return "issue-cli health lint"
}

// lintLine is the Status page Health card's lint line, from the latest
// snapshot (as its scores are): "2 lint warnings", "no lint warnings", or
// "" when the snapshot predates the lint family.
func lintLine(s *health.Snapshot) string {
	v, ok := s.Value("lint.warnings", nil)
	if !ok {
		return ""
	}
	switch n := int(v); n {
	case 0:
		return "no lint warnings"
	case 1:
		return "1 lint warning"
	default:
		return fmt.Sprintf("%d lint warnings", n)
	}
}

func lintMarkdown(b *strings.Builder, line, command string) {
	if line == "" {
		return
	}
	fmt.Fprintf(b, "- Lint: %s · %s\n", line, command)
}
