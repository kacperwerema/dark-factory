package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var updateCommand = &Command{
	Name:      "update",
	ShortHelp: `Change an issue's title (keeps its slug), replace its body and/or set its parent epic`,
	LongHelp: `Update issue title, body and/or parent. At least one of --title, --body,
--body-file or --parent is required.

A new title keeps the issue's slug, so links, parent:/depends_on: references
and branch names stay valid: the old slug is pinned with a slug: field. Pass
--rename to let the slug follow the new title instead; the file is renamed to
match and issues that still reference the old slug are listed.

--body replaces the whole body (frontmatter and comments are kept);
--body-file <path> (or - for stdin) reads it raw, as create and append do,
so backticks and $ survive the shell.

--parent <epic> sets parent:, the epic the issue belongs to; a slug that
isn't an epic on the board (type epic, or already another issue's parent) is
refused. To clear it: issue-cli set-meta <slug> --key parent --clear.

Examples:
  issue-cli update <slug> --title "new title"
  issue-cli update <slug> --title "new title" --rename
  issue-cli update <slug> --body "new body content"
  issue-cli update <slug> --body-file body.md
  issue-cli update <slug> --parent cli/some-epic`,
	Run: runUpdate,
}

func init() {
	registerCommand(updateCommand)
}

func runUpdate(ctx *Context, args []string) error {
	slug, rest, err := issueSlug(ctx, args, "update")
	if err != nil {
		return err
	}
	fs := newFlagSet("update", ctx)
	titleFlag := fs.String("title", "", "new issue title (the slug is kept)")
	bodyFlag := fs.String("body", "", "new issue body")
	bodyFileFlag := fs.String("body-file", "", "read the new body from a file (or - for stdin); avoids shell mangling")
	parentFlag := fs.String("parent", "", parentFlagHelp)
	renameFlag := fs.Bool("rename", false, "let the slug follow the title: rename the file and drop the slug: pin")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}

	titleSet := flagWasSet(fs, "title")
	bodySet := flagWasSet(fs, "body") || flagWasSet(fs, "body-file")
	parentSet := flagWasSet(fs, "parent")
	if !titleSet && !bodySet && !parentSet && !*renameFlag {
		return fmt.Errorf("update requires --title, --body, --body-file or --parent\n\nExample:\n  issue-cli update %s --title \"new title\" --body \"new body content\"", slug)
	}
	body, err := resolveBodyInput(ctx, normalizeEscapedText(*bodyFlag), flagWasSet(fs, "body"), *bodyFileFlag, flagWasSet(fs, "body-file"))
	if err != nil {
		return err
	}

	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	var parent string
	if parentSet {
		if parent, err = checkParent(issues, *parentFlag, issue.Slug); err != nil {
			return fmt.Errorf("update: %w", err)
		}
	}
	update := tracker.IssueUpdate{}
	if titleSet || *renameFlag {
		title := issue.Title
		if titleSet {
			title = *titleFlag
		}
		update = tracker.RetitleUpdate(issue, title, *renameFlag)
	}
	if parentSet {
		update.ExtraFields = map[string]string{"parent": parent}
	}
	if bodySet {
		b := body
		if err := ownerTickGuard(ctx, issue)(issue.BodyRaw, b); err != nil {
			return writeErr("failed to update", err)
		}
		update.Body = &b
	}
	newPath := issue.FilePath
	if *renameFlag {
		newPath = filepath.Join(filepath.Dir(issue.FilePath), tracker.Slugify(*update.Title)+".md")
		if _, err := os.Stat(newPath); err == nil && newPath != issue.FilePath {
			return fmt.Errorf("cannot rename: %s already exists\n\nPick another title, or keep the slug by dropping --rename", newPath)
		}
	}
	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, update); err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}
	if newPath != issue.FilePath {
		if err := os.Rename(issue.FilePath, newPath); err != nil {
			return fmt.Errorf("updated, but renaming the file failed: %w", err)
		}
	}

	switch {
	case *renameFlag:
		newSlug := strings.TrimSuffix(issue.Slug, tracker.BaseSlug(issue)) + tracker.Slugify(*update.Title)
		fmt.Fprintf(ctx.Stdout, "✓ Renamed: %s → %s\n", issue.Slug, newSlug)
		printStaleReferences(ctx, issues, issue.Slug)
	case update.Slug != nil:
		fmt.Fprintf(ctx.Stdout, "✓ Retitled: %s (slug kept: pinned with slug: in frontmatter)\n", issue.Slug)
		fmt.Fprintf(ctx.Stdout, "  To let the slug follow the title: issue-cli update %s --title %s --rename\n", issue.Slug, shell.Quote(*update.Title))
	default:
		fmt.Fprintf(ctx.Stdout, "✓ Updated: %s\n", issue.Slug)
	}
	if parentSet {
		fmt.Fprintf(ctx.Stdout, "  parent: %s\n", parent)
		if tracker.UsesLegacyUmbrella(issue) {
			fmt.Fprintf(ctx.Stdout, "  ⚠ umbrella: %s is still set; parent: wins. Drop it: issue-cli set-meta %s --key umbrella --clear\n", issue.Field("umbrella"), issue.Slug)
		}
	}
	fmt.Fprintf(ctx.Stdout, "file: %s\n", newPath)
	return nil
}

// printStaleReferences lists the issues whose parent: or depends_on: still
// name old after a rename; they are reported, not rewritten.
func printStaleReferences(ctx *Context, issues []*tracker.Issue, old string) {
	var stale []string
	for _, it := range issues {
		if it.Slug == old {
			continue
		}
		refs := tracker.DependsOn(it)
		if p := tracker.ParentSlug(it); p != "" {
			refs = append(refs, strings.ToLower(p))
		}
		for _, r := range refs {
			if r == old {
				stale = append(stale, it.Slug)
				break
			}
		}
	}
	if len(stale) == 0 {
		return
	}
	fmt.Fprintf(ctx.Stdout, "⚠ %d issue(s) still reference %s in parent:/depends_on: — update them:\n", len(stale), old)
	for _, s := range stale {
		fmt.Fprintf(ctx.Stdout, "  %s\n", s)
	}
}
