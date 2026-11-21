package main

import (
	"fmt"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clean"
)

// leadTickCleanPath is the machine's clean state: a var for tests.
var leadTickCleanPath = clean.Path

// leadTickClean says, once per wait, a clean that has waited more than
// clean.WaitsLong: the whole clean held (mem-guard), or the gocache area
// waiting on a go build or test, with the hold that kept it. With the
// factory busy around the clock the nightly slot can slip
// (factory/clean-holds-per-area-…); the lead may free the machine or
// run issue-cli clean itself.
func leadTickClean(t *leadTicker) {
	w, ok := clean.Waiting(clean.Load(leadTickCleanPath()))
	if !ok {
		return
	}
	since := w.From()
	waited := t.now.Sub(since)
	if waited <= clean.WaitsLong {
		return
	}
	t.say("clean-waits:"+since.UTC().Format(time.RFC3339), fmt.Sprintf("clean: waits since %s (%s): %s  (issue-cli clean --dry-run shows what it would free)", since.Local().Format("2 Jan 15:04"), roundHours(waited), w.Why))
}
