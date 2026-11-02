package push

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/webpush"
)

var t0 = time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)

func gate(slug string) Seen {
	return Seen{Key: "orchard|gate|" + slug + "|backlog", Project: "orchard", ProjectName: "Orchard", Kind: "gate", Title: "Gate ready: " + slug, URL: "/p/orchard/issue/" + slug}
}

func newState(devs ...string) *State {
	st := &State{Events: map[string]*Event{}}
	for _, d := range devs {
		st.Devices = append(st.Devices, Device{ID: d, Endpoint: "https://e/" + d, Label: d})
	}
	return st
}

// scan runs one Observe + Plan and answers every job with outcome(job).
func scan(st *State, now time.Time, seen []Seen, outcome func(Job) webpush.Outcome) []Job {
	st.Observe(seen, []string{"orchard"}, now)
	jobs := st.Plan(now)
	for _, j := range jobs {
		st.Record(j, webpush.Result{Outcome: outcome(j)}, now)
	}
	return jobs
}

func ok(Job) webpush.Outcome { return webpush.Sent }

func TestEventSentOnceAndAgainAfterItCloses(t *testing.T) {
	st := newState("phone")
	g := gate("a")
	if n := len(scan(st, t0, []Seen{g}, ok)); n != 1 {
		t.Fatalf("first scan sent %d", n)
	}
	for i := 1; i <= 3; i++ {
		if n := len(scan(st, t0.Add(time.Duration(i)*time.Minute), []Seen{g}, ok)); n != 0 {
			t.Fatalf("repeat scan %d sent %d", i, n)
		}
	}
	// Send back: the gate row is gone for one scan, then back: same event.
	scan(st, t0.Add(4*time.Minute), nil, ok)
	if n := len(scan(st, t0.Add(5*time.Minute), []Seen{g}, ok)); n != 0 {
		t.Fatalf("a wait back within %d scans sent %d", closeAfter, n)
	}
	// Gone for two scans: closed; reached again: a new event.
	scan(st, t0.Add(6*time.Minute), nil, ok)
	scan(st, t0.Add(7*time.Minute), nil, ok)
	if st.Events[g.Key].Open() {
		t.Fatal("event still open after two scans without it")
	}
	if n := len(scan(st, t0.Add(30*time.Minute), []Seen{g}, ok)); n != 1 {
		t.Fatalf("gate reached again sent %d, want 1", n)
	}
}

func TestEditingAWaitingIssueSendsNothing(t *testing.T) {
	st := newState("phone")
	g := gate("a")
	scan(st, t0, []Seen{g}, ok)
	g.Title = "Gate ready: a (renamed)"
	if n := len(scan(st, t0.Add(time.Minute), []Seen{g}, ok)); n != 0 {
		t.Fatalf("an edit sent %d", n)
	}
}

func TestTwoDevicesOneFails(t *testing.T) {
	st := newState("phone", "desk")
	g := gate("a")
	fail := func(j Job) webpush.Outcome {
		if j.Device == "desk" {
			return webpush.Retry
		}
		return webpush.Sent
	}
	if n := len(scan(st, t0, []Seen{g}, fail)); n != 2 {
		t.Fatalf("first scan sent %d", n)
	}
	for i := 1; i <= 4; i++ {
		jobs := scan(st, t0.Add(time.Duration(i)*time.Minute), []Seen{g}, fail)
		for _, j := range jobs {
			if j.Device == "phone" {
				t.Fatal("the phone got the event twice")
			}
		}
		if i >= MaxAttempts && len(jobs) != 0 {
			t.Fatalf("scan %d still retried after %d attempts", i, MaxAttempts)
		}
	}
	if d := st.Events[g.Key].Deliveries["desk"]; d.State != StateFailed || d.Attempts != MaxAttempts {
		t.Fatalf("desk delivery %+v", d)
	}
}

func TestGoneRemovesTheDeviceAndSaysSo(t *testing.T) {
	st := newState("phone")
	scan(st, t0, []Seen{gate("a")}, func(Job) webpush.Outcome { return webpush.Gone })
	if len(st.Devices) != 0 || len(st.Removed) != 1 || st.Removed[0].Label != "phone" {
		t.Fatalf("devices %v removed %v", st.Devices, st.Removed)
	}
}

