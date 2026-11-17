// Package recent keeps a reading of the machine (tmux's sessions, mem-guard,
// the load) for a moment, so one page that asks several times, or several
// projects' parts of a page at once, spawn one process.
//
// It is off until Keep is called: a test always reads afresh. The viewer
// turns it on (a scenario viewer doesn't, OffEnv), and so does a short
// issue-cli command that reads the machine several times in one run (queue
// next, through agentrun.KeepReadings). Forget drops every kept reading at
// once: a decision taken under the start lock reads afresh.
package recent

import (
	"sync"
	"sync/atomic"
	"time"
)

// OffEnv set to "1" keeps a viewer from turning readings on. A scenario sets
// it: a playtest step starts a tmux session and opens the page at once, which
// a reading kept from the page before would miss.
const OffEnv = "ISSUE_VIEWER_READ_AFRESH"

var (
	window atomic.Int64
	epoch  atomic.Int64
)

// Keep sets how long every Reading in this process stands; 0 turns them off.
func Keep(d time.Duration) { window.Store(int64(d)) }

// Window is how long readings stand now (what Keep set last).
func Window() time.Duration { return time.Duration(window.Load()) }

// Forget drops every reading kept so far: the next Get reads afresh.
func Forget() { epoch.Add(1) }

// Reading is one value read from the machine. Callers that ask while a read
// runs wait for it and share its answer.
type Reading[T any] struct {
	mu    sync.Mutex
	at    time.Time
	epoch int64
	value T
}

// Get is the value read at most Keep ago, else what read returns now.
func (r *Reading[T]) Get(read func() T) T {
	keep := time.Duration(window.Load())
	if keep <= 0 {
		return read()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := epoch.Load()
	if !r.at.IsZero() && r.epoch == now && time.Since(r.at) < keep {
		return r.value
	}
	r.value = read()
	r.at, r.epoch = time.Now(), now
	return r.value
}

// Readings is a Reading per key: one folder's listing, say, kept for the
// same moment as every other reading.
type Readings[K comparable, T any] struct {
	mu sync.Mutex
	m  map[K]*Reading[T]
}

// maxReadings bounds a Readings; past it, it starts over.
const maxReadings = 4096

// Get is key's value read at most Keep ago, else what read returns now.
func (rs *Readings[K, T]) Get(key K, read func() T) T {
	if time.Duration(window.Load()) <= 0 {
		return read()
	}
	rs.mu.Lock()
	r, ok := rs.m[key]
	if !ok {
		if rs.m == nil || len(rs.m) >= maxReadings {
			rs.m = map[K]*Reading[T]{}
		}
		r = &Reading[T]{}
		rs.m[key] = r
	}
	rs.mu.Unlock()
	return r.Get(read)
}
