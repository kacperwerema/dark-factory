package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var checkCommand = &Command{
	Name:      "check",
	ShortHelp: "Check off checkboxes by id (several at once), text, or section + index",
	LongHelp: `Mark checkboxes as checked. Address them by:

  ids            issue-cli check <slug> D3 D4 AC1      (several in one call)
  long id        issue-cli check <slug> "Design#3"
  whole section  issue-cli check <slug> --section "Design" --all
  text           issue-cli check <slug> "Code changes complete"
  index          issue-cli check <slug> --section "Design" --index 2
  position       issue-cli check <slug> --index 5

Run 'issue-cli checklist <slug>' to see each box's id. An id is the section's
initials plus the box's 1-based index in that section: D3 is the 3rd box under
"## Design", AC2 the 2nd under "## Acceptance Criteria". When two sections share
initials, the later one gets more letters of its first word (Documentation is
"Do" when Design holds "D"). Ids are case-insensitive and stable: indexes count
checked and unchecked boxes, and new sections are appended, so a box's id never
changes as work progresses.

Several ids in one call are all-or-nothing: if any id does not exist, nothing
is ticked. A box that is already checked is reported, not an error.
Acceptance criteria under the proof gate are the exception: among other ids
they are skipped with one "Not ticked" line each and the rest are ticked; the
call fails only when it ticked nothing (criteria alone, or every other box
already done).

A text query matches a box whose label contains it (case-insensitive). If it
matches more than one unchecked box, check errors and lists the candidates
with their ids. Flags may come before or after the ids or text.

A box that doesn't fit the change: tick it with its N/A reason, in a named form
  issue-cli check <slug> T4 --na "no page changes"
The forms:
` + tracker.NAFormsHelp("  ") + `
An agent never ticks an owner box (Human Testing, "(owner)", owner-only
criteria, a playtest judgement step, a box a guided check left to the
owner): check, and append/replace/update writing [x] into one, refuse it,
and check --na can't skip it. Leave it for the owner; they tick it on the
Owner checks page or in their own shell.

To change a box's text instead of ticking it: issue-cli check-edit <slug> <id> --text "..."
To untick a box: issue-cli uncheck <slug> <id>
To drop a box (the other boxes keep their ids): issue-cli check-remove <slug> <id>`,
	Run: runCheck,
}

func init() {
	registerCommand(checkCommand)
}

func runCheck(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "check")
	if err != nil {
		return err
	}
	fs := newFlagSet("check", ctx)
	sectionFlag := fs.String("section", "", "section to scope the match to (\"## <name>\")")
	indexFlag := fs.Int("index", 0, "1-based stable index of the checkbox within the section (or whole body)")
	allFlag := fs.Bool("all", false, "check every open box in --section")
	naFlag := fs.String("na", "", "tick one box as not applicable, with the reason in a named form")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	section := strings.TrimSpace(*sectionFlag)
	positional := fs.Args()
	query := strings.Join(positional, " ")

	if *naFlag != "" && (*allFlag || *indexFlag != 0 || section != "" || len(positional) != 1) {
		return naRefusal("--na ticks one box by id\n\nExample:\n  issue-cli check %s T4 --na \"no page changes\"", slug)
	}
	if *allFlag {
		if section == "" {
			return fmt.Errorf("--all requires --section\n\nExample:\n  issue-cli check %s --section \"Design\" --all", slug)
		}
		if *indexFlag != 0 || query != "" {
			return fmt.Errorf("--all ticks a whole section; do not combine it with --index, ids or text")
		}
	}
	if !*allFlag && *indexFlag == 0 && query == "" {
		return fmt.Errorf("check requires checkbox ids, a text query, or --index\n\nExamples:\n  issue-cli check %s D1 D2          # ids from 'issue-cli checklist %s'\n  issue-cli check %s \"Code changes complete\"\n  issue-cli check %s --section \"Design\" --all", slug, slug, slug, slug)
	}
	if *indexFlag != 0 && query != "" {
		return fmt.Errorf("pass either a text query or --index, not both")
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}

	refs := section == "" && *naFlag == "" && !*allFlag && *indexFlag == 0 && looksLikeRefs(issue.BodyRaw, positional)
	var proofOnly []string
	if refs {
		// Mixed ids: the plain ones are ticked, the criteria are skipped
		// with a line each. Only criteria alone refuse the call.
		if keep, crit := splitCriteriaRefs(ctx, issue, positional); len(crit) > 0 && len(keep) > 0 {
			positional, proofOnly = keep, crit
		}
	}
	if err := refuseCriteria(ctx, issue, section, *indexFlag, *allFlag, positional, query); err != nil {
		if *naFlag != "" {
			return naRefusal("%v", err)
		}
		return err
	}

	switch {
	case *naFlag != "":
		return checkNA(ctx, issue, positional[0], normalizeEscapedText(*naFlag))
	case *allFlag:
		return checkSectionAll(ctx, issue, section)
	case *indexFlag != 0:
		return checkByIndex(ctx, issue, section, *indexFlag)
	case refs:
		return checkByRefs(ctx, issue, positional, proofOnly)
	}
	return checkByText(ctx, issue, section, query)
}

