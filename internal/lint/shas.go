package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// shaWord is a word that may be a commit ref: 7 to 40 lowercase hex with at
// least one digit and one letter (so "defaced" and "20261115" are words,
// not refs). A '#' in front makes it an anchor or a colour.
var (
	wordRE   = regexp.MustCompile(`#?[0-9A-Za-z_]+`)
	shaWord  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	hasDigit = regexp.MustCompile(`[0-9]`)
	hasHexAZ = regexp.MustCompile(`[a-f]`)
)

// shaRefs is every word in text that looks like a commit ref, in order,
// each once.
func shaRefs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range wordRE.FindAllString(text, -1) {
		if !shaWord.MatchString(w) || !hasDigit.MatchString(w) || !hasHexAZ.MatchString(w) || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// checkSHAs flags a commit ref in a done issue's ## Implementation that is
// not on main: most often the SHA from before an amend or a rebase, while
// the commit that landed has another. A ref git doesn't know is left alone:
// it is most often a commit in another repository (a department's project)
// or another hash (an attachment's name). One cat-file --batch-check call resolves every ref on the
// board and one rev-list call finds those main doesn't reach, whatever the
// number of issues. Outside git, or with no main branch, there is nothing
// to check.
func checkSHAs(env *Env) []Finding {
	refs := map[string][]string{} // ref → slugs naming it
	var slugs []string
	closed := tracker.ClosedIssueFunc(env.Project)
	for _, issue := range env.Issues {
		if tracker.IsParkedStatus(issue.Status) || !closed(issue) {
			continue
		}
		text, ok := tracker.SectionText(issue.BodyRaw, "Implementation")
		if !ok {
			continue
		}
		found := shaRefs(text)
		if len(found) == 0 {
			continue
		}
		slugs = append(slugs, issue.Slug)
		for _, r := range found {
			refs[r] = append(refs[r], issue.Slug)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	root := env.Project.Root()
	if _, err := gitmain.Git(root, "rev-parse", "--git-dir"); err != nil {
		return nil
	}
	root = gitmain.Root(root)
	configured := ""
	if env.Workflow != nil && env.Workflow.CI != nil {
		configured = env.Workflow.CI.Branch
	}
	branch, err := gitmain.Branch(root, configured)
	if err != nil {
		return nil
	}
	if head, err := gitmain.Head(root, branch); err != nil || head == "" {
		return nil
	}
	names := make([]string, 0, len(refs))
	for r := range refs {
		names = append(names, r)
	}
	sort.Strings(names)

	// One call resolves every ref: "<full> commit <size>" or
	// "<ref>^{commit} missing" / "ambiguous".
	var in strings.Builder
	for _, r := range names {
		in.WriteString(r + "^{commit}\n")
	}
	out, err := gitmain.GitInput(root, in.String(), "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) != len(names) {
		return nil
	}
	full := map[string]string{} // ref → full commit SHA
	for i, line := range lines {
		// "missing" and "ambiguous" (a short ref naming several objects)
		// are left alone.
		if f := strings.Fields(line); len(f) == 2 && f[1] == "commit" {
			full[names[i]] = f[0]
		}
	}

	// One call lists the commits those refs reach that main does not.
	offMain := map[string]bool{}
	if len(full) > 0 {
		var revs strings.Builder
		for _, sha := range full {
			revs.WriteString(sha + "\n")
		}
		revs.WriteString("^refs/heads/" + branch + "\n")
		out, err := gitmain.GitInput(root, revs.String(), "rev-list", "--stdin")
		if err != nil {
			return nil
		}
		for _, sha := range strings.Fields(out) {
			offMain[sha] = true
		}
	}

	bySlug := map[string][]string{}
	for _, r := range names {
		if full[r] == "" || !offMain[full[r]] {
			continue
		}
		for _, s := range refs[r] {
			bySlug[s] = append(bySlug[s], r)
		}
	}
	var findings []Finding
	for _, s := range slugs {
		bad := bySlug[s]
		if len(bad) == 0 {
			continue
		}
		findings = append(findings, Finding{
			Where:   s,
			Message: fmt.Sprintf("## Implementation names %s, not on %s: a commit from before an amend or rebase, so the issue points at code that never landed", strings.Join(bad, ", "), branch),
			Fix:     fmt.Sprintf("replace it with the landed SHA (git log --oneline %s --grep %s) in ## Implementation", branch, shell.Quote(s)),
		})
	}
	return findings
}
