package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Draft is a memory an agent wants to write.
type Draft struct {
	Text, Kind, Scope           string
	Project, System, Department string
	Author                      string
	Tags, Links                 []string
	Source                      Source
	Expires                     string
	SuggestPin                  bool
}

// Modes of the update step: what the agent decided after seeing similar
// memories.
const (
	// ModeCheck (the default): ADD unless the same fact exists (NOOP) or a
	// near-duplicate does (refused with SimilarError).
	ModeCheck = iota
	// ModeNew: ADD even though similar ones exist.
	ModeNew
	// ModeUpdates: UPDATE Target's text in place.
	ModeUpdates
	// ModeSupersedes: ADD, and invalidate Target (superseded, linked).
	ModeSupersedes
)

// AddOptions are the update step's decision.
type AddOptions struct {
	Mode   int
	Target string
	// Targets are further memories a supersede invalidates (a merge of
	// near-duplicates: one new fact replaces them all).
	Targets []string
}

// targets is Target then Targets, without repeats or blanks.
func (o AddOptions) targets() []string {
	var out []string
	for _, id := range append([]string{o.Target}, o.Targets...) {
		if id = strings.TrimSpace(id); id != "" {
			out = appendUnique(out, id)
		}
	}
	return out
}

// AddResult is what the add did: OpAdd, OpNoop, OpUpdate or OpSupersede.
type AddResult struct {
	Op     string
	Memory Memory
}

// SimilarThreshold is the word overlap (Jaccard) at which a new memory is
// a near-duplicate the agent must decide on.
const SimilarThreshold = 0.5

// SimilarError is an add refused because near-duplicates exist; the agent
// reruns with --updates, --supersedes or --new.
type SimilarError struct{ Similar []Hit }

func (e *SimilarError) Error() string {
	var b strings.Builder
	b.WriteString("similar memories exist, so nothing was written. Decide like mem0's update step and run add again with one of:\n")
	for _, h := range e.Similar {
		fmt.Fprintf(&b, "  %s (%s, %.0f%% the same words): %s\n", h.ID, h.Kind, h.Score*100, h.Text)
	}
	id := e.Similar[0].ID
	fmt.Fprintf(&b, "  --updates %s     the same fact, better worded: replace its text (history kept)\n", id)
	fmt.Fprintf(&b, "  --supersedes %s  it is no longer true: yours replaces it (it stays in history, invalid)\n", id)
	b.WriteString("  --new             a different fact after all: add it alongside")
	return b.String()
}

// Hash is the identity of a fact's words: case, spacing and punctuation
// aside.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(strings.Join(tokens(text), " ")))
	return hex.EncodeToString(sum[:8])
}

// Similarity is the Jaccard overlap of a's and b's words.
func Similarity(a, b string) float64 {
	wa, wb := wordSet(a), wordSet(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(wa)+len(wb)-inter)
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range Words(s) {
		out[w] = true
	}
	return out
}

