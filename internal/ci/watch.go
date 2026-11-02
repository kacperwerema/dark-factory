package ci

import (
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// respawnAfter is how long a watcher waits before starting another run for a
// head it already started one for (the run may have failed before writing
// any state).
const respawnAfter = 5 * time.Minute

// Watcher starts a run when a project's branch head is untested and no run
// is live. The viewer and `issue-cli ci watch` both call Check on a tick;
// the lock makes sure only one run starts.
type Watcher struct {
	// Spawn starts a detached `issue-cli ci run` for the project.
	Spawn func(root string) error
	// SpawnTier starts a detached `issue-cli ci run --tier <tier>`, for
	// CheckNightly; nil turns the nightly run off.
	SpawnTier func(root, tier string) error
	// Memory says whether a nightly run may start now (mem-guard check) and,
	// when not, why. nil always allows it.
	Memory func() (bool, string)
	Now    func() time.Time

	mu      sync.Mutex
	spawned map[string]spawnRecord
	// nightly is the slot each root's nightly run was started for: one
	// start per night, even when the run dies before writing its state.
	nightly map[string]time.Time
}

type spawnRecord struct {
	head string
	at   time.Time
}

func (w *Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Check looks at root's branch once and spawns a run when it needs one. It
// returns the head and whether it spawned.
func (w *Watcher) Check(root string, cfg *tracker.CIConfig) (string, bool, error) {
	if !cfg.Enabled() {
		return "", false, nil
	}
	branch, err := ResolveBranch(root, cfg)
	if err != nil {
		return "", false, err
	}
	head, err := Head(root, branch)
	if err != nil {
		return "", false, err
	}
	if _, alive := ReadLock(root); alive {
		return head, false, nil
	}
	if st, _ := ReadState(root); st != nil && st.LastSha == head {
		return head, false, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.spawned == nil {
		w.spawned = map[string]spawnRecord{}
	}
	if rec, ok := w.spawned[root]; ok && rec.head == head && w.now().Sub(rec.at) < respawnAfter {
		return head, false, nil
	}
	if err := w.Spawn(root); err != nil {
		return head, false, err
	}
	w.spawned[root] = spawnRecord{head: head, at: w.now()}
	return head, true, nil
}

// Nightly is what CheckNightly decided, for the caller's log line.
type Nightly struct {
	Tier    string
	Head    string
	Spawned bool
	// Held says why a due run did not start ("mem-guard says no"), "" when
	// it started or nothing was due.
	Held string
}

// CheckNightly starts cfg's nightly tier (ci.nightly) once a night: after
// today's at: (or before it, when yesterday's slot was missed), when no run
// is live, the tier has not run since the slot, and Memory (mem-guard)
// allows it. SpawnTier starts the detached run.
func (w *Watcher) CheckNightly(root string, cfg *tracker.CIConfig) (Nightly, error) {
	tier := cfg.NightlyTier()
	n := Nightly{Tier: tier}
	if !cfg.Enabled() || tier == "" || w.SpawnTier == nil || !hasTier(cfg, tier) {
		return n, nil
	}
	now := w.now()
	slot := NightlySlot(cfg, now)
	st, _ := ReadState(root)
	if ts, ok := st.Tier(tier); ok && !ts.Finished.Before(slot) {
		return n, nil
	}
	branch, err := ResolveBranch(root, cfg)
	if err != nil {
		return n, err
	}
	head, err := Head(root, branch)
	if err != nil {
		return n, err
	}
	n.Head = head
	if _, alive := ReadLock(root); alive {
		return n, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.nightly == nil {
		w.nightly = map[string]time.Time{}
	}
	if w.nightly[root].Equal(slot) {
		return n, nil
	}
	if w.Memory != nil {
		if ok, why := w.Memory(); !ok {
			n.Held = why
			return n, nil
		}
	}
	if err := w.SpawnTier(root, tier); err != nil {
		return n, err
	}
	w.nightly[root] = slot
	n.Spawned = true
	return n, nil
}

// NightlySlot is the newest nightly start time at or before now: today's
// at: once it has passed, else yesterday's.
func NightlySlot(cfg *tracker.CIConfig, now time.Time) time.Time {
	h, m := cfg.NightlyClock()
	slot := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if slot.After(now) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}
