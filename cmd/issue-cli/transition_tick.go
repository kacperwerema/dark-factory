package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// parseCheckFlag pulls transition's --check ids out of args: the ids after
// one --check (as long as each is shaped like a box id), a comma list, or
// the flag repeated. given reports whether the flag was there at all.
func parseCheckFlag(args []string) (ids, rest []string, given bool, err error) {
	add := func(v string) {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "check" {
			rest = append(rest, a)
			continue
		}
		given = true
		if hasVal {
			add(val)
			continue
		}
		for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && tracker.IsCheckboxRefShaped(strings.Split(args[i+1], ",")[0]) {
			i++
			add(args[i])
		}
	}
	if given && len(ids) == 0 {
		return nil, nil, true, fmt.Errorf("--check takes the ids of the boxes to tick first (from 'issue-cli checklist <slug>')\n\nExample:\n  issue-cli transition <slug> --to testing --check Im1 Im2 TP1")
	}
	return ids, rest, given, nil
}

// tickForTransition ticks ids the way check does (same output, same
// rules: an unknown id refuses it all; an owner box or a criterion under
// the proof gate among other ids is left open with a line, the rest tick)
// before transition moves the issue. When it ticked nothing, nothing moves.
// Under --json check's lines go to stderr so stdout stays one JSON value.
func tickForTransition(ctx *Context, issue *tracker.Issue, ids []string) error {
	if err := refuseNonRefs(ctx, issue, ids); err != nil {
		return err
	}
	if ctx.JSONOutput {
		out := ctx.Stdout
		ctx.Stdout = ctx.Stderr
		defer func() { ctx.Stdout = out }()
	}
	if err := runCheck(ctx, append([]string{issue.Slug}, ids...)); err != nil {
		return fmt.Errorf("%w\n\nNothing was moved: the issue stays at %s", err, issue.Status)
	}
	return nil
}

// refuseNonRefs stops --check ids that check would read as a text query
// (none names a section of this issue): transition --check takes ids only.
func refuseNonRefs(ctx *Context, issue *tracker.Issue, ids []string) error {
	if looksLikeRefs(issue.BodyRaw, ids) {
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "No checkbox with id %s — nothing was ticked.\n\nCheckboxes:\n", strings.Join(ids, ", "))
	printIssueCheckboxes(ctx, ctx.Stdout, issue, issue.BodyRaw)
	return fmt.Errorf("unknown checkbox id(s): %s\n\nNothing was moved: the issue stays at %s", strings.Join(ids, ", "), issue.Status)
}

// probeTicked is issue as it would be with ids ticked, for --dry-run
// --check: the same refusals as check, nothing written.
func probeTicked(ctx *Context, issue *tracker.Issue, ids []string) (*tracker.Issue, error) {
	if err := refuseNonRefs(ctx, issue, ids); err != nil {
		return nil, err
	}
	keep, crit := splitCriteriaRefs(ctx, issue, ids)
	if len(crit) > 0 && len(keep) == 0 {
		return nil, fmt.Errorf("nothing would be ticked: acceptance criteria are ticked by their proof, not by hand: %s\n\nSee what proves each one and what is missing:\n  %s",
			strings.Join(crit, ", "), tracker.InjectProject(ctx.Project, "issue-cli prove "+issue.Slug+" --list"))
	}
	items := tracker.ListCheckboxes(issue.BodyRaw)
	var lines []int
	var unknown []string
	for _, ref := range keep {
		it, st := tracker.ResolveCheckboxRef(items, ref)
		if st != tracker.RefFound {
			unknown = append(unknown, ref)
			continue
		}
		if !it.Checked {
			lines = append(lines, it.Line)
		}
	}
	if len(unknown) > 0 {
		fmt.Fprintf(ctx.Stdout, "No checkbox with id %s — nothing was ticked.\n\nCheckboxes:\n", strings.Join(unknown, ", "))
		printIssueCheckboxes(ctx, ctx.Stdout, issue, issue.BodyRaw)
		return nil, fmt.Errorf("unknown checkbox id(s): %s", strings.Join(unknown, ", "))
	}
	for _, id := range crit {
		fmt.Fprintf(ctx.Stdout, "✗ Would not tick: %s is ticked by its proof, not by hand\n", id)
	}
	// The owner's boxes: left open with a line, as check leaves them.
	owners := newOwnerTicks(ctx, issue)
	var refused []proof.OwnerItem
	var mine []int
	for _, line := range lines {
		if hit := owners.of(issue.BodyRaw, tracker.CheckLines(issue.BodyRaw, []int{line})); len(hit) > 0 {
			refused = append(refused, hit...)
			continue
		}
		mine = append(mine, line)
	}
	if len(refused) > 0 && len(mine) == 0 {
		return nil, owners.refusal(refused)
	}
	for _, t := range refused {
		fmt.Fprintf(ctx.Stdout, "✗ Would not tick, the owner's: %s\n", strings.TrimSpace(owners.hitLine(t)))
	}
	lines = mine
	probe := *issue
	if len(lines) == 0 {
		return &probe, nil
	}
	next := tracker.CheckLines(issue.BodyRaw, lines)
	if err := ownerTickGuard(ctx, issue)(issue.BodyRaw, next); err != nil {
		return nil, err
	}
	probe.BodyRaw = next
	return &probe, nil
}