// looksLikeRefs reports whether args should be read as checkbox ids rather
// than a text query: every arg is shaped like an id and at least one names a
// section of this issue. A lone word such as "phase1" whose letters name no
// section stays a text query.
func looksLikeRefs(body string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	items := tracker.ListCheckboxes(body)
	known := false
	for _, a := range args {
		if !tracker.IsCheckboxRefShaped(a) {
			return false
		}
		if _, st := tracker.ResolveCheckboxRef(items, a); st != tracker.RefNotARef {
			known = true
		}
	}
	return known
}

// checkboxLabel renders a checkbox as "<id> [Section #index] text" (or
// "[#index] text" when the box sits before any section heading).
func checkboxLabel(it tracker.CheckboxItem) string {
	if it.Section == "" {
		return fmt.Sprintf("[#%d] %s", it.Index, it.Label())
	}
	label := fmt.Sprintf("[%s #%d] %s", it.Section, it.Index, it.Label())
	if it.ID != "" {
		label = it.ID + " " + label
	}
	return label
}

// printCheckResult reports what a check call changed: one line per ticked or
// already-checked box, overall progress plus progress of each touched section,
// and the issue file.
func printCheckResult(w io.Writer, issue *tracker.Issue, newBody string, ticked, already []tracker.CheckboxItem) {
	for _, it := range ticked {
		fmt.Fprintf(w, "✓ Checked: %s\n", checkboxLabel(it))
	}
	for _, it := range already {
		fmt.Fprintf(w, "  Already checked: %s\n", checkboxLabel(it))
	}
	total, checked := tracker.CountCheckboxes(newBody)
	var sections []string
	seen := map[string]bool{}
	for _, it := range append(append([]tracker.CheckboxItem{}, ticked...), already...) {
		if it.Section == "" || seen[it.Section] {
			continue
		}
		seen[it.Section] = true
		st, sc := tracker.CountCheckboxesInSection(newBody, it.Section)
		sections = append(sections, fmt.Sprintf("%s %d/%d", it.Section, sc, st))
	}
	if len(sections) > 0 {
		fmt.Fprintf(w, "  Progress: %d/%d (%s)\n", checked, total, strings.Join(sections, ", "))
	} else {
		fmt.Fprintf(w, "  Progress: %d/%d\n", checked, total)
	}
	fmt.Fprintf(w, "file: %s\n", issue.FilePath)
}

// splitCriteriaRefs splits ids into the ones check may tick and the
// unchecked acceptance criteria among them, which the proof gate ticks
// (none when the gate doesn't cover the issue).
func splitCriteriaRefs(ctx *Context, issue *tracker.Issue, refs []string) (keep, criteria []string) {
	if !ctx.Project.LoadWorkflowForIssue(issue).ProofRequired(issue) {
		return refs, nil
	}
	items := tracker.ListCheckboxes(issue.BodyRaw)
	for _, ref := range refs {
		if it, st := tracker.ResolveCheckboxRef(items, ref); st == tracker.RefFound && !it.Checked && strings.EqualFold(it.Section, tracker.CriteriaSection) {
			criteria = append(criteria, it.ID)
			continue
		}
		keep = append(keep, ref)
	}
	return keep, criteria
}

