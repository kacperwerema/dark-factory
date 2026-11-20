// Package diskuse measures the disk the factory fills outside a project's
// checkout: Go's build cache, where every worktree's builds and tests leave
// their own entries (a build keys them by directory), and the agent
// transcripts (Claude Code's projects, Codex's sessions). On 2026-11-16 the
// cache held 159 GB while the storage family's checkout total said 4.3 GB.
//
// A walk of the cache is about a million files, so a reading is kept in a
// small state file and reused for an hour (Recent): the storage health
// family takes it, Needs you only reads the file and one statfs of the
// cache's volume (Check). The nightly clean (issue-cli clean,
// internal/clean) trims the cache; nothing here deletes.
package diskuse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// Command is the owner's fix: Go rebuilds what it needs afterwards.
const Command = "go clean -cache"

// The lines: the volume over VolumeLimitPct used, or the cache over
// GoCacheLimitMB.
const (
	VolumeLimitPct = 85.0
	GoCacheLimitMB = 100 * 1024
)

// FreshFor is how long a reading is reused before Recent walks again;
// StaleAfter is how old the newest reading may be for Check to trust its
// cache size (the volume is always read live).
const (
	FreshFor   = time.Hour
	StaleAfter = 72 * time.Hour
)

// Keep is how many readings the state file holds.
const Keep = 14

// Reading is one walk.
type Reading struct {
	At         time.Time `json:"at"`
	GoCacheDir string    `json:"gocache_dir,omitempty"`
	// GoCacheMB is the cache's size; HasGoCache is false without a cache
	// folder (GOCACHE=off, never built).
	GoCacheMB  float64 `json:"gocache_mb"`
	HasGoCache bool    `json:"has_gocache"`
	// GoCacheNewMB is the MB of cache files created in the 24 hours before
	// At; HasNew is false where files keep no birth time (Linux).
	GoCacheNewMB float64 `json:"gocache_new_mb"`
	HasNew       bool    `json:"has_new"`
	// TranscriptsMB is the agent transcripts' size (TranscriptDirs).
	TranscriptsMB  float64 `json:"transcripts_mb"`
	HasTranscripts bool    `json:"has_transcripts"`
	// Volume is the cache's volume at At.
	Volume Volume `json:"volume"`
}

// Volume is a file system's use, as df counts it.
type Volume struct {
	UsedPct float64 `json:"used_pct"`
	FreeGB  float64 `json:"free_gb"`
	OK      bool    `json:"ok"`
}

// GoCacheDir is Go's build cache folder by Go's own rule: $GOCACHE, else
// the user cache dir's go-build. "" for GOCACHE=off or no cache dir.
func GoCacheDir() string {
	if d := strings.TrimSpace(os.Getenv("GOCACHE")); d != "" {
		if d == "off" {
			return ""
		}
		return d
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "go-build")
}

