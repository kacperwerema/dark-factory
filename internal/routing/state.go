// Package routing picks which of the owner's installed agent CLIs (claude,
// codex) starts the next worker, lead or job run, from a workflow file's
// routing: rules, and keeps which subscription is resting after its CLI
// showed a usage-limit message. It never retries a request, moves a running
// worker or touches credentials: it only chooses the next start's command.
// See docs/agent-dispatch.md, "Routing between Claude and Codex".
package routing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/michal-franc/issue-viewer/internal/viewerstate"
)

// Rest is one subscription resting until its reset.
type Rest struct {
	Agent string    `json:"agent"`
	Until time.Time `json:"until"`
	// KnownReset is false when the message named no time (Until is a guess).
	KnownReset bool      `json:"known_reset"`
	SeenAt     time.Time `json:"seen_at"`
	// Session and Project are where the message was seen; By is "scan" or
	// "owner" (issue-cli agents rest).
	Session string `json:"session,omitempty"`
	Project string `json:"project,omitempty"`
	Line    string `json:"line,omitempty"`
	By      string `json:"by"`
}

// seen is one limit message already recorded, so the same message still on
// a pane after its reset doesn't start a new rest.
type seen struct {
	Session string    `json:"session"`
	Line    string    `json:"line"`
	At      time.Time `json:"at"`
}

// State is $XDG_STATE_HOME/ai-workflow-engine/subscriptions/state.json: the
// machine's subscriptions, shared by every project (a subscription is the
// owner's login on this machine, not a project's).
type State struct {
	Rests map[string]Rest `json:"rests"`
	Seen  []seen          `json:"seen,omitempty"`
}

// seenFor is how long a recorded message is remembered.
const seenFor = 24 * time.Hour

// Dir is where the state lives; "" without a home directory.
func Dir() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "subscriptions")
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

// Load reads the state; an empty one when there is none.
func Load(dir string) (*State, error) {
	st := &State{Rests: map[string]Rest{}}
	if dir == "" {
		return st, nil
	}
	data, err := os.ReadFile(statePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return &State{Rests: map[string]Rest{}}, err
	}
	if st.Rests == nil {
		st.Rests = map[string]Rest{}
	}
	return st, nil
}

// Resting is agent's rest when it hasn't ended at now.
func (s *State) Resting(agent string, now time.Time) (Rest, bool) {
	if s == nil {
		return Rest{}, false
	}
	r, ok := s.Rests[agent]
	if !ok || !now.Before(r.Until) {
		return Rest{}, false
	}
	return r, true
}

// Active lists the rests not ended at now, by agent.
func (s *State) Active(now time.Time) []Rest {
	var out []Rest
	if s == nil {
		return out
	}
	for _, r := range s.Rests {
		if now.Before(r.Until) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}

// update runs fn on the state under the directory's lock and writes the
// result atomically when fn reports a change.
func update(dir string, fn func(*State) bool) (*State, error) {
	if dir == "" {
		return nil, errors.New("no state directory for subscriptions (HOME unset)")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	st, err := Load(dir)
	if err != nil {
		st = &State{Rests: map[string]Rest{}}
	}
	if !fn(st) {
		return st, nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp := statePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	return st, os.Rename(tmp, statePath(dir))
}

// Mark records a limit seen in project's session. A message already
// recorded from that session in the last day is skipped (it is still on the
// pane), and a rest is only ever lengthened by a message, never shortened.
// changed reports whether a new rest started.
func Mark(dir string, l Limit, project, session string, now time.Time) (changed bool, err error) {
	_, err = update(dir, func(st *State) bool {
		kept := st.Seen[:0]
		for _, s := range st.Seen {
			if now.Sub(s.At) < seenFor {
				kept = append(kept, s)
			}
		}
		st.Seen = kept
		for _, s := range st.Seen {
			if s.Session == session && s.Line == l.Line {
				return false
			}
		}
		st.Seen = append(st.Seen, seen{Session: session, Line: l.Line, At: now})
		if old, ok := st.Rests[l.Agent]; !ok || old.Until.Before(l.Until) {
			st.Rests[l.Agent] = Rest{Agent: l.Agent, Until: l.Until, KnownReset: l.KnownReset, SeenAt: now,
				Session: session, Project: project, Line: l.Line, By: "scan"}
			changed = true
		}
		return true
	})
	return changed, err
}

// SetRest is the owner's rest by hand (issue-cli agents rest).
func SetRest(dir, agent string, until, now time.Time) error {
	_, err := update(dir, func(st *State) bool {
		st.Rests[agent] = Rest{Agent: agent, Until: until, KnownReset: true, SeenAt: now, By: "owner"}
		return true
	})
	return err
}

// Clear ends agent's rest (issue-cli agents clear); found is false when it
// wasn't resting.
func Clear(dir, agent string, now time.Time) (found bool, err error) {
	_, err = update(dir, func(st *State) bool {
		r, ok := st.Rests[agent]
		found = ok && now.Before(r.Until)
		if !ok {
			return false
		}
		delete(st.Rests, agent)
		return true
	})
	return found, err
}
