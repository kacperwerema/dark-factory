package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

const catalogBumpUsage = `Usage:
  issue-cli workflow catalog-bump <id>[,<id>...] [--version vX.Y.Z] [--dry-run]   # in the issue-viewer repo

For issue-cli developers: sets each catalog entry's version: (in
cmd/issue-cli/templates/workflow-features.yaml or its own file under
workflow-features/) and carries every bundled template the entry applies to
along: its template: stamp rises to that version, and its features: record
line <id>@<old> becomes <id>@<version>. The default version is the next
release (the one after the newest ## vX.Y.Z in cmd/issue-cli/CHANGELOG.md).
Only those lines change; --dry-run lists them without writing.`

// runCatalogBump bumps catalog entries and the template stamps with them.
func runCatalogBump(ctx *Context, args []string) error {
	fs := newFlagSet("workflow catalog-bump", ctx)
	versionFlag := fs.String("version", "", "version to set (default: the next release)")
	dryRun := fs.Bool("dry-run", false, "list the changes without writing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	var ids []string
	for _, a := range fs.Args() {
		for _, id := range strings.Split(a, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return &usageError{msg: "workflow catalog-bump needs an entry id\n\n" + catalogBumpUsage}
	}
	root, err := catalogGit("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("catalog-bump runs inside a git checkout of issue-viewer: %w", err)
	}
	version := strings.TrimSpace(*versionFlag)
	if version == "" {
		changelog, err := os.ReadFile(filepath.Join(root, workflowupgrade.ChangelogPath))
		if err != nil {
			return fmt.Errorf("%s not found: catalog-bump runs in the issue-viewer repository\n\n%s", workflowupgrade.ChangelogPath, catalogBumpUsage)
		}
		last, ok := workflowupgrade.LastRelease(changelog)
		if !ok {
			return fmt.Errorf("no `## vX.Y.Z` release heading in %s", workflowupgrade.ChangelogPath)
		}
		version = workflowupgrade.NextRelease(last)
	}
	plan, err := workflowupgrade.PlanBump(repoAt(root), ids, version)
	if err != nil {
		return err
	}
	if !*dryRun {
		for _, c := range plan.Changes {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(c.Path)), c.Data, 0o644); err != nil {
				return err
			}
		}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, plan)
	}
	if len(plan.Changes) == 0 {
		fmt.Fprintf(ctx.Stdout, "catalog-bump: %s already at %s, templates in line; nothing to change\n", strings.Join(ids, ", "), version)
		return nil
	}
	verb := "changed"
	if *dryRun {
		verb = "would change (dry run)"
	}
	for _, c := range plan.Changes {
		fmt.Fprintf(ctx.Stdout, "%s %s\n", verb, c.Path)
		for _, w := range c.What {
			fmt.Fprintf(ctx.Stdout, "    %s\n", w)
		}
	}
	if !*dryRun {
		fmt.Fprintln(ctx.Stdout, "\nNext: update the entry's what:/snippet if what projects adopt changed, then go test ./internal/workflowupgrade/")
	}
	return nil
}

// repoAt reads files of the checkout at root.
func repoAt(root string) workflowupgrade.Repo {
	return workflowupgrade.Repo{
		Read: func(p string) ([]byte, bool) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			return data, err == nil
		},
		Dir: func(dir string) []string {
			entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
			var names []string
			for _, e := range entries {
				if !e.IsDir() {
					names = append(names, e.Name())
				}
			}
			return names
		},
	}
}
