package tracker

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Decision challenges (docs/workflow.md#decision-ledger): an agent that
// thinks a decision is wrong names its ref and why with issue-cli decision
// challenge. The state is the decisions log: a challenge is open while a
// done "challenged" event for (ref, issue) has no later done "resolved"
// one. The challenge: and challenge-resolved: comments are the trail
// people read; anyone can type a comment, so no comment opens or closes
// anything. Only the owner resolves (keep, or a replacing decision), and
// the decision_unchallenged rule holds only the challenger's own issue.

// How a challenge was resolved (the challenged and resolved events are
// DecisionChallenged and DecisionResolved in decision_log.go).
const (
	ChallengeKept     = "kept"
	ChallengeReplaced = "replaced"

	// DecisionUnchallengedRule holds the challenger's issue while its
	// challenge is open.
	DecisionUnchallengedRule = "decision_unchallenged"

	ChallengePrefix         = "challenge:"
	ChallengeResolvedPrefix = "challenge-resolved:"
)

// Challenge is one open challenge on a decision.
type Challenge struct {
	Ref     string    `json:"ref"`
	Issue   string    `json:"issue"` // the challenger's issue
	Why     string    `json:"why"`
	Propose string    `json:"propose,omitempty"`
	By      string    `json:"by,omitempty"`
	Since   time.Time `json:"since"`
}

func challengeKey(ref, issue string) string { return ref + "\x00" + issue }

// OpenChallenges folds done log events into the challenges still open,
// oldest first.
func OpenChallenges(events []DecisionEvent) []Challenge {
	open := map[string]Challenge{}
	for _, ev := range events {
		if ev.Op != DecisionOpDone {
			continue
		}
		key := challengeKey(ev.Ref, ev.Issue)
		switch ev.Event {
		case DecisionChallenged:
			at, _ := time.Parse(time.RFC3339, ev.At)
			open[key] = Challenge{Ref: ev.Ref, Issue: ev.Issue, Why: ev.Why, Propose: ev.Propose, By: ev.By, Since: at}
		case DecisionResolved:
			delete(open, key)
		}
	}
	out := make([]Challenge, 0, len(open))
	for _, c := range open {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return challengeKey(out[i].Ref, out[i].Issue) < challengeKey(out[j].Ref, out[j].Issue)
	})
	return out
}

// LoadOpenChallenges is the open challenges in issueDir's decisions log.
func LoadOpenChallenges(issueDir string) ([]Challenge, error) {
	if issueDir == "" {
		return nil, nil
	}
	evs, err := ReadDecisionLog(issueDir)
	return OpenChallenges(evs), err
}

// ChallengesOn is the open challenges on ref.
func ChallengesOn(open []Challenge, ref string) []Challenge {
	var out []Challenge
	for _, c := range open {
		if c.Ref == ref {
			out = append(out, c)
		}
	}
	return out
}

// ChallengesBy is the open challenges the issue made.
func ChallengesBy(open []Challenge, issue string) []Challenge {
	var out []Challenge
	for _, c := range open {
		if strings.EqualFold(c.Issue, issue) {
			out = append(out, c)
		}
	}
	return out
}

// ChallengeComment is the comment a challenge posts on the challenger's
// issue and on the decision's:
//
//	challenge: <ref> — <why>[ · propose: <proposal>] (for <issue>, by <who>)
func ChallengeComment(c Challenge) string {
	s := fmt.Sprintf("%s %s — %s", ChallengePrefix, c.Ref, oneLine(c.Why))
	if p := strings.TrimSpace(c.Propose); p != "" {
		s += " · propose: " + oneLine(p)
	}
	by := c.By
	if by == "" {
		by = "owner"
	}
	return s + fmt.Sprintf(" (for %s, by %s)", c.Issue, by)
}

// ChallengeResolvedComment is the comment on each challenger's issue when
// the owner answers:
//
//	challenge-resolved: <ref> kept[, "<words>"] — <who>
//	challenge-resolved: <ref> replaced by <new ref>[, "<words>"] — <who>
func ChallengeResolvedComment(ref, how, byRef, words, who string) string {
	s := ChallengeResolvedPrefix + " " + ref + " " + how
	if how == ChallengeReplaced && byRef != "" {
		s += " by " + byRef
	}
	if w := strings.TrimSpace(words); w != "" {
		s += fmt.Sprintf(", %q", oneLine(w))
	}
	return s + " — " + who
}

// ChallengeWrite says where a challenge write goes: the log's issues dir,
// who runs it, and each issue's file by slug.
type ChallengeWrite struct {
	IssueDir string
	Actor    DecisionActor
	// File is an issue's file by slug ("" when the issue isn't found).
	File func(slug string) string
	// Author is the comments' author ("" for none).
	Author string
	Source string
}

func (w ChallengeWrite) source() string {
	if w.Source == "" {
		return "cli"
	}
	return w.Source
}

