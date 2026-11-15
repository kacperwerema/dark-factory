package memory

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Pins is the pinned memories that fit a briefing.
type Pins struct {
	Shown []Memory
	// More is how many pinned memories did not fit.
	More int
}

// SplitPins takes the pinned memories out of mems: at most limit of them
// and chars characters in all (Letta's core memory budget), newest pin
// first as mems is ordered; rest is everything else.
func SplitPins(mems []Memory, limit, chars int) (Pins, []Memory) {
	var p Pins
	var rest []Memory
	used := 0
	for _, m := range mems {
		if !m.Pinned {
			rest = append(rest, m)
			continue
		}
		n := utf8.RuneCountInString(m.Text)
		if len(p.Shown) >= limit || used+n > chars {
			p.More++
			continue
		}
		used += n
		p.Shown = append(p.Shown, m)
	}
	return p, rest
}

// ForStatus takes the memories that matter at status out of mems, strongest
// first: at most limit of them and chars characters in all. A pinned memory
// is not one of them, since the briefing carries it, nor is one no longer
// valid. rest is everything else, those that did not fit included.
func ForStatus(mems []Memory, status string, now time.Time, limit, chars int) (Pins, []Memory) {
	at, rest := stageSplit(mems, status, nil, now, false)
	return capAt(at, rest, status, nil, now, limit, chars)
}

// ForStage is what a stage at status gets from mems (memory as a cache):
// the memories --at status, the most useful there first (then the
// strongest), within limit and chars, as Pins; rest is what the caller may
// still rank for the issue, without the memories --at another status and
// those measured unhelpful at this one (a memory with no data at status
// still reaches it, so a new one can prove itself). A pinned memory is
// never filtered: it stays in rest for the briefing's pins.
func ForStage(mems []Memory, status string, scores Scores, now time.Time, limit, chars int) (Pins, []Memory) {
	at, rest := stageSplit(mems, status, scores, now, true)
	return capAt(at, rest, status, scores, now, limit, chars)
}

// capAt orders at (the most useful at status, then the strongest) and
// keeps what fits limit and chars; the rest go back to rest.
func capAt(at, rest []Memory, status string, scores Scores, now time.Time, limit, chars int) (Pins, []Memory) {
	sort.SliceStable(at, func(i, j int) bool {
		li, lj := scores.LiftAt(at[i], status), scores.LiftAt(at[j], status)
		if li != lj {
			return li > lj
		}
		return Strength(at[i], now) > Strength(at[j], now)
	})
	var p Pins
	used := 0
	for _, m := range at {
		n := utf8.RuneCountInString(m.Text)
		if len(p.Shown) >= limit || used+n > chars {
			p.More++
			rest = append(rest, m)
			continue
		}
		used += n
		p.Shown = append(p.Shown, m)
	}
	return p, rest
}

// stageSplit is the memories --at status, and the rest. For a stage
// (ForStage) the rest leaves out memories --at another status and those
// measured unhelpful at this one.
func stageSplit(mems []Memory, status string, scores Scores, now time.Time, stage bool) (at, rest []Memory) {
	for _, m := range mems {
		switch {
		case m.Pinned:
			rest = append(rest, m)
		case m.At != "" && strings.EqualFold(m.At, status):
			if m.Valid(now) {
				at = append(at, m)
			} else {
				rest = append(rest, m)
			}
		case !stage:
			rest = append(rest, m)
		case m.At != "":
			// --at another status: it matters at that step, not this one.
		case scores.UnhelpfulAt(m, status):
		default:
			rest = append(rest, m)
		}
	}
	return at, rest
}
