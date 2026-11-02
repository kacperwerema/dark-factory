package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Expiry's part of `decide`: --renew D-n --expires <date> gives a decision
// a new last day; --let-go D-n closes out an expired one (ended: <today>).
// On a hard decision both are the owner's acts (ownerActFor: a worker is
// refused, a lead relays --quote, the owner's shell may, its --quote
// posted as a decision: comment). Each run reads the clock once.

// pinClock fixes ctx's clock at one reading for the rest of the command,
// so the check, the line, the comment and the log events agree on the day.
func pinClock(ctx *Context) time.Time {
	now := ctx.now()
	ctx.Now = func() time.Time { return now }
	return now
}

// expiryAct is renew or let go.
type expiryAct struct {
	flag    string // "--renew" or "--let-go"
	expires string // renew's new date
}

func runDecideExpiry(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue, id, quote string, act expiryAct) error {
	now := pinClock(ctx)
	ref := tracker.NormalizeDecisionRef(issue.Slug, id)
	if ref == "" || !strings.HasPrefix(ref, issue.Slug+"#") {
		refSlug, _, _ := strings.Cut(strings.TrimPrefix(ref, "#"), "#")
		if refSlug != "" {
			return fmt.Errorf("%s %s: a decision is renewed or let go on its own issue:\n  %s", act.flag, id,
				tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s %s %s …", refSlug, act.flag, strings.TrimPrefix(ref, refSlug+"#"))))
		}
		return fmt.Errorf("%s %q: want a D-n of this issue", act.flag, id)
	}
	_, num, _ := strings.Cut(ref, "#")
	e := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project)).Lookup(ref)
	switch {
	case e == nil:
		return fmt.Errorf("%s %s: no such decision\n\nList the decisions in this issue:\n  issue-cli decisions --issue %s", act.flag, ref, issue.Slug)
	case e.ReplacedBy != nil:
		return fmt.Errorf("%s %s: it is replaced by %s, so it is out of force already", act.flag, ref, e.ReplacedBy.Ref)
	}
	if act.flag == "--renew" {
		if err := checkAttrsRest(ctx, issues, tracker.DecisionAttrs{Expires: act.expires}, true); err != nil {
			return err
		}
	}

	owner := ownerActFor(ctx, issue, issues, quote)
	me := owner.me
	if me.Role == tracker.RoleWorker && me.Slug != issue.Slug && !e.Hard {
		route := tracker.LeadFor(ctx.Project, issue, issues, sessionLive(ctx.Project))
		return fmt.Errorf("%s %s: a worker renews or lets go of decisions of its own issue only; ask the lead (%s, %s) on your issue trail", act.flag, ref, route.Lead, liveWord(route.Live))
	}
	// The hard check runs again under the issue's lock.
	check := func(cur tracker.Decision) error {
		if cur.Hard && owner.err != nil {
			return ownerExpiryRefusal(act, owner.err)
		}
		return nil
	}
	if err := check(e.Decision); err != nil {
		return err
	}
	relay := owner.relay
	if !e.Hard {
		relay = nil // a soft decision's renew is not an owner act
	}
	w := decisionWrite(ctx, me, relay)
	if relay == nil && me.Role == tracker.RoleUnmanaged {
		w.OwnerWords = quote
	}

	var before, after tracker.Decision
	var err error
	if act.flag == "--renew" {
		before, after, err = tracker.RenewDecisionAs(issue.FilePath, issue.Slug, num, act.expires, check, now, w)
	} else {
		before, after, err = tracker.LetGoDecisionAs(issue.FilePath, issue.Slug, num, check, now, w)
	}
	switch {
	case errors.Is(err, tracker.ErrNoExpiry), errors.Is(err, tracker.ErrDecisionEnded), errors.Is(err, tracker.ErrNotExpired), errors.Is(err, tracker.ErrNothingChanged):
		return fmt.Errorf("%s %s", act.flag, tracker.ExpiryRefusal(ref, err))
	case err != nil:
		return fmt.Errorf("%s %s: %w", act.flag, ref, err)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, decideOutput{Decision: after, StaleCiters: []staleCiter{}, Decisions: decisionsURL(ctx.Project)})
	}
	if act.flag == "--renew" {
		was := "in force"
		if before.ExpiredOn(now) {
			was = "expired"
		}
		fmt.Fprintf(ctx.Stdout, "✓ Renewed %s until %s (was %s, expires: %s)\n", after.Ref, after.Expires, was, before.Expires)
	} else {
		fmt.Fprintf(ctx.Stdout, "✓ Let go of %s (expired after %s); it stays out of force and waits on no one\n", after.Ref, after.Expires)
	}
	fmt.Fprintln(ctx.Stdout, after.Raw)
	switch {
	case relay != nil:
		fmt.Fprintf(ctx.Stdout, "Relayed the owner's words in a decision: comment on %s%s.\n", issue.Slug, yoloNote(relay))
	case w.OwnerWords != "":
		fmt.Fprintf(ctx.Stdout, "Your words are in a decision: comment on %s.\n", issue.Slug)
	}
	fmt.Fprintf(ctx.Stdout, "History: %s\n", tracker.InjectProject(ctx.Project, "issue-cli decisions log --ref "+after.Ref))
	return nil
}

// ownerExpiryRefusal words ownerActFor's refusal for renew and let go.
func ownerExpiryRefusal(act expiryAct, err error) error {
	verb := "renewing"
	if act.flag == "--let-go" {
		verb = "letting go of"
	}
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "changes to a hard decision are the owner's: a worker can't make a decision hard, amend a hard one or replace it."); ok {
		return fmt.Errorf("%s a hard decision is the owner's act: a worker can't.%s", verb, rest)
	}
	return fmt.Errorf("%s a hard decision: %s", verb, msg)
}
