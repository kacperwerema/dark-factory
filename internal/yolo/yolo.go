// Package yolo is YOLO mode: for a window the owner chose, require_human_approval
// gates pass without the owner, each pass is marked on the issue, and the owner
// reviews them after the fact. State lives in the main checkout under .yolo/,
// one file per project, next to .ci/. Both the viewer and issue-cli read it;
// an expired window or a tripped valve reads as off without anyone writing.
package yolo

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ci"
)

// DefaultCIRedMinutes is the CI valve: YOLO turns itself off when the
// integration branch has been red for longer than this.
const DefaultCIRedMinutes = 30

// MorningHour is the end of "until morning", the default window.
const MorningHour = 8

// Valves are the safety valves chosen when YOLO was turned on.
type Valves struct {
	// CIRedMinutes turns YOLO off when CI has been red longer; 0 = no CI valve.
	CIRedMinutes int `json:"ci_red_minutes"`
	// Releases unlocks `release publish` while YOLO is on.
	Releases bool `json:"releases"`
}

// State is .yolo/<project>.json: the current (or last) window.
type State struct {
	On      bool      `json:"on"`
	Since   time.Time `json:"since"`
	Until   time.Time `json:"until,omitempty"`
	Forever bool      `json:"forever,omitempty"`
	By      string    `json:"by"`
	Words   string    `json:"words"`
	How     string    `json:"how"`
	Valves  Valves    `json:"valves"`
	// Ended / EndReason are set when the window closed (expired, valve, owner).
	Ended     time.Time `json:"ended,omitempty"`
	EndReason string    `json:"end_reason,omitempty"`
}

// Dir is root's .yolo directory.
func Dir(root string) string { return filepath.Join(root, ".yolo") }

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
		if err := os.WriteFile(ignore, []byte("# YOLO mode state (issue-cli yolo); never committed\n*\n"), 0o644); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(Dir(root), project+".*.tmp")
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
	return os.Rename(tmp.Name(), Path(root, project))
}

// Check is whether YOLO is on right now and, when a written-on state no
// longer holds, why it is off (the reason TurnOff should record).
func Check(st *State, now time.Time, ciState *ci.State) (on bool, offReason string) {
	if st == nil || !st.On {
		return false, ""
	}
	if !st.Forever && !st.Until.IsZero() && !now.Before(st.Until) {
		return false, "expired"
	}
	if red, ok := ciRedFor(ciState, now); ok && st.Valves.CIRedMinutes > 0 && red > time.Duration(st.Valves.CIRedMinutes)*time.Minute {
		return false, fmt.Sprintf("valve: CI red %d min", int(red.Minutes()))
	}
	return true, ""
}

// ciRedFor is how long CI has been red; ok is false when it is not red.
// States written before red_at existed fall back to the last update.
func ciRedFor(st *ci.State, now time.Time) (time.Duration, bool) {
	if !st.IsRed() {
		return 0, false
	}
	at := st.RedAt
	if at.IsZero() {
		at = st.Updated
	}
	if at.IsZero() {
		return 0, false
	}
	return now.Sub(at), true
}

// Active loads the project's state and the CI state and reports whether
// YOLO is on. A state that reads as off but is still written on is closed
// with its reason, so pages and the log say why it ended.
func Active(root, project string, now time.Time) (*State, bool, error) {
	st, err := Load(root, project)
	if err != nil || !st.On {
		return st, false, err
	}
	ciState, _ := ci.ReadState(root)
	on, reason := Check(st, now, ciState)
	if on {
		return st, true, nil
	}
	if err := TurnOff(root, project, st, now, reason); err != nil {
		return st, false, err
	}
	return st, false, nil
}

// TurnOn opens a window. It refuses while one is already on.
func TurnOn(root, project string, next State, now time.Time) error {
	cur, err := Load(root, project)
	if err != nil {
		return err
	}
	if cur.On {
		if on, _ := Check(cur, now, nil); on {
			return errors.New("YOLO is already on")
		}
	}
	if !next.Forever && !next.Until.After(now) {
		return errors.New("YOLO needs an end in the future (or until turned off)")
	}
	if strings.TrimSpace(next.Words) == "" {
		return errors.New("turning YOLO on needs the owner's words")
	}
	next.On, next.Since, next.Ended, next.EndReason = true, now, time.Time{}, ""
	return Save(root, project, &next)
}

// Extend moves the end of the window that is on.
func Extend(root, project string, until time.Time, forever bool, now time.Time) (*State, error) {
	st, on, err := Active(root, project, now)
	if err != nil {
		return nil, err
	}
	if !on {
		return nil, errors.New("YOLO is off: turn it on instead")
	}
	if !forever && !until.After(now) {
		return nil, errors.New("the new end is not in the future")
	}
	st.Until, st.Forever = until, forever
	if forever {
		st.Until = time.Time{}
	}
	return st, Save(root, project, st)
}

// TurnOff closes the window with reason and appends it to the log. Off
// stays off: closing an already closed window is a no-op.
func TurnOff(root, project string, st *State, now time.Time, reason string) error {
	if st == nil || !st.On {
		return nil
	}
	st.On, st.Ended, st.EndReason = false, now, reason
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

// ParseUntil reads a window end: "08:00" (the next such time), "4h" (a Go
// duration), "morning" (the next 08:00) or "off" (until turned off).
func ParseUntil(s string, now time.Time) (until time.Time, forever bool, err error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		return time.Time{}, false, errors.New("YOLO always has an end: --until 08:00, 4h, morning or off (until turned off)")
	case "off", "until-off", "forever":
		return time.Time{}, true, nil
	case "morning":
		s = fmt.Sprintf("%02d:00", MorningHour)
	}
	if d, derr := time.ParseDuration(s); derr == nil {
		if d <= 0 {
			return time.Time{}, false, fmt.Errorf("--until %s is not in the future", s)
		}
		return now.Add(d), false, nil
	}
	hh, mm, ok := strings.Cut(s, ":")
	h, herr := strconv.Atoi(hh)
	m, merr := strconv.Atoi(mm)
	if !ok || herr != nil || merr != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, false, fmt.Errorf("--until %q: use 08:00, 4h, morning or off", s)
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, false, nil
}

// EndLabel is how pages and the CLI name the end: "08:00", "Thu 08:00", or
// "until turned off".
func (s *State) EndLabel(now time.Time) string {
	if s.Forever || s.Until.IsZero() {
		return "until turned off"
	}
	if s.Until.Sub(now) < 24*time.Hour {
		return s.Until.Format("15:04")
	}
	return s.Until.Format("Mon 15:04")
}
