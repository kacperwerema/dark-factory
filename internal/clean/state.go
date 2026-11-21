package clean

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// Result is one clean that ran.
type Result struct {
	At   time.Time `json:"at"`
	Done time.Time `json:"done"`
	// Why is what started it: "nightly", "the volume is 93% used", "by hand".
	Why   string `json:"why,omitempty"`
	Areas []Area `json:"areas"`
	// Before and After are the data volume's use around it.
	Before diskuse.Volume `json:"before"`
	After  diskuse.Volume `json:"after"`
}

// Freed is the bytes every area freed.
func (r Result) Freed() int64 {
	var n int64
	for _, a := range r.Areas {
		n += a.FreedBytes
	}
	return n
}

// Summary is the result in one sentence for a page.
func (r Result) Summary() string {
	s := fmt.Sprintf("the last clean (%s) freed %s", r.At.Format("2 Jan 15:04"), MB(r.Freed()))
	if r.Before.OK && r.After.OK {
		s += fmt.Sprintf(", the volume %.0f%% → %.0f%% used", r.Before.UsedPct, r.After.UsedPct)
	}
	return s
}

// Hold is a due clean (or its gocache area) that didn't run, and why: At
// is the last try, Since the first of the run of tries it belongs to.
type Hold struct {
	At    time.Time `json:"at"`
	Since time.Time `json:"since,omitempty"`
	Why   string    `json:"why"`
}

// From is when the hold began: Since, else At (a file written before Since).
func (h Hold) From() time.Time {
	if h.Since.IsZero() {
		return h.At
	}
	return h.Since
}

// File is the state file: the last results, oldest first, and the last
// hold when it is newer than the last result. The gocache area keeps its
// own: CacheWaits while it waited (a go build or test, local CI) and the
// rest ran, cleared when it runs; CacheAt is when it last ran.
type File struct {
	Results    []Result  `json:"results"`
	Held       *Hold     `json:"held,omitempty"`
	CacheWaits *Hold     `json:"cache_waits,omitempty"`
	CacheAt    time.Time `json:"cache_at,omitempty"`
}

// Keep is how many results the file holds.
const Keep = 14

// Path is the state file under the state dir, "" without one.
func Path() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "machine", "clean.json")
}

// Load reads the file at path; missing or bad is empty.
func Load(path string) File {
	var f File
	if path == "" {
		return f
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &f)
	}
	return f
}

// Last is the newest result.
func (f File) Last() (Result, bool) {
	if len(f.Results) == 0 {
		return Result{}, false
	}
	return f.Results[len(f.Results)-1], true
}