// checkByRefs ticks the boxes refs name. proofOnly are criteria the call
// named but the proof gate ticks: each gets a line, and the call fails when
// it ticked nothing else.
func checkByRefs(ctx *Context, issue *tracker.Issue, refs, proofOnly []string) error {
	guard := ownerTickGuard(ctx, issue)
	var ticked, already []tracker.CheckboxItem
	var unknown []string
	newBody, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		items := tracker.ListCheckboxes(body)
		seen := map[int]bool{}
		var lines []int
		for _, ref := range refs {
			it, st := tracker.ResolveCheckboxRef(items, ref)
			if st != tracker.RefFound {
				unknown = append(unknown, ref)
				continue
			}
			if seen[it.Line] {
				continue
			}
			seen[it.Line] = true
			if it.Checked {
				already = append(already, it)
				continue
			}
			ticked = append(ticked, it)
			lines = append(lines, it.Line)
		}
		if len(unknown) > 0 || len(lines) == 0 {
			return body, false, nil
		}
		next := tracker.CheckLines(body, lines)
		return next, true, guard(body, next)
	})
	if err != nil {
		return writeErr("failed to update", err)
	}
	if len(unknown) > 0 {
		fmt.Fprintf(ctx.Stdout, "No checkbox with id %s — nothing was ticked.\n\n", strings.Join(unknown, ", "))
		fmt.Fprintln(ctx.Stdout, "Checkboxes:")
		printCheckboxes(ctx.Stdout, newBody)
		return fmt.Errorf("unknown checkbox id(s): %s", strings.Join(unknown, ", "))
	}
	for i := range ticked {
		ticked[i].Checked = true
	}
	printCheckResult(ctx.Stdout, issue, newBody, ticked, already)
	for _, id := range proofOnly {
		fmt.Fprintf(ctx.Stdout, "✗ Not ticked: %s is ticked by its proof, not by hand (issue-cli prove %s --list)\n", id, issue.Slug)
	}
	if len(proofOnly) > 0 && len(ticked) == 0 {
		return fmt.Errorf("nothing was ticked: acceptance criteria are ticked by their proof, not by hand: %s\n\nSee what proves each one and what is missing:\n  issue-cli prove %s --list", strings.Join(proofOnly, ", "), issue.Slug)
	}
	return nil
}

// checkNA ticks one box as not applicable: its reason, in a named form
// (tracker.NAForms), goes into the box's text. The owner's boxes are
// refused: an N/A never takes a box from the owner.
func checkNA(ctx *Context, issue *tracker.Issue, ref, reason string) error {
	reason = strings.TrimSpace(reason)
	it, st := tracker.ResolveCheckboxRef(tracker.ListCheckboxes(issue.BodyRaw), ref)
	if st != tracker.RefFound {
		fmt.Fprintf(ctx.Stdout, "No checkbox with id %s — nothing was ticked.\n\nCheckboxes:\n", ref)
		printCheckboxes(ctx.Stdout, issue.BodyRaw)
		return naRefusal("unknown checkbox id: %s", ref)
	}
	report := boxProofReport(ctx, issue)
	if why := report.OwnerBoxWhy(it); why != "" {
		hint := ""
		if h := report.JudgementHint(it.ID); h != "" {
			hint = "\n\n" + h + "."
		}
		return naRefusal("%s is the owner's box (%s): an N/A can't skip it, and nothing changed. Leave it for the owner; under YOLO it waits in the review queue.%s\n\nAsk on your issue trail if it really doesn't apply:\n  %s",
			it.ID, why, hint, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: ...\"", issue.Slug)))
	}
	if tracker.NANoForm(it.Section) {
		return naRefusal("%s is a %s box: shipping steps have no N/A form. Tick it plainly when it's done (a box that says \"or none\" is ticked as it is):\n  %s",
			it.ID, it.Section, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli check %s %s", issue.Slug, it.ID)))
	}
	if p := tracker.CheckNA(reason); p != "" {
		return naRefusal("%s.\n\nWrite it in one of these forms:\n%s\n\nExample:\n  issue-cli check %s %s --na \"no page changes\"",
			p, tracker.NAFormsHelp("  "), issue.Slug, ref)
	}
	var item tracker.CheckboxItem
	newBody, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, edited, err := tracker.EditCheckboxText(body, ref, tracker.WithNA(it.Label(), reason))
		if err != nil {
			return body, false, err
		}
		item = edited
		return tracker.CheckLines(next, []int{edited.Line}), true, nil
	})
	if err != nil {
		return naRefusal("%v\n\nRun: issue-cli checklist %s", err, issue.Slug)
	}
	for _, now := range tracker.ListCheckboxes(newBody) {
		if now.Line == item.Line {
			item = now
		}
	}
	printCheckResult(ctx.Stdout, issue, newBody, []tracker.CheckboxItem{item}, nil)
	return nil
}

