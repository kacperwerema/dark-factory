package memory

import (
	"regexp"
	"strings"
)

// Memory as a cache (the epic's D-2): the issue file and HANDOFF.md hold
// the current work; memory holds durable project facts. These two tests
// find text that is a note on one issue rather than such a fact. They look
// only for what plainly ties a sentence to one piece of work, so a general
// fact naming a path, a package or a status passes.

// notePhrases tie a sentence to the writer's own piece of work.
// "this branch" is left out: "the files this branch changed" is a general
// rule about any branch.
var notePhrases = []string{"this issue", "my issue", "this ticket", "my branch", "my pr"}

// workBranch is a worker's real branch name: work/<area>/<slug> with a slug
// long enough to be an issue's (work/ui/x in an example is not one).
var workBranch = regexp.MustCompile(`(^|[^a-z0-9_./-])work/[a-z0-9][a-z0-9._-]*/[a-z0-9._-]{20,}`)

// IssueNote is why text reads as a note on the writer's own issue, or "":
// "this issue" and its kin, a work/ branch name, or own (the writer's issue
// slug, or its last part when that alone is long enough to be unique).
func IssueNote(text, own string) string {
	low := strings.ToLower(text)
	for _, p := range notePhrases {
		if containsWord(low, p) {
			return `it says "` + p + `"`
		}
	}
	if m := workBranch.FindString(low); m != "" {
		m = strings.TrimLeft(m, " (\"'`")
		if i := strings.Index(m[len("work/"):], "/"); i >= 0 {
			m = m[:len("work/")+i+1]
		}
		return "it names a work branch (" + m + "…)"
	}
	if own != "" {
		if why := namesSlug(low, own); why != "" {
			return why
		}
	}
	return ""
}

// NamesIssue is why text names one of slugs (an issue on the board), or "".
func NamesIssue(text string, slugs []string) string {
	low := strings.ToLower(text)
	for _, s := range slugs {
		if why := namesSlug(low, s); why != "" {
			return why
		}
	}
	return ""
}

// slugPartMin: a slug's last part alone counts when it is at least this
// long (short ones like "cli/help" are words, not names).
const slugPartMin = 20

// namesSlug: low names slug outside a decision citation (slug#D-n is a
// pointer to a decision, which outlives its issue).
func namesSlug(low, slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	// A short slug with no folder ("engine") is a word, not a name.
	if (strings.Contains(slug, "/") || len(slug) >= slugPartMin) && mentions(low, slug) {
		return "it names the issue " + slug
	}
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		part := slug[i+1:]
		if len(part) >= slugPartMin && mentions(low, part) {
			return "it names the issue " + slug
		}
	}
	return ""
}

// mentions: s occurs in low as a whole name (no slug character on either
// side) at least once not followed by "#" (a citation).
func mentions(low, s string) bool {
	for i := 0; ; {
		j := strings.Index(low[i:], s)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(s)
		before := start == 0 || !isSlugByte(low[start-1])
		after := end >= len(low) || (!isSlugByte(low[end]) && low[end] != '#')
		if before && after {
			return true
		}
		i = start + 1
	}
}

func isSlugByte(b byte) bool { return isWordByte(b) || b == '-' || b == '/' }

// containsWord: p occurs in low with no letter or digit right before or
// after it.
func containsWord(low, p string) bool {
	for i := 0; ; {
		j := strings.Index(low[i:], p)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(p)
		if (j == 0 || !isWordByte(low[j-1])) && (end == len(low) || !isWordByte(low[end])) {
			return true
		}
		i = j + 1
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_'
}
