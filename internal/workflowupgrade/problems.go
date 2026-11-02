package workflowupgrade

import (
	"fmt"
	"path"
	"strings"
)

// CatalogProblems are the catalog's own mistakes that each name their fix:
// an unknown category, an entry without sources, @prompt beside real
// sources, a source that matches no file (a bare folder: the folder's **),
// one that matches only exempt files, a #symbol source that isn't one Go
// declaration. files are the repository's files (nil skips the sources
// checks); read returns a file's content (nil skips the #symbol check). The
// guard tests and catalog-check both run it.
func CatalogProblems(entries []Entry, files []string, read func(string) ([]byte, bool)) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	for _, e := range entries {
		file := CatalogPath + "/" + e.ID + ".yaml"
		if categoryRank(e.Category) == len(Categories) {
			add("%s: category %q is not one of %s; set category: in %s (entries sort by category, then id)", e.ID, e.Category, strings.Join(Categories, ", "), file)
		}
		if files == nil {
			continue
		}
		if len(e.Sources) == 0 {
			add("%s: no sources: in %s; list the code behind the feature (globs, `**` allowed, or file.go#Symbol), or [%q] alone for a prompt-only convention", e.ID, file, PromptSource)
		}
		for _, s := range e.Sources {
			if s == PromptSource {
				if len(e.Sources) > 1 {
					add("%s: %s can't be combined with real sources; keep sources: [%q] alone for a prompt-only convention, or drop it and list only the code", e.ID, PromptSource, PromptSource)
				}
				continue
			}
			glob, sym, hasSym := strings.Cut(s, "#")
			var matched []string
			for _, f := range files {
				if MatchSource(glob, f) {
					matched = append(matched, f)
				}
			}
			if len(matched) == 0 {
				if dir := strings.TrimSuffix(glob, "/"); !hasSym && underFolder(files, dir) {
					add("%s: source %s is a folder and matches no file; write %s/** for every file under it", e.ID, s, dir)
				} else {
					add("%s: source %s matches no tracked file; fix the path in %s", e.ID, s, file)
				}
				continue
			}
			watched := false
			for _, f := range matched {
				watched = watched || !Exempt(f)
			}
			if !watched {
				add("%s: source %s matches only files in ExemptSources (tests, testdata, scenario fixtures): catalog-check would never watch it; name the code those files test", e.ID, s)
			}
			if !hasSym {
				continue
			}
			if len(matched) != 1 || path.Ext(matched[0]) != ".go" {
				add("%s: %s: a #symbol source names one Go file, matched %v", e.ID, s, matched)
				continue
			}
			if read == nil {
				continue
			}
			if src, ok := read(matched[0]); ok && len(SymbolDecls(src, sym)) == 0 {
				add("%s: %s has no top-level %s; name a func, type, var or const declared there", e.ID, matched[0], sym)
			}
		}
	}
	return out
}

// underFolder reports whether any file sits under dir.
func underFolder(files []string, dir string) bool {
	if dir == "" || strings.ContainsAny(dir, "*?[") {
		return false
	}
	for _, f := range files {
		if strings.HasPrefix(f, dir+"/") {
			return true
		}
	}
	return false
}
