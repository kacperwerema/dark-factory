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
	// At is the status it matters at; the caller checks it against the
	// workflows, which this package doesn't read.
	At         string
	SuggestPin bool
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
	// Yes lets ModeUpdates replace a text it shares under UpdateThreshold
	// of its words with (refused with UnrelatedUpdateError otherwise).
	Yes bool
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

// Containment thresholds of the update step: a fact retold in other words
// keeps most of the shorter text's words while its Jaccard stays under
// SimilarThreshold (m-60b23e beside m-7bffcb: 0.43, yet 0.69 of the
// shorter one's words). On the project's 320 memories (2026-11-22) 0.65
// named only the two known retold pairs; the next pair was at 0.59.
const (
	// ContainedThreshold is the share of the shorter memory's distinct
	// words the other must hold to be a near-duplicate.
	ContainedThreshold = 0.65
	// ContainedMinWords: a shorter memory with fewer distinct words is
	// never a near-duplicate by containment, so a short fact inside a long
	// one is not refused.
	ContainedMinWords = 8
)

// Contained is the share of the shorter text's distinct words found in the
// other, 0 when the shorter has fewer than ContainedMinWords.
func Contained(a, b string) float64 {
	wa, wb := wordSet(a), wordSet(b)
	if len(wb) < len(wa) {
		wa, wb = wb, wa
	}
	if len(wa) < ContainedMinWords {
		return 0
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(wa))
}

// nearDuplicate is the score at which text is a near-duplicate of other
// (its Jaccard, else its containment), 0 when it is neither.
func nearDuplicate(text, other string) float64 {
	if sim := Similarity(text, other); sim >= SimilarThreshold {
		return sim
	}
	if c := Contained(text, other); c >= ContainedThreshold {
		return c
	}
	return 0
}

// SimilarError is an add refused because near-duplicates exist; the agent
// reruns with --updates, --supersedes or --new (--force).
type SimilarError struct {
	Similar []Hit
	// Text is the refused memory's text, for the overlap figures.
	Text string
}

func (e *SimilarError) Error() string {
	var b strings.Builder
	b.WriteString("similar memories exist, so nothing was written. Decide like mem0's update step and run add again with one of:\n")
	for _, h := range e.Similar {
		fmt.Fprintf(&b, "  %s (%s, %s): %s\n", h.ID, h.Kind, overlapWords(e.Text, h), h.Text)
	}
	id := e.Similar[0].ID
	fmt.Fprintf(&b, "  --updates %s     the same fact, better worded: replaces its whole text (history kept)\n", id)
	fmt.Fprintf(&b, "  --supersedes %s  the same subject, no longer true: yours replaces it (it stays in history, invalid)\n", id)
	fmt.Fprintf(&b, "  --new             a different fact: add it alongside (not --updates, which would overwrite %s); --force is the same", id)
	return b.String()
}

// overlapWords is how much h shares with text, in words: the Jaccard, and
// the containment when that is what made it a near-duplicate.
func overlapWords(text string, h Hit) string {
	j := Similarity(text, h.Text)
	if text == "" || j >= SimilarThreshold {
		return fmt.Sprintf("%.0f%% the same words", h.Score*100)
	}
	return fmt.Sprintf("%.0f%% the same words, %.0f%% of the shorter one's", j*100, Contained(text, h.Text)*100)
}

// UpdateThreshold is the word overlap (Jaccard) under which --updates is
// refused: the new text is not the same fact reworded.
const UpdateThreshold = 1.0 / 3

// UnrelatedUpdateError is an --updates refused because the new text shares
// under UpdateThreshold of its words with the one it would replace.
type UnrelatedUpdateError struct {
	Old   Memory
	Text  string
	Score float64
}

func (e *UnrelatedUpdateError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "--updates %s replaces its whole text, and yours shares only %.0f%% of its words (under a third), so nothing was written.\n", e.Old.ID, e.Score*100)
	fmt.Fprintf(&b, "  %s now: %s\n", e.Old.ID, e.Old.Text)
	fmt.Fprintf(&b, "  yours:   %s\n", e.Text)
	b.WriteString("Run add again with one of:\n")
	fmt.Fprintf(&b, "  --new             a different fact: add it alongside, %s stays as it is\n", e.Old.ID)
	fmt.Fprintf(&b, "  --supersedes %s  %s is no longer true: yours replaces it (it stays in history, invalid)\n", e.Old.ID, e.Old.ID)
	fmt.Fprintf(&b, "  --updates %s --yes  the same fact after all: replace its text (history kept)", e.Old.ID)
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

// Similar is the valid memories in scope that text near-duplicates (words
// overlapping at least SimilarThreshold, or the shorter one's words held
// at least ContainedThreshold), most similar first, at most limit.
func (s *Set) Similar(text, scope string, now time.Time, limit int) []Hit {
	var hits []Hit
	for _, m := range s.Active(now) {
		if m.Scope != scope {
			continue
		}
		if sim := nearDuplicate(text, m.Text); sim > 0 {
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
			if sim := Similarity(m.Text, d.Text); !opt.Yes && sim < UpdateThreshold && Hash(m.Text) != Hash(d.Text) {
				return &UnrelatedUpdateError{Old: *m, Text: d.Text, Score: sim}
			}
			evs := []Event{{Op: OpUpdate, ID: m.ID, At: at, By: d.Author, Old: m.Text, Text: d.Text}}
			if d.At != "" {
				evs = append(evs, Event{Op: OpSetAt, ID: m.ID, At: at, By: d.Author, Status: d.At})
				m.At = d.At
			}
			if err := s.appendEvents(evs...); err != nil {
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
			for _, old := range olds { // a merge keeps the step they mattered at
				if m.At == "" {
					m.At = old.At
				}
			}
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
					if d.At != "" && d.At != m.At { // the same fact, now tied to a step
						if err := s.appendEvents(Event{Op: OpSetAt, ID: m.ID, At: at, By: d.Author, Status: d.At}); err != nil {
							return err
						}
						m.At = d.At
					}
					res = AddResult{Op: OpNoop, Memory: m}
					return nil
				}
			}
			if sim := set.Similar(d.Text, d.Scope, now, 3); len(sim) > 0 {
				return &SimilarError{Similar: sim, Text: d.Text}
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
		Source: d.Source, Author: d.Author, Created: at, Expires: d.Expires, At: d.At,
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
