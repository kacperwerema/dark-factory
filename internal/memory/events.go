package memory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
)

// Event ops.
const (
	OpAdd         = "add"
	OpUpdate      = "update"
	OpSupersede   = "supersede"
	OpForget      = "forget"
	OpOwnerEdit   = "owner-edit"
	OpOwnerDelete = "owner-delete"
	OpPin         = "pin"
	OpUnpin       = "unpin"
	OpUse         = "use"
	// OpNoop is an add that changed nothing (never written).
	OpNoop = "noop"
)

// Event is one line of the log.
type Event struct {
	Op string `json:"op"`
	ID string `json:"id,omitempty"`
	At string `json:"at"`
	By string `json:"by,omitempty"`
	// Memory is the new memory (add).
	Memory *Memory `json:"memory,omitempty"`
	// Old and Text are the text before and after (update, owner-edit).
	Old  string `json:"old,omitempty"`
	Text string `json:"text,omitempty"`
	// Reason says why it was forgotten or superseded.
	Reason string `json:"reason,omitempty"`
	// Target: the superseding memory's id (supersede).
	Target string `json:"target,omitempty"`
	// IDs are the memories one use event covers.
	IDs []string `json:"ids,omitempty"`
}

// Set is a store folded: every memory the owner has not deleted, with its
// events.
type Set struct {
	byID   map[string]*Memory
	order  []string
	events []Event
}

// Get is the memory with id, or nil.
func (s *Set) Get(id string) *Memory { return s.byID[id] }

// All is every memory, valid or not, newest change first.
func (s *Set) All() []Memory {
	var out []Memory
	for _, id := range s.order {
		if m := s.byID[id]; m != nil {
			out = append(out, *m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Changed() > out[j].Changed() })
	return out
}

// Active is every memory valid at now, newest change first.
func (s *Set) Active(now time.Time) []Memory {
	var out []Memory
	for _, m := range s.All() {
		if m.Valid(now) {
			out = append(out, m)
		}
	}
	return out
}

// History is every event about id but use, oldest first.
func (s *Set) History(id string) []Event {
	var out []Event
	for _, e := range s.events {
		if e.ID == id && e.Op != OpUse {
			out = append(out, e)
		}
	}
	return out
}

// Recent is the events of the last days (use events left out), newest
// first: what the agents learned lately.
func (s *Set) Recent(now time.Time, days int) []Event {
	cut := now.Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339)
	var out []Event
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Op == OpUse || e.At < cut {
			continue
		}
		out = append(out, e)
	}
	return out
}

// fold applies events in order.
func fold(events []Event) *Set {
	s := &Set{byID: map[string]*Memory{}, events: events}
	for _, e := range events {
		if e.Op == OpUse {
			for _, id := range e.IDs {
				if m := s.byID[id]; m != nil {
					m.Uses++
					m.LastUsed = e.At
				}
			}
			continue
		}
		if e.Op == OpAdd {
			if e.Memory == nil {
				continue
			}
			m := *e.Memory
			if _, ok := s.byID[m.ID]; !ok {
				s.order = append(s.order, m.ID)
			}
			s.byID[m.ID] = &m
			continue
		}
		m := s.byID[e.ID]
		if m == nil {
			continue
		}
		switch e.Op {
		case OpUpdate, OpOwnerEdit:
			m.Text, m.Hash, m.Updated = e.Text, Hash(e.Text), e.At
		case OpSupersede:
			m.InvalidAt, m.SupersededBy, m.InvalidReason = e.At, e.Target, e.Reason
		case OpForget:
			m.InvalidAt, m.InvalidReason = e.At, e.Reason
		case OpPin:
			m.Pinned, m.PinnedBy, m.SuggestPin = true, e.By, false
		case OpUnpin:
			m.Pinned, m.PinnedBy = false, ""
		case OpOwnerDelete:
			delete(s.byID, e.ID)
		}
	}
	return s
}

// Load reads and folds the log; a missing file is an empty set.
func (s Store) Load() (*Set, error) {
	evs, err := s.readEvents()
	if err != nil {
		return nil, err
	}
	return fold(evs), nil
}

