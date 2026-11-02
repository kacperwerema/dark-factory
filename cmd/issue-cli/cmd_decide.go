package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

var decideCommand = &Command{
	Name:      "decide",
	ShortHelp: "Record a decision as a D-n line under ## Decisions",
	LongHelp: `Record a decision as a dated D-n line under the issue's ## Decisions
section: one made in chat, by a project rule or after a playtest. Questions
themselves stay in chat (Claude Code / Codex); only the outcome is recorded
here. The viewer's
Decisions tab (/p/<project>/decisions) is generated from these lines.

Other issues cite a decision as <slug>#D-n anywhere in their body.
--replaces marks older decisions as replaced by this one; the output lists the
open issues that still cite them, so they can be revised.

  --text       the decision, one line (required)
  --how        how it was made, e.g. "chat", "project rule", "playtest"
               (default "recorded by agent"; must not contain ':')
  --quote      the human's own words, quoted after the text
  --replaces   repeatable: <slug>#D-n (or D-n in this issue) it replaces
  --ref        repeatable: what this decision answers outside the D-n
               scheme, e.g. a lead's provisional N-26; written as
               "(ref N-26)" on the D-n line, shown by show and decisions
  --deferred   the decision is to decide later
  --scope S    whom it reaches: issue (default: this issue, issues below it
               and issues citing it), project, system:<S>,
               department:<key>, type:<t>
  --at "s1, s2"
               the statuses where it matters (default all)
  --hard       the owner's hard rule. Needs the owner's words on the line
               (--quote, not --provisional) and is the owner's act: a
               worker is refused, a lead relays the owner's --quote
  --paths "g1, g2"
               globs a hard decision guards (tracker.MatchGlob: ** any dirs)
  --expires YYYY-MM-DD
               the last day it is in force
  --amend D-n  change only that decision's attribute group: give any of
               --scope, --hard, --soft, --at, --paths, --expires ("" clears
               at, paths, expires; --scope issue resets it). Date, how,
               text, quote and replaces stay byte for byte; a text change
               is a new decision (--replaces). Any change to a hard
               decision is the owner's act, as --hard.
  --renew D-n --expires YYYY-MM-DD
               give a decision with expires: a new last day, before or
               after it passed (logged as renewed). Refused on one let go:
               that is final, replace it instead.
  --let-go D-n close out an expired decision: ended: <today> goes in its
               group (logged as expired); it stays out of force and no
               longer waits on the owner. Renewing or letting go of a hard
               decision is the owner's act, as --hard; the owner's own
               --quote is posted as a decision: comment.
  --provisional
               the decision waits for the owner's words: its how gets
               "provisional" (e.g. "YOLO, provisional"), the Decisions log
               lists it as waiting, and --confirm clears the mark. Under
               YOLO, record every answer you gave yourself this way.
  --confirm    D-n: record the human's --quote on that existing decision
               instead of a new line: its "(provisional…)" mark goes, its
               how ends "confirmed by owner <date>", and its ref and
               citations stay valid. --quote is required. The owner's
               Confirm button in the Decisions log does the same.

The attribute group sits last on the line:
  - **D-4** · 2026-10-31 · chat: Agents merge into local main, "only main" {scope: project; hard; paths: cmd/**}
A decision replacing a hard one is hard with its scope and paths unless
set (--soft drops hard; the owner's act). Every write is logged in
<issues>/.decisions/log.jsonl (refs and groups, no text).

Examples:
  issue-cli decide sim/et-style-objective-format-6v6 \
    --text "The continuous raid becomes an ET-style objective format, 6v6." \
    --how chat --quote "The old continuous raid had no readable tension." \
    --replaces sim/gdd-match-phases#D-1
  issue-cli decide ai/fantasy-classes --deferred --how chat \
    --text "Balance classes once more elements are in place."
  issue-cli decide ai/fantasy-classes --provisional \
    --text "Healers get one slot per team until the owner says otherwise."
  issue-cli decide ai/fantasy-classes --provisional --ref N-26 \
    --text "Follow the lead's N-26: one healer slot per team."
  issue-cli decide ai/fantasy-classes --confirm D-2 \
    --quote "Yes, keep the healer slot."
  issue-cli decide ai/fantasy-classes --amend D-2 --scope system:AI --at "in design"
  issue-cli decide ai/fantasy-classes --amend D-2 --hard --paths "ai/classes/**" \
    --quote "Make the healer slot a hard rule."   # the lead relaying the owner
  issue-cli decide ai/fantasy-classes --renew D-2 --expires 2027-06-30 \
    --quote "Keep it till summer."
  issue-cli decide ai/fantasy-classes --let-go D-2 --quote "Let it go."`,
	Run: runDecide,
}

