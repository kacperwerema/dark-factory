package privacy

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ScanStaged runs the leak scan's line matchers (ScanText) over the lines
// the staged diff of the repo at root adds: what make leak-check would
// refuse once the commit exists. One git diff --cached call, so a large
// staged diff stays fast; git honours GIT_INDEX_FILE, so the hook of a
// `commit -a` or `commit <path>` sees the index the commit will have.
// Binary files have no text hunks and are skipped.
func ScanStaged(root string, names []string) ([]Leak, error) {
	out, err := git(root, "-c", "core.quotePath=false", "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--diff-filter=d")
	if err != nil {
		return nil, err
	}
	var leaks []Leak
	addedLines(out, func(path string, line int, text string) {
		for _, what := range ScanText(text, names) {
			leaks = append(leaks, Leak{Path: path, Line: line, What: what})
		}
	})
	return leaks, nil
}

// addedLines calls fn with each line a unified diff (-U0) adds: the new
// file's path, the line's number there and its text.
func addedLines(diff string, fn func(path string, line int, text string)) {
	path, line, inHunk := "", 0, false
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			path, inHunk = "", false
		case !inHunk && strings.HasPrefix(l, "+++ "):
			// An added line "++…" reads "+++…" too: only a file header
			// before the file's first hunk names the path.
			path = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			if path == "/dev/null" {
				path = ""
			}
		case strings.HasPrefix(l, "@@ "):
			line, inHunk = hunkStart(l), true
		case strings.HasPrefix(l, "+") && path != "":
			fn(path, line, strings.TrimSuffix(l[1:], "\r"))
			line++
		}
	}
}

// hunkStart is the new-file line a hunk header starts at:
// "@@ -a,b +c,d @@" gives c.
func hunkStart(header string) int {
	i := strings.Index(header, " +")
	if i < 0 {
		return 0
	}
	rest := header[i+2:]
	if j := strings.IndexAny(rest, ", "); j >= 0 {
		rest = rest[:j]
	}
	n, _ := strconv.Atoi(rest)
	return n
}

// ScanMessage runs ScanText over a commit message's lines; each hit's Path
// is label and its Line the message's own line number. Comment lines
// (git strips them) are left out.
func ScanMessage(msg, label string, names []string) []Leak {
	var leaks []Leak
	for i, l := range strings.Split(msg, "\n") {
		if strings.HasPrefix(l, "# ") && strings.Contains(l, ">8") {
			break
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		for _, what := range ScanText(l, names) {
			leaks = append(leaks, Leak{Path: label, Line: i + 1, What: what})
		}
	}
	return leaks
}

// Merging reports whether the repo at root has a merge in progress: its
// staged diff then holds the other side's lines, which that side's own
// checks already saw.
func Merging(root string) bool {
	dir, err := git(root, "rev-parse", "--git-dir")
	if err != nil {
		return false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	_, err = os.Stat(filepath.Join(dir, "MERGE_HEAD"))
	return err == nil
}

// LeakWarning is what the commit-msg hook prints for staged hits: each
// file:line (10 at most), then the stand-in to use. It never names a
// private name, only its line in the list.
func LeakWarning(leaks []Leak) string {
	if len(leaks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "warning: %d line(s) in this commit would fail make leak-check (the leak scan, docs/workflow.md#the-leak-scan):\n", len(leaks))
	for i, l := range leaks {
		if i == 10 {
			fmt.Fprintf(&b, "warning:   … and %d more\n", len(leaks)-10)
			break
		}
		fmt.Fprintf(&b, "warning:   %s\n", l)
	}
	b.WriteString("warning: use a stand-in: describe another project by its layout (\"a project without worktree: true\") or use a placeholder project such as Orchard / orchard; a home path takes a placeholder user (/home/someone/). The commit goes through: fix it with git commit --amend before anything pins its sha.")
	return b.String()
}
