package lint

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// SHASwap is one stale ref FixSHAs replaced.
type SHASwap struct {
	Slug    string `json:"slug"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Subject string `json:"subject"`
}

// SHAKept is one stale ref FixSHAs left as it was, and why.
type SHAKept struct {
	Slug    string `json:"slug"`
	Ref     string `json:"ref"`
	Subject string `json:"subject,omitempty"`
	Reason  string `json:"reason"`
	// Hint names the one commit on main with the same subject when no
	// commit names the slug: a swap to check and make by hand.
	Hint string `json:"hint,omitempty"`
}

// SHAFix is what health lint --check shas --fix did.
type SHAFix struct {
	Branch string    `json:"branch,omitempty"`
	Swaps  []SHASwap `json:"swaps"`
	Kept   []SHAKept `json:"kept"`
	// Issues is how many issue files it wrote, one write each.
	Issues int `json:"issues"`
}

// FixSHAs swaps each stale ref the shas check finds for the commit that
// landed, when that is mechanical: of the commits on main whose message
// names the issue's slug, exactly one has the stale commit's subject. The
// rest are kept with the reason. Only whole words equal to the ref in
// ## Implementation change, and each issue with a swap is written once,
// under its lock (tracker.UpdateIssueBody). The slug must be named: an
// issue that quotes another issue's stale SHA as an example keeps it.
func FixSHAs(env *Env) (*SHAFix, error) {
	fix := &SHAFix{Swaps: []SHASwap{}, Kept: []SHAKept{}}
	scan := staleSHAs(env)
	if scan == nil {
		return fix, nil
	}
	fix.Branch = scan.Branch
	main := "refs/heads/" + scan.Branch

	// One call reads every stale commit's subject.
	var fulls []string
	seen := map[string]bool{}
	for _, s := range scan.Slugs {
		for _, st := range scan.Stale[s] {
			if !seen[st.Full] {
				seen[st.Full] = true
				fulls = append(fulls, st.Full)
			}
		}
	}
	out, err := gitmain.Git(scan.Root, append([]string{"log", "--no-walk=unsorted", "--format=%H%x00%s"}, fulls...)...)
	if err != nil {
		return nil, err
	}
	subject := parseSubjects(out)

	var bySubject map[string][]string // main's commits by subject, read once when needed
	for _, slug := range scan.Slugs {
		out, err := gitmain.Git(scan.Root, "log", "--format=%H%x00%s", "--fixed-strings", "--grep="+slug, main)
		if err != nil {
			return nil, err
		}
		named := map[string][]string{} // subject → commits naming the slug
		for _, line := range strings.Split(out, "\n") {
			if sha, subj, ok := strings.Cut(line, "\x00"); ok {
				named[subj] = append(named[subj], sha)
			}
		}
		swap := map[string]string{}
		var swaps []SHASwap
		for _, st := range scan.Stale[slug] {
			subj := subject[st.Full]
			switch cands := named[subj]; {
			case subj == "":
				fix.Kept = append(fix.Kept, SHAKept{Slug: slug, Ref: st.Ref, Reason: "git gives the commit no subject"})
			case len(cands) == 1:
				short, err := gitmain.Git(scan.Root, "rev-parse", "--short="+strconv.Itoa(max(len(st.Ref), 7)), cands[0])
				if err != nil {
					return nil, err
				}
				swap[st.Ref] = short
				swaps = append(swaps, SHASwap{Slug: slug, Old: st.Ref, New: short, Subject: subj})
			case len(cands) > 1:
				fix.Kept = append(fix.Kept, SHAKept{Slug: slug, Ref: st.Ref, Subject: subj,
					Reason: fmt.Sprintf("%d commits on %s name %s with this subject: pick one by hand", len(cands), scan.Branch, slug)})
			default:
				k := SHAKept{Slug: slug, Ref: st.Ref, Subject: subj,
					Reason: fmt.Sprintf("no commit on %s names %s with this subject", scan.Branch, slug)}
				if bySubject == nil {
					all, err := gitmain.Git(scan.Root, "log", "--format=%H%x00%s", main)
					if err != nil {
						return nil, err
					}
					bySubject = map[string][]string{}
					for _, line := range strings.Split(all, "\n") {
						if sha, s, ok := strings.Cut(line, "\x00"); ok {
							bySubject[s] = append(bySubject[s], sha)
						}
					}
				}
				if c := bySubject[subj]; len(c) == 1 {
					k.Hint = fmt.Sprintf("the one commit on %s with this subject is %s: if it is this issue's, replace it by hand", scan.Branch, c[0][:min(len(c[0]), 12)])
				}
				fix.Kept = append(fix.Kept, k)
			}
		}
		if len(swap) == 0 {
			continue
		}
		issue := scan.Issues[slug]
		_, changed, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
			next, ok, changed := tracker.MapSection(body, "Implementation", func(text string) string {
				return swapWords(text, swap)
			})
			if !ok {
				return body, false, fmt.Errorf("%s has no ## Implementation any more", slug)
			}
			return next, changed, nil
		})
		if err != nil {
			for _, sw := range swaps {
				fix.Kept = append(fix.Kept, SHAKept{Slug: slug, Ref: sw.Old, Subject: sw.Subject, Reason: "writing the issue failed: " + err.Error()})
			}
			continue
		}
		if changed {
			fix.Issues++
			fix.Swaps = append(fix.Swaps, swaps...)
		}
	}
	return fix, nil
}

// parseSubjects reads "<sha>\x00<subject>" lines.
func parseSubjects(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, subj, ok := strings.Cut(line, "\x00"); ok {
			m[sha] = subj
		}
	}
	return m
}

// swapWords replaces each word of text (split as shaRefs splits it, so
// "#a05f230" is not the ref a05f230) that is a key of swap.
func swapWords(text string, swap map[string]string) string {
	var b strings.Builder
	last := 0
	for _, loc := range wordRE.FindAllStringIndex(text, -1) {
		if to, ok := swap[text[loc[0]:loc[1]]]; ok {
			b.WriteString(text[last:loc[0]])
			b.WriteString(to)
			last = loc[1]
		}
	}
	b.WriteString(text[last:])
	return b.String()
}