var decisionsCommand = &Command{
	Name:      "decisions",
	ShortHelp: "List the project's decisions index (D-n lines, citations, replaced/stale)",
	LongHelp: `List every D-n decision in the project, newest first, with its state:
active, deferred, replaced, or stale (replaced but still cited by open issues).

  --system S       only decisions recorded in issues of system S
  --state X        active | expired | deferred | replaced | stale
                   (expired: past its expires: day, not replaced; an
                   expired hard one waits on the owner until renewed or
                   let go, and the listing prints both commands)
  --issue <slug>   only decisions recorded in this issue
  --in-force <slug>
                   the decisions in force for that issue: not replaced,
                   deferred or expired, at: naming its status, and a scope
                   that reaches it (own, parent, cites, system, department,
                   type, project); hard first. Reads only.
  --status S       with --in-force: at status S (default the issue's own)
  --lead ceo|<department>
                   the decisions in force for a lead: project scope for the
                   CEO, project and department:<key> for a department's
                   lead (at: not filtered). Reads only.

issue-cli decisions log [--ref <slug>#D-n] [--issue <slug>] [--since YYYY-MM-DD] [--json]
                   the audit log (<issues>/.decisions/log.jsonl) in time
                   order: recorded, amended, confirmed, replaced, the
                   briefings that carried a decision (injected), the gates
                   a decision held (held: refused, or passed under YOLO).
                   Writes that did not land are left out. Reads only.

start, show and the worker and lead briefings carry the capped view of
--in-force (--lead for leads) as "Decisions in force": hard ones always,
soft ones up to decisions: brief_cap: (12), the rest as a count line.

The same index is the viewer's Decisions tab (/p/<project>/decisions).

Examples:
  issue-cli decisions --state stale
  issue-cli decisions --issue sim/gdd-match-phases --json
  issue-cli decisions --in-force sim/creep-camps --status "in progress"
  issue-cli decisions --lead ceo
  issue-cli decisions log --ref sim/gdd-match-phases#D-2 --since 2026-10-01`,
	Run: runDecisions,
}

func init() {
	registerCommand(decideCommand)
	registerCommand(decisionsCommand)
}

type staleCiter struct {
	Slug   string `json:"slug"`
	Status string `json:"status"`
	Cites  string `json:"cites"`
}

type decideOutput struct {
	Decision    tracker.Decision `json:"decision"`
	StaleCiters []staleCiter     `json:"stale_citers"`
	Decisions   string           `json:"decisions_url"`
}

