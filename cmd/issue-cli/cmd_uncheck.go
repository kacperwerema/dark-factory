package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var uncheckCommand = &Command{
	Name:      "uncheck",
	ShortHelp: "Untick checkboxes by id (several at once)",
	LongHelp: `Turn ticked boxes back into open ones, addressed by their ids from
'issue-cli checklist'. Several ids in one call are all-or-nothing: if any id
does not exist, nothing changes. A box that is already open is reported, not
an error. The boxes keep their text, position and ids.

Examples:
  issue-cli uncheck <slug> D3
  issue-cli uncheck <slug> Im1 Im2

An agent can't untick the owner's boxes: a criterion only the owner can prove
(a playtest judgement step, "(owner)"), a box a guided check left to the
owner, or a Human Testing box. An acceptance criterion whose proof still
passes is ticked again by that proof (prove <slug> <id> --clear drops its
prove links).

To drop a box: issue-cli check-remove <slug> <id>`,
	Run: runUncheck,
}

func init() {
	registerCommand(uncheckCommand)
}

func runUncheck(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "uncheck")
	if err != nil {
		return err
	}
	fs := newFlagSet("uncheck", ctx)
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return &usageError{msg: fmt.Sprintf("uncheck needs box ids (see issue-cli checklist %s)\n\nExample:\n  issue-cli uncheck %s D3", slug, slug)}
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	if _, _, _, err := tracker.UncheckBoxes(issue.BodyRaw, fs.Args()); err != nil {
		return fmt.Errorf("%w\n\nNothing was unticked. Run: issue-cli checklist %s", err, issue.Slug)
	}
	report := boxProofReport(ctx, issue)
	if err := refuseOwnerBoxes(ctx, issue, report, fs.Args(), "untick"); err != nil {
		return err
	}

	var unticked, already []tracker.CheckboxItem
	newBody, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, u, a, err := tracker.UncheckBoxes(body, fs.Args())
		if err != nil {
			return body, false, err
		}
		unticked, already = u, a
		return next, next != body, nil
	})
	if err != nil {
		return fmt.Errorf("%w\n\nRun: issue-cli checklist %s", err, issue.Slug)
	}
	for _, it := range unticked {
		fmt.Fprintf(ctx.Stdout, "✓ Unchecked: %s\n", checkboxLabel(it))
	}
	for _, it := range already {
		fmt.Fprintf(ctx.Stdout, "  Already open: %s\n", checkboxLabel(it))
	}
	for _, it := range unticked {
		if c, ok := report.criterion(it.ID); ok && c.Proven() {
			fmt.Fprintf(ctx.Stdout, "  Note: %s's proof still passes, so it is ticked again; see issue-cli prove %s --list\n", it.ID, issue.Slug)
		}
	}
	total, checked := tracker.CountCheckboxes(newBody)
	fmt.Fprintf(ctx.Stdout, "  Progress: %d/%d\n", checked, total)
	fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
	return nil
}

// boxReport is the proof report behind the box-editing commands: which
// criteria the owner alone proves and which links each criterion has. It is
// empty when the issue's workflow doesn't link criteria to proof.
type boxReport struct {
	proof.Report
	ok bool
}

func boxProofReport(ctx *Context, issue *tracker.Issue) boxReport {
	if !ctx.Project.LoadWorkflowForIssue(issue).ProofRequired(issue) {
		return boxReport{}
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	return boxReport{Report: proof.Collect(projectRoot(ctx.Project), issue, comments), ok: true}
}

func (r boxReport) criterion(id string) (proof.Criterion, bool) {
	for _, c := range r.Criteria {
		if id != "" && strings.EqualFold(c.ID, id) {
			return c, true
		}
	}
	return proof.Criterion{}, false
}

// refuseOwnerBoxes refuses an agent session changing the owner's boxes;
// the owner, in their own shell, may.
func refuseOwnerBoxes(ctx *Context, issue *tracker.Issue, r boxReport, refs []string, verb string) error {
	who := agentSession(ctx)
	if who == "" {
		return nil
	}
	items := tracker.ListCheckboxes(issue.BodyRaw)
	var hit []string
	for _, ref := range refs {
		it, st := tracker.ResolveCheckboxRef(items, ref)
		if st != tracker.RefFound {
			continue
		}
		if why := r.OwnerBoxWhy(it); why != "" {
			hit = append(hit, ownerHitLine(r.Report, it.ID, it.Label(), why))
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return ownerBoxesError(ctx, issue, who, verb, hit)
}

// ownerHitLine is one box in an owner-box refusal; a box a playtest
// judgement step makes the owner's adds how to make the step automated.
func ownerHitLine(r proof.Report, id, text, why string) string {
	line := fmt.Sprintf("  %s %s (%s)", id, text, why)
	if hint := r.JudgementHint(id); hint != "" {
		line += "\n    " + hint
	}
	return line
}

// ownerBoxesError is the refusal every owner-box path gives an agent: the
// boxes, who asked, and where the owner ticks them.
func ownerBoxesError(ctx *Context, issue *tracker.Issue, who, verb string, hit []string) error {
	return fmt.Errorf("these boxes are the owner's; %s can't %s them, and nothing changed:\n%s\n\nThe owner ticks them on the Owner checks page:\n  %s\nUnder YOLO they wait in the review queue. Ask on your issue trail:\n  %s",
		who, verb, strings.Join(hit, "\n"), ownerChecksURL(ctx.Project, issue), tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: ...\"", issue.Slug)))
}

// ownerTickGuard is the check a body write runs before it lands: for an
// agent session, a write that would tick an owner box (proof.OwnerTicks)
// is refused; for the owner it passes everything. The proof report is
// read once, on the first write that leaves a ticked box.
func ownerTickGuard(ctx *Context, issue *tracker.Issue) func(before, after string) error {
	who := agentSession(ctx)
	if who == "" {
		return func(string, string) error { return nil }
	}
	var report *boxReport
	return func(before, after string) error {
		if _, a := tracker.CountCheckboxes(after); a == 0 {
			return nil
		}
		if report == nil {
			// Always the full report: a playtest judgement step or a guided
			// check's owner step is the owner's even when the workflow
			// doesn't link criteria to proof.
			comments, _ := tracker.LoadComments(issue.FilePath)
			report = &boxReport{Report: proof.Collect(projectRoot(ctx.Project), issue, comments), ok: true}
		}
		ticks := report.OwnerTicks(before, after)
		if len(ticks) == 0 {
			return nil
		}
		hit := make([]string, 0, len(ticks))
		for _, o := range ticks {
			id := o.ID
			if id == "" {
				id = "(new box)"
			}
			hit = append(hit, ownerHitLine(report.Report, id, o.Text, o.Why))
		}
		return ownerTickRefusal{ownerBoxesError(ctx, issue, who, "tick", hit)}
	}
}

// ownerTickRefusal is ownerTickGuard's refusal, passed through a body
// update unwrapped (writeErr).
type ownerTickRefusal struct{ error }

// writeErr is a body write's error: the owner-box refusal as it is, any
// other under what (as "failed to update: …").
func writeErr(what string, err error) error {
	var r ownerTickRefusal
	if errors.As(err, &r) {
		return r.error
	}
	return fmt.Errorf("%s: %w", what, err)
}
