package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/restrace"
)

// The runner stops what it started on every way out. A normal return runs
// each Close by defer; a signal doesn't (Go's default for SIGINT, SIGTERM
// and SIGHUP exits at once, without defers), so stopOnSignal runs the same
// hooks first. SIGKILL can run nothing: the next runner's first browser
// reaps what it left (reapOrphanChromes). Chromes left that way lived for
// days, parented by launchd, each with its profile's file watchers that
// fseventsd keeps state for.

var stopHooks struct {
	mu    sync.Mutex
	next  int
	hooks map[int]func()
}

// atStop registers f to run if the runner is stopped by a signal, and
// returns the func that forgets it again (call it when f has run anyway).
func atStop(f func()) (forget func()) {
	stopHooks.mu.Lock()
	defer stopHooks.mu.Unlock()
	if stopHooks.hooks == nil {
		stopHooks.hooks = map[int]func(){}
	}
	id := stopHooks.next
	stopHooks.next++
	stopHooks.hooks[id] = f
	return func() {
		stopHooks.mu.Lock()
		delete(stopHooks.hooks, id)
		stopHooks.mu.Unlock()
	}
}

// runStopHooks runs every registered hook once, newest first.
func runStopHooks() {
	stopHooks.mu.Lock()
	ids := make([]int, 0, len(stopHooks.hooks))
	for id := range stopHooks.hooks {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	hooks := make([]func(), 0, len(ids))
	for _, id := range ids {
		hooks = append(hooks, stopHooks.hooks[id])
	}
	stopHooks.hooks = nil
	stopHooks.mu.Unlock()
	for _, f := range hooks {
		f()
	}
}

// graceful counts the loops that handle SIGINT themselves (the owner's
// playtest saves what was judged): a first Ctrl-C leaves them
// gracefulWait to return, a second one stops at once.
var graceful atomic.Int32

const gracefulWait = 15 * time.Second

// stopOnSignal makes SIGINT, SIGTERM and SIGHUP stop the browsers (and
// scenario boards) before the runner exits with 128+signal.
func stopOnSignal() {
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		waited := false
		for s := range sig {
			if s == syscall.SIGINT && graceful.Load() > 0 && !waited {
				waited = true
				go func() {
					time.Sleep(gracefulWait)
					stopAndExit(s)
				}()
				continue
			}
			stopAndExit(s)
		}
	}()
}

func stopAndExit(s os.Signal) {
	runStopHooks()
	n := 1
	if ss, ok := s.(syscall.Signal); ok {
		n = int(ss)
	}
	os.Exit(128 + n)
}

var reapOnce sync.Once

// reapOrphanChromes stops the playtest Chromes whose runner is gone and
// removes their profiles, and the profiles of dead runners no Chrome uses.
// It runs once per runner, before its first browser starts. It returns
// what it stopped, for the log.
func reapOrphanChromes() []string {
	list, err := procs.Mine()
	if err != nil {
		return nil
	}
	var done []string
	for _, o := range procs.OrphanChromes(list) {
		if procs.Stop(o.PID, 3*time.Second) {
			restrace.Stop(restrace.KindChrome, o.PID, o.Profile)
			done = append(done, fmt.Sprintf("Chrome %d (%s)", o.PID, o.Why))
		}
		stopProfileHelpers(o.Profile)
		removeProfile(o.Profile)
	}
	if list, err = procs.Mine(); err == nil {
		for _, p := range procs.StaleProfiles(os.TempDir(), list) {
			removeProfile(p)
		}
	}
	return done
}

// stopProfileHelpers stops what is left of a reaped Chrome: its renderer,
// GPU and utility helpers run with the same --user-data-dir. They exit by
// themselves once the main process is gone, but on a loaded machine that
// takes seconds, and until then they hold the profile.
func stopProfileHelpers(profile string) {
	if profile == "" {
		return
	}
	list, err := procs.Mine()
	if err != nil {
		return
	}
	want := filepath.Clean(profile)
	for _, p := range list {
		if d := procs.UserDataDir(p.Command); d != "" && filepath.Clean(d) == want {
			procs.Stop(p.PID, time.Second)
		}
	}
}

// removeProfile removes a Chrome profile folder, only one in the temp
// folder with a playtest or chromedp name.
func removeProfile(dir string) {
	base := filepath.Base(dir)
	if dir == "" || !procs.TempPlace(dir) || (!strings.HasPrefix(base, procs.PlaytestProfilePrefix) && !strings.HasPrefix(base, procs.ChromedpProfilePrefix)) {
		return
	}
	_ = os.RemoveAll(dir)
}
