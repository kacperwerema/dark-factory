// Package push keeps the owner's phone notifications: the devices that
// subscribed, the settings (per-project mute, quiet hours, the VAPID
// contact) and the events ledger that sends each event to each device once.
//
// Everything lives in $XDG_STATE_HOME/ai-workflow-engine/push/ (files 0600):
// device state, not business data (factory/phone-notifications…#D-1). An
// endpoint is a capability URL: it never leaves this folder, and nothing
// here prints it.
//
// The ledger works by presence: an event opens when its key first shows in
// a scan, and closes after it has been absent for closeAfter scans in a row.
// A key that comes back after closing is a new event. Each open event holds
// one delivery per device.
package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/viewerstate"
	"github.com/kacperwerema/dark-factory/internal/webpush"
)

// Dir is the state dir's push/ folder; "" without a home.
func Dir() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "push")
}

// Delivery states.
const (
	StateSending  = "sending"  // chosen by a scan, being sent
	StateSent     = "sent"     // the push service took it
	StateRetry    = "retry"    // 429, 5xx, network: tried again next scan
	StateFailed   = "failed"   // still failing after MaxAttempts
	StateGone     = "gone"     // 404/410: the device is removed
	StateRefused  = "refused"  // any other answer
	StateSeeded   = "seeded"   // waited before the device subscribed
	StateMuted    = "muted"    // the project is muted
	StateQuiet    = "quiet"    // held in quiet hours for the digest
	StateDigested = "digested" // counted in a digest
)

const (
	// closeAfter scans without the key close an event.
	closeAfter = 2
	// MaxAttempts a delivery gets before it is failed.
	MaxAttempts = 3
	// MaxSends is the most pushes one scan sends; the rest wait for the next.
	MaxSends = 20
	// keepClosed is how long closed events stay in the ledger.
	keepClosed = 7 * 24 * time.Hour
	// inFlight: a "sending" younger than this belongs to a scan still
	// sending; an older one was left by a crash and is tried again.
	inFlight = 2 * time.Minute
	// keepRemoved is how long a removed device's line stays on /push.
	keepRemoved = 30 * 24 * time.Hour
)

// Device is one subscribed browser.
type Device struct {
	ID       string    `json:"id"`
	Endpoint string    `json:"endpoint"`
	P256dh   string    `json:"p256dh"`
	Auth     string    `json:"auth"`
	Origin   string    `json:"origin"`
	Label    string    `json:"label"`
	Added    time.Time `json:"added"`
	SeededAt time.Time `json:"seeded_at"`
	// Refused is the push service's last refusal ("" when none).
	Refused string `json:"refused,omitempty"`
}

// Sub is the device as webpush wants it.
func (d Device) Sub() webpush.Subscription {
	return webpush.Subscription{Endpoint: d.Endpoint, P256dh: d.P256dh, Auth: d.Auth}
}

// DeviceID is a device's id: base64url of its endpoint's SHA-256, cut to
// 12 bytes. It names the device in the ledger without the endpoint.
func DeviceID(endpoint string) string { return webpush.TopicFor(endpoint)[:16] }

// Removed is a device the push service dropped, shown until it subscribes
// again or keepRemoved passes.
type Removed struct {
	Label string    `json:"label"`
	At    time.Time `json:"at"`
}