// TranscriptDirs are the agent transcripts' folders: Claude Code's
// projects ($CLAUDE_CONFIG_DIR, else ~/.claude) and Codex's sessions
// ($CODEX_HOME, else ~/.codex).
func TranscriptDirs() []string {
	home, _ := os.UserHomeDir()
	dir := func(env, def, sub string) string {
		if d := strings.TrimSpace(os.Getenv(env)); d != "" {
			return filepath.Join(d, sub)
		}
		if home == "" {
			return ""
		}
		return filepath.Join(home, def, sub)
	}
	var out []string
	for _, d := range []string{dir("CLAUDE_CONFIG_DIR", ".claude", "projects"), dir("CODEX_HOME", ".codex", "sessions")} {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// Live is false in a test binary and in a playtest scenario
// (viewerstate.MachineOffEnv): Recent and Current then read nothing of the
// machine (a walk of the real cache takes seconds, and a full volume would
// put a row in every Needs you count a test or a scenario takes). A test of
// this package's readings sets it.
var Live = LiveIn(testing.Testing(), os.Getenv)

// LiveIn is Live for a process: off in a test binary or where getenv sets
// viewerstate.MachineOffEnv.
func LiveIn(testBinary bool, getenv func(string) string) bool {
	return !testBinary && !viewerstate.MachineOff(getenv)
}

// ErrOff: Live is false.
var ErrOff = errors.New("diskuse: not measured in a test binary or a scenario")

// VolumeOf reads the volume that holds path (statfs). A var for tests.
var VolumeOf = volumeOf

// walkWorkers bounds the walk's readers (as the storage family's walk).
var walkWorkers = 8

// Measure walks the cache and the transcripts now.
func Measure(ctx context.Context, now time.Time) (Reading, error) {
	r := Reading{At: now}
	if dir := GoCacheDir(); dir != "" {
		r.GoCacheDir = dir
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			since := now.Add(-24 * time.Hour)
			total, fresh, hasBirth, err := walk(ctx, dir, since)
			if err != nil {
				return r, err
			}
			r.HasGoCache, r.GoCacheMB = true, mb(total)
			r.HasNew, r.GoCacheNewMB = hasBirth, mb(fresh)
			r.Volume = VolumeOf(dir)
		}
	}
	var transcripts int64
	for _, d := range TranscriptDirs() {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		total, _, _, err := walk(ctx, d, time.Time{})
		if err != nil {
			return r, err
		}
		r.HasTranscripts, transcripts = true, transcripts+total
	}
	r.TranscriptsMB = mb(transcripts)
	if !r.Volume.OK {
		if home, err := os.UserHomeDir(); err == nil {
			r.Volume = VolumeOf(home)
		}
	}
	return r, nil
}

func mb(b int64) float64 { return float64(int64(float64(b)/(1<<20)*10+0.5)) / 10 }

// walk sums the sizes of every non-directory under root (Lstat sizes,
// symlinks not followed) and of those born at or after since. hasBirth is
// false where the platform keeps no birth time. Unreadable entries are left
// out; a cancelled ctx stops it.
func walk(ctx context.Context, root string, since time.Time) (total, fresh int64, hasBirth bool, err error) {
	hasBirth = birthKnown
	type sums struct{ total, fresh int64 }
	work := make(chan string, walkWorkers)
	var pending, done sync.WaitGroup
	all := make([]sums, walkWorkers)
	var read func(s *sums, dir string)
	read = func(s *sums, dir string) {
		if ctx.Err() != nil {
			return
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() {
				pending.Add(1)
				select {
				case work <- p:
				default:
					read(s, p)
					pending.Done()
				}
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			s.total += info.Size()
			if !since.IsZero() {
				if b, ok := birth(info); ok && !b.Before(since) {
					s.fresh += info.Size()
				}
			}
		}
	}
	for i := range walkWorkers {
		done.Add(1)
		go func(s *sums) {
			defer done.Done()
			for d := range work {
				read(s, d)
				pending.Done()
			}
		}(&all[i])
	}
	pending.Add(1)
	work <- root
	pending.Wait()
	close(work)
	done.Wait()
	for _, s := range all {
		total += s.total
		fresh += s.fresh
	}
	return total, fresh, hasBirth, ctx.Err()
}

// Path is the readings file under the state dir, "" without one.
func Path() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "machine", "disk.json")
}

type file struct {
	Readings []Reading `json:"readings"`
}

// Load reads the readings, oldest first; a missing or bad file is none.
func Load(path string) []Reading {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f file
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	return f.Readings
}

// Append adds r to the file at path and keeps the last Keep readings.
func Append(path string, r Reading) error {
	if path == "" {
		return errors.New("no state dir")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rs := append(Load(path), r)
	if len(rs) > Keep {
		rs = rs[len(rs)-Keep:]
	}
	return jsonfile.Write(path, file{Readings: rs}, 0o644)
}

// Newest is the file's newest reading not after now (none while Live is
// false).
func Newest(now time.Time) (Reading, bool) {
	if !Live {
		return Reading{}, false
	}
	rs := Load(Path())
	for i := len(rs) - 1; i >= 0; i-- {
		if !rs[i].At.After(now) {
			return rs[i], true
		}
	}
	return Reading{}, false
}

// walking holds the one walk running in this process.
var walking = make(chan struct{}, 1)

var refreshing atomic.Bool

// RefreshInBackground starts Recent on its own goroutine, without a
// deadline, unless one is still running: the viewer's health tick calls it,
// so the readings stay under an hour old while a snapshot only reads them.
func RefreshInBackground(now time.Time, logf func(format string, args ...any)) {
	if !Live || !refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer refreshing.Store(false)
		if _, err := Recent(context.Background(), now); err != nil && logf != nil {
			logf("disk: %v", err)
		}
	}()
}

