package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

const scanUsage = `Usage:
  issue-cli workflow scan --dry-run                         # report detected features and the record it would write
  issue-cli workflow scan                                   # write the features: record into workflow.yaml
  issue-cli workflow scan --installed types-of-work,scoring # record features you installed (sets the catalog version)
  issue-cli workflow scan --declined worktree               # record features the human declined
  issue-cli workflow scan --forget worktree                 # drop ids from the record (re-opens a declined feature)
  issue-cli --json workflow scan --dry-run                  # machine-readable report
  issue-cli workflow scan --template development            # a bundled template's record: in a dark-factory checkout
                                                            # its file and catalog, rewritten in place (--dry-run: print);
                                                            # elsewhere the bundled copy, printed only`

// runWorkflowScan detects catalog features in workflow.yaml and writes the
// features: record. It only ever rewrites the features: block.
func runWorkflowScan(ctx *Context, args []string) error {
	fs := newFlagSet("workflow scan", ctx)
	dryRun := fs.Bool("dry-run", false, "print the report and the record without writing workflow.yaml")
	installed := fs.String("installed", "", "comma-separated feature ids to record as installed at the catalog version")
	declined := fs.String("declined", "", "comma-separated feature ids to record as declined")
	forget := fs.String("forget", "", "comma-separated feature ids to drop from the record (re-opens a declined feature)")
	deptFlag := fs.String("department", "", "read this department's own workflow file (projects.yaml departments: workflow:)")
	tmpl := fs.String("template", "", "scan a bundled template (development, review, …) instead of the project's workflow.yaml")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("workflow scan takes no arguments, got %q\n\n%s", strings.Join(fs.Args(), " "), scanUsage)
	}
	if *tmpl != "" {
		if *deptFlag != "" || *installed != "" || *declined != "" || *forget != "" {
			return fmt.Errorf("--template takes only --dry-run: a bundled template records what it has and declines nothing\n\n%s", scanUsage)
		}
		return scanTemplate(ctx, *tmpl, *dryRun)
	}
	path, err := workflowFileFor(ctx.Project, *deptFlag)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("no workflow.yaml found: this project runs on the built-in default workflow\n\nCreate one from a bundled template:\n  issue-cli workflow init --template <%s>", strings.Join(workflowupgrade.Templates(), "|"))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	res, err := workflowupgrade.Scan(path, raw, workflowupgrade.ScanOptions{
		Installed: splitIDList(*installed),
		Declined:  splitIDList(*declined),
		Forget:    splitIDList(*forget),
	})
	if err != nil {
		return err
	}
	res.DryRun = *dryRun
	if !res.DryRun && (len(res.Changes) > 0 || !res.HadRecord) {
		out, err := workflowupgrade.WriteRecord(raw, res.Record)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		res.Written = true
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, res)
	}
	printScanResult(ctx.Stdout, res)
	return nil
}

// scanTemplate scans a bundled template. In a dark-factory checkout it reads
// the checkout's template file and catalog and writes the record into the
// file (unless dryRun): the template an agent is changing, not the copy in
// this binary. Elsewhere it prints the bundled template's record.
func scanTemplate(ctx *Context, name string, dryRun bool) error {
	if !workflowupgrade.IsBundled(name) {
		return fmt.Errorf("no bundled template %q (bundled: %s)", name, strings.Join(workflowupgrade.Templates(), ", "))
	}
	entries, err := workflowupgrade.Catalog()
	if err != nil {
		return err
	}
	file := "bundled " + name + ".yaml"
	raw, err := workflowupgrade.ReadTemplate(name)
	if err != nil {
		return err
	}
	inRepo := false
	if root, err := catalogGit("", "rev-parse", "--show-toplevel"); err == nil {
		p := workflowupgrade.TemplatesPath + "/" + name + ".yaml"
		if data, ok := repoAt(root).Read(p); ok {
			if local, err := workflowupgrade.RepoCatalog(repoAt(root)); err == nil {
				entries, raw, file, inRepo = local, data, filepath.Join(root, filepath.FromSlash(p)), true
			}
		}
	}
	res, err := workflowupgrade.ScanWith(entries, file, raw, workflowupgrade.ScanOptions{})
	if err != nil {
		return err
	}
	res.DryRun = dryRun || !inRepo
	if !res.DryRun && (len(res.Changes) > 0 || !res.HadRecord) {
		out, err := workflowupgrade.WriteRecord(raw, res.Record)
		if err != nil {
			return err
		}
		if err := os.WriteFile(file, out, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", file, err)
		}
		res.Written = true
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, res)
	}
	printScanResult(ctx.Stdout, res)
	if !inRepo {
		fmt.Fprintln(ctx.Stdout, "(the template bundled in this binary; run it in a dark-factory checkout to rewrite the file there)")
	}
	return nil
}

func printScanResult(w io.Writer, res *workflowupgrade.ScanResult) {
	fmt.Fprintln(w, "== Workflow scan ==")
	fmt.Fprintf(w, "File:   %s\n", res.WorkflowFile)
	if res.HadRecord {
		fmt.Fprintln(w, "Record: features: present")
	} else {
		fmt.Fprintln(w, "Record: none yet")
	}
	switch {
	case res.DryRun:
		fmt.Fprintln(w, "Mode:   dry run, nothing written")
	case res.Written:
		fmt.Fprintln(w, "Mode:   wrote the features: record (nothing else in the file changed)")
	default:
		fmt.Fprintln(w, "Mode:   record already up to date, nothing written")
	}

	detected := 0
	for _, f := range res.Features {
		if f.Detected {
			detected++
		}
	}
	fmt.Fprintf(w, "\nDetected %d of %d catalog features (✓ detected, + recorded only, ✗ declined, · absent):\n", detected, len(res.Features))
	category := ""
	for _, f := range res.Features {
		if f.Category != category {
			category = f.Category
			fmt.Fprintf(w, "  %s\n", category)
		}
		mark, detail := "·", ""
		switch {
		case f.Detected:
			mark, detail = "✓", f.Evidence
			if f.Matches > 1 {
				detail += fmt.Sprintf(" (+%d more)", f.Matches-1)
			}
		case f.Recorded == "declined":
			mark, detail = "✗", "declined"
		case f.Recorded != "":
			mark, detail = "+", "recorded, not detected"
		}
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("    %s %-31s %s", mark, f.ID, detail), " "))
	}

	if len(res.Changes) > 0 {
		fmt.Fprintln(w, "\nChanges to the record:")
		for _, c := range res.Changes {
			fmt.Fprintf(w, "  %s\n", c)
		}
	} else {
		fmt.Fprintln(w, "\nChanges to the record: none")
	}
	for _, n := range res.Notes {
		fmt.Fprintf(w, "Note: %s\n", n)
	}
	fmt.Fprintln(w, "\nRecord after this scan:")
	for _, line := range strings.Split(strings.TrimRight(res.Block, "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
	if res.DryRun {
		fmt.Fprintln(w, "\nRun without --dry-run to write it. Only the features: block is changed.")
	}
}

// splitIDList parses a comma/space separated --installed/--declined value.
func splitIDList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
