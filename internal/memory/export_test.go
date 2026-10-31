package memory

import (
	"fmt"
	"time"
)

// seed writes n plain add events at once, for size tests.
func (s Store) seed(n int, now time.Time) error {
	var evs []Event
	for i := 0; i < n; i++ {
		m := Memory{ID: fmt.Sprintf("m-%06x", i), Text: fmt.Sprintf("fact %d about the playtest runner and the board number %d", i, i%37), Kind: KindFact, Scope: ScopeProject, Created: now.Format(time.RFC3339)}
		m.Hash = Hash(m.Text)
		evs = append(evs, Event{Op: OpAdd, ID: m.ID, At: m.Created, Memory: &m})
	}
	return s.appendEvents(evs...)
}