func TestSeedingIsPerDevice(t *testing.T) {
	st := newState("phone")
	a, b := gate("a"), gate("b")
	fail := func(Job) webpush.Outcome { return webpush.Retry }
	scan(st, t0, []Seen{a}, fail) // the phone's push of a is pending a retry
	st.Subscribe(Device{Endpoint: "https://e/desk", Label: "desk"}, t0.Add(time.Second))
	desk := DeviceID("https://e/desk")
	if d := st.Events[a.Key].Deliveries[desk]; d == nil || d.State != StateSeeded {
		t.Fatalf("desk not seeded for what waited: %+v", d)
	}
	if d := st.Events[a.Key].Deliveries["phone"]; d.State != StateRetry {
		t.Fatalf("seeding touched the phone's delivery: %+v", d)
	}
	jobs := scan(st, t0.Add(time.Minute), []Seen{a, b}, ok)
	sent := map[string]int{}
	for _, j := range jobs {
		sent[j.Device+" "+j.EventKey]++
	}
	if sent[desk+" "+a.Key] != 0 || sent[desk+" "+b.Key] != 1 || sent["phone "+a.Key] != 1 || sent["phone "+b.Key] != 1 {
		t.Fatalf("sent %v", sent)
	}
}

func TestMissingLedgerSeedsEveryDevice(t *testing.T) {
	st := newState("phone")
	st.NeedSeed = true
	if n := len(scan(st, t0, []Seen{gate("a"), gate("b")}, ok)); n != 0 {
		t.Fatalf("a missing ledger flooded %d", n)
	}
	if n := len(scan(st, t0.Add(time.Minute), []Seen{gate("a"), gate("b"), gate("c")}, ok)); n != 1 {
		t.Fatalf("a new wait after seeding sent %d", n)
	}
}

func TestOnlyScannedProjectsClose(t *testing.T) {
	st := newState("phone")
	g := gate("a")
	scan(st, t0, []Seen{g}, ok)
	// Another viewer that serves other projects: sees none of orchard's.
	for i := 1; i <= 3; i++ {
		st.Observe(nil, []string{"sandbox"}, t0.Add(time.Duration(i)*time.Minute))
	}
	if !st.Events[g.Key].Open() {
		t.Fatal("a scan without the project closed its event")
	}
}

func TestMuteSuppresses(t *testing.T) {
	st := newState("phone")
	st.Settings.Muted = []string{"orchard"}
	if n := len(scan(st, t0, []Seen{gate("a")}, ok)); n != 0 {
		t.Fatalf("muted project sent %d", n)
	}
	st.Settings.Muted = nil
	if n := len(scan(st, t0.Add(time.Minute), []Seen{gate("a")}, ok)); n != 0 {
		t.Fatalf("unmuting sent the muted event: %d", n)
	}
	if n := len(scan(st, t0.Add(2*time.Minute), []Seen{gate("a"), gate("b")}, ok)); n != 1 {
		t.Fatalf("after unmute a new wait sent %d", n)
	}
}

func TestQuietWindowCrossesMidnight(t *testing.T) {
	q := Quiet{From: "22:00", To: "07:00"}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 2, h, m, 0, 0, time.Local) }
	for _, c := range []struct {
		t    time.Time
		want bool
	}{{at(21, 59), false}, {at(22, 0), true}, {at(23, 30), true}, {at(0, 0), true}, {at(6, 59), true}, {at(7, 0), false}, {at(12, 0), false}} {
		if q.In(c.t) != c.want {
			t.Errorf("In(%s) = %v", c.t.Format("15:04"), !c.want)
		}
	}
	day := Quiet{From: "13:00", To: "14:00"}
	if !day.In(at(13, 30)) || day.In(at(14, 0)) || (Quiet{}).In(at(1, 0)) || (Quiet{From: "x", To: "y"}).In(at(1, 0)) {
		t.Fatal("same-day or off window wrong")
	}
	if ValidQuiet(Quiet{From: "25:00", To: "07:00"}) || !ValidQuiet(Quiet{}) || !ValidQuiet(q) {
		t.Fatal("ValidQuiet")
	}
}