// naRefusal is every check --na refusal: it exits non-zero and starts
// with "✗ not ticked:", so a refused N/A never reads as a tick.
func naRefusal(format string, args ...any) error {
	return &exitCodeError{Code: 1, Msg: "✗ not ticked: " + fmt.Sprintf(format, args...)}
}

func checkSectionAll(ctx *Context, issue *tracker.Issue, section string) error {
	guard := ownerTickGuard(ctx, issue)
	var ticked, inSection []tracker.CheckboxItem
	newBody, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		var lines []int
		for _, it := range tracker.ListCheckboxes(body) {
			if !strings.EqualFold(it.Section, section) {
				continue
			}
			inSection = append(inSection, it)
			if !it.Checked {
				ticked = append(ticked, it)
				lines = append(lines, it.Line)
			}
		}
		if len(lines) == 0 {
			return body, false, nil
		}
		next := tracker.CheckLines(body, lines)
		return next, true, guard(body, next)
	})
	if err != nil {
		return writeErr("failed to update", err)
	}
	if len(inSection) == 0 {
		fmt.Fprintf(ctx.Stdout, "No checkboxes in section %q\n\n", section)
		fmt.Fprintln(ctx.Stdout, "Checkboxes:")
		printCheckboxes(ctx.Stdout, newBody)
		return fmt.Errorf("no checkboxes in section %q", section)
	}
	name := inSection[0].Section
	if len(ticked) == 0 {
		fmt.Fprintf(ctx.Stdout, "Nothing to check: section %q already complete (%d/%d)\n", name, len(inSection), len(inSection))
		fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
		return nil
	}
	for i := range ticked {
		ticked[i].Checked = true
	}
	printCheckResult(ctx.Stdout, issue, newBody, ticked, nil)
	return nil
}

func checkByIndex(ctx *Context, issue *tracker.Issue, section string, index int) error {
	guard := ownerTickGuard(ctx, issue)
	var matched tracker.CheckboxItem
	var already, ok bool
	newBody, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		updated, item, was, found := tracker.CheckByIndex(body, section, index)
		matched, already, ok = item, was, found
		return updated, found && !was, guard(body, updated)
	})
	if err != nil {
		return writeErr("failed to update", err)
	}
	if !ok {
		scope := "the body"
		if section != "" {
			scope = fmt.Sprintf("section %q", section)
		}
		fmt.Fprintf(ctx.Stdout, "No checkbox at index %d in %s\n\n", index, scope)
		fmt.Fprintln(ctx.Stdout, "Checkboxes:")
		printCheckboxes(ctx.Stdout, newBody)
		return fmt.Errorf("no checkbox at index %d in %s", index, scope)
	}
	if already {
		printCheckResult(ctx.Stdout, issue, newBody, nil, []tracker.CheckboxItem{matched})
		return nil
	}
	matched.Checked = true
	printCheckResult(ctx.Stdout, issue, newBody, []tracker.CheckboxItem{matched}, nil)
	return nil
}