// Quiet is the quiet-hours window, local time, "HH:MM"; From == To is off.
type Quiet struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// minutes parses "HH:MM".
func minutes(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// ValidQuiet reports whether q is off or two valid times.
func ValidQuiet(q Quiet) bool {
	if q.From == "" && q.To == "" {
		return true
	}
	_, a := minutes(q.From)
	_, b := minutes(q.To)
	return a && b
}

// In reports whether t (in its own location) is inside the window. A window
// may cross midnight (22:00 → 07:00).
func (q Quiet) In(t time.Time) bool {
	from, ok1 := minutes(q.From)
	to, ok2 := minutes(q.To)
	if !ok1 || !ok2 || from == to {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

// Settings are the owner's choices.
type Settings struct {
	// Config is the projects.yaml the store is bound to: only a viewer of
	// that config sends.
	Config  string   `json:"config,omitempty"`
	Muted   []string `json:"muted,omitempty"`
	Quiet   Quiet    `json:"quiet"`
	Subject string   `json:"subject,omitempty"`
}

// IsMuted reports whether the project slug is muted.
func (s Settings) IsMuted(project string) bool {
	for _, m := range s.Muted {
		if m == project {
			return true
		}
	}
	return false
}

// Delivery is one event's push to one device.
type Delivery struct {
	State    string    `json:"state"`
	Attempts int       `json:"attempts,omitempty"`
	At       time.Time `json:"at"`
	// Count is a digest's number of held events for this device.
	Count int `json:"count,omitempty"`
}

// Event is one thing that waits on the owner, while it waits.
type Event struct {
	Key     string `json:"key"`
	Project string `json:"project"`
	// ProjectName, Kind, Title and URL make the payload.
	ProjectName string    `json:"project_name"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Urgent      bool      `json:"urgent,omitempty"`
	Opened      time.Time `json:"opened"`
	LastSeen    time.Time `json:"last_seen"`
	Missed      int       `json:"missed,omitempty"`
	Closed      time.Time `json:"closed,omitzero"`
	// Digest marks the quiet-hours summary.
	Digest     bool                 `json:"digest,omitempty"`
	Deliveries map[string]*Delivery `json:"deliveries,omitempty"`
}

// Open reports whether the event still waits.
func (e *Event) Open() bool { return e.Closed.IsZero() }

// Seen is one thing a scan found waiting.
type Seen struct {
	Key, Project, ProjectName, Kind, Title, URL string
	Urgent                                      bool
	// Cooldown: a key that closed less than this ago reopens the same
	// event (no new push), so a worker that stalls, works and stalls again
	// within the hour pings once.
	Cooldown time.Duration
}

// State is the whole push/ folder.
type State struct {
	Devices  []Device          `json:"devices"`
	Removed  []Removed         `json:"removed,omitempty"`
	Settings Settings          `json:"-"`
	Events   map[string]*Event `json:"-"`
	// NeedSeed: events.json was missing or unreadable while devices exist,
	// so every device is seeded on the next Observe instead of flooded.
	NeedSeed bool `json:"-"`
}

// Store is push/ under one lock: an in-process mutex and flock on
// store.lock, so two viewers (or a scan and a subscribe) never overwrite
// each other's changes.
type Store struct {
	Dir string
	mu  sync.Mutex
}

// NewStore is the store at dir.
func NewStore(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) path(name string) string { return filepath.Join(s.Dir, name) }

// Update loads the state under the lock, runs fn, and saves what fn left
// when it returns nil.
func (s *Store) Update(fn func(*State) error) error {
	if s.Dir == "" {
		return errors.New("no state directory for push (HOME unset)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.flock()
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return s.save(st)
}

// Read loads the state under the lock without saving.
func (s *Store) Read() (*State, error) {
	if s.Dir == "" {
		return &State{Events: map[string]*Event{}}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.Dir); errors.Is(err, os.ErrNotExist) {
		return &State{Events: map[string]*Event{}}, nil
	}
	unlock, err := s.flock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.load()
}

func (s *Store) flock() (func(), error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.path("store.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

type subsFile struct {
	Devices []Device  `json:"devices"`
	Removed []Removed `json:"removed,omitempty"`
}

type eventsFile struct {
	Events []*Event `json:"events"`
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, v)
}

func (s *Store) load() (*State, error) {
	st := &State{Events: map[string]*Event{}}
	var subs subsFile
	if _, err := readJSON(s.path("subscriptions.json"), &subs); err != nil {
		return nil, fmt.Errorf("push subscriptions unreadable: %w", err)
	}
	st.Devices, st.Removed = subs.Devices, subs.Removed
	if _, err := readJSON(s.path("settings.json"), &st.Settings); err != nil {
		return nil, fmt.Errorf("push settings unreadable: %w", err)
	}
	var ev eventsFile
	found, err := readJSON(s.path("events.json"), &ev)
	if !found || err != nil {
		// Missing or unreadable: start over, seeding instead of flooding.
		ev.Events = nil
		st.NeedSeed = len(st.Devices) > 0
	}
	for _, e := range ev.Events {
		if e != nil && e.Key != "" {
			if e.Deliveries == nil {
				e.Deliveries = map[string]*Delivery{}
			}
			st.Events[e.Key] = e
		}
	}
	return st, nil
}

func (s *Store) save(st *State) error {
	write := func(name string, v any) error {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		return webpush.WriteFile(s.path(name), append(data, '\n'))
	}
	if err := write("subscriptions.json", subsFile{Devices: st.Devices, Removed: st.Removed}); err != nil {
		return err
	}
	if err := write("settings.json", st.Settings); err != nil {
		return err
	}
	keys := make([]string, 0, len(st.Events))
	for k := range st.Events {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ev := eventsFile{Events: make([]*Event, 0, len(keys))}
	for _, k := range keys {
		ev.Events = append(ev.Events, st.Events[k])
	}
	return write("events.json", ev)
}

// Device returns the device with id.
func (st *State) Device(id string) (Device, bool) {
	for _, d := range st.Devices {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

// Observe records one scan: what it saw waiting, among the projects it
// scanned. Events of a project this scan didn't cover are left alone.
func (st *State) Observe(seen []Seen, scanned []string, now time.Time) {
	covered := map[string]bool{}
	for _, p := range scanned {
		covered[p] = true
	}
	here := map[string]bool{}
	for _, s := range seen {
		here[s.Key] = true
		e := st.Events[s.Key]
		if e != nil && !e.Open() && s.Cooldown > 0 && now.Sub(e.Closed) < s.Cooldown {
			e.Closed = time.Time{}
		}
		if e == nil || !e.Open() {
			e = &Event{Key: s.Key, Opened: now, Deliveries: map[string]*Delivery{}}
			st.Events[s.Key] = e
		}
		e.Project, e.ProjectName, e.Kind, e.Title, e.URL, e.Urgent = s.Project, s.ProjectName, s.Kind, s.Title, s.URL, s.Urgent
		e.LastSeen, e.Missed = now, 0
	}
	for k, e := range st.Events {
		switch {
		case e.Digest:
			if now.Sub(e.Opened) > keepClosed {
				delete(st.Events, k)
			}
		case !e.Open():
			if now.Sub(e.Closed) > keepClosed {
				delete(st.Events, k)
			}
		case !here[k] && covered[e.Project]:
			e.Missed++
			if e.Missed >= closeAfter {
				e.Closed = now
			}
		}
	}
	if st.NeedSeed {
		for _, d := range st.Devices {
			st.seed(d.ID, now)
		}
		st.NeedSeed = false
	}
	var kept []Removed
	for _, r := range st.Removed {
		if now.Sub(r.At) <= keepRemoved {
			kept = append(kept, r)
		}
	}
	st.Removed = kept
}

// seed marks every open event as already waiting for device id.
func (st *State) seed(id string, now time.Time) {
	for _, e := range st.Events {
		if e.Open() && !e.Digest {
			if _, ok := e.Deliveries[id]; !ok {
				e.Deliveries[id] = &Delivery{State: StateSeeded, At: now}
			}
		}
	}
}

// Subscribe adds or replaces a device and seeds it: what waits now is not
// sent to it. Other devices' deliveries are untouched.
func (st *State) Subscribe(d Device, now time.Time) {
	d.ID = DeviceID(d.Endpoint)
	d.SeededAt = now
	if d.Added.IsZero() {
		d.Added = now
	}
	replaced := false
	for i := range st.Devices {
		if st.Devices[i].ID == d.ID {
			d.Added = st.Devices[i].Added
			st.Devices[i] = d
			replaced = true
		}
	}
	if !replaced {
		st.Devices = append(st.Devices, d)
	}
	var kept []Removed
	for _, r := range st.Removed {
		if r.Label != d.Label {
			kept = append(kept, r)
		}
	}
	st.Removed = kept
	st.seed(d.ID, now)
}

// Change swaps a rotated subscription (pushsubscriptionchange): the new
// endpoint takes over the old device's deliveries. It reports whether the
// old one was known.
func (st *State) Change(oldEndpoint string, d Device, now time.Time) bool {
	oldID := DeviceID(oldEndpoint)
	old, ok := st.Device(oldID)
	if !ok {
		return false
	}
	d.ID = DeviceID(d.Endpoint)
	d.Added, d.SeededAt = old.Added, old.SeededAt
	if d.Label == "" {
		d.Label = old.Label
	}
	for i := range st.Devices {
		if st.Devices[i].ID == oldID {
			st.Devices[i] = d
		}
	}
	for _, e := range st.Events {
		if del, ok := e.Deliveries[oldID]; ok {
			delete(e.Deliveries, oldID)
			e.Deliveries[d.ID] = del
		}
	}
	return true
}

// Unsubscribe removes the device; it reports whether there was one.
func (st *State) Unsubscribe(id string) bool {
	for i, d := range st.Devices {
		if d.ID == id {
			st.Devices = append(st.Devices[:i], st.Devices[i+1:]...)
			for _, e := range st.Events {
				delete(e.Deliveries, id)
			}
			return true
		}
	}
	return false
}

// Job is one push a scan chose to send.
type Job struct {
	EventKey, Device string
}

// Plan picks the pushes to send now, at most MaxSends, and marks them
// sending. A device with no delivery for an open event gets one: muted,
// quiet, or sending. A retry (or a crashed sending) is tried again. Outside
// quiet hours, held quiet deliveries make one digest per device.
func (st *State) Plan(now time.Time) []Job {
	quiet := st.Settings.Quiet.In(now)
	var jobs []Job
	add := func(e *Event, id string, d *Delivery) {
		if len(jobs) >= MaxSends {
			return
		}
		d.State, d.At = StateSending, now
		d.Attempts++
		jobs = append(jobs, Job{EventKey: e.Key, Device: id})
	}
	if !quiet {
		st.digest(now)
	}
	keys := make([]string, 0, len(st.Events))
	for k := range st.Events {
		keys = append(keys, k)
	}
	// Digests first, then the oldest events.
	sort.Slice(keys, func(i, j int) bool {
		a, b := st.Events[keys[i]], st.Events[keys[j]]
		if a.Digest != b.Digest {
			return a.Digest
		}
		if !a.Opened.Equal(b.Opened) {
			return a.Opened.Before(b.Opened)
		}
		return a.Key < b.Key
	})
	for _, k := range keys {
		e := st.Events[k]
		if !e.Open() {
			continue
		}
		for _, dev := range st.Devices {
			d := e.Deliveries[dev.ID]
			switch {
			case d == nil && e.Digest:
				// A digest names only the devices it was made for.
			case d == nil:
				d = &Delivery{At: now}
				e.Deliveries[dev.ID] = d
				switch {
				case st.Settings.IsMuted(e.Project):
					d.State = StateMuted
				case quiet:
					d.State = StateQuiet
				default:
					add(e, dev.ID, d)
					if d.State != StateSending {
						// Over this scan's cap: try next scan.
						delete(e.Deliveries, dev.ID)
					}
				}
			case d.State == StateRetry, d.State == StateSending && now.Sub(d.At) >= inFlight:
				if d.Attempts >= MaxAttempts {
					d.State = StateFailed
					continue
				}
				if !quiet || e.Digest {
					add(e, dev.ID, d)
				}
			}
		}
	}
	return jobs
}

// digest turns each device's held quiet deliveries (still open, project not
// muted now) into one digest event, in the same change, so a crash retries
// the digest and never the held events one by one.
func (st *State) digest(now time.Time) {
	counts := map[string]int{}
	for _, e := range st.Events {
		if e.Digest {
			continue
		}
		for id, d := range e.Deliveries {
			if d.State != StateQuiet {
				continue
			}
			d.State, d.At = StateDigested, now
			if e.Open() && !st.Settings.IsMuted(e.Project) {
				counts[id]++
			}
		}
	}
	if len(counts) == 0 {
		return
	}
	key := "digest|" + now.UTC().Format(time.RFC3339)
	e := &Event{Key: key, Digest: true, Kind: "digest", URL: "/needs-you", Opened: now, LastSeen: now, Deliveries: map[string]*Delivery{}}
	for id, n := range counts {
		e.Deliveries[id] = &Delivery{State: StateRetry, Count: n, At: now}
	}
	st.Events[key] = e
	// A digest is "open" until every delivery ends; Plan sends it.
}

// Record writes the results of a scan's pushes. A gone device is removed
// and listed under Removed.
func (st *State) Record(job Job, r webpush.Result, now time.Time) {
	e := st.Events[job.EventKey]
	if e == nil {
		return
	}
	d := e.Deliveries[job.Device]
	if d == nil {
		return
	}
	d.At = now
	switch r.Outcome {
	case webpush.Sent:
		d.State = StateSent
	case webpush.Retry:
		d.State = StateRetry
		if d.Attempts >= MaxAttempts {
			d.State = StateFailed
		}
	case webpush.Gone:
		d.State = StateGone
		if dev, ok := st.Device(job.Device); ok {
			st.Unsubscribe(job.Device)
			st.Removed = append(st.Removed, Removed{Label: dev.Label, At: now})
		}
	default:
		d.State = StateRefused
		for i := range st.Devices {
			if st.Devices[i].ID == job.Device {
				st.Devices[i].Refused = refusalWords(r)
			}
		}
	}
	if r.Outcome == webpush.Sent {
		for i := range st.Devices {
			if st.Devices[i].ID == job.Device {
				st.Devices[i].Refused = ""
			}
		}
	}
	if e.Digest {
		done := true
		for _, d := range e.Deliveries {
			if d.State == StateRetry || d.State == StateSending {
				done = false
			}
		}
		if done {
			e.Closed = now
		}
	}
}

// refusalWords is what /push says about a refusal.
func refusalWords(r webpush.Result) string {
	if strings.Contains(r.Why, "BadJwtToken") || r.Status == 403 {
		return "the push service refused the contact (set a real email or https address)"
	}
	if r.Status != 0 {
		return fmt.Sprintf("the push service answered %d", r.Status)
	}
	return r.Why
}

// Payload is what a push carries: nothing more (Must not: no secrets, file
// contents or paths).
type Payload struct {
	Title   string `json:"title"`
	Project string `json:"project"`
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	Tag     string `json:"tag"`
}

// PayloadFor is job's payload for device id.
func (st *State) PayloadFor(job Job) (Payload, bool) {
	e := st.Events[job.EventKey]
	if e == nil {
		return Payload{}, false
	}
	if e.Digest {
		n := 0
		if d := e.Deliveries[job.Device]; d != nil {
			n = d.Count
		}
		title := fmt.Sprintf("%d new things wait on you", n)
		if n == 1 {
			title = "1 new thing waits on you"
		}
		return Payload{Title: title, Kind: "digest", URL: "/needs-you", Tag: webpush.TopicFor(e.Key)}, true
	}
	return Payload{Title: e.Title, Project: e.ProjectName, Kind: e.Kind, URL: e.URL, Tag: webpush.TopicFor(e.Key)}, true
}

// SafeURL keeps a link only when it is a relative path on the viewer's own
// pages (no scheme, no host, no //).
func SafeURL(u string) string {
	if strings.HasPrefix(u, "/p/") || u == "/needs-you" || strings.HasPrefix(u, "/needs-you?") {
		if !strings.Contains(u, "//") && !strings.Contains(u, "\\") && !strings.Contains(u, "..") {
			return u
		}
	}
	return "/needs-you"
}
