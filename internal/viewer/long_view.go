package viewer

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/leaks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// What runs past its bound in the viewer (internal/leaks LongReport): the
// health watcher records the long-lived processes' RSS every 10 minutes
// (recordLongLived) and reads the report over every project (lead sessions
// past their rotation, orphan leads, processes that doubled in a day);
// Needs you gets one machine-wide row while it holds anything
// (longNeedsYou). Pages only read the last report. Nothing here rotates,
// closes or restarts anything: the row names issue-cli health, which prints
// each command.

// recordLongLived and readLongLived are vars so tests never read the real
// machine.
var (
	recordLongLived = func(now time.Time) error { return leaks.RecordMem(leaks.MemPath(), now) }
	readLongLived   = leaks.LongNow
	// longLive is false in a test binary and a playtest scenario
	// (diskuse.Live): their board reads none of the host's sessions.
	longLive = func() bool { return diskuse.Live }
)

// longLast is the last report the health watcher read.
var longLast atomic.Pointer[leaks.LongReport]

// refreshLongLived records this tick's readings and reads the report; an
// error is logged once until it changes.
func refreshLongLived(projects []tracker.Project, now time.Time, lastErr map[string]string) {
	if !longLive() {
		return
	}
	if err := recordLongLived(now); err != nil {
		if msg := err.Error(); lastErr["#rss"] != msg {
			lastErr["#rss"] = msg
			log.Printf("machine: long-lived processes: %v", err)
		}
	} else {
		delete(lastErr, "#rss")
	}
	ps := make([]*tracker.Project, len(projects))
	for i := range projects {
		ps[i] = &projects[i]
	}
	r := readLongLived(ps, now)
	longLast.Store(&r)
}

// longNow is the last report, empty before the first tick.
func longNow() leaks.LongReport {
	if r := longLast.Load(); r != nil {
		return *r
	}
	return leaks.LongReport{}
}

// longNeedsYou is the machine-wide row while a lead is past its rotation
// or a process doubled in a day.
func longNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	r := longNow()
	if r.Empty() {
		return nil
	}
	var parts, names []string
	if n := len(r.Sessions); n > 0 {
		parts = append(parts, plural(n, "lead session ran past its rotation (26 h)", "lead sessions ran past their rotation (26 h)"))
		for _, s := range r.Sessions {
			names = append(names, s.Session+" "+leaks.Age(s.Age))
		}
	}
	if n := len(r.Grown); n > 0 {
		parts = append(parts, plural(n, "long-lived process doubled its memory in a day", "long-lived processes doubled their memory in a day"))
		for _, g := range r.Grown {
			names = append(names, fmt.Sprintf("%s %.0f→%.0f MB", g.Name, g.FromMB, g.ToMB))
		}
	}
	return []WbQueueCard{{
		Kind: "sessions", Label: "Long-running", Tone: "you",
		Project: p.Name, ProjectSlug: p.Slug, Color: color,
		Slug: "issue-cli health", Title: "Sessions or processes past their bound",
		Note:  strings.Join(parts, "; ") + ": " + strings.Join(names, ", ") + ". issue-cli health prints the command for each (rotate, close or restart).",
		Since: now, CTA: "Open status", URL: prefix + "/status",
	}}
}
