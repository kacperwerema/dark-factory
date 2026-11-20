package viewer

import (
	"time"

	"github.com/kacperwerema/dark-factory/internal/clean"
	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The disk in the viewer: a big Go build cache or a full data volume
// (internal/diskuse). The storage health family takes the reading (one walk
// an hour at most); Needs you gets one row while it is over a line
// (diskNeedsYou), listed once across projects like fseventsd's. Pages read
// only the readings file and one statfs. The row also says how the last
// nightly clean went (internal/clean; the health tick starts it,
// clean_watch.go).

// diskVerdict is the readings file's verdict with the volume read now: a
// var so tests never read the real machine.
var diskVerdict = diskuse.Current

// cleanStatus is the last clean's state in words, "" with none: a var so
// tests never read the machine's state file.
var cleanStatus = func() string {
	if !diskuse.Live {
		return ""
	}
	return clean.Status(clean.Load(clean.Path()))
}

// cleanNote is cleanStatus as the row's last sentence.
func cleanNote() string {
	if s := cleanStatus(); s != "" {
		return " " + upperFirst(s) + "."
	}
	return ""
}

// diskNeedsYou is the disk row while it is over a line.
func diskNeedsYou(p *tracker.Project, color, prefix string, now time.Time) []WbQueueCard {
	v := diskVerdict(now)
	if !v.Over {
		return nil
	}
	return []WbQueueCard{{
		Kind: "disk", Label: "Disk", Tone: "red",
		Project: p.Name, ProjectSlug: p.Slug, Color: color,
		Slug: diskuse.Command, Title: "The disk is filling up",
		Note:  upperFirst(v.Line) + ": " + v.Why + ". A full disk stops every build and every worker at once. " + diskuse.Advice() + cleanNote(),
		Since: v.At, Age: shortDuration(now.Sub(v.At)),
		CTA: "Open health", URL: prefix + "/health",
	}}
}