func (s Store) readEvents() ([]Event, error) {
	f, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var evs []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", s.Path, line, err)
		}
		evs = append(evs, e)
	}
	return evs, sc.Err()
}

// locked runs fn with the store's lock held.
func (s Store) locked(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	unlock, err := telemetry.LockFile(s.Path, 5*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// appendEvents writes evs at the end of the log (the lock is held).
func (s Store) appendEvents(evs ...Event) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, e := range evs {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// rewrite replaces the log with evs atomically (the lock is held).
func (s Store) rewrite(evs []Event) error {
	var buf bytes.Buffer
	for _, e := range evs {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	tmp := fmt.Sprintf("%s.tmp-%d", s.Path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func stamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// Edit replaces memory id's text: an agent's correction (update), or the
// owner's (owner-edit).
func (s Store) Edit(id, text, by string, owner bool, now time.Time) (Memory, error) {
	text = strings.TrimSpace(text)
	if err := CheckText(text); err != nil {
		return Memory{}, err
	}
	op := OpUpdate
	if owner {
		op = OpOwnerEdit
	}
	var out Memory
	err := s.locked(func() error {
		set, err := s.Load()
		if err != nil {
			return err
		}
		m := set.Get(id)
		if m == nil {
			return fmt.Errorf("%w %q", ErrNotFound, id)
		}
		e := Event{Op: op, ID: id, At: stamp(now), By: by, Old: m.Text, Text: text}
		if err := s.appendEvents(e); err != nil {
			return err
		}
		m.Text, m.Hash, m.Updated = text, Hash(text), e.At
		out = *m
		return nil
	})
	return out, err
}

// Forget invalidates memory id with the reason; it stays in the log.
func (s Store) Forget(id, reason, by string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("forget needs a reason: why it is no longer true")
	}
	if err := CheckText(reason); err != nil {
		return err
	}
	return s.change(id, Event{Op: OpForget, ID: id, At: stamp(now), By: by, Reason: reason})
}

// Pin puts memory id into every briefing of its scope (on), or takes it out.
func (s Store) Pin(id, by string, on bool, now time.Time) error {
	op := OpUnpin
	if on {
		op = OpPin
	}
	return s.change(id, Event{Op: op, ID: id, At: stamp(now), By: by})
}

// change appends e after checking the memory exists.
func (s Store) change(id string, e Event) error {
	return s.locked(func() error {
		set, err := s.Load()
		if err != nil {
			return err
		}
		if set.Get(id) == nil {
			return fmt.Errorf("%w %q", ErrNotFound, id)
		}
		return s.appendEvents(e)
	})
}

// RecordUse notes that ids were shown to an agent (start), for ranking by
// use later (phase 3).
func (s Store) RecordUse(ids []string, by string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.locked(func() error {
		return s.appendEvents(Event{Op: OpUse, At: stamp(now), By: by, IDs: ids})
	})
}

// OwnerDelete removes memory id for good: every earlier event about it
// loses its text, and an owner-delete tombstone is written.
func (s Store) OwnerDelete(id string, now time.Time) error {
	return s.locked(func() error {
		evs, err := s.readEvents()
		if err != nil {
			return err
		}
		if fold(evs).Get(id) == nil {
			return fmt.Errorf("%w %q", ErrNotFound, id)
		}
		for i := range evs {
			if evs[i].ID != id {
				continue
			}
			evs[i].Old, evs[i].Text, evs[i].Reason = "", "", scrubReason(evs[i].Reason)
			if m := evs[i].Memory; m != nil {
				c := *m
				c.Text, c.Tags, c.Hash = "", nil, ""
				evs[i].Memory = &c
			}
		}
		evs = append(evs, Event{Op: OpOwnerDelete, ID: id, At: stamp(now), By: "owner"})
		return s.rewrite(evs)
	})
}

func scrubReason(r string) string {
	if r == "" {
		return ""
	}
	return "(deleted)"
}
