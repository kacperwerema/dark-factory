package autostart

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// Start is the last auto-start on this machine, in any project.
type Start struct {
	Project string    `json:"project"`
	Slug    string    `json:"slug"`
	At      time.Time `json:"at"`
	Memory  int       `json:"memory"`
	By      string    `json:"by,omitempty"`
}

// SharedDir is where every project's leads share the start budget:
// $XDG_STATE_HOME/ai-workflow-engine/autostart. "" without a home directory.
func SharedDir() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "autostart")
}

func lastPath(dir string) string { return filepath.Join(dir, "last.json") }

// LastStart reads the last machine-wide start; nil when there was none.
func LastStart(dir string) (*Start, error) {
	data, err := os.ReadFile(lastPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Start
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// LockWait is how long WithBudget waits for another project's start to
// finish before giving up.
var LockWait = 60 * time.Second

// ErrBusy is WithBudget's answer when another start held the lock too long.
var ErrBusy = errors.New("another auto-start is starting a worker right now")

// WithBudget runs fn holding the machine-wide lock, with the last start.
// When fn returns a start, it becomes the last one. Two projects calling at
// once run one after the other, so the second sees the first's start.
func WithBudget(dir string, fn func(last *Start) (*Start, error)) error {
	if dir == "" {
		return errors.New("no state directory for the auto-start budget (HOME unset)")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	deadline := time.Now().Add(LockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if time.Now().After(deadline) {
			return ErrBusy
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	last, err := LastStart(dir)
	if err != nil {
		return err
	}
	started, err := fn(last)
	if started != nil {
		if werr := writeJSONFile(dir, lastPath(dir), started); werr != nil && err == nil {
			err = werr
		}
	}
	return err
}
