// Package ownerstep keeps the owner's hand steps: what only the owner can
// do (reinstall a build whose signing expires, request indexing of URLs,
// submit to a directory), with the exact text to paste, a due day and an
// optional repeat. Agents add them; only the owner ticks one off.
//
// The state is an append-only log, <issues>/.owner-steps/log.jsonl, one
// JSON event a line: add, done, drop. Open folds it: a step is open while
// it has no done or drop, and a repeating one opens again on the calendar
// day its last tick plus the interval, due that day. Nothing runs on a
// timer. Days are calendar days in local time, never 24-hour spans.
package ownerstep

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Event kinds.
const (
	OpAdd  = "add"
	OpDone = "done"
	OpDrop = "drop"
)

// Who ticked a step (Event.Role on a done event).
const (
	RoleOwnerBrowser = "owner-browser"
	RoleOwnerShell   = "owner-shell"
	RoleLeadRelay    = "lead-relay"
)

// MaxBody is the longest body add takes, in bytes.
const MaxBody = 4000

// dayLayout is a due day.
const dayLayout = "2006-01-02"

// Event is one line of the log.
type Event struct {
	Op    string `json:"op"`
	ID    string `json:"id"`
	At    string `json:"at"`
	By    string `json:"by,omitempty"`
	Role  string `json:"role,omitempty"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	// Due is the first due day (YYYY-MM-DD), Repeat the interval in days
	// (0: none), Issue the issue the step is on ("" for the project).
	Due    string `json:"due,omitempty"`
	Repeat int    `json:"repeat_days,omitempty"`
	Issue  string `json:"issue,omitempty"`
	// Words are the owner's words on a done, Why a drop's reason.
	Words string `json:"words,omitempty"`
	Why   string `json:"why,omitempty"`
}

// Step is one step folded from the log.
type Step struct {
	ID, Title, Body, Issue, By string
	// Due is the day the step is due now: the first due day, or for a
	// repeating step that was ticked, the tick's day plus the interval.
	Due    string
	Repeat int
	Added  time.Time
	// Done is the newest tick (zero when never ticked), DoneBy who.
	Done   time.Time
	DoneBy string
	// Dropped is set when a drop closed it for good.
	Dropped time.Time
	DropWhy string
}

// Dir is the log's folder under the issues dir.
func Dir(issueDir string) string { return filepath.Join(issueDir, ".owner-steps") }

// LogPath is the log file.
func LogPath(issueDir string) string { return filepath.Join(Dir(issueDir), "log.jsonl") }

// LockWait is how long an append waits for another writer.
var LockWait = 10 * time.Second

// Append writes events to the log under its lock.
func Append(issueDir string, evs ...Event) error {
	if issueDir == "" {
		return errors.New("owner steps: the project has no issues folder")
	}
	if err := os.MkdirAll(Dir(issueDir), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(Dir(issueDir), "log.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(LockWait)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("the owner-steps log is being written by another issue-cli; try again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var b strings.Builder
	for _, ev := range evs {
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	f, err := os.OpenFile(LogPath(issueDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Read is every event in the log, in order; a missing log is none.
func Read(issueDir string) ([]Event, error) {
	if issueDir == "" {
		return nil, nil
	}
	f, err := os.Open(LogPath(issueDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.ID != "" && ev.Op != "" {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}

// Fold is every step in the log, by when it was added.
func Fold(evs []Event) []Step {
	byID := map[string]*Step{}
	var order []string
	for _, ev := range evs {
		at, _ := time.Parse(time.RFC3339, ev.At)
		s := byID[ev.ID]
		switch ev.Op {
		case OpAdd:
			if s != nil {
				continue
			}
			s = &Step{ID: ev.ID, Title: ev.Title, Body: ev.Body, Issue: ev.Issue, By: ev.By, Due: ev.Due, Repeat: ev.Repeat, Added: at}
			byID[ev.ID] = s
			order = append(order, ev.ID)
		case OpDone:
			if s == nil {
				continue
			}
			s.Done, s.DoneBy = at, ev.By
			if s.Repeat > 0 {
				s.Due = AddDays(DayOf(at), s.Repeat)
			}
		case OpDrop:
			if s == nil {
				continue
			}
			s.Dropped, s.DropWhy = at, ev.Why
		}
	}
	out := make([]Step, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// IsOpen reports whether the step waits on the owner on now's day: never
// ticked nor dropped, or repeating and its next due day has come.
func (s Step) IsOpen(now time.Time) bool {
	if !s.Dropped.IsZero() {
		return false
	}
	if s.Done.IsZero() {
		return true
	}
	return s.Repeat > 0 && DayOf(now) >= s.Due
}

// Overdue reports whether now's day is after the step's due day.
func (s Step) Overdue(now time.Time) bool { return s.Due != "" && DayOf(now) > s.Due }

// Open is the steps open on now's day: overdue first, then by due day,
// then by when they were added.
func Open(steps []Step, now time.Time) []Step {
	var out []Step
	for _, s := range steps {
		if s.IsOpen(now) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Due != out[j].Due {
			return out[i].Due < out[j].Due
		}
		return out[i].Added.Before(out[j].Added)
	})
	return out
}

// LoadOpen is the open steps of the project whose issues are in issueDir.
func LoadOpen(issueDir string, now time.Time) ([]Step, error) {
	evs, err := Read(issueDir)
	if err != nil {
		return nil, err
	}
	return Open(Fold(evs), now), nil
}

// Find is the step with id (case aside), or nil.
func Find(steps []Step, id string) *Step {
	for i := range steps {
		if strings.EqualFold(steps[i].ID, strings.TrimSpace(id)) {
			return &steps[i]
		}
	}
	return nil
}

// NewID is a fresh step id: st-<6 hex>, random so two agents adding at
// once never clash on a counter.
func NewID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("st-%06x", time.Now().UnixNano()&0xffffff)
	}
	return "st-" + hex.EncodeToString(b[:])
}

// DayOf is t's calendar day in local time.
func DayOf(t time.Time) string { return t.Local().Format(dayLayout) }

// AddDays is the calendar day n days after day (YYYY-MM-DD): a date, never
// n 24-hour spans, so a DST change moves nothing.
func AddDays(day string, n int) string {
	t, err := time.ParseInLocation(dayLayout, day, time.Local)
	if err != nil {
		return day
	}
	return time.Date(t.Year(), t.Month(), t.Day()+n, 0, 0, 0, 0, time.Local).Format(dayLayout)
}

// DaysBetween is how many calendar days from a to b (b later: positive).
func DaysBetween(a, b string) int {
	ta, err1 := time.Parse(dayLayout, a)
	tb, err2 := time.Parse(dayLayout, b)
	if err1 != nil || err2 != nil {
		return 0
	}
	// Both are UTC midnights, so the difference is whole days.
	return int(tb.Sub(ta).Hours() / 24)
}

var relDayRe = regexp.MustCompile(`^\+(\d+)([dw])$`)

// ParseDue reads a due day: YYYY-MM-DD, "today", "tomorrow" or +Nd / +Nw
// from now's day. A day before now's is refused.
func ParseDue(raw string, now time.Time) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	today := DayOf(now)
	var day string
	switch {
	case raw == "":
		return "", errors.New("--due is required: YYYY-MM-DD, today, tomorrow or +Nd")
	case raw == "today":
		day = today
	case raw == "tomorrow":
		day = AddDays(today, 1)
	case relDayRe.MatchString(raw):
		day = AddDays(today, intervalDays(relDayRe.FindStringSubmatch(raw)))
	default:
		t, err := time.ParseInLocation(dayLayout, raw, time.Local)
		if err != nil {
			return "", fmt.Errorf("--due %q: want YYYY-MM-DD, today, tomorrow or +Nd", raw)
		}
		day = t.Format(dayLayout)
	}
	if day < today {
		return "", fmt.Errorf("--due %s is before today (%s): a step is due today or later", day, today)
	}
	return day, nil
}

var repeatRe = regexp.MustCompile(`^(\d+)([dw])$`)

// ParseRepeat reads an interval, Nd or Nw, as days; "" is none.
func ParseRepeat(raw string) (int, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return 0, nil
	}
	m := repeatRe.FindStringSubmatch(raw)
	if m == nil {
		return 0, fmt.Errorf("--repeat %q: want Nd or Nw (7d, 2w)", raw)
	}
	n := intervalDays(m)
	if n < 1 || n > 366 {
		return 0, fmt.Errorf("--repeat %q: between 1 day and a year", raw)
	}
	return n, nil
}

func intervalDays(m []string) int {
	n, _ := strconv.Atoi(m[1])
	if m[2] == "w" {
		n *= 7
	}
	return n
}

// RepeatLabel is an interval as it was most likely written: 2w, 7d.
func RepeatLabel(days int) string {
	if days > 0 && days%7 == 0 && days != 7 {
		return strconv.Itoa(days/7) + "w"
	}
	return strconv.Itoa(days) + "d"
}

// DueLine is the step's date as the owner reads it: "due 2026-11-22",
// "due today" or "overdue: was due 2026-11-22 (3 days ago)".
func (s Step) DueLine(now time.Time) string {
	today := DayOf(now)
	switch {
	case s.Due == "":
		return "no due date"
	case s.Overdue(now):
		n := DaysBetween(s.Due, today)
		unit := "days"
		if n == 1 {
			unit = "day"
		}
		return fmt.Sprintf("overdue: was due %s (%d %s ago)", s.Due, n, unit)
	case s.Due == today:
		return "due today"
	}
	return "due " + s.Due
}

// Line is the step on one line, for a handoff note or a lead's state:
// "st-3fa9c1 · due 2026-11-22 · repeats 7d: Reinstall the phone build".
func (s Step) Line(now time.Time) string {
	l := s.ID + " · " + s.DueLine(now)
	if s.Repeat > 0 {
		l += " · repeats " + RepeatLabel(s.Repeat)
	}
	if s.Issue != "" {
		l += " · " + s.Issue
	}
	return l + ": " + s.Title
}

// Lines is Line for each step.
func Lines(steps []Step, now time.Time) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Line(now))
	}
	return out
}
