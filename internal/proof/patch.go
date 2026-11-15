package proof

import (
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Amending or rebasing a commit gives it a new sha and a new commit time,
// though its change is the same. A tests: comment ("tests: prove --run at
// 0ff4c62: …") or a prove commit link naming the old sha still holds then:
// it matches the branch commit with the same `git patch-id`.

// shaWordRe matches a word shaped like a commit sha.
var shaWordRe = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

// namedCommits resolves the words of text shaped like a sha that name a
// commit in root, first three at most.
func namedCommits(root, text string) []string {
	var out []string
	for _, w := range shaWordRe.FindAllString(text, -1) {
		if len(out) == 3 {
			break
		}
		if full, err := ResolveCommit(root, w); err == nil && !containsString(out, full) {
			out = append(out, full)
		}
	}
	return out
}

// patchID is the stable `git patch-id` of a commit's change; "" for a merge,
// an empty change or an unreadable commit.
func patchID(root, sha string) string {
	if sha == "" || strings.HasPrefix(sha, "-") {
		return ""
	}
	diff, err := tracker.GitCached(root, "diff-tree", "-p", "--root", "--no-color", sha)
	if err != nil || strings.TrimSpace(diff) == "" {
		return ""
	}
	out, err := tracker.GitCachedIn(root, diff, "patch-id", "--stable")
	if err != nil {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	return id
}

// samePatch is the newest of the first-parent commits (newest first) up to
// and including stop ("" for all) whose change is sha's, by patch-id; ""
// when none is.
func samePatch(root, sha string, commits []commit, stop string) string {
	want := patchID(root, sha)
	if want == "" {
		return ""
	}
	for _, cm := range commits {
		if !cm.merge && patchID(root, cm.sha) == want {
			return cm.sha
		}
		if cm.sha == stop {
			break
		}
	}
	return ""
}

// testedLastChange reports whether a commit the tests: comment names covers
// the branch's last code change lc: lc is that commit or under it, or a
// branch commit from lc on has its patch (it was amended or rebased). It
// returns the branch commit it matched.
func testedLastChange(root, text string, commits []commit, lc commit) (string, bool) {
	for _, s := range namedCommits(root, text) {
		if s == lc.sha {
			return s, true
		}
		if _, err := tracker.GitCached(root, "merge-base", "--is-ancestor", lc.sha, s); err == nil {
			return s, true
		}
		if m := samePatch(root, s, commits, lc.sha); m != "" {
			return m, true
		}
	}
	return "", false
}

// patchMatch is the branch commit with the patch of a commit link that is
// not on the branch (amended or rebased), "" when none has it.
func (c *checker) patchMatch(full string) string {
	return samePatch(c.root, full, c.commits, "")
}
