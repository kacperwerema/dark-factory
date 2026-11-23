package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memguard"
)

// defaultJobs is how many sessions ux-test runs side by side unless -jobs
// says otherwise (make ux-test JOBS=). Every step already stages its own
// scenario (temp root, HOME/XDG, tmux socket, port) and browser context, so
// sessions share nothing but the built binaries; each worker has its own
// Chrome. factory/ux-test-all1-runs-its-200-sessions-in-parallel-980-s-to-6-min-or-less#D-2.
const defaultJobs = 3

// memCheck is `mem-guard check` (found, blocked, message); a test swaps it.
var memCheck = memguard.Check

// uxWorkers is how many workers a ux-test run starts and the line that says
// why: -headed runs one (the owner watches one window), a machine mem-guard
// calls BLOCKED runs one, else jobs (at least 1).
func uxWorkers(jobs int, headed bool) (int, string) {
	n := max(jobs, 1)
	if headed && n > 1 {
		return 1, "ux-test: 1 worker (-headed shows one window)"
	}
	if n == 1 {
		return 1, ""
	}
	found, blocked, msg := memCheck()
	switch {
	case blocked:
		return 1, fmt.Sprintf("ux-test: 1 worker, not %d (mem-guard: %s)", n, firstLine(msg))
	case found:
		return n, fmt.Sprintf("ux-test: %d workers (mem-guard: %s)", n, firstLine(msg))
	}
	return n, fmt.Sprintf("ux-test: %d workers", n)
}

// memBlocked: mem-guard says the machine is too full for more work. Workers
// after the first ask before each new session and stop when it is.
func memBlocked() bool {
	_, blocked, _ := memCheck()
	return blocked
}

// uxSummary is the end line's "200 sessions, 3 workers, 6m13s".
func uxSummary(sessions, workers int, took time.Duration) string {
	return fmt.Sprintf("%s, %s, %s", plural(sessions, "session"), plural(workers, "worker"), took)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// runPool runs work(worker, i) for every i in [0, items) on n workers.
// Worker 0 always carries on, so every item runs; a later worker asks more
// before it takes an item and stops for good on false (a full machine).
// stop, once it returns true, hands out no new item. runPool returns when
// every worker has finished.
func runPool(n, items int, more func() bool, stop func() bool, work func(worker, i int)) {
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := range max(min(n, items), 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if stop != nil && stop() {
					return
				}
				if w > 0 && more != nil && !more() {
					return
				}
				i := int(next.Add(1)) - 1
				if i >= items {
					return
				}
				work(w, i)
			}
		}()
	}
	wg.Wait()
}

// handedPorts is every port freePort gave out in this process: the OS can
// hand the same free port to two workers between freePort's close and the
// board's bind, so a port is never given twice in one run.
var handedPorts = struct {
	sync.Mutex
	m map[int]bool
}{m: map[int]bool{}}

// claimPort records port as given out; false when it already was.
func claimPort(port int) bool {
	handedPorts.Lock()
	defer handedPorts.Unlock()
	if handedPorts.m[port] {
		return false
	}
	handedPorts.m[port] = true
	return true
}