// Recent is the newest reading under FreshFor old, else a walk now that is
// appended to the file: one walk an hour across projects and CI runs. A
// failed write still returns the reading.
func Recent(ctx context.Context, now time.Time) (Reading, error) {
	if !Live {
		return Reading{}, ErrOff
	}
	// One walk at a time; a caller whose ctx ends while another walks gives
	// up, and one that waited reads what the other wrote.
	select {
	case walking <- struct{}{}:
	case <-ctx.Done():
		return Reading{}, ctx.Err()
	}
	defer func() { <-walking }()
	if r, ok := Newest(now); ok && now.Sub(r.At) < FreshFor {
		return r, nil
	}
	r, err := Measure(ctx, now)
	if err != nil {
		return r, err
	}
	_ = Append(Path(), r)
	return r, nil
}

// Verdict is what the newest reading and the live volume say.
type Verdict struct {
	Over bool
	// Line is the state in words, Why the line it crossed.
	Line, Why string
	At        time.Time
}

// Check reads the verdict: the volume (live, vol) over VolumeLimitPct
// used, or the reading's cache over GoCacheLimitMB. A reading older than
// StaleAfter counts for nothing; the volume still does.
func Check(r Reading, has bool, vol Volume, now time.Time) Verdict {
	if has && now.Sub(r.At) > StaleAfter {
		has = false
	}
	v := Verdict{At: now}
	var parts, why []string
	if has && r.HasGoCache {
		parts = append(parts, "the Go build cache holds "+gb(r.GoCacheMB))
		v.At = r.At
		if r.GoCacheMB > GoCacheLimitMB {
			why = append(why, "the cache is over "+gb(GoCacheLimitMB))
		}
	}
	if vol.OK {
		parts = append(parts, fmt.Sprintf("the data volume is %.0f%% used (%.0f GB free)", vol.UsedPct, vol.FreeGB))
		if vol.UsedPct > VolumeLimitPct {
			why = append(why, fmt.Sprintf("the volume is over %.0f%% used", VolumeLimitPct))
		}
	}
	v.Line = strings.Join(parts, ", ")
	if len(why) > 0 {
		v.Over, v.Why = true, strings.Join(why, " and ")
	}
	return v
}

// Current is the verdict from the readings file and the cache's volume now:
// what pages show. No walk.
func Current(now time.Time) Verdict {
	if !Live {
		return Verdict{}
	}
	r, has := Newest(now)
	dir := r.GoCacheDir
	if dir == "" {
		dir = GoCacheDir()
	}
	var vol Volume
	if dir != "" {
		vol = VolumeOf(dir)
	}
	if !vol.OK {
		if home, err := os.UserHomeDir(); err == nil {
			vol = VolumeOf(home)
		}
	}
	return Check(r, has, vol, now)
}

// CleanCommand is the nightly clean (cmd/issue-cli clean, internal/clean),
// which trims the cache to its cap.
const CleanCommand = "issue-cli clean"

// Advice is the sentence that tells the owner what to do.
func Advice() string {
	return "The nightly clean (" + CleanCommand + ") trims the cache to its cap while no local CI run or go build is running, and starts early while the volume is over 85% used; " +
		CleanCommand + " --dry-run shows what it would free. By hand, while no worker is running: " + Command + " (Go rebuilds what it needs)."
}

// gb is a size given in MB, in GB ("159 GB", "1.4 GB").
func gb(mb float64) string {
	g := mb / 1024
	if g >= 10 {
		return fmt.Sprintf("%.0f GB", g)
	}
	return fmt.Sprintf("%.1f GB", g)
}