func TestQuietHoursHoldThenOneDigestAfterRestart(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	night := time.Date(2026, 11, 2, 23, 0, 0, 0, time.Local)
	morning := time.Date(2026, 11, 3, 7, 1, 0, 0, time.Local)
	a, b, c, m := gate("a"), gate("b"), gate("c"), Seen{Key: "muted|gate|x|y", Project: "muted", Kind: "gate", URL: "/p/muted/x"}
	step := func(now time.Time, seen []Seen, outcome webpush.Outcome) []Payload {
		var jobs []Job
		var pays []Payload
		if err := s.Update(func(st *State) error {
			st.Observe(seen, []string{"orchard", "muted"}, now)
			jobs = st.Plan(now)
			for _, j := range jobs {
				p, _ := st.PayloadFor(j)
				pays = append(pays, p)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.Update(func(st *State) error {
			for _, j := range jobs {
				st.Record(j, webpush.Result{Outcome: outcome}, now)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return pays
	}
	s.Update(func(st *State) error {
		st.Devices = []Device{{ID: "phone", Endpoint: "https://e/p", Label: "phone"}}
		st.Settings.Quiet = Quiet{From: "22:00", To: "07:00"}
		return nil
	})
	if p := step(night, []Seen{a, b, m}, webpush.Sent); len(p) != 0 {
		t.Fatalf("quiet hours sent %v", p)
	}
	if p := step(night.Add(time.Hour), []Seen{a, b, c, m}, webpush.Sent); len(p) != 0 {
		t.Fatalf("quiet hours sent %v", p)
	}
	s.Update(func(st *State) error { st.Settings.Muted = []string{"muted"}; return nil })
	// b was resolved overnight (gone two scans).
	step(night.Add(2*time.Hour), []Seen{a, c, m}, webpush.Sent)
	step(night.Add(3*time.Hour), []Seen{a, c, m}, webpush.Sent)
	// The viewer was off at 07:00; this is the first scan after (a new
	// Store reads the same files: a restart). The digest's first try fails.
	s = NewStore(dir)
	p := step(morning, []Seen{a, c, m}, webpush.Retry)
	if len(p) != 1 || p[0].Kind != "digest" || p[0].Title != "2 new things wait on you" || p[0].URL != "/needs-you" {
		t.Fatalf("morning sent %+v, want one digest of 2 (a, c; b resolved, muted left out)", p)
	}
	p = step(morning.Add(time.Minute), []Seen{a, c, m}, webpush.Sent)
	if len(p) != 1 || p[0].Kind != "digest" {
		t.Fatalf("digest retry sent %+v", p)
	}
	if p := step(morning.Add(2*time.Minute), []Seen{a, c, m}, webpush.Sent); len(p) != 0 {
		t.Fatalf("after the digest sent %+v", p)
	}
}

func TestCrashMidDigestRetriesTheDigestOnly(t *testing.T) {
	st := newState("phone")
	st.Settings.Quiet = Quiet{From: "22:00", To: "07:00"}
	night := time.Date(2026, 11, 2, 23, 0, 0, 0, time.Local)
	scan(st, night, []Seen{gate("a"), gate("b")}, ok)
	// Plan marks the digest sending; the process dies before Record.
	morning := time.Date(2026, 11, 3, 8, 0, 0, 0, time.Local)
	st.Observe([]Seen{gate("a"), gate("b")}, []string{"orchard"}, morning)
	if jobs := st.Plan(morning); len(jobs) != 1 {
		t.Fatalf("morning planned %d", len(jobs))
	}
	later := morning.Add(5 * time.Minute)
	st.Observe([]Seen{gate("a"), gate("b")}, []string{"orchard"}, later)
	jobs := st.Plan(later)
	if len(jobs) != 1 || !st.Events[jobs[0].EventKey].Digest {
		t.Fatalf("after a crash planned %v, want the digest again", jobs)
	}
}

func TestInFlightSendingIsNotDoubled(t *testing.T) {
	st := newState("phone")
	st.Observe([]Seen{gate("a")}, []string{"orchard"}, t0)
	if len(st.Plan(t0)) != 1 {
		t.Fatal("no job")
	}
	// A second scan (another viewer of the same config) 30 s later.
	if jobs := st.Plan(t0.Add(30 * time.Second)); len(jobs) != 0 {
		t.Fatalf("an in-flight push was planned again: %v", jobs)
	}
}

func TestPlanCapsSendsPerScan(t *testing.T) {
	st := newState("phone")
	var seen []Seen
	for i := 0; i < MaxSends+5; i++ {
		seen = append(seen, gate(string(rune('a'+i))))
	}
	if n := len(scan(st, t0, seen, ok)); n != MaxSends {
		t.Fatalf("first scan sent %d", n)
	}
	if n := len(scan(st, t0.Add(time.Minute), seen, ok)); n != 5 {
		t.Fatalf("second scan sent %d", n)
	}
}

func TestRotatedSubscriptionKeepsDeliveries(t *testing.T) {
	st := newState()
	st.Subscribe(Device{Endpoint: "https://e/old", Label: "phone"}, t0)
	scan(st, t0.Add(time.Minute), []Seen{gate("a")}, ok)
	if !st.Change("https://e/old", Device{Endpoint: "https://e/new"}, t0.Add(2*time.Minute)) {
		t.Fatal("change refused")
	}
	if n := len(scan(st, t0.Add(3*time.Minute), []Seen{gate("a")}, ok)); n != 0 {
		t.Fatalf("rotated device got the event again: %d", n)
	}
	if st.Change("https://e/unknown", Device{Endpoint: "https://e/x"}, t0) {
		t.Fatal("an unknown endpoint was swapped")
	}
}

func TestSubscribeDuringAScanKeepsItsSeed(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Update(func(st *State) error {
		st.Devices = []Device{{ID: "phone", Endpoint: "https://e/p", Label: "phone"}}
		return nil
	})
	a := gate("a")
	var jobs []Job
	s.Update(func(st *State) error {
		st.Observe([]Seen{a}, []string{"orchard"}, t0)
		jobs = st.Plan(t0)
		return nil
	})
	// While the scan's pushes are out, the desk subscribes.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Update(func(st *State) error {
			st.Subscribe(Device{Endpoint: "https://e/desk", Label: "desk"}, t0.Add(time.Second))
			return nil
		})
	}()
	wg.Wait()
	s.Update(func(st *State) error {
		for _, j := range jobs {
			st.Record(j, webpush.Result{Outcome: webpush.Sent}, t0.Add(2*time.Second))
		}
		return nil
	})
	var next []Job
	s.Update(func(st *State) error {
		st.Observe([]Seen{a}, []string{"orchard"}, t0.Add(time.Minute))
		next = st.Plan(t0.Add(time.Minute))
		return nil
	})
	if len(next) != 0 {
		t.Fatalf("the new device was flooded: %v", next)
	}
}

func TestStoreFilesAre0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "push")
	s := NewStore(dir)
	if err := s.Update(func(st *State) error { st.Subscribe(Device{Endpoint: "https://e/a", Label: "a"}, t0); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"subscriptions.json", "settings.json", "events.json"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, fi, err)
		}
	}
	// A corrupt ledger seeds instead of flooding.
	os.WriteFile(filepath.Join(dir, "events.json"), []byte("{"), 0o600)
	s.Update(func(st *State) error {
		if !st.NeedSeed {
			t.Fatal("corrupt ledger not seeded")
		}
		return nil
	})
}

