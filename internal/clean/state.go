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

// Hold is a due clean that didn't run, and why.
type Hold struct {
	At  time.Time `json:"at"`
	Why string    `json:"why"`
}

// File is the state file: the last results, oldest first, and the last
// hold when it is newer than the last result.
type File struct {
	Results []Result `json:"results"`
	Held    *Hold    `json:"held,omitempty"`
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

// Record appends r and clears the hold.
func Record(path string, r Result) error {
	f := Load(path)
	f.Results = append(f.Results, r)
	if len(f.Results) > Keep {
		f.Results = f.Results[len(f.Results)-Keep:]
	}
	f.Held = nil
	return write(path, f)
}

// RecordHold keeps h as the last hold.
func RecordHold(path string, h Hold) error {
	f := Load(path)
	f.Held = &h
	return write(path, f)
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

// Due says whether a clean should start now, and why: none since the
// nightly slot (h:m), or the volume over diskuse.VolumeLimitPct used and
// the last clean over EarlyAfter ago (the early start).
func Due(f File, h, m int, vol diskuse.Volume, now time.Time) (bool, string) {
	last, has := f.Last()
	if vol.OK && vol.UsedPct > diskuse.VolumeLimitPct && (!has || now.Sub(last.At) >= EarlyAfter) {
		return true, fmt.Sprintf("the volume is %.0f%% used", vol.UsedPct)
	}
	if !has || last.At.Before(Slot(h, m, now)) {
		return true, "nightly"
	}
	return false, ""
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
// it is newer than the last result, else the last result; "" with
// neither.
func Status(f File) string {
	last, has := f.Last()
	if f.Held != nil && (!has || f.Held.At.After(last.At)) {
		s := "the clean waits since " + f.Held.At.Format("2 Jan 15:04") + ": " + f.Held.Why
		if has {
			s += "; " + last.Summary()
		}
		return s
	}
	if has {
		return last.Summary()
	}
	return ""
}
