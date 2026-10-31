package tracker

import (
	"regexp"
	"strings"
)

// The owner marker: which box text says the box is the owner's. One rule,
// read by proof.IsOwnerCriterion (and so proof.OwnerBoxWhy, every tick
// path, the criteria_proven gate, the YOLO queue and the owner-checks page)
// and by lint. A box is marked when:
//
//   - "(owner)" stands anywhere in it ("(owner) The owner picks …",
//     "… (owner)"), outside quotes and backticks: `"(owner)"` names the
//     marker, it doesn't mark the box;
//   - or it starts with "Owner " and names a check,
//     read-through, review or decision ("Owner visual check … (after ship)",
//     "Owner decides the price").
//
// Widening the rule only makes more boxes the owner's: an agent never
// ticks more because of it.

var (
	// quotedSpan is text in double quotes, curly quotes or backticks.
	quotedSpan = regexp.MustCompile("\"[^\"]*\"|“[^”]*”|`[^`]*`")
	// ownerLead is "Owner …" naming a check or a decision at the start of a
	// box. "checks" is left out: "Owner checks page …" is a page's name.
	ownerLead = regexp.MustCompile(`(?i)^owner\s+((visual|manual|final|hands-on|eyes-on|ui|ux|page|board)\s+)?` +
		`(check|look|read-through|read|review|reviews|decision|decides|picks|chooses|approves|approval|confirms|judges|judgement|sign-off|signs\s+off|playtest|walk-through|walks)\b`)
	// ownerLooking is wording that reads like a check only the owner does,
	// anywhere in a box: lint's hint, never the rule itself.
	ownerLooking = regexp.MustCompile(`(?i)\b(the\s+owner\s+(\w+\s+)?(checks|looks|reviews|reads|confirms|decides|picks|chooses|approves|judges|verifies|walks|signs\s+off)|owner'?s?\s+(visual\s+check|manual\s+check|read-through|sign-off|eyes)|visual\s+check\s+by\s+the\s+owner)\b`)
)

// unquoted is text with its quoted and backticked spans blanked.
func unquoted(text string) string {
	return quotedSpan.ReplaceAllString(text, " ")
}

// IsOwnerMarked reports whether a box's text marks it as the owner's: the
// rule above.
func IsOwnerMarked(text string) bool {
	t := strings.TrimSpace(unquoted(text))
	return strings.Contains(strings.ToLower(t), "(owner)") || ownerLead.MatchString(t)
}

// LooksLikeOwnerCheck reports whether a box reads like a check only the
// owner does ("the owner confirms it looks right") without the marker:
// lint warns on it, the rule doesn't count it.
func LooksLikeOwnerCheck(text string) bool {
	return !IsOwnerMarked(text) && ownerLooking.MatchString(unquoted(text))
}