func runDecide(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "decide")
	if err != nil {
		return err
	}
	fs := newFlagSet("decide", ctx)
	textFlag := fs.String("text", "", "the decision, one line")
	howFlag := fs.String("how", "", `how it was made: "chat", "project rule", "playtest" (default "recorded by agent")`)
	quoteFlag := fs.String("quote", "", "the human's own words")
	deferredFlag := fs.Bool("deferred", false, "the decision is deferred (decide later)")
	provisionalFlag := fs.Bool("provisional", false, "the decision waits for the owner's words (marks its how provisional)")
	confirmFlag := fs.String("confirm", "", "D-n: record --quote on this existing decision (clears its provisional mark)")
	amendFlag := fs.String("amend", "", "D-n: change only this decision's attribute group (--scope, --hard, --soft, --at, --paths, --expires)")
	renewFlag := fs.String("renew", "", "D-n: give this decision a new last day (--expires YYYY-MM-DD)")
	letGoFlag := fs.String("let-go", "", "D-n: close out this expired decision (ended: today)")
	af := addAttrFlags(fs)
	var replaces []string
	fs.Func("replaces", "repeatable: <slug>#D-n (or D-n in this issue) this decision replaces", func(v string) error {
		replaces = append(replaces, v)
		return nil
	})
	var refs []string
	fs.Func("ref", "repeatable: what this decision answers outside the D-n scheme, e.g. a lead's N-26", func(v string) error {
		refs = append(refs, v)
		return nil
	})
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	af.visit(fs)

	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	renew, letGo := strings.TrimSpace(*renewFlag), strings.TrimSpace(*letGoFlag)
	if renew != "" || letGo != "" {
		others := *textFlag != "" || *howFlag != "" || *deferredFlag || *provisionalFlag || len(replaces) > 0 || len(refs) > 0 || *confirmFlag != "" || *amendFlag != ""
		for _, k := range []string{"scope", "at", "paths", "hard", "soft"} {
			others = others || af.set[k]
		}
		switch {
		case renew != "" && letGo != "":
			return fmt.Errorf("--renew and --let-go together: renew keeps the decision in force, let go closes it out; pick one")
		case others || (letGo != "" && af.set["expires"]):
			return fmt.Errorf("--renew takes only --expires and --quote, --let-go only --quote: they change only the decision's expires: or ended:")
		case renew != "" && strings.TrimSpace(*af.expires) == "":
			return fmt.Errorf("--renew %s needs --expires YYYY-MM-DD: the new last day it is in force\n\nExample:\n  issue-cli decide %s --renew %s --expires %s", renew, issue.Slug, renew, ctx.now().AddDate(0, 0, 90).Format("2006-01-02"))
		case renew != "":
			return runDecideExpiry(ctx, issue, issues, renew, *quoteFlag, expiryAct{flag: "--renew", expires: strings.TrimSpace(*af.expires)})
		default:
			return runDecideExpiry(ctx, issue, issues, letGo, *quoteFlag, expiryAct{flag: "--let-go"})
		}
	}
	if strings.TrimSpace(*confirmFlag) != "" {
		if *textFlag != "" || *howFlag != "" || *deferredFlag || *provisionalFlag || len(replaces) > 0 || len(refs) > 0 || af.any() || *amendFlag != "" {
			return fmt.Errorf("--confirm takes only --quote: it records the human's words on %s as it stands (to make it hard after: decide %s --amend %s --hard)", *confirmFlag, issue.Slug, *confirmFlag)
		}
		return runDecideConfirm(ctx, issue, issues, *confirmFlag, *quoteFlag)
	}
	if amend := strings.TrimSpace(*amendFlag); amend != "" {
		if *textFlag != "" || *howFlag != "" {
			return fmt.Errorf("--amend changes only %s's attribute group; its text, how and quote stay as recorded. To change what was decided, record a new decision that replaces it:\n  %s",
				amend, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --replaces %s --text \"…\"", issue.Slug, amend)))
		}
		if *deferredFlag || *provisionalFlag || len(replaces) > 0 || len(refs) > 0 {
			return fmt.Errorf("--amend changes only the attribute group (--scope, --hard, --soft, --at, --paths, --expires); --deferred, --provisional, --replaces and --ref make a new decision")
		}
		return runDecideAmend(ctx, issue, issues, amend, *quoteFlag, af)
	}
	how := *howFlag
	if strings.TrimSpace(*quoteFlag) == "" {
		if _, on, _ := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now()); on {
			how = "YOLO" // made without the owner's words while YOLO is on: the Decisions log asks them later
		}
	}
	if *provisionalFlag {
		how = tracker.MarkProvisional(how)
	}
	nd := tracker.NewDecision{Text: *textFlag, How: how, Quote: *quoteFlag, Deferred: *deferredFlag, Refs: refs}
	if err := tracker.ValidateNewDecision(nd); err != nil {
		return fmt.Errorf("%w\n\nExample:\n  issue-cli decide %s --text \"Gold resets each stage.\" --how chat", err, issue.Slug)
	}
	if nd.Attrs, err = af.apply(tracker.DecisionAttrs{Scope: tracker.ScopeIssue}); err != nil {
		return err
	}

	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project))
	var hardReplaced []*tracker.DecisionEntry
	for _, raw := range replaces {
		ref := tracker.NormalizeDecisionRef(issue.Slug, raw)
		if ref == "" {
			return fmt.Errorf("--replaces %q is not a decision ref; use <slug>#D-n (or D-n in this issue)", raw)
		}
		if index.Lookup(ref) == nil {
			refSlug, _, _ := strings.Cut(ref, "#")
			return fmt.Errorf("--replaces %s: no such decision\n\nList the decisions in that issue:\n  issue-cli decisions --issue %s", ref, refSlug)
		}
		nd.Replaces = append(nd.Replaces, ref)
		if e := index.Lookup(ref); e.Hard {
			hardReplaced = append(hardReplaced, e)
		}
	}
	// A decision replacing a hard one inherits hard, scope and paths: unless
	// the command sets them (--soft drops hard: still the owner's act).
	if len(hardReplaced) > 0 {
		if !af.set["hard"] && !af.set["soft"] {
			nd.Attrs.Hard = true
		}
		if !af.set["scope"] {
			scope := hardReplaced[0].ScopeOrDefault()
			for _, e := range hardReplaced[1:] {
				if e.ScopeOrDefault() != scope {
					return fmt.Errorf("--replaces: the hard decisions it replaces have different scopes (%s, %s): set the new one's with --scope", scope, e.ScopeOrDefault())
				}
			}
			nd.Attrs.Scope = scope
		}
		if !af.set["paths"] && nd.Attrs.Hard {
			seen := map[string]bool{}
			for _, e := range hardReplaced {
				for _, p := range e.Paths {
					if !seen[p] {
						seen[p] = true
						nd.Attrs.Paths = append(nd.Attrs.Paths, p)
					}
				}
			}
		}
	}
	// Replacing a decision with an open challenge answers the challenge:
	// the owner's act even for a soft decision.
	var challenged []tracker.Challenge
	if len(nd.Replaces) > 0 {
		open, err := tracker.LoadOpenChallenges(ctx.Project.IssueDir)
		if err != nil {
			return err
		}
		for _, ref := range nd.Replaces {
			challenged = append(challenged, tracker.ChallengesOn(open, ref)...)
		}
	}
	me := whoAmI(ctx.Project, issues)
	var relay *tracker.DecisionRelay
	var owner ownerActRules
	if nd.Attrs.Hard || len(hardReplaced) > 0 || len(challenged) > 0 {
		owner = ownerActFor(ctx, issue, issues, *quoteFlag)
		if owner.err != nil {
			if len(hardReplaced) == 0 && !nd.Attrs.Hard {
				return ownerChallengeRefusal(owner.err)
			}
			return owner.err
		}
		if nd.Attrs.Hard && (*provisionalFlag || tracker.NeedsOwnerWords(tracker.Decision{How: how, Quote: strings.TrimSpace(*quoteFlag), Text: nd.Text})) {
			return fmt.Errorf("a hard decision needs the owner's words on the line: record it soft (no --hard), get the owner's words (decide %s --confirm D-n --quote \"…\"), then make it hard (decide %s --amend D-n --hard)", issue.Slug, issue.Slug)
		}
		me, relay = owner.me, owner.relay
	}
	if err := workerScopeRefusal(ctx, me, issue, issues, nd.Attrs.Scope); err != nil {
		return err
	}
	if err := checkAttrs(ctx, issues, nd.Attrs, af.set["expires"]); err != nil {
		return err
	}

	d, err := tracker.AddDecisionAs(issue.FilePath, issue.Slug, nd, ctx.now(), decisionWrite(ctx, me, relay))
	if err != nil {
		return fmt.Errorf("failed to record decision: %w", err)
	}
	if len(challenged) > 0 {
		if err := resolveChallenges(ctx, issues, owner, challenged, tracker.ChallengeReplaced, d.Ref, *quoteFlag); err != nil {
			return fmt.Errorf("recorded %s, but: %w", d.Ref, err)
		}
	}

	var stale []staleCiter
	for _, ref := range nd.Replaces {
		for _, citer := range index.Lookup(ref).CitedOpen {
			if citer.Slug != issue.Slug {
				stale = append(stale, staleCiter{Slug: citer.Slug, Status: citer.Status, Cites: ref})
			}
		}
	}
	out := decideOutput{Decision: d, StaleCiters: stale, Decisions: decisionsURL(ctx.Project)}
	if out.StaleCiters == nil {
		out.StaleCiters = []staleCiter{}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}

	fmt.Fprintf(ctx.Stdout, "✓ Recorded %s\n", d.Ref)
	fmt.Fprintln(ctx.Stdout, d.Raw)
	fmt.Fprintln(ctx.Stdout, attrsSummary(d.DecisionAttrs))
	if relay != nil {
		fmt.Fprintf(ctx.Stdout, "Relayed the owner's words in a decision: comment on %s%s.\n", issue.Slug, yoloNote(relay))
	}
	for _, c := range challenged {
		fmt.Fprintf(ctx.Stdout, "Resolved %s's challenge on %s (replaced by %s).\n", c.Issue, c.Ref, d.Ref)
	}
	for _, ref := range nd.Replaces {
		fmt.Fprintf(ctx.Stdout, "Replaced %s.", ref)
		var lines []string
		for _, s := range stale {
			if s.Cites == ref {
				lines = append(lines, fmt.Sprintf("  %s (%s)", s.Slug, s.Status))
			}
		}
		if len(lines) == 0 {
			fmt.Fprintln(ctx.Stdout, " No open issue cites it.")
			continue
		}
		fmt.Fprintln(ctx.Stdout, " Open issues still citing it:")
		fmt.Fprintln(ctx.Stdout, strings.Join(lines, "\n"))
	}
	for _, ref := range nd.Replaces {
		if old := index.Lookup(ref); tracker.NeedsOwnerWords(old.Decision) && old.Slug == issue.Slug {
			fmt.Fprintf(ctx.Stdout, "To only add the human's words to a decision, keep its D-n instead: issue-cli decide %s --confirm %s --quote \"…\"\n", old.Slug, old.ID)
		}
	}
	if len(stale) > 0 {
		fmt.Fprintf(ctx.Stdout, "Next: revise those issues to cite %s, or leave them for the human (they show as stale in %s).\n", d.Ref, out.Decisions)
	}
	fmt.Fprintf(ctx.Stdout, "Cite this decision from dependent issues as %s.\n", d.Ref)
	if d.ScopeOrDefault() == tracker.ScopeIssue && !d.Hard {
		fmt.Fprintf(ctx.Stdout, "It reaches this issue, issues below it and issues citing it. To reach more: %s\n",
			tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --amend %s --scope system:<S>", issue.Slug, d.ID)))
	}
	return nil
}

