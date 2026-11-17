package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// BranchField is the frontmatter key that names an issue's branch when it
// isn't work/<slug>: issue-cli set-meta <slug> --key branch --value <name>.
const BranchField = "branch"

// The rules IssueBranch finds a branch by (Branch.How).
const (
	HowField    = "branch: field"
	HowWork     = "work/<slug>"
	HowWorktree = "worktree branch"
	HowHandoff  = "HANDOFF.md"
)

// Branch is where an issue's work is: its branch and the worktree checked
// out on it. A project with worktree: true always has work/<slug> in
// .worktrees/<slug>; a project without one lets its workers branch as they
// like, so the lookup also reads the issue's branch: field and the
// repository's worktrees.
type Branch struct {
	// Name is the branch, "" when none was found.
	Name string
	// Worktree is the folder checked out on Name, "" when there is none
	// (the branch has no worktree, or nothing was found).
	Worktree string
	// How says which rule found it: HowField, HowWork, HowWorktree or
	// HowHandoff; "" when nothing was.
	How string
	// Ambiguous lists the worktrees one rule matched when it matched more
	// than one: none of them is used.
	Ambiguous []string
	// Tried is what the lookup looked for, one phrase per rule, for the
	// message when nothing was found.
	Tried []string
	slug  string
}

// Found reports whether the lookup found a branch.
func (b Branch) Found() bool { return b.Name != "" }

// IssueBranch finds the issue's branch in the repository at root, in order:
//  1. the branch its branch: field names, when that branch exists;
//  2. work/<slug>, when it exists;
//  3. the one linked worktree whose branch ends in the slug (its last
//     segment is the slug's last segment), else the one whose HANDOFF.md
//     names the slug as a whole word.
//
// Several worktrees matching one rule are ambiguous: none is used and
// Ambiguous lists them. The worktree of a branch found by 1 or 2 is the
// worktree list's entry for it, else .worktrees/<slug> when that folder
// exists.
func IssueBranch(root string, issue *tracker.Issue) Branch {
	if issue == nil {
		return Branch{}
	}
	return issueBranch(root, issue.Slug, issue.Field(BranchField))
}

func issueBranch(root, slug, field string) Branch {
	b := Branch{slug: slug}
	if root == "" || slug == "" {
		return b
	}
	trees := worktreeList(root)
	byBranch := map[string]string{}
	for _, w := range trees {
		if w.Branch != "" {
			byBranch[w.Branch] = w.Path
		}
	}
	at := func(name, how string) Branch {
		b.Name, b.How = name, how
		if p := byBranch[name]; p != "" && pathExists(p) {
			b.Worktree = p
		} else if p := filepath.Join(root, ".worktrees", slug); pathExists(p) {
			b.Worktree = p
		}
		return b
	}

	if field != "" {
		if tracker.BranchHead(root, field) != "" {
			return at(field, HowField)
		}
		b.Tried = append(b.Tried, "the branch its branch: field names ("+field+", no such branch)")
	} else {
		b.Tried = append(b.Tried, "a branch: field (unset)")
	}
	work := "work/" + slug
	if tracker.BranchHead(root, work) != "" {
		return at(work, HowWork)
	}
	b.Tried = append(b.Tried, work+" (no such branch)")

	main := resolved(root)
	var linked []worktree
	for _, w := range trees {
		if w.Branch != "" && resolved(w.Path) != main {
			linked = append(linked, w)
		}
	}
	last := lastSegment(slug)
	var byName []worktree
	for _, w := range linked {
		if BranchNamesSlug(w.Branch, slug) {
			byName = append(byName, w)
		}
	}
	if len(byName) == 1 {
		b.Name, b.Worktree, b.How = byName[0].Branch, byName[0].Path, HowWorktree
		return b
	}
	if len(byName) > 1 {
		b.Ambiguous = worktreeLabels(byName)
		b.Tried = append(b.Tried, fmt.Sprintf("a worktree whose branch ends in %s (%d match)", last, len(byName)))
		return b
	}
	var byHandoff []worktree
	for _, w := range linked {
		if handoffNames(w.Path, slug) {
			byHandoff = append(byHandoff, w)
		}
	}
	switch len(byHandoff) {
	case 1:
		b.Name, b.Worktree, b.How = byHandoff[0].Branch, byHandoff[0].Path, HowHandoff
		return b
	case 0:
		b.Tried = append(b.Tried, fmt.Sprintf("a worktree whose branch ends in %s or whose HANDOFF.md names %s (none of %d)", last, slug, len(linked)))
	default:
		b.Ambiguous = worktreeLabels(byHandoff)
		b.Tried = append(b.Tried, fmt.Sprintf("a worktree whose HANDOFF.md names %s (%d match)", slug, len(byHandoff)))
	}
	return b
}