func write(path string, f File) error {
	if path == "" {
		return errors.New("no state dir")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return jsonfile.Write(path, f, 0o644)
}

// Record appends r and clears the hold. A gocache area in r that waited
// keeps (or starts) CacheWaits; one that ran clears it and sets CacheAt.
func Record(path string, r Result) error {
	f := Load(path)
	f.Results = append(f.Results, r)
	if len(f.Results) > Keep {
		f.Results = f.Results[len(f.Results)-Keep:]
	}
	f.Held = nil
	for _, a := range r.Areas {
		if a.Name != CacheArea {
			continue
		}
		if a.Waited {
			f.CacheWaits = cont(f.CacheWaits, Hold{At: r.At, Why: strings.TrimPrefix(a.Note, "waits: ")})
		} else {
			f.CacheWaits, f.CacheAt = nil, r.At
		}
	}
	return write(path, f)
}

// RecordHold keeps h as the last hold, its Since carried over from a hold
// still standing (no result since).
func RecordHold(path string, h Hold) error {
	f := Load(path)
	last, has := f.Last()
	prev := f.Held
	if prev != nil && has && !prev.At.After(last.At) {
		prev = nil
	}
	f.Held = cont(prev, h)
	return write(path, f)
}

// RecordCacheWait keeps h as the gocache area's wait: a cache-only clean
// that found a go build or test running.
func RecordCacheWait(path string, h Hold) error {
	f := Load(path)
	f.CacheWaits = cont(f.CacheWaits, h)
	return write(path, f)
}

// cont is h, its Since carried over from prev when prev stands.
func cont(prev *Hold, h Hold) *Hold {
	h.Since = h.At
	if prev != nil {
		h.Since = prev.From()
	}
	return &h
}

// EarlyAfter is how long after the last clean a full volume starts the
// next one early.
const EarlyAfter = 6 * time.Hour

// Slot is the newest nightly start at or before now: today's h:m once it
// has passed, else yesterday's.
func Slot(h, m int, now time.Time) time.Time {
	slot := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if slot.After(now) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

// CacheOver is how far over its cap the cache may grow between nights:
// past it the day's tick trims it (factory/clean-holds-per-area-…).
const CacheOver = 1.25

// Cache is the Go cache's size as the last disk reading measured it, and
// its cap. OK is false without a reading.
type Cache struct {
	At       time.Time
	Bytes    int64
	CapBytes int64
	OK       bool
}

// Plan is a due clean: why it starts, and whether only the gocache area
// runs (it waited at the last clean, or grew past CacheOver of its cap).
type Plan struct {
	Why       string
	CacheOnly bool
}

// Due says whether a clean should start now, and why: none since the
// nightly slot (h:m), or the volume over diskuse.VolumeLimitPct used and
// the last clean over EarlyAfter ago (the early start), each every area.
// Else the gocache area alone: it waited at the last clean, or the newest
// reading, taken after it last ran, has it over its cap by more than
// CacheOver.
func Due(f File, h, m int, vol diskuse.Volume, cache Cache, now time.Time) (Plan, bool) {
	last, has := f.Last()
	if vol.OK && vol.UsedPct > diskuse.VolumeLimitPct && (!has || now.Sub(last.At) >= EarlyAfter) {
		return Plan{Why: fmt.Sprintf("the volume is %.0f%% used", vol.UsedPct)}, true
	}
	if !has || last.At.Before(Slot(h, m, now)) {
		return Plan{Why: "nightly"}, true
	}
	if w := f.CacheWaits; w != nil {
		return Plan{Why: "the Go cache waits since " + w.From().Format("2 Jan 15:04"), CacheOnly: true}, true
	}
	if cache.OK && cache.CapBytes > 0 && float64(cache.Bytes) > CacheOver*float64(cache.CapBytes) && cache.At.After(f.CacheAt) {
		return Plan{Why: fmt.Sprintf("the Go cache holds %s, over its cap of %s by more than %.0f%%", MB(cache.Bytes), MB(cache.CapBytes), (CacheOver-1)*100), CacheOnly: true}, true
	}
	return Plan{}, false
}

// WaitsLong is how long a clean may wait before the lead tick says so.
const WaitsLong = 3 * time.Hour

// Waiting is the clean's standing wait: the whole clean held since the
// last result, else the gocache area waiting; ok false with neither.
func Waiting(f File) (Hold, bool) {
	last, has := f.Last()
	if f.Held != nil && (!has || f.Held.At.After(last.At)) {
		return *f.Held, true
	}
	if f.CacheWaits != nil {
		return *f.CacheWaits, true
	}
	return Hold{}, false
}

// Lock takes the clean's lock file beside path: one clean at a time on the
// machine. A lock whose pid is gone is taken over. release drops it.
func Lock(path string) (release func(), err error) {
	if path == "" {
		return nil, errors.New("no state dir")
	}
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		return nil, err
	}
	for range 2 {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if pid, ok := Running(path); ok {
			return nil, fmt.Errorf("a clean is running (pid %d)", pid)
		}
		_ = os.Remove(lock)
	}
	return nil, errors.New("cannot take the clean lock")
}

// Running is the pid of a clean holding the lock beside path, if alive.
func Running(path string) (int, bool) {
	data, err := os.ReadFile(path + ".lock")
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, viewerstate.Alive(pid)
}

// Status is the clean's state in one sentence for a page: the hold when
// it is newer than the last result, else the last result with the gocache
// area's wait; "" with neither.
func Status(f File) string {
	last, has := f.Last()
	if f.Held != nil && (!has || f.Held.At.After(last.At)) {
		s := "the clean waits since " + f.Held.From().Format("2 Jan 15:04") + ": " + f.Held.Why
		if has {
			s += "; " + last.Summary()
		}
		return s
	}
	if !has {
		return ""
	}
	s := last.Summary()
	if w := f.CacheWaits; w != nil {
		s += "; the Go cache waits since " + w.From().Format("2 Jan 15:04") + ": " + w.Why
	}
	return s
}

// CacheNow is the Go cache's size by the newest disk reading (the viewer's
// tick takes one an hour, diskuse.RefreshInBackground), with capBytes.
func CacheNow(now time.Time, capBytes int64) Cache {
	r, ok := diskuse.Newest(now)
	return Cache{At: r.At, Bytes: int64(r.GoCacheMB * (1 << 20)), CapBytes: capBytes, OK: ok && r.HasGoCache}
}