// runDecideConfirm records the human's words on an existing decision.
func runDecideConfirm(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue, id, quote string) error {
	if strings.TrimSpace(quote) == "" {
		return fmt.Errorf("--confirm needs --quote: the human's own words confirming it\n\nExample:\n  issue-cli decide %s --confirm %s --quote \"Yes, keep it.\"", issue.Slug, id)
	}
	ref := tracker.NormalizeDecisionRef(issue.Slug, id)
	if ref == "" || !strings.HasPrefix(ref, issue.Slug+"#") {
		return fmt.Errorf("--confirm %q: want a D-n of this issue", id)
	}
	_, num, _ := strings.Cut(ref, "#")
	e := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project)).Lookup(ref)
	switch {
	case e == nil:
		return fmt.Errorf("--confirm %s: no such decision\n\nList the decisions in this issue:\n  issue-cli decisions --issue %s", ref, issue.Slug)
	case e.ReplacedBy != nil:
		return fmt.Errorf("--confirm %s: it is superseded by %s; confirm that one", ref, e.ReplacedBy.Ref)
	}
	// Confirming a challenged decision answers the challenge (keep): the
	// owner's act.
	challenged, err := openChallengesFor(ctx, ref, "")
	if err != nil {
		return err
	}
	me := whoAmI(ctx.Project, issues)
	var owner ownerActRules
	if len(challenged) > 0 {
		if owner = ownerActFor(ctx, issue, issues, quote); owner.err != nil {
			return ownerChallengeRefusal(owner.err)
		}
		me = owner.me
	}
	d, err := tracker.ConfirmDecisionAs(issue.FilePath, issue.Slug, num, quote, false, ctx.now(), decisionWrite(ctx, me, nil))
	if errors.Is(err, tracker.ErrDecisionConfirmed) {
		return fmt.Errorf("--confirm %s: it already has the human's words (no provisional mark); nothing to do", ref)
	}
	if err != nil {
		return fmt.Errorf("failed to confirm decision: %w", err)
	}
	if len(challenged) > 0 {
		if err := resolveChallenges(ctx, issues, owner, challenged, tracker.ChallengeKept, "", quote); err != nil {
			return fmt.Errorf("confirmed %s, but: %w", d.Ref, err)
		}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, decideOutput{Decision: d, StaleCiters: []staleCiter{}, Decisions: decisionsURL(ctx.Project)})
	}
	fmt.Fprintf(ctx.Stdout, "✓ Confirmed %s\n", d.Ref)
	fmt.Fprintln(ctx.Stdout, d.Raw)
	return nil
}

