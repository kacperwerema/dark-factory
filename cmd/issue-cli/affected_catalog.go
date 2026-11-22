package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// plannedFiles are the repository paths an issue body names
// (tracker.PlannedFiles).
func plannedFiles(root, body string) []string { return tracker.PlannedFiles(root, body) }

// affectedCatalog is the catalog impact of files at root: the entries whose
// sources: they match. ok is false when root has no catalog (another
// project than dark-factory), which prints nothing.
func affectedCatalog(root string, files []string) (entries []workflowupgrade.PlannedEntry, ok bool) {
	if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(workflowupgrade.CatalogPath))); err != nil || !fi.IsDir() {
		return nil, false
	}
	cat, err := workflowupgrade.RepoCatalog(repoAt(root))
	if err != nil {
		return nil, false
	}
	return workflowupgrade.PlannedEntries(cat, files), true
}

// printAffectedCatalog prints the catalog impact: each entry with the
// files and sources: that match, then the lines to write, the same ones
// catalog-check's fix prints, so a design can name them before the build.
func printAffectedCatalog(w io.Writer, entries []workflowupgrade.PlannedEntry, planned []string) {
	fmt.Fprintln(w)
	if len(planned) > 0 {
		fmt.Fprintf(w, "Planned files named in the issue: %s\n", strings.Join(planned, ", "))
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "Catalog impact: none — no entry's sources: match these files; the commits need no Catalog: line")
		return
	}
	fmt.Fprintf(w, "Catalog impact: %d entries turn red in catalog-check once these files change\n", len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
		var parts []string
		for _, m := range e.Matches {
			if m.Source == m.File {
				parts = append(parts, m.File)
				continue
			}
			parts = append(parts, fmt.Sprintf("%s (matches %s)", m.File, m.Source))
		}
		note := ""
		if e.Narrow {
			note = " — only if that symbol's code changes"
		}
		fmt.Fprintf(w, "  %s %s: %s%s\n", e.ID, e.Version, strings.Join(parts, ", "), note)
	}
	list := strings.Join(ids, ",")
	fmt.Fprintln(w, "Write in the design's Catalog box, and on the commit:")
	fmt.Fprintf(w, "  what projects adopt changes:  issue-cli workflow catalog-bump %s   (then Catalog: %s on the commit names it; a bumped entry needs no line)\n", list, list)
	fmt.Fprintf(w, "  a refactor or fix:            Catalog: n/a %s <why>   (or record it now: issue-cli workflow catalog-na %s <why>)\n", list, list)
}
