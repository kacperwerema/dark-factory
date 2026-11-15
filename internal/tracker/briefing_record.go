package tracker

import (
	"time"

	"github.com/kacperwerema/dark-factory/internal/tokens"
)

// Briefing is one briefing really sent to an issue's agent: its size and
// the ids it carried, never its text. A launched dispatch, a delivered
// reprompt and issue-cli start write one (RecordBriefing); reads (show,
// dispatch-prompt, critique --brief) never do. The trace's spans
// (internal/trace) carry them by status visit.
type Briefing struct {
	At time.Time `json:"at"`
	// Kind is BriefingDispatch, BriefingReprompt or BriefingStart.
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Session string `json:"session,omitempty"`
	Bytes   int    `json:"bytes"`
	// Tokens is tokens.Estimate of the text: an estimate, not a count.
	Tokens int `json:"tokens"`
	// Memories are the memory ids the briefing showed; Decisions the
	// decision refs its block carried.
	Memories  []string `json:"memories,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
}

// NewBriefing is the record of text sent as kind at status: its bytes and
// estimated tokens, not the text.
func NewBriefing(kind, status, session, text string, memories, decisions []string, at time.Time) Briefing {
	return Briefing{At: at.UTC(), Kind: kind, Status: status, Session: session, Bytes: len(text),
		Tokens: tokens.Estimate(text), Memories: memories, Decisions: decisions}
}

// RecordBriefing appends one briefing to the issue's stats sidecar.
func RecordBriefing(issuePath string, b Briefing) error {
	return withIssueLock(issuePath, func() error {
		store, err := LoadStats(issuePath)
		if err != nil {
			return err
		}
		b.At = b.At.UTC()
		store.Briefings = append(store.Briefings, b)
		return SaveStats(issuePath, store)
	})
}