// runChallengeWrite appends the events pending, posts each comment, and
// appends them done, or failed with the first comment error.
func runChallengeWrite(w ChallengeWrite, evs []DecisionEvent, now time.Time, comments []challengeNote) error {
	if w.IssueDir == "" {
		return fmt.Errorf("the decisions log needs the project's issues folder")
	}
	id := newWriteID()
	for i := range evs {
		evs[i].Write, evs[i].At, evs[i].By, evs[i].Role = id, now.Format(time.RFC3339), w.Actor.Name, w.Actor.Role
	}
	if err := AppendDecisionEvents(w.IssueDir, withOp(evs, DecisionOpPending, "")); err != nil {
		return fmt.Errorf("decisions log: %w", err)
	}
	var err error
	for _, n := range comments {
		if n.Path == "" {
			continue
		}
		if err = AddCommentBy(n.Path, 0, n.Text, w.source(), w.Author); err != nil {
			err = fmt.Errorf("commenting on %s: %w", n.Slug, err)
			break
		}
	}
	op, msg := DecisionOpDone, ""
	if err != nil {
		op, msg = DecisionOpFailed, err.Error()
	}
	if lerr := AppendDecisionEvents(w.IssueDir, withOp(evs, op, msg)); lerr != nil && err == nil {
		err = fmt.Errorf("the challenge is commented, but its done event is not in the decisions log: %w", lerr)
	}
	return err
}

type challengeNote struct{ Slug, Path, Text string }

// OpenChallenge records c (Ref, Issue, Why, Propose): the challenged event
// and the challenge: comment on the challenger's issue and the decision's.
func OpenChallenge(w ChallengeWrite, c Challenge, now time.Time) error {
	if strings.TrimSpace(c.Why) == "" {
		return fmt.Errorf("a challenge needs --why")
	}
	c.By = w.Actor.Name
	text := ChallengeComment(c)
	notes := []challengeNote{{Slug: c.Issue, Path: w.File(c.Issue), Text: text}}
	if decSlug := refSlug(c.Ref); !strings.EqualFold(decSlug, c.Issue) {
		notes = append(notes, challengeNote{Slug: decSlug, Path: w.File(decSlug), Text: text})
	}
	ev := DecisionEvent{Event: DecisionChallenged, Ref: c.Ref, Issue: c.Issue, Why: oneLine(c.Why), Propose: oneLine(c.Propose)}
	return runChallengeWrite(w, []DecisionEvent{ev}, now, notes)
}

// ResolveChallenges closes each challenge as kept or replaced (by byRef),
// with the owner's words if any: resolved events and a challenge-resolved:
// comment on each challenger's issue.
func ResolveChallenges(w ChallengeWrite, list []Challenge, how, byRef, words string, now time.Time) error {
	if len(list) == 0 {
		return nil
	}
	who := w.Actor.Name
	if who == "" || w.Actor.Role == ActorOwnerShell || w.Actor.Role == ActorOwnerBrowser {
		who = "owner"
	}
	if w.Actor.Role == ActorLeadRelay {
		who = "the owner, relayed by " + w.Actor.Name
	}
	var evs []DecisionEvent
	var notes []challengeNote
	seen := map[string]bool{}
	for _, c := range list {
		evs = append(evs, DecisionEvent{Event: DecisionResolved, Ref: c.Ref, Issue: c.Issue, New: how, ByRef: byRef})
		if seen[strings.ToLower(c.Issue)] {
			continue
		}
		seen[strings.ToLower(c.Issue)] = true
		notes = append(notes, challengeNote{Slug: c.Issue, Path: w.File(c.Issue), Text: ChallengeResolvedComment(c.Ref, how, byRef, words, who)})
	}
	return runChallengeWrite(w, evs, now, notes)
}

func refSlug(ref string) string {
	s, _, _ := strings.Cut(ref, "#D-")
	return s
}

func (w *WorkflowConfig) checkDecisionUnchallenged(arg string, issue *Issue) error {
	since, err := parseRuleSince(DecisionUnchallengedRule, arg)
	if err != nil {
		return err
	}
	if createdBefore(issue, since) {
		return nil
	}
	if w.IssuesRoot == "" {
		return fmt.Errorf("decision_unchallenged needs the project's decisions log, which this program doesn't load — run the transition with issue-cli")
	}
	open, err := LoadOpenChallenges(w.IssuesRoot)
	if err != nil {
		return fmt.Errorf("decision_unchallenged: reading the decisions log: %w", err)
	}
	mine := ChallengesBy(open, folderSlug(w.IssuesRoot, issue.FilePath, issue.Slug))
	if len(mine) == 0 && issue.Slug != "" {
		mine = ChallengesBy(open, issue.Slug)
	}
	if len(mine) == 0 {
		return nil
	}
	var holds []DecisionHold
	for _, c := range mine {
		holds = append(holds, DecisionHold{Rule: DecisionUnchallengedRule, Ref: c.Ref, Why: "challenged by this issue: " + oneLineCut(c.Why, decisionTextCut)})
	}
	if w.holdDecisions(holds) {
		return nil
	}
	return challengedError(issue.Slug, mine)
}

func challengedError(slug string, mine []Challenge) error {
	var b strings.Builder
	noun := "challenge"
	if len(mine) > 1 {
		noun = "challenges"
	}
	fmt.Fprintf(&b, "decision_unchallenged: this issue's %s waits for the owner's answer:\n", noun)
	for _, c := range mine {
		fmt.Fprintf(&b, "  %s (challenged %s): %s\n", c.Ref, c.Since.Format("2006-01-02"), oneLineCut(c.Why, decisionTextCut))
	}
	ref := mine[0].Ref
	fmt.Fprintf(&b, "The owner answers it on Needs you, or the lead relays the owner's words:\n")
	fmt.Fprintf(&b, "  keep:   issue-cli decision resolve %s --keep --quote \"…\"\n", ref)
	fmt.Fprintf(&b, "  change: issue-cli decide %s --replaces %s --text \"…\" --quote \"…\"\n", refSlug(ref), ref)
	fmt.Fprintf(&b, "Ask the lead on your trail: issue-cli comment %s \"question: answer on %s?\"", slug, ref)
	return fmt.Errorf("%s", b.String())
}
