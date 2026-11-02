// Package queue is the lead's queue: one project's ordered plan of items,
// each an issue to start or a one-line idea to file, with what it is
// blocked on and the owner's words that authorise it. The lead's tick takes
// the first startable item (issue-cli queue next --start) while memory and
// CPU allow; the item is marked started, and done with its merge SHA when
// the issue ships. The file sits with the work files,
// <issues>/.queue/queue.json, so a private tracker keeps it private.
//
// The "out of work: think up more" item is not stored: every list ends
// with it (RefillText), so it is never done, moved or dropped.
package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Item states.
const (
	Open    = "open"
	Started = "started"
	Done    = "done"
	Dropped = "dropped"
)

// RefillText is the fixed last line of every queue.
const RefillText = "Out of work: think up more. Read the roadmap, the epics, the health snapshots, the retros and the owner's words from the last days; add the best new items above this one, then go on."

// Default limits while auto-start is off (factory/the-leads-queue-…#D-3):
// memory under 50%, no worker cap (the owner, 2026-10-31: "don't limit
// yourself on the 5 individual workers - just on the memory you have").
const (
	DefaultBelow = 50
)

// Item is one entry of the queue.
type Item struct {
	ID        int       `json:"id"`
	Slug      string    `json:"slug,omitempty"`
	Idea      string    `json:"idea,omitempty"`
	Quote     string    `json:"quote,omitempty"`
	How       string    `json:"how,omitempty"`
	BlockedOn []string  `json:"blocked_on,omitempty"`
	State     string    `json:"state"`
	Note      string    `json:"note,omitempty"`
	AddedBy   string    `json:"added_by,omitempty"`
	AddedAt   time.Time `json:"added_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	StartedBy string    `json:"started_by,omitempty"`
	DoneAt    time.Time `json:"done_at,omitempty"`
	DoneSHA   string    `json:"done_sha,omitempty"`
}

// Label is the item's slug, or its idea.
func (it *Item) Label() string {
	if it.Slug != "" {
		return it.Slug
	}
	return it.Idea
}

// Active is open or started.
func (it *Item) Active() bool { return it.State == Open || it.State == Started }

// Queue is the file: the items in order and the limits used while
// auto-start is off.
type Queue struct {
	NextID int    `json:"next_id"`
	Items  []Item `json:"items"`
	// Below is the memory % a start must be under (0: DefaultBelow);
	// MaxWorkers the live-worker cap (0: none, memory is the limit).
	Below      int `json:"below,omitempty"`
	MaxWorkers int `json:"max_workers,omitempty"`
	// Paused is the owner's pause from the viewer's Crew page: queue next
	// starts nothing, not even an item with its own quote, until the owner
	// resumes it there. issue-cli has no resume, so an agent can't lift it.
	Paused   bool      `json:"paused,omitempty"`
	PausedBy string    `json:"paused_by,omitempty"`
	PausedAt time.Time `json:"paused_at,omitempty"`
}

// PauseLine is the pause in words ("paused by the owner (Crew page) at Sat
// 21:40: resume it on /crew"); "" when the queue runs.
func (q *Queue) PauseLine() string {
	if !q.Paused {
		return ""
	}
	who := q.PausedBy
	if who == "" {
		who = "the owner"
	}
	at := ""
	if !q.PausedAt.IsZero() {
		at = " at " + q.PausedAt.Local().Format("Mon 15:04")
	}
	return "the queue is paused by " + who + at + ": resume it on the viewer's /crew page"
}

// SetPaused pauses (by names who) or resumes the queue in issueDir. A
// project without a queue file is an error: there is nothing to pause.
func SetPaused(issueDir string, paused bool, by string, now time.Time) error {
	if !Exists(issueDir) {
		return fmt.Errorf("no queue in %s", Path(issueDir))
	}
	return Update(issueDir, func(q *Queue) error {
		q.Paused = paused
		q.PausedBy, q.PausedAt = "", time.Time{}
		if paused {
			q.PausedBy, q.PausedAt = by, now
		}
		return nil
	})
}

// BelowOr is Below, or the default.
func (q *Queue) BelowOr() int {
	if q.Below > 0 && q.Below <= 100 {
		return q.Below
	}
	return DefaultBelow
}

// Dir is the queue's folder under the issues dir.
func Dir(issueDir string) string { return filepath.Join(issueDir, ".queue") }

// Path is the queue file.
func Path(issueDir string) string { return filepath.Join(Dir(issueDir), "queue.json") }

// Exists reports whether the project has a queue file.
func Exists(issueDir string) bool {
	_, err := os.Stat(Path(issueDir))
	return err == nil
}

// Load reads the queue; a missing file is an empty queue.
func Load(issueDir string) (*Queue, error) {
	data, err := os.ReadFile(Path(issueDir))
	if errors.Is(err, os.ErrNotExist) {
		return &Queue{NextID: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var q Queue
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(issueDir), err)
	}
	if q.NextID < 1 {
		q.NextID = 1
	}
	for _, it := range q.Items {
		if it.ID >= q.NextID {
			q.NextID = it.ID + 1
		}
	}
	return &q, nil
}

func save(issueDir string, q *Queue) error {
	dir := Dir(issueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "queue.json.*.tmp")
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
	return os.Rename(tmp.Name(), Path(issueDir))
}

// LockWait is how long Update waits for another writer.
var LockWait = 10 * time.Second

// Update loads the queue under the lock, runs fn and saves the result when
// fn returns nil. The lead and a worker's transition to done can write at
// once; the lock keeps one after the other.
func Update(issueDir string, fn func(q *Queue) error) error {
	dir := Dir(issueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "queue.lock"), os.O_CREATE|os.O_RDWR, 0o644)
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
			return errors.New("the queue is being written by another issue-cli; try again")
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	q, err := Load(issueDir)
	if err != nil {
		return err
	}
	if err := fn(q); err != nil {
		return err
	}
	return save(issueDir, q)
}

// Find returns the item a reference names: its number ("12" or "#12"), or
// the slug of an active item (else the newest with that slug).
func (q *Queue) Find(ref string) *Item {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if n, err := strconv.Atoi(ref); err == nil {
		for i := range q.Items {
			if q.Items[i].ID == n {
				return &q.Items[i]
			}
		}
		return nil
	}
	var found *Item
	for i := range q.Items {
		it := &q.Items[i]
		if !strings.EqualFold(it.Slug, ref) {
			continue
		}
		if it.Active() {
			return it
		}
		found = it
	}
	return found
}

func (q *Queue) index(id int) int {
	for i := range q.Items {
		if q.Items[i].ID == id {
			return i
		}
	}
	return -1
}

// Where says where Add or Move puts an item.
type Where struct {
	Top, Bottom   bool
	Before, After string // item references
}

// Add puts it in the queue (Where: the end above the refill line by
// default) and returns its number. A slug already active in the queue is
// refused.
func (q *Queue) Add(it Item, where Where, now time.Time) (int, error) {
	it.Slug = strings.TrimSpace(it.Slug)
	it.Idea = strings.TrimSpace(it.Idea)
	if (it.Slug == "") == (it.Idea == "") {
		return 0, errors.New("an item is an issue slug or an --idea, one of them")
	}
	if it.Slug != "" {
		if dup := q.Find(it.Slug); dup != nil && dup.Active() {
			return 0, fmt.Errorf("%s is already item %d (%s)", it.Slug, dup.ID, dup.State)
		}
	}
	it.ID = q.NextID
	q.NextID++
	it.State, it.AddedAt = Open, now
	q.Items = append(q.Items, it)
	if where != (Where{}) {
		if err := q.Move(strconv.Itoa(it.ID), where); err != nil {
			q.Items = q.Items[:len(q.Items)-1]
			q.NextID--
			return 0, err
		}
	}
	return it.ID, nil
}

// Move puts the referenced item at the top, the bottom, or before or after
// another one.
func (q *Queue) Move(ref string, where Where) error {
	it := q.Find(ref)
	if it == nil {
		return fmt.Errorf("no queue item %s", ref)
	}
	id := it.ID
	from := q.index(id)
	moved := q.Items[from]
	rest := append(append([]Item{}, q.Items[:from]...), q.Items[from+1:]...)
	at := len(rest)
	switch {
	case where.Top:
		at = 0
	case where.Bottom:
		at = len(rest)
	case where.Before != "" || where.After != "":
		ref := where.Before
		if ref == "" {
			ref = where.After
		}
		other := q.Find(ref)
		if other == nil {
			return fmt.Errorf("no queue item %s", ref)
		}
		if other.ID == id {
			return errors.New("an item can't move next to itself")
		}
		oid := other.ID
		for i := range rest {
			if rest[i].ID == oid {
				at = i
				if where.After != "" {
					at = i + 1
				}
			}
		}
	default:
		return errors.New("move needs --top, --bottom, --before <item> or --after <item>")
	}
	q.Items = append(append(append([]Item{}, rest[:at]...), moved), rest[at:]...)
	return nil
}

// Block adds on (an item number or an issue slug) to the item's blockers.
func (q *Queue) Block(ref, on string) error {
	it := q.Find(ref)
	if it == nil {
		return fmt.Errorf("no queue item %s", ref)
	}
	on = strings.TrimSpace(on)
	if on == "" {
		return errors.New("block needs --on <item or slug>")
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(on, "#")); err == nil {
		if q.Find(strconv.Itoa(n)) == nil {
			return fmt.Errorf("no queue item %d to block on", n)
		}
		if n == it.ID {
			return errors.New("an item can't be blocked on itself")
		}
		on = "#" + strconv.Itoa(n)
	} else if strings.EqualFold(on, it.Slug) {
		return errors.New("an item can't be blocked on itself")
	}
	for _, b := range it.BlockedOn {
		if strings.EqualFold(b, on) {
			return nil
		}
	}
	it.BlockedOn = append(it.BlockedOn, on)
	return nil
}

// Unblock removes on from the item's blockers, or all of them when on is "".
func (q *Queue) Unblock(ref, on string) error {
	it := q.Find(ref)
	if it == nil {
		return fmt.Errorf("no queue item %s", ref)
	}
	if on == "" {
		it.BlockedOn = nil
		return nil
	}
	on = strings.TrimSpace(on)
	if _, err := strconv.Atoi(strings.TrimPrefix(on, "#")); err == nil && !strings.HasPrefix(on, "#") {
		on = "#" + on
	}
	kept := it.BlockedOn[:0]
	found := false
	for _, b := range it.BlockedOn {
		if strings.EqualFold(b, on) {
			found = true
			continue
		}
		kept = append(kept, b)
	}
	if !found {
		return fmt.Errorf("item %d isn't blocked on %s", it.ID, on)
	}
	it.BlockedOn = kept
	return nil
}

// MarkDone marks every active item with slug done with sha ("" when not
// known) and returns their numbers.
func (q *Queue) MarkDone(slug, sha string, now time.Time) []int {
	var ids []int
	for i := range q.Items {
		it := &q.Items[i]
		if it.Active() && it.Slug != "" && strings.EqualFold(it.Slug, slug) {
			it.State, it.DoneAt, it.DoneSHA = Done, now, sha
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// HasActive reports whether slug is an active item.
func (q *Queue) HasActive(slug string) bool {
	it := q.Find(slug)
	return it != nil && it.Active() && it.Slug != ""
}

// ActiveItems is the open and started items in order.
func (q *Queue) ActiveItems() []Item {
	var out []Item
	for _, it := range q.Items {
		if it.Active() {
			out = append(out, it)
		}
	}
	return out
}

// DoneOn is the items done on now's day (local time), newest first.
func (q *Queue) DoneOn(now time.Time) []Item {
	y, m, d := now.Date()
	var out []Item
	for i := len(q.Items) - 1; i >= 0; i-- {
		it := q.Items[i]
		if it.State != Done {
			continue
		}
		dy, dm, dd := it.DoneAt.In(now.Location()).Date()
		if dy == y && dm == m && dd == d {
			out = append(out, it)
		}
	}
	return out
}

// BlockerDone reports whether blocker b of the queue is done: an item
// number whose item is done or dropped, or a slug isDone says shipped.
// The second result says it exists.
func (q *Queue) BlockerDone(b string, isDone func(slug string) (done, found bool)) (bool, bool) {
	if strings.HasPrefix(b, "#") {
		it := q.Find(b)
		if it == nil {
			return false, false
		}
		if it.State == Done || it.State == Dropped {
			return true, true
		}
		if it.Slug != "" {
			if d, ok := isDone(it.Slug); ok && d {
				return true, true
			}
		}
		return false, true
	}
	return isDone(b)
}
