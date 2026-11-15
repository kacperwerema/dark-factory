package retro

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A retro sweep reads the retros written since the last one and moves them
// to retros/archive/. The sweep's mark is the newest Until in the archive's
// log, so a sweep that dies before it archives moved nothing and marked
// nothing: the next one reads the same files.

// ArchiveDir is the folder under retros/ swept files move to.
const ArchiveDir = "archive"

// Sweep is one line of the archive's log: one `issue-cli retro archive`.
type Sweep struct {
	At    time.Time `json:"at"`
	By    string    `json:"by"`
	Since time.Time `json:"since,omitzero"`
	Until time.Time `json:"until"`
	Moved []string  `json:"moved"`
	// Waiting are post-mortems left in place: the owner hasn't answered
	// their actions, and they stay in the next sweep's sight until then.
	Waiting []string `json:"waiting,omitempty"`
	// Clashes are files left in place: archive/ has one of that name.
	Clashes []string `json:"clashes,omitempty"`
}

// LogPath is the archive's log under dir, the retros folder.
func LogPath(dir string) string {
	return filepath.Join(dir, ArchiveDir, "log.jsonl")
}

// LastSweep is the log's mark: the newest Until, whatever order the lines
// are in (an archive of an older window doesn't move it back). ok is false
// with no sweep logged. A line that doesn't parse is skipped: the worst it
// can do is show a sweep files it has read already.
func LastSweep(dir string) (time.Time, bool, error) {
	f, err := os.Open(LogPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	defer f.Close()
	var mark time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // a line names every file moved
	for sc.Scan() {
		var s Sweep
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Until.After(mark) {
			mark = s.Until
		}
	}
	if err := sc.Err(); err != nil {
		return time.Time{}, false, fmt.Errorf("reading %s: %w", LogPath(dir), err)
	}
	return mark, !mark.IsZero(), nil
}

// AfterMark is the first time a sweep after mark reads. Retro dates have
// whole seconds, and a window holds both its ends.
func AfterMark(mark time.Time) time.Time { return mark.Add(time.Second) }

// Window picks the retros a sweep reads, both ends included; a zero end is
// open.
type Window struct {
	Since, Until time.Time
}

func (w Window) holds(t time.Time) bool {
	return !t.Before(w.Since) && (w.Until.IsZero() || !t.After(w.Until))
}

// Select are the retros of entries directly in dir (nothing swept already)
// inside w, oldest first.
func Select(entries []*Entry, dir string, w Window) []*Entry {
	var out []*Entry
	for _, e := range entries {
		if filepath.Dir(e.FilePath) == filepath.Clean(dir) && w.holds(e.Time()) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time().Before(out[j].Time()) })
	return out
}

// Archive moves picked to dir/archive/ and logs the sweep s with what
// happened to each. A file keep names stays, as does one whose name is
// taken in the archive: a file is never overwritten. Files moved before a
// failure are still logged, and the failure is returned.
func Archive(dir string, picked []*Entry, keep func(*Entry) bool, s Sweep) (Sweep, error) {
	to := filepath.Join(dir, ArchiveDir)
	if err := os.MkdirAll(to, 0o755); err != nil {
		return s, err
	}
	s.Moved = []string{}
	var failed []error
	for _, e := range picked {
		dest := filepath.Join(to, e.FileName)
		switch _, err := os.Lstat(dest); {
		case keep != nil && keep(e):
			s.Waiting = append(s.Waiting, e.FileName)
		case err == nil:
			s.Clashes = append(s.Clashes, e.FileName)
		default:
			if err := os.Rename(e.FilePath, dest); err != nil {
				failed = append(failed, err)
				continue
			}
			s.Moved = append(s.Moved, e.FileName)
		}
	}
	if err := appendLog(dir, s); err != nil {
		failed = append(failed, fmt.Errorf("moved %d file(s), but the log line was not written: %w", len(s.Moved), err))
	}
	return s, errors.Join(failed...)
}

func appendLog(dir string, s Sweep) error {
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(LogPath(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
