package main

import (
	"errors"
	"fmt"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var closeCommand = &Command{
	Name:      "close",
	ShortHelp: "Park a duplicate, or an issue another one fixes, with a link both ways",
	LongHelp: `Park an issue from any status because another issue covers it: a
duplicate of it (--duplicate-of) or one whose change fixes it (--fixed-by).
One of the two is required.

It runs the workflow's park transition (the side status every status can move
to, e.g. parked) with its required fields answered from the link, sets
duplicate_of: or fixed_by: on the closed issue, and comments on both issues.
No checkbox on either issue changes.

` + tracker.CloseRule + `

Undo: issue-cli transition <slug> --to "<previous status>".

Examples:
  issue-cli close ui/old-idea --duplicate-of ui/new-idea
  issue-cli close cli/flaky-flag --fixed-by cli/flag-parser-rewrite`,
	Run: runClose,
}

func init() {
	registerCommand(closeCommand)
}

func runClose(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "close")
	if err != nil {
		return err
	}
	fs := newFlagSet("close", ctx)
	dupFlag := fs.String("duplicate-of", "", "the issue this one duplicates")
	fixedFlag := fs.String("fixed-by", "", "the issue whose change fixes this one")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	usage := fmt.Sprintf("\n\nExamples:\n  issue-cli close %s --duplicate-of <slug>\n  issue-cli close %s --fixed-by <slug>", slug, slug)
	key, other, verb := "duplicate_of", *dupFlag, "duplicate of"
	switch {
	case *dupFlag != "" && *fixedFlag != "":
		return errors.New("close takes --duplicate-of or --fixed-by, not both" + usage)
	case *fixedFlag != "":
		key, other, verb = "fixed_by", *fixedFlag, "fixed by"
	case *dupFlag == "":
		return errors.New("close needs --duplicate-of <slug> or --fixed-by <slug>" + usage)
	}

	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	target, _, err := findIssueOrErr(ctx, other)
	if err != nil {
		return err
	}
	if target.Slug == issue.Slug {
		return fmt.Errorf("close %s changed nothing: an issue can't be a %s itself", issue.Slug, verb)
	}
	if err := closeAllowed(ctx, issues, issue, target); err != nil {
		return err
	}
	wf, err := issueWorkflow(ctx, issue)
	if err != nil {
		return err
	}
	park := parkStatus(wf, issue.Status)
	if park == "" {
		return fmt.Errorf("close %s changed nothing: the workflow has no status every status can move to (a global optional status with a from: \"*\" transition, e.g. parked)", issue.Slug)
	}
	if next, _, _ := defaultNextStep(wf, issue.Status); issue.Status == park || (next == "" && !wf.IsSideStatus(issue.Status)) {
		return fmt.Errorf("close %s changed nothing: it is already %s", issue.Slug, issue.Status)
	}

	reason := fmt.Sprintf("%s %s.", capitalize(verb), target.Slug)
	fields := map[string]string{}
	if t := wf.ResolveTransition(issue.Status, park); t != nil {
		for _, f := range t.Fields {
			if f.Required {
				fields[f.Name] = reason
			}
		}
	}
	gate, err := passYOLOGate(ctx, wf, issue, issues, park, fields)
	if err != nil {
		return err
	}
	from, _, err := wf.ApplyTransitionToFileWithFields(issue.FilePath, park, fields)
	if err != nil {
		return yoloRefusal(ctx.Stderr, gate, issue.Status, park, ctx.now(), fmt.Errorf("close %s: %w", issue.Slug, err))
	}
	if err := tracker.SetFrontmatterField(issue.FilePath, key, target.Slug, false); err != nil {
		return fmt.Errorf("close %s: parked, but setting %s failed: %w", issue.Slug, key, err)
	}
	author := commentAuthor(ctx.Project, issue)
	if err := tracker.AddCommentBy(issue.FilePath, 0, fmt.Sprintf("closed: %s %s (was %s)", verb, target.Slug, from), "cli", author); err != nil {
		return fmt.Errorf("close %s: parked, but the comment failed: %w", issue.Slug, err)
	}
	if err := tracker.AddCommentBy(target.FilePath, 0, fmt.Sprintf("closed: %s as %s this issue", issue.Slug, verb), "cli", author); err != nil {
		return fmt.Errorf("close %s: parked, but the comment on %s failed: %w", issue.Slug, target.Slug, err)
	}

	fmt.Fprintf(ctx.Stdout, "✓ %s: %s → %s (%s %s)\n", issue.Slug, from, park, verb, target.Slug)
	fmt.Fprintf(ctx.Stdout, "✓ Set %s = %q on %s\n", key, target.Slug, issue.Slug)
	fmt.Fprintf(ctx.Stdout, "✓ Comment on %s and on %s\n", issue.Slug, target.Slug)
	printYOLOGate(ctx.Stdout, gate, from, park, ctx.now())
	handoffEnded(ctx, ctx.Stdout, wf, issue.Slug, park)
	fmt.Fprintf(ctx.Stdout, "Undo: %s\n", tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli transition %s --to %q", issue.Slug, from)))
	return nil
}

// closeAllowed applies tracker.CloseRule: a worker closes only issues it names its
// own issue for; a worker whose issue is unknown can't show that and is
// refused. Leads and the human pass.
func closeAllowed(ctx *Context, issues []*tracker.Issue, issue, target *tracker.Issue) error {
	me := whoAmI(ctx.Project, issues)
	if me.Role != tracker.RoleWorker {
		return nil
	}
	own := me.Slug
	if found := tracker.FindIssue(issues, own); found != nil {
		own = found.Slug
	}
	if own != "" && target.Slug == own {
		return nil
	}
	msg := fmt.Sprintf("close %s changed nothing: %s", issue.Slug, tracker.CloseRule)
	if own == "" {
		return fmt.Errorf("%s\n\nThis session's issue is unknown, so it can't close anything.", msg)
	}
	return fmt.Errorf("%s\n\nYou work on %s. Ask on your issue trail:\n  %s", msg, own,
		tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: ...\"", own)))
}

// parkStatus returns the side status the issue can move to from status:
// "parked" when the workflow has it, else the first other one.
func parkStatus(wf *tracker.WorkflowConfig, status string) string {
	found := ""
	for _, s := range wf.Statuses {
		if !wf.IsSideStatus(s.Name) {
			continue
		}
		t := wf.ResolveTransition(status, s.Name)
		if t == nil && s.Name != status {
			continue
		}
		if s.Name == "parked" {
			return s.Name
		}
		if found == "" {
			found = s.Name
		}
	}
	return found
}