func TestSafeURL(t *testing.T) {
	for in, want := range map[string]string{
		"/p/orchard/issue/a":   "/p/orchard/issue/a",
		"/needs-you":           "/needs-you",
		"https://evil.example": "/needs-you",
		"//evil.example/x":     "/needs-you",
		"/p//evil":             "/needs-you",
		"/p/../owner":          "/needs-you",
		"/owner":               "/needs-you",
	} {
		if got := SafeURL(in); got != want {
			t.Errorf("SafeURL(%q) = %q", in, got)
		}
	}
}

func TestStuckCooldownPingsOnce(t *testing.T) {
	st := newState("phone")
	stuck := Seen{Key: "orchard|stuck|agent-a|", Project: "orchard", Kind: "stuck", URL: "/p/orchard/agents", Cooldown: time.Hour}
	if n := len(scan(st, t0, []Seen{stuck}, ok)); n != 1 {
		t.Fatalf("stuck sent %d", n)
	}
	scan(st, t0.Add(time.Minute), nil, ok)
	scan(st, t0.Add(2*time.Minute), nil, ok) // working again: closed
	if n := len(scan(st, t0.Add(20*time.Minute), []Seen{stuck}, ok)); n != 0 {
		t.Fatalf("stuck again within the hour sent %d", n)
	}
	scan(st, t0.Add(21*time.Minute), nil, ok)
	scan(st, t0.Add(22*time.Minute), nil, ok)
	if n := len(scan(st, t0.Add(90*time.Minute), []Seen{stuck}, ok)); n != 1 {
		t.Fatalf("stuck after the cooldown sent %d", n)
	}
}
