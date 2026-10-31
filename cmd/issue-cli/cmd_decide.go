package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
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
  --deferred   the decision is to decide later
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

Examples:
  issue-cli decide sim/et-style-objective-format-6v6 \
    --text "The continuous raid becomes an ET-style objective format, 6v6." \
    --how chat --quote "The old continuous raid had no readable tension." \
    --replaces sim/gdd-match-phases#D-1
  issue-cli decide ai/fantasy-classes --deferred --how chat \
    --text "Balance classes once more elements are in place."
  issue-cli decide ai/fantasy-classes --provisional \
    --text "Healers get one slot per team until the owner says otherwise."
  issue-cli decide ai/fantasy-classes --confirm D-2 \
    --quote "Yes, keep the healer slot."`,
	Run: runDecide,
}

var decisionsCommand = &Command{
	Name:      "decisions",
	ShortHelp: "List the project's decisions index (D-n lines, citations, replaced/stale)",
	LongHelp: `List every D-n decision in the project, newest first, with its state:
active, deferred, replaced, or stale (replaced but still cited by open issues).

  --system S       only decisions recorded in issues of system S
  --state X        active | deferred | replaced | stale
  --issue <slug>   only decisions recorded in this issue

The same index is the viewer's Decisions tab (/p/<project>/decisions).

Examples:
  issue-cli decisions --state stale
  issue-cli decisions --issue sim/gdd-match-phases --json`,
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
	var replaces []string
	fs.Func("replaces", "repeatable: <slug>#D-n (or D-n in this issue) this decision replaces", func(v string) error {
		replaces = append(replaces, v)
		return nil
	})
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}

	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*confirmFlag) != "" {
		if *textFlag != "" || *howFlag != "" || *deferredFlag || *provisionalFlag || len(replaces) > 0 {
			return fmt.Errorf("--confirm takes only --quote: it records the human's words on %s as it stands", *confirmFlag)
		}
		return runDecideConfirm(ctx, issue, issues, *confirmFlag, *quoteFlag)
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
	nd := tracker.NewDecision{Text: *textFlag, How: how, Quote: *quoteFlag, Deferred: *deferredFlag}
	if err := tracker.ValidateNewDecision(nd); err != nil {
		return fmt.Errorf("%w\n\nExample:\n  issue-cli decide %s --text \"Gold resets each stage.\" --how chat", err, issue.Slug)
	}

	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project))
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
	}

	d, err := tracker.AddDecision(issue.FilePath, issue.Slug, nd, ctx.now())
	if err != nil {
		return fmt.Errorf("failed to record decision: %w", err)
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
	d, err := tracker.ConfirmDecision(issue.FilePath, issue.Slug, num, quote, ctx.now())
	if errors.Is(err, tracker.ErrDecisionConfirmed) {
		return fmt.Errorf("--confirm %s: it already has the human's words (no provisional mark); nothing to do", ref)
	}
	if err != nil {
		return fmt.Errorf("failed to confirm decision: %w", err)
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
}

func runDecisions(ctx *Context, args []string) error {
	fs := newFlagSet("decisions", ctx)
	systemFlag := fs.String("system", "", "only decisions recorded in issues of this system")
	stateFlag := fs.String("state", "", "active | deferred | replaced | stale")
	issueFlag := fs.String("issue", "", "only decisions recorded in this issue")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	state := strings.ToLower(strings.TrimSpace(*stateFlag))
	switch state {
	case "", tracker.DecisionActive, tracker.DecisionDeferred, tracker.DecisionReplaced, tracker.DecisionStale:
	default:
		return fmt.Errorf("--state %q: want active, deferred, replaced or stale", *stateFlag)
	}

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
		if !matchesDecisionState(e, state) {
			continue
		}
		row := decisionRow{Decision: e.Decision, State: e.State, System: e.Issue.System, Status: e.Issue.Status, CitedOpen: []string{}, CitedClosed: len(e.CitedClosed)}
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
		if r.ReplacedBy == "" && tracker.NeedsOwnerWords(r.Decision) {
			state += " · needs owner's words"
		}
		fmt.Fprintf(ctx.Stdout, "%s · %s · %s · cited %d open/%d closed\n", r.Ref, r.Date.Format("2006-01-02"), state, len(r.CitedOpen), r.CitedClosed)
		fmt.Fprintf(ctx.Stdout, "    %s\n", truncateRunes(r.Text, 120))
		if r.ReplacedBy != "" {
			fmt.Fprintf(ctx.Stdout, "    replaced by %s\n", r.ReplacedBy)
		}
		if r.State == tracker.DecisionStale {
			fmt.Fprintf(ctx.Stdout, "    still cited by: %s\n", strings.Join(r.CitedOpen, ", "))
		}
	}
	return nil
}

func matchesDecisionState(e *tracker.DecisionEntry, state string) bool {
	switch state {
	case "":
		return true
	case tracker.DecisionReplaced:
		return e.ReplacedBy != nil
	default:
		return e.State == state
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
