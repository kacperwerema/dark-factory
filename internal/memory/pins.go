package memory

import "unicode/utf8"

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
