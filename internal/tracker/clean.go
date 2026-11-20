package tracker

import (
	"fmt"
	"strings"
)

// CleanConfig is projects.yaml's clean: block, the nightly clean's
// settings (issue-cli clean, docs/workbench-files.md#the-nightly-clean).
// It is machine-wide: the clean runs once for every project in the file.
// Set by hand; the engine never writes it.
type CleanConfig struct {
	// Off turns the whole clean off.
	Off bool `yaml:"off"`
	// At is the nightly start, "HH:MM" local time (default 04:00).
	At string `yaml:"at"`
	// GoCacheCapGB is the Go build cache's cap in GB (default 40): over
	// it, the oldest entries go until it is under.
	GoCacheCapGB float64 `yaml:"gocache_cap_gb"`
	// TranscriptDays is how many calendar days of agent transcripts stay
	// (default 14).
	TranscriptDays int `yaml:"transcript_days"`
	// SnapshotDays is how many calendar days of health snapshots stay in
	// full (default 7); before that, one per day.
	SnapshotDays int `yaml:"snapshot_days"`
}

// The clean's defaults.
const (
	DefaultCleanAt             = "04:00"
	DefaultCleanGoCacheCapGB   = 40
	DefaultCleanTranscriptDays = 14
	DefaultCleanSnapshotDays   = 7
)

// Enabled is false only for clean.off.
func (c *CleanConfig) Enabled() bool { return c == nil || !c.Off }

// Clock is the nightly start's hour and minute; a bad or unset at: is
// the default.
func (c *CleanConfig) Clock() (int, int) {
	at := DefaultCleanAt
	if c != nil && strings.TrimSpace(c.At) != "" {
		at = strings.TrimSpace(c.At)
	}
	var h, m int
	if n, err := fmt.Sscanf(at, "%d:%d", &h, &m); err != nil || n != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return 4, 0
	}
	return h, m
}

// CapGB is the Go cache cap in GB.
func (c *CleanConfig) CapGB() float64 {
	if c == nil || c.GoCacheCapGB <= 0 {
		return DefaultCleanGoCacheCapGB
	}
	return c.GoCacheCapGB
}

// Transcripts is the transcript window in days.
func (c *CleanConfig) Transcripts() int {
	if c == nil || c.TranscriptDays <= 0 {
		return DefaultCleanTranscriptDays
	}
	return c.TranscriptDays
}

// Snapshots is the snapshot window in days.
func (c *CleanConfig) Snapshots() int {
	if c == nil || c.SnapshotDays <= 0 {
		return DefaultCleanSnapshotDays
	}
	return c.SnapshotDays
}