// oneCallLine is the transition that would pass once the open boxes are
// ticked: issue-cli transition <slug> --to <s> --check <ids>. It is empty
// unless every problem left (after YOLO drops the approval it would pass)
// is a checkbox gate and every box those gates wait on is the agent's to
// tick: not the owner's, not a criterion its proof ticks.
//
// It previews on its own copy of the config: the preview's ledger holds
// must not reach a real attempt's held events (recordDecisionHolds reads
// wf's), and a gate command is not run again (SkipCommandRules): the
// transition runs it when the one call comes.
func oneCallLine(ctx *Context, base *tracker.WorkflowConfig, issue *tracker.Issue, to string, fields map[string]string) string {
	wf, err := issueWorkflow(ctx, issue)
	if err != nil {
		return ""
	}
	wf.DeferOwnerProof, wf.DeferDecisionHolds, wf.KeepOwnAssignee = base.DeferOwnerProof, base.DeferDecisionHolds, base.KeepOwnAssignee
	wf.SkipCommandRules = true
	problems := collectTransitionProblems(ctx, wf, issue, issue.Slug, issue.Status, to, fields)
	problems, _ = yoloDryRun(ctx, wf, issue, issue.Status, to, problems)
	if len(problems) == 0 {
		return ""
	}
	want := map[string]bool{}
	for _, p := range problems {
		if p.Kind != "validator" || len(p.boxes) == 0 {
			return ""
		}
		for _, id := range p.boxes {
			want[strings.ToUpper(id)] = true
		}
	}
	proofTicks := wf.ProofRequired(issue)
	var ids []string
	var lines []int
	for _, it := range tracker.ListCheckboxes(issue.BodyRaw) {
		if it.Checked || !want[strings.ToUpper(it.ID)] {
			continue
		}
		if proofTicks && strings.EqualFold(it.Section, tracker.CriteriaSection) {
			return ""
		}
		ids = append(ids, it.ID)
		lines = append(lines, it.Line)
	}
	if len(ids) != len(want) {
		return ""
	}
	if ownerTickGuard(ctx, issue)(issue.BodyRaw, tracker.CheckLines(issue.BodyRaw, lines)) != nil {
		return ""
	}
	cmd := fmt.Sprintf("issue-cli transition %s --to %s", issue.Slug, shell.Quote(to))
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cmd += " --field " + shell.Quote(k+"="+fields[k])
	}
	return tracker.InjectProject(ctx.Project, cmd+" --check "+strings.Join(ids, " "))
}

