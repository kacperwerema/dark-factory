package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

const catalogNAUsage = `Usage:
  issue-cli workflow catalog-na <id>[,<id>...] <why>   # in the dark-factory repo
  issue-cli workflow catalog-na --clear

For issue-cli developers: records the Catalog: n/a line of the commit you
haven't made yet, so make validate's catalog-check is green on the
uncommitted change. The line goes in this checkout's own git dir
(CATALOG_NA, one per worktree); catalog-check counts it while the working
tree has uncommitted changes, and the commit-msg hook (make hooks) writes
it into the next commit's message and drops the file. Once everything is
committed a recorded line counts for nothing: catalog-check says to paste
it. A second call replaces the line; --clear drops it.`

// catalogNAFile is the per-worktree file a recorded Catalog: n/a line
// waits in for the next commit.
const catalogNAFile = "CATALOG_NA"

// runCatalogNA records (or with --clear drops) the Catalog: n/a line the
// next commit gets.
func runCatalogNA(ctx *Context, args []string) error {
	fs := newFlagSet("workflow catalog-na", ctx)
	clear := fs.Bool("clear", false, "drop the recorded line")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	root, err := catalogGit("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("catalog-na runs inside a git checkout of issue-viewer: %w", err)
	}
	path, err := catalogNAPath(root)
	if err != nil {
		return err
	}
	if *clear {
		if fs.NArg() > 0 {
			return &usageError{msg: "catalog-na --clear takes no ids\n\n" + catalogNAUsage}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintln(ctx.Stdout, "✓ No Catalog: n/a line recorded for the next commit")
		return nil
	}
	if fs.NArg() < 2 {
		return &usageError{msg: "catalog-na needs the entry ids and why\n\n" + catalogNAUsage}
	}
	versions, err := workflowupgrade.EntryVersions(repoAt(root))
	if err != nil {
		return fmt.Errorf("catalog-na runs in the dark-factory repository: %w\n\n%s", err, catalogNAUsage)
	}
	value := "n/a " + strings.Join(fs.Args(), " ")
	if bad := workflowupgrade.CheckTrailers([]workflowupgrade.Trailer{{Value: value}}, workflowupgrade.TrailerCatalog{Versions: versions}); len(bad) > 0 {
		return fmt.Errorf("Catalog: %s: %s", value, bad[0].Problem)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Recorded for the next commit: Catalog: %s\n", value)
	fmt.Fprintln(ctx.Stdout, "  catalog-check counts it while this change is uncommitted")
	if catalogHookInstalled(root) {
		fmt.Fprintln(ctx.Stdout, "  the commit-msg hook writes it into your next commit's message")
	} else {
		fmt.Fprintf(ctx.Stdout, "  no commit-msg hook here (make hooks installs it): paste the line into your next commit's message yourself, then issue-cli workflow catalog-na --clear\n")
	}
	return nil
}

// catalogNAPath is the CATALOG_NA file in root's own git dir (a
// worktree's, not the common one), absolute.
func catalogNAPath(root string) (string, error) {
	p, err := catalogGit(root, "rev-parse", "--git-path", catalogNAFile)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return p, nil
}

// catalogRecordedNA is the line catalog-na recorded at root ("" for none).
func catalogRecordedNA(root string) string {
	path, err := catalogNAPath(root)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// catalogHookInstalled reports whether root's commit-msg hook is ours.
func catalogHookInstalled(root string) bool {
	dir, err := privacy.HooksDir(root)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "commit-msg"))
	return err == nil && strings.Contains(string(data), commitMsgMarker)
}

// addRecordedNA writes the line catalog-na recorded into the message file
// and drops the record: the commit about to be made carries it. It
// reports the line added, "" when there was none or git couldn't add it
// (the record stays then, and catalog-check says to paste it).
func addRecordedNA(root, file string) string {
	value := catalogRecordedNA(root)
	if value == "" {
		return ""
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return ""
	}
	if _, err := catalogGit(root, "interpret-trailers", "--in-place", "--if-exists", "addIfDifferent", "--trailer", "Catalog: "+value, abs); err != nil {
		return ""
	}
	if path, err := catalogNAPath(root); err == nil {
		os.Remove(path)
	}
	return value
}