type decisionRow struct {
	tracker.Decision
	State       string   `json:"state"`
	System      string   `json:"system"`
	Status      string   `json:"issue_status"`
	ReplacedBy  string   `json:"replaced_by,omitempty"`
	CitedOpen   []string `json:"cited_by_open"`
	CitedClosed int      `json:"cited_by_closed"`
	// WaitsOnOwner: an expired hard decision nobody renewed or let go.
	WaitsOnOwner bool `json:"waits_on_owner,omitempty"`
}

func runDecisions(ctx *Context, args []string) error {
	if len(args) > 0 && args[0] == "log" {
		return runDecisionsLog(ctx, args[1:])
	}
	fs := newFlagSet("decisions", ctx)
	systemFlag := fs.String("system", "", "only decisions recorded in issues of this system")
	stateFlag := fs.String("state", "", "active | expired | deferred | replaced | stale")
	issueFlag := fs.String("issue", "", "only decisions recorded in this issue")
	inForceFlag := fs.String("in-force", "", "<slug>: the decisions in force for this issue")
	statusFlag := fs.String("status", "", "with --in-force: at this status (default the issue's own)")
	leadFlag := fs.String("lead", "", "ceo|<department>: the decisions in force for that lead")
	touchedFlag := fs.String("touched", "", "<slug>: the hard decisions whose paths: the issue's branch changes (the decisions_respected audit)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if slug := strings.TrimSpace(*touchedFlag); slug != "" {
		if *systemFlag != "" || *stateFlag != "" || *issueFlag != "" || *inForceFlag != "" || *statusFlag != "" || *leadFlag != "" {
			return fmt.Errorf("--touched takes only --json")
		}
		return runDecisionsTouched(ctx, slug)
	}
	if lead := strings.TrimSpace(*leadFlag); lead != "" {
		if *systemFlag != "" || *stateFlag != "" || *issueFlag != "" || *inForceFlag != "" || *statusFlag != "" {
			return fmt.Errorf("--lead takes only --json")
		}
		return runDecisionsLead(ctx, lead)
	}
	if strings.TrimSpace(*inForceFlag) != "" {
		if *systemFlag != "" || *stateFlag != "" || *issueFlag != "" {
			return fmt.Errorf("--in-force takes only --status and --json")
		}
		return runDecisionsInForce(ctx, strings.TrimSpace(*inForceFlag), strings.TrimSpace(*statusFlag))
	}
	if *statusFlag != "" {
		return fmt.Errorf("--status goes with --in-force <slug>")
	}
	state := strings.ToLower(strings.TrimSpace(*stateFlag))
	switch state {
	case "", tracker.DecisionActive, tracker.DecisionExpired, tracker.DecisionDeferred, tracker.DecisionReplaced, tracker.DecisionStale:
	default:
		return fmt.Errorf("--state %q: want active, expired, deferred, replaced or stale", *stateFlag)
	}
	today := ctx.now()

	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	onlyIssue := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(*issueFlag), ".md"))
	if onlyIssue != "" {
		found := false
		for _, i := range issues {
			if i.Slug == onlyIssue {
				found = true
				break
			}
		}
		if !found {
			if _, _, err := findIssueOrErr(ctx, onlyIssue); err != nil {
				return err
			}
		}
	}

	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project))
	rows := []decisionRow{}
	for _, e := range index.Entries {
		if *systemFlag != "" && !strings.EqualFold(e.Issue.System, *systemFlag) {
			continue
		}
		if onlyIssue != "" && e.Slug != onlyIssue {
			continue
		}
		if !matchesDecisionState(e, state, today) {
			continue
		}
		row := decisionRow{Decision: e.Decision, State: e.StateOn(today), WaitsOnOwner: e.WaitsOnOwner(today), System: e.Issue.System, Status: e.Issue.Status, CitedOpen: []string{}, CitedClosed: len(e.CitedClosed)}
		if e.ReplacedBy != nil {
			row.ReplacedBy = e.ReplacedBy.Ref
		}
		for _, c := range e.CitedOpen {
			row.CitedOpen = append(row.CitedOpen, c.Slug)
		}
		rows = append(rows, row)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rows)
	}
	if len(rows) == 0 {
		if len(index.Entries) == 0 {
			fmt.Fprintln(ctx.Stdout, "No decisions recorded yet. Record one with: issue-cli decide <slug> --text \"...\"")
		} else {
			fmt.Fprintln(ctx.Stdout, "No decisions match these filters.")
		}
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Decisions (%d) ==\n", len(rows))
	for _, r := range rows {
		state := r.State
		if state == tracker.DecisionStale {
			state = "replaced · stale"
		}
		if r.State == tracker.DecisionExpired {
			state += " after " + r.Expires
			if r.Ended != "" {
				state += " · let go " + r.Ended
			}
		}
		if r.ReplacedBy == "" && tracker.NeedsOwnerWords(r.Decision) {
			state += " · needs owner's words"
		}
		if r.Hard {
			state += " · hard"
		}
		if r.WaitsOnOwner {
			state += " · waits for the owner"
		}
		if sc := r.ScopeOrDefault(); sc != tracker.ScopeIssue {
			state += " · scope " + sc
		}
		fmt.Fprintf(ctx.Stdout, "%s · %s · %s · cited %d open/%d closed\n", r.Ref, r.Date.Format("2006-01-02"), state, len(r.CitedOpen), r.CitedClosed)
		fmt.Fprintf(ctx.Stdout, "    %s\n", truncateRunes(r.Text, 120))
		if r.WaitsOnOwner {
			fmt.Fprintf(ctx.Stdout, "    renew: %s\n    let go: %s\n",
				tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --renew %s --expires YYYY-MM-DD --quote \"…\"", r.Slug, r.ID)),
				tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --let-go %s --quote \"…\"", r.Slug, r.ID)))
		}
		if len(r.Refs) > 0 {
			fmt.Fprintf(ctx.Stdout, "    ref %s\n", strings.Join(r.Refs, ", "))
		}
		if r.ReplacedBy != "" {
			fmt.Fprintf(ctx.Stdout, "    replaced by %s\n", r.ReplacedBy)
		}
		if r.State == tracker.DecisionStale {
			fmt.Fprintf(ctx.Stdout, "    still cited by: %s\n", strings.Join(r.CitedOpen, ", "))
		}
	}
	return nil
}