// Miss is the message for a lookup that found nothing: what it looked for
// and the two fixes (rename the branch to work/<slug>, or name it in the
// branch: field). "" when the lookup found a branch.
func (b Branch) Miss() string {
	if b.Found() {
		return ""
	}
	var s strings.Builder
	fmt.Fprintf(&s, "no branch for %s: looked for %s", b.slug, strings.Join(b.Tried, ", "))
	if len(b.Ambiguous) > 0 {
		fmt.Fprintf(&s, "; several worktrees match: %s", strings.Join(b.Ambiguous, ", "))
	}
	fmt.Fprintf(&s, ". Fix: git branch -m <your-branch> work/%s, or issue-cli set-meta %s --key %s --value <your-branch>", b.slug, shell.Quote(b.slug), BranchField)
	return s.String()
}

// BranchNamesSlug reports whether branch ends in slug: it is the slug, ends
// in /<slug>, or its last segment is the slug's last segment
// (work/ocena-jakosci for testy/ocena-jakosci).
func BranchNamesSlug(branch, slug string) bool {
	if branch == "" || slug == "" {
		return false
	}
	if branch == slug || strings.HasSuffix(branch, "/"+slug) {
		return true
	}
	return lastSegment(branch) == lastSegment(slug)
}

func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func worktreeLabels(ws []worktree) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Branch+" in "+w.Path)
	}
	slices.Sort(out)
	return out
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// handoffCache keeps each HANDOFF.md read by its size and modification
// time: a board page looks up many issues against the same worktrees.
var handoffCache = struct {
	sync.Mutex
	m map[string]cachedHandoff
}{m: map[string]cachedHandoff{}}

type cachedHandoff struct {
	stamp string
	text  string
}

func handoffText(dir string) string {
	p := filepath.Join(dir, "HANDOFF.md")
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return ""
	}
	stamp := fmt.Sprintf("%d %d", fi.Size(), fi.ModTime().UnixNano())
	handoffCache.Lock()
	c, ok := handoffCache.m[p]
	handoffCache.Unlock()
	if ok && c.stamp == stamp {
		return c.text
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	handoffCache.Lock()
	handoffCache.m[p] = cachedHandoff{stamp: stamp, text: string(data)}
	handoffCache.Unlock()
	return string(data)
}

// handoffNames reports whether dir's HANDOFF.md names slug as a whole word:
// not inside a longer slug or path.
func handoffNames(dir, slug string) bool {
	return namesWord(handoffText(dir), slug)
}

// HandoffSlug is the one slug of slugs that dir's HANDOFF.md names, "" when
// it names none or several.
func HandoffSlug(dir string, slugs []string) string {
	text := handoffText(dir)
	if text == "" {
		return ""
	}
	found := ""
	for _, s := range slugs {
		if namesWord(text, s) {
			if found != "" {
				return ""
			}
			found = s
		}
	}
	return found
}

func namesWord(text, word string) bool {
	if text == "" || word == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		// A slash may come before (work/<slug>, .worktrees/<slug>), not
		// after: <slug>/more is a longer slug or a path inside one.
		if (start == 0 || !wordByte(text[start-1])) && (end == len(text) || !wordByte(text[end]) && text[end] != '/') {
			return true
		}
		i = start + 1
	}
}

func wordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}