// withOneCallLine ends a refused transition with oneCallLine when there is
// one, so the agent's next call ticks the boxes and moves the issue.
func withOneCallLine(ctx *Context, wf *tracker.WorkflowConfig, slug, to string, fields map[string]string, err error) error {
	issue, _, ferr := findIssueOrErr(ctx, slug)
	if ferr != nil {
		return err
	}
	line := oneCallLine(ctx, wf, issue, to, fields)
	if line == "" {
		return err
	}
	return fmt.Errorf("%w\n\nTick them and move in one call:\n  %s", err, line)
}

// printOneCallLine is withOneCallLine for a dry run's report.
func printOneCallLine(w io.Writer, line string) {
	if line == "" {
		return
	}
	fmt.Fprintf(w, "Tick them and move in one call:\n  %s\n", line)
}

// openCheckboxIDs are the open boxes a checkbox gate (rule) waits on.
func openCheckboxIDs(body, rule string) []string {
	name, arg := tracker.SplitRule(rule)
	if name != "section_checkboxes_checked" && name != "all_checkboxes_checked" {
		return nil
	}
	var ids []string
	for _, item := range tracker.ListCheckboxes(body) {
		if item.Checked || (name == "section_checkboxes_checked" && !strings.EqualFold(item.Section, arg)) {
			continue
		}
		if item.ID == "" {
			return nil // a box with no id can't be named by --check
		}
		ids = append(ids, item.ID)
	}
	return ids
}

// hasOpenBox reports whether any box with an id is still open that isn't
// the owner's (the tick guard's test, carried in Owner): the --check hint.
func hasOpenBox(items []transitionChecklistItem) bool {
	for _, it := range items {
		if !it.Checked && it.ID != "" && it.Owner == "" {
			return true
		}
	}
	return false
}

// checkSectionNote is a line for each section transition --check ids
// landed in that the move doesn't wait on, when it waits on others: ids
// are claimed by section in order, so "I2" at in progress is Idea's box
// (Implementation's are Im1, Im2, …), and check's own "Already checked: I2"
// doesn't say which section it was in. Nothing when the move's sections
// are all ticked. body is the issue after the tick.
func checkSectionNote(wf *tracker.WorkflowConfig, body, from, to string, ids []string) []string {
	t := wf.ResolveTransition(from, to)
	if t == nil {
		return nil
	}
	var gated []string
	for _, a := range t.Actions {
		if name, arg := tracker.SplitRule(a.Rule); a.Type == "validate" && name == "section_checkboxes_checked" && strings.TrimSpace(arg) != "" {
			gated = append(gated, strings.TrimSpace(arg))
		}
	}
	if len(gated) == 0 {
		return nil
	}
	waits := func(section string) bool {
		for _, g := range gated {
			if strings.EqualFold(g, section) {
				return true
			}
		}
		return false
	}
	items := tracker.ListCheckboxes(body)
	var order []string
	bySection := map[string][]string{}
	for _, ref := range ids {
		it, st := tracker.ResolveCheckboxRef(items, ref)
		if st != tracker.RefFound || it.Section == "" || waits(it.Section) || strings.EqualFold(it.Section, tracker.CriteriaSection) {
			continue
		}
		if _, ok := bySection[it.Section]; !ok {
			order = append(order, it.Section)
		}
		bySection[it.Section] = append(bySection[it.Section], it.ID)
	}
	if len(order) == 0 {
		return nil
	}
	var waiting []string
	anyOpen := false
	for _, g := range gated {
		var open []string
		for _, it := range items {
			if strings.EqualFold(it.Section, g) && !it.Checked && it.ID != "" {
				open = append(open, it.ID)
			}
		}
		state := "all ticked"
		if len(open) > 0 {
			state = "open: " + strings.Join(open, " ")
			anyOpen = true
		}
		waiting = append(waiting, fmt.Sprintf("## %s (%s)", g, state))
	}
	if !anyOpen {
		return nil // the move's sections are done: a box ticked for a later gate is no mistake
	}
	var out []string
	for _, s := range order {
		out = append(out, fmt.Sprintf("%s ticked in ## %s; this move waits on %s", strings.Join(bySection[s], ", "), s, strings.Join(waiting, ", ")))
	}
	return out
}
