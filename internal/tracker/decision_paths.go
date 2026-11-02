package tracker

import (
	"strings"
)

// BranchHeads is the work/<slug> branch's head and main's, "" when either
// is missing: the key a caller caches BranchTouches under.
func BranchHeads(root, slug string) (branch, main string) {
	if root == "" || slug == "" {
		return "", ""
	}
	b, err := runGit(root, "rev-parse", "--verify", "--quiet", "refs/heads/work/"+slug+"^{commit}")
	if err != nil {
		return "", ""
	}
	name, err := MainBranch(root)
	if err != nil {
		return "", ""
	}
	m, err := runGit(root, "rev-parse", "--verify", "--quiet", name+"^{commit}")
	if err != nil {
		return "", ""
	}
	return strings.TrimSpace(b), strings.TrimSpace(m)
}

// BranchTouches is the files the work/<slug> branch changed since it left
// main (the merge-base three-dot diff, so what a chore(merge) of main
// brought in doesn't count) that match any of globs (MatchGlob).
func BranchTouches(root, slug string, globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	branch, main := BranchHeads(root, slug)
	if branch == "" || main == "" {
		return nil
	}
	out, err := runGit(root, "diff", "--name-only", main+"..."+branch)
	if err != nil {
		return nil
	}
	var hit []string
	for _, f := range strings.Split(out, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		for _, g := range globs {
			if MatchGlob(g, f) {
				hit = append(hit, f)
				break
			}
		}
	}
	return hit
}
