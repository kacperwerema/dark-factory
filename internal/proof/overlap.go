package proof

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Overlap is a criterion that is the owner's only because a playtest
// judgement step lists it in proves:, while a test or commit link proves it
// too. The judgement step wins (owner.go), so the test's pass never ticks
// the box and the issue can close with it open. A warning, not a gate: an
// owner's look on top of a test may be meant.
type Overlap struct {
	ID string `json:"id"`
	// Steps are the judgement steps linking it, as "TP4 (session s)".
	Steps []string `json:"steps"`
	// Links are the test and commit links, as Describe gives them.
	Links []string `json:"links"`
}

// Overlaps lists the unticked criteria a judgement step makes the owner's
// while a test or commit link (a Proves: trailer or issue-cli prove) also
// proves them. A criterion only a judgement step proves isn't one, nor one
// the owner already passed.
func (r Report) Overlaps() []Overlap {
	var out []Overlap
	for _, c := range r.Criteria {
		if c.Checked || c.OwnerProven() {
			continue
		}
		var o Overlap
		for _, l := range c.Links {
			switch {
			case l.Owner && !l.Stale:
				o.Steps = append(o.Steps, fmt.Sprintf("%s (session %s)", l.Ref, l.From))
			case !l.Owner && (l.Kind == tracker.ProofTest || l.Kind == tracker.ProofCommit):
				o.Links = append(o.Links, describe(l.Link))
			}
		}
		if len(o.Steps) > 0 && len(o.Links) > 0 {
			o.ID = c.ID
			out = append(out, o)
		}
	}
	return out
}

// Line is the warning for one overlap: what makes it the owner's, the link
// that proves it anyway, and the step to drop proves: from.
func (o Overlap) Line() string {
	return fmt.Sprintf("%s is the owner's only because judgement step %s lists it, while %s proves it too: drop %s from that step's proves: and re-run make ux-test, or keep it if the owner's look is meant (a warning, not a gate)",
		o.ID, strings.Join(o.Steps, ", "), strings.Join(o.Links, ", "), o.ID)
}