func matchesDecisionState(e *tracker.DecisionEntry, state string, today time.Time) bool {
	switch state {
	case "":
		return true
	case tracker.DecisionReplaced:
		return e.ReplacedBy != nil
	default:
		return e.StateOn(today) == state
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// decisionsURL links the viewer's Decisions tab.
func decisionsURL(proj *tracker.Project) string {
	base := viewerBaseURL(proj)
	if proj != nil {
		return fmt.Sprintf("%s/p/%s/decisions", base, proj.Slug)
	}
	return base + "/decisions"
}

// printReplacedCitations warns when the issue builds on decisions that have
// since been replaced, so a re-dispatched agent knows the issue needs revision.
func printReplacedCitations(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue) {
	if !strings.Contains(issue.BodyRaw, "#D-") {
		return
	}
	index := tracker.BuildDecisionIndex(issues, nil)
	var lines []string
	for _, e := range index.Cited(issue.Slug) {
		if e.ReplacedBy != nil && e.Slug != issue.Slug {
			lines = append(lines, fmt.Sprintf("  %s → replaced by %s: %s", e.Ref, e.ReplacedBy.Ref, truncateRunes(e.ReplacedBy.Text, 100)))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(ctx.Stdout, "== Cites replaced decisions (%d) ==\n", len(lines))
	fmt.Fprintln(ctx.Stdout, strings.Join(lines, "\n"))
	fmt.Fprintln(ctx.Stdout, "Revise this issue to follow the newer decisions and cite them instead.")
	fmt.Fprintln(ctx.Stdout)
}

type inForceRow struct {
	decisionRow
	Why string `json:"why"`
}

// runDecisionsInForce lists the decisions in force for one issue at a
// status: what reaches it by scope and at:, hard first. It only reads.
func runDecisionsInForce(ctx *Context, slug, status string) error {
	pinClock(ctx)
	issue, list, index, err := inForceIndex(ctx, slug, status)
	if err != nil {
		return err
	}
	if status == "" {
		status = issue.Status
	}
	if len(list) == 0 && !ctx.JSONOutput {
		fmt.Fprintf(ctx.Stdout, "None in force for %s at %s. A decision reaches an issue by its scope:\n  %s\n", issue.Slug, status,
			tracker.InjectProject(ctx.Project, "issue-cli decide <slug> --amend D-n --scope system:<S>|project|department:<key>|type:<t>"))
		printExpiredReaching(ctx, index, issue, status)
		return nil
	}
	if err := printInForce(ctx, list, fmt.Sprintf("%s at %s", issue.Slug, status)); err != nil || ctx.JSONOutput {
		return err
	}
	printExpiredReaching(ctx, index, issue, status)
	return nil
}

// printExpiredReaching notes the decisions that would reach the issue but
// have expired, with the command that lists them.
func printExpiredReaching(ctx *Context, index *tracker.DecisionIndex, issue *tracker.Issue, status string) {
	today := ctx.now()
	n := 0
	for _, f := range tracker.InForce(index, issue, status, tracker.InForceOptions{Department: ctx.Project.DepartmentOf}) {
		if f.Entry.ExpiredOn(today) {
			n++
		}
	}
	if n > 0 {
		fmt.Fprintf(ctx.Stdout, "%s out of force by its expires: date: %s\n", pluralWord(n, "1 more decision is", fmt.Sprintf("%d more decisions are", n)),
			tracker.InjectProject(ctx.Project, "issue-cli decisions --state expired"))
	}
}

// runDecisionsLead lists the decisions in force for a lead: ceo, or a
// department's key. It only reads.
func runDecisionsLead(ctx *Context, lead string) error {
	dept := ""
	if !strings.EqualFold(lead, "ceo") {
		d := ctx.Project.DepartmentByKey(lead)
		if d == nil {
			return fmt.Errorf("--lead %q: want ceo or a department key (issue-cli department list)", lead)
		}
		dept = d.Name
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project))
	list := tracker.InForceLead(index, dept, ctx.now())
	label := "the CEO (project scope)"
	if dept != "" {
		label = fmt.Sprintf("the %s lead (project and department:%s)", dept, tracker.DepartmentKey(dept))
	}
	if len(list) == 0 && !ctx.JSONOutput {
		fmt.Fprintf(ctx.Stdout, "None in force for %s. A decision reaches a lead by its scope:\n  %s\n", label,
			tracker.InjectProject(ctx.Project, "issue-cli decide <slug> --amend D-n --scope project|department:<key>"))
		return nil
	}
	return printInForce(ctx, list, label)
}

// inForceList is InForce for slug at status ("" the issue's own), with the
// project's department lookup and today's date: the one list start, show,
// the briefing and decisions --in-force all read.
func inForceList(ctx *Context, slug, status string) (*tracker.Issue, []tracker.InForceEntry, error) {
	issue, list, _, err := inForceIndex(ctx, slug, status)
	return issue, list, err
}

// inForceIndex is inForceList with the index it read.
func inForceIndex(ctx *Context, slug, status string) (*tracker.Issue, []tracker.InForceEntry, *tracker.DecisionIndex, error) {
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return nil, nil, nil, err
	}
	if status == "" {
		status = issue.Status
	}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project))
	return issue, tracker.InForce(index, issue, status, tracker.InForceOptions{Department: ctx.Project.DepartmentOf, Today: ctx.now()}), index, nil
}

// printInForce prints an in-force list in full (or as JSON), hard first.
func printInForce(ctx *Context, list []tracker.InForceEntry, label string) error {
	rows := []inForceRow{}
	hard := 0
	for _, f := range list {
		e := f.Entry
		row := inForceRow{decisionRow: decisionRow{Decision: e.Decision, State: e.State, System: e.Issue.System, Status: e.Issue.Status, CitedOpen: []string{}, CitedClosed: len(e.CitedClosed)}, Why: f.Why}
		for _, c := range e.CitedOpen {
			row.CitedOpen = append(row.CitedOpen, c.Slug)
		}
		if e.Hard {
			hard++
		}
		rows = append(rows, row)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rows)
	}
	fmt.Fprintf(ctx.Stdout, "== Decisions in force for %s (%d: %d hard) ==\n", label, len(rows), hard)
	for _, r := range rows {
		strength := "soft"
		if r.Hard {
			strength = "hard"
		}
		fmt.Fprintf(ctx.Stdout, "%s · %s · %s · %s\n", r.Ref, strength, r.ScopeOrDefault(), r.Why)
		fmt.Fprintf(ctx.Stdout, "    %s\n", truncateRunes(r.Text, 120))
	}
	return nil
}