// Similar is the valid memories in scope whose words overlap text's at
// least SimilarThreshold, most similar first, at most limit.
func (s *Set) Similar(text, scope string, now time.Time, limit int) []Hit {
	var hits []Hit
	for _, m := range s.Active(now) {
		if m.Scope != scope {
			continue
		}
		if sim := Similarity(text, m.Text); sim >= SimilarThreshold {
			hits = append(hits, Hit{Memory: m, Score: sim})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// check normalises d and says why it can't be written.
func (d *Draft) check(machine bool) error {
	d.Text = strings.TrimSpace(d.Text)
	if err := CheckText(d.Text); err != nil {
		return err
	}
	if d.Kind == "" {
		d.Kind = KindFact
	}
	if err := CheckKind(d.Kind); err != nil {
		return err
	}
	switch {
	case machine:
		d.Scope = ScopeMachine
	case d.Scope == "":
		d.Scope = ScopeProject
	case d.Scope == ScopeMachine:
		return fmt.Errorf("a machine memory goes into the machine store, not the project's")
	}
	if err := CheckScope(d.Scope); err != nil {
		return err
	}
	if d.Expires != "" {
		if _, err := time.Parse("2006-01-02", d.Expires); err != nil {
			return fmt.Errorf("expires %q: give a date as YYYY-MM-DD", d.Expires)
		}
	}
	for _, t := range d.Tags {
		if err := CheckText(t); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
	}
	return nil
}

// Add runs the update step and writes what it decides.
func (s Store) Add(d Draft, opt AddOptions, now time.Time) (AddResult, error) {
	if err := d.check(s.Machine); err != nil {
		return AddResult{}, err
	}
	var res AddResult
	err := s.locked(func() error {
		set, err := s.Load()
		if err != nil {
			return err
		}
		at := stamp(now)
		switch opt.Mode {
		case ModeUpdates:
			m := set.Get(opt.Target)
			if m == nil {
				return fmt.Errorf("%w %q", ErrNotFound, opt.Target)
			}
			e := Event{Op: OpUpdate, ID: m.ID, At: at, By: d.Author, Old: m.Text, Text: d.Text}
			if err := s.appendEvents(e); err != nil {
				return err
			}
			m.Text, m.Hash, m.Updated = d.Text, Hash(d.Text), at
			res = AddResult{Op: OpUpdate, Memory: *m}
			return nil
		case ModeSupersedes:
			ids := opt.targets()
			if len(ids) == 0 {
				return fmt.Errorf("%w: --supersedes names no memory", ErrNotFound)
			}
			var olds []*Memory
			for _, id := range ids {
				old := set.Get(id)
				if old == nil {
					return fmt.Errorf("%w %q", ErrNotFound, id)
				}
				olds = append(olds, old)
			}
			m := s.newMemory(d, set, at)
			m.Supersedes = olds[0].ID
			for _, old := range olds {
				m.Links = appendUnique(m.Links, old.ID)
			}
			evs := []Event{{Op: OpAdd, ID: m.ID, At: at, By: d.Author, Memory: &m}}
			for _, old := range olds {
				evs = append(evs, Event{Op: OpSupersede, ID: old.ID, At: at, By: d.Author, Target: m.ID, Reason: "superseded by " + m.ID})
			}
			if err := s.appendEvents(evs...); err != nil {
				return err
			}
			res = AddResult{Op: OpSupersede, Memory: m}
			return nil
		case ModeCheck:
			h := Hash(d.Text)
			for _, m := range set.Active(now) {
				if m.Hash == h && m.Scope == d.Scope {
					res = AddResult{Op: OpNoop, Memory: m}
					return nil
				}
			}
			if sim := set.Similar(d.Text, d.Scope, now, 3); len(sim) > 0 {
				return &SimilarError{Similar: sim}
			}
		}
		m := s.newMemory(d, set, at)
		if err := s.appendEvents(Event{Op: OpAdd, ID: m.ID, At: at, By: d.Author, Memory: &m}); err != nil {
			return err
		}
		res = AddResult{Op: OpAdd, Memory: m}
		return nil
	})
	return res, err
}

func (s Store) newMemory(d Draft, set *Set, at string) Memory {
	return Memory{
		ID: newID(s.Machine, set), Text: d.Text, Kind: d.Kind, Scope: d.Scope,
		Project: d.Project, System: d.System, Department: d.Department, Tags: d.Tags,
		Source: d.Source, Author: d.Author, Created: at, Expires: d.Expires,
		Links: d.Links, SuggestPin: d.SuggestPin, Hash: Hash(d.Text),
	}
}

// newID is a short id not in set (deleted ones included): m-<6 hex> for a
// project memory, s-<6 hex> for a machine one.
func newID(machine bool, set *Set) string {
	prefix := "m-"
	if machine {
		prefix = "s-"
	}
	taken := map[string]bool{}
	for _, e := range set.events {
		taken[e.ID] = true
	}
	for {
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		id := prefix + hex.EncodeToString(b)
		if !taken[id] {
			return id
		}
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
