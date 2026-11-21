package viewer

import (
	"errors"
	"log"
	"time"

	"github.com/kacperwerema/dark-factory/internal/fseventsd"
	"github.com/kacperwerema/dark-factory/internal/restrace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The machine in the viewer: a runaway fseventsd (internal/fseventsd). The
// health watcher records a reading every 10 minutes (recordMachine); Needs
// you gets one row while it is over a line (machineNeedsYou), listed once
// across projects like unsent bug reports, and status.md a Machine: line
// (machineStatusLine). Pages only read the readings file. The fix is the
// owner's command; nothing here runs it.

// machineVerdict is the readings file's verdict: a var so tests never read
// the real machine.
var machineVerdict = fseventsd.Current

// machineWideKinds are the Needs you kinds that belong to the machine, not a
// project: listed once across projects and counted once in the total.
var machineWideKinds = map[string]bool{"bugs": true, "machine": true, "disk": true, "sessions": true}

// machineWideRows is how many machine-wide rows each project's list holds.
func machineWideRows(now time.Time) int {
	n := 0
	if unsentBugReports() > 0 {
		n++
	}
	if machineVerdict(now).Over {
		n++
	}
	if diskVerdict(now).Over {
		n++
	}
	if !longNow().Empty() {
		n++
	}
	return n
}

// machineNeedsYou is the fseventsd row while it is over a line.
func machineNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	v := machineVerdict(now)
	if !v.Over {
		return nil
	}
	return []WbQueueCard{{
		Kind: "machine", Label: "fseventsd", Tone: "red",
		Project: p.Name, ProjectSlug: p.Slug, Color: color,
		Slug: fseventsd.Command, Title: "fseventsd is running away",
		Note:  v.Line + ": " + v.Why + ". It counts toward mem-guard's reading and holds worker starts. " + fseventsd.Advice(),
		Since: v.Latest.At, Age: shortDuration(now.Sub(v.Latest.At)),
		CTA: "Open status", URL: prefix + "/status",
	}}
}

// machineStatusLine is status.md's Machine: line, "" while nothing is over.
func machineStatusLine(now time.Time) string {
	v := machineVerdict(now)
	if !v.Over {
		return ""
	}
	return "Machine: " + v.Line + " (" + v.Why + "). Restart it: " + fseventsd.Command
}

// recordMachine takes this tick's fseventsd reading; an error is logged
// once until it changes. No fseventsd (not macOS) is quiet.
func recordMachine(now time.Time, lastErr map[string]string) {
	r, err := fseventsd.Record(now)
	if errors.Is(err, fseventsd.ErrNone) {
		return
	}
	if err != nil {
		if msg := err.Error(); lastErr["#fseventsd"] != msg {
			lastErr["#fseventsd"] = msg
			log.Printf("machine: fseventsd: %v", err)
		}
		return
	}
	delete(lastErr, "#fseventsd")
	// With DF_TRACE=resources (or -debug) the resource trace gets the
	// footprint beside what the factory has running, every tick.
	restrace.SampleNow(r.PID, r.FootprintMB)
	if v := fseventsd.Current(now); v.Over {
		log.Printf("machine: %s (%s); the owner's fix: %s", fseventsd.Words(r), v.Why, fseventsd.Command)
	}
}
