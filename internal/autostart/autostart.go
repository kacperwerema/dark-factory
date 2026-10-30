// Package autostart is the owner's standing start: for a project where the
// owner turned it on with their words, the lead may start one more worker
// while memory is low, picked from the project's own next order. State lives
// in the main checkout under .autostart/, one file per project, next to
// .yolo/. The start budget is shared by every project on the machine: a lock
// and the last start under the user state dir, so leads ticking in the same
// minute start one worker between them. Nothing here runs on a timer: the
// lead asks (Decide) and starts (issue-cli dispatch --autostart).
package autostart

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Defaults for a window turned on without limits.
const (
	DefaultBelow      = 50 // start only while memory used is under this %
	DefaultMaxWorkers = 2  // live workers per project (the ~/CLAUDE.md rule)
	DefaultGapMinutes = 5  // minutes between two starts, machine-wide
)

// State is .autostart/<project>.json: the current (or last) window.
type State struct {
	On          bool      `json:"on"`
	Paused      bool      `json:"paused,omitempty"`
	PausedAt    time.Time `json:"paused_at,omitempty"`
	PauseReason string    `json:"pause_reason,omitempty"`
	Since       time.Time `json:"since"`
	By          string    `json:"by"`
	Words       string    `json:"words"`
	How         string    `json:"how"`
	Below       int       `json:"below"`
	MaxWorkers  int       `json:"max_workers"`
	GapMinutes  int       `json:"gap_minutes"`
	// Ended / EndReason are set when the window closed.
	Ended     time.Time `json:"ended,omitempty"`
	EndReason string    `json:"end_reason,omitempty"`
}

// Gap is the time between two starts.
func (s *State) Gap() time.Duration { return time.Duration(s.GapMinutes) * time.Minute }

// Dir is root's .autostart directory.
func Dir(root string) string { return filepath.Join(root, ".autostart") }

// Path is the state file of project under root.
func Path(root, project string) string { return filepath.Join(Dir(root), project+".json") }

// LogPath is the project's closed windows, one JSON line each.
func LogPath(root, project string) string { return filepath.Join(Dir(root), project+".log.jsonl") }

// Load reads the project's state; a missing file is a zero (off) state.
func Load(root, project string) (*State, error) {
	data, err := os.ReadFile(Path(root, project))
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(root, project), err)
	}
	return &st, nil
}

// Save writes st atomically.
func Save(root, project string, st *State) error {
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(Dir(root), ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte("# auto-start state (issue-cli autostart); never committed\n*\n"), 0o644); err != nil {
			return err
		}
	}
	return writeJSONFile(Dir(root), Path(root, project), st)
}

func writeJSONFile(dir, path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// TurnOn opens a window with the owner's words. Zero limits take the
// defaults. It refuses while one is already on.
func TurnOn(root, project string, next State, now time.Time) error {
	cur, err := Load(root, project)
	if err != nil {
		return err
	}
	if cur.On {
		return errors.New("auto-start is already on")
	}
	if strings.TrimSpace(next.Words) == "" {
		return errors.New("turning auto-start on needs the owner's words")
	}
	if next.Below <= 0 || next.Below > 100 {
		next.Below = DefaultBelow
	}
	if next.MaxWorkers <= 0 {
		next.MaxWorkers = DefaultMaxWorkers
	}
	if next.GapMinutes <= 0 {
		next.GapMinutes = DefaultGapMinutes
	}
	next.On, next.Since = true, now
	next.Paused, next.PausedAt, next.PauseReason = false, time.Time{}, ""
	next.Ended, next.EndReason = time.Time{}, ""
	return Save(root, project, &next)
}

// Pause holds starts without closing the window.
func Pause(root, project, reason string, now time.Time) (*State, error) {
	st, err := Load(root, project)
	if err != nil {
		return nil, err
	}
	if !st.On {
		return st, errors.New("auto-start is off")
	}
	st.Paused, st.PausedAt, st.PauseReason = true, now, reason
	return st, Save(root, project, st)
}

// Resume lifts a pause.
func Resume(root, project string) (*State, error) {
	st, err := Load(root, project)
	if err != nil {
		return nil, err
	}
	if !st.On {
		return st, errors.New("auto-start is off: turn it on instead")
	}
	st.Paused, st.PausedAt, st.PauseReason = false, time.Time{}, ""
	return st, Save(root, project, st)
}

// TurnOff closes the window with reason and appends it to the log. Off
// stays off: closing an already closed window is a no-op.
func TurnOff(root, project string, st *State, now time.Time, reason string) error {
	if st == nil || !st.On {
		return nil
	}
	st.On, st.Paused, st.Ended, st.EndReason = false, false, now, reason
	if err := Save(root, project, st); err != nil {
		return err
	}
	f, err := os.OpenFile(LogPath(root, project), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, _ := json.Marshal(st)
	_, err = f.Write(append(line, '\n'))
	return err
}

// History is the project's closed windows, oldest first.
func History(root, project string) []State {
	f, err := os.Open(LogPath(root, project))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []State
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var st State
		if json.Unmarshal(sc.Bytes(), &st) == nil {
			out = append(out, st)
		}
	}
	return out
}