func checkByText(ctx *Context, issue *tracker.Issue, section, query string) error {
	matches := tracker.MatchUncheckedByText(issue.BodyRaw, section, query)
	switch len(matches) {
	case 0:
		scope := ""
		if section != "" {
			scope = fmt.Sprintf(" in section %q", section)
		}
		fmt.Fprintf(ctx.Stdout, "No unchecked item matching \"%s\"%s\n\n", query, scope)
		fmt.Fprintln(ctx.Stdout, "Unchecked items:")
		printUncheckedItems(ctx.Stdout, issue.BodyRaw)
		return fmt.Errorf("no unchecked item matched %q", query)
	case 1:
		// fall through to the single-match check below
	default:
		fmt.Fprintf(ctx.Stdout, "Ambiguous: %d unchecked boxes match \"%s\":\n", len(matches), query)
		for _, it := range matches {
			fmt.Fprintf(ctx.Stdout, "  %s\n", checkboxLabel(it))
		}
		first := matches[0]
		if first.ID != "" {
			fmt.Fprintf(ctx.Stdout, "\nRe-run with the box's id, e.g.:\n  issue-cli check %s %s\n", issue.Slug, first.ID)
		} else {
			fmt.Fprintf(ctx.Stdout, "\nRe-run with the exact box, e.g.:\n  issue-cli check %s --index %d\n", issue.Slug, first.Index)
		}
		return fmt.Errorf("%q is ambiguous — %d unchecked boxes match", query, len(matches))
	}

	target := matches[0]
	guard := ownerTickGuard(ctx, issue)
	newBody, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		updated, _, _, found := tracker.CheckByIndex(body, target.Section, target.Index)
		return updated, found, guard(body, updated)
	})
	if err != nil {
		return writeErr("failed to update", err)
	}
	target.Checked = true
	printCheckResult(ctx.Stdout, issue, newBody, []tracker.CheckboxItem{target}, nil)
	return nil
}

// printUncheckedItems lists every unchecked box with its id and
// [Section #index] label.
func printUncheckedItems(w io.Writer, body string) {
	for _, it := range tracker.ListCheckboxes(body) {
		if !it.Checked {
			fmt.Fprintf(w, "  %s\n", checkboxLabel(it))
		}
	}
}

// refuseCriteria stops check from ticking an acceptance criterion while the
// proof gate covers the issue: a criterion is ticked by its proof (prove).
func refuseCriteria(ctx *Context, issue *tracker.Issue, section string, index int, all bool, positional []string, query string) error {
	wf := ctx.Project.LoadWorkflowForIssue(issue)
	if !wf.ProofRequired(issue) {
		return nil
	}
	items := tracker.ListCheckboxes(issue.BodyRaw)
	var hit []string
	switch {
	case all:
		if strings.EqualFold(section, tracker.CriteriaSection) {
			for _, it := range tracker.Criteria(issue.BodyRaw) {
				if !it.Checked {
					hit = append(hit, it.ID)
				}
			}
		}
	case index != 0:
		if _, it, already, ok := tracker.CheckByIndex(issue.BodyRaw, section, index); ok && !already && strings.EqualFold(it.Section, tracker.CriteriaSection) {
			hit = append(hit, it.ID)
		}
	case section == "" && looksLikeRefs(issue.BodyRaw, positional):
		for _, ref := range positional {
			if it, st := tracker.ResolveCheckboxRef(items, ref); st == tracker.RefFound && strings.EqualFold(it.Section, tracker.CriteriaSection) && !it.Checked {
				hit = append(hit, it.ID)
			}
		}
	default:
		if m := tracker.MatchUncheckedByText(issue.BodyRaw, section, query); len(m) == 1 && strings.EqualFold(m[0].Section, tracker.CriteriaSection) {
			hit = append(hit, m[0].ID)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return fmt.Errorf("acceptance criteria are ticked by their proof, not by hand: %s\n\nSee what proves each one and what is missing:\n  issue-cli prove %s --list\nA criterion is ticked as soon as one of its links passes.", strings.Join(hit, ", "), issue.Slug)
}
