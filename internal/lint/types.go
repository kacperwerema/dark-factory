package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// wantedSection is the section a brief states the work in.
const wantedSection = "Wanted"

// A docs issue has no Test Plan and no code on its path. These read what a
// brief asks for, so they are narrower than criterionTest: "make validate
// is green (readme tests)" quotes a check that exists, and a README's
// `go install …/cmd/issue-cli@latest` names no package to change.
var (
	// asksForGoFile names a .go path; pkg.go.dev is not one.
	asksForGoFile = regexp.MustCompile(`[\w/-]+\.go(?:$|[^\w.-]|\.(?:$|\s))`)
	// asksForPackage names a Go package by its folder or in words; a bare
	// "package" is too often an npm package or a package manager.
	asksForPackage = regexp.MustCompile(`\b(?:internal|pkg)/\w[\w/-]*|(?i:\bgo package\b)`)
	// asksForTest names a test, or asks for one: "a test", "a go test",
	// "add tests", a TestName.
	asksForTest = regexp.MustCompile(`\bTest[A-Z0-9_]\w*|(?i:\bgo test\b|\b(?:a|an|one|new|add|adds|write|writes)\s+(?:\w+\s+)?tests?\b)`)
)

// asksForCode is the first thing in text that asks for code or a test,
// "" when nothing does.
func asksForCode(text string) string {
	for _, re := range []*regexp.Regexp{asksForTest, asksForGoFile, asksForPackage} {
		if m := re.FindString(text); m != "" {
			return strings.TrimRight(strings.TrimSpace(m), ".,;:)`'\"")
		}
	}
	return ""
}

// checkTypes reads the open docs-type issues: a Wanted section or an open
// acceptance criterion naming a test, a package or a .go path asks for work
// the docs path can't check. The workflow's types: block itself is the
// `workflow` check's.
func checkTypes(env *Env) []Finding {
	closed := tracker.ClosedIssueFunc(env.Project)
	memo := env.Project.WorkflowMemo()
	var out []Finding
	for _, issue := range env.Issues {
		if closed(issue) {
			continue
		}
		// Without types: the type: field is plain text and picks no path.
		wf := memo.ForIssue(issue)
		if wf == nil {
			continue
		}
		if resolved, _ := wf.ResolveType(issue.Type); resolved != "docs" {
			continue
		}
		out = append(out, docsAsksForCode(issue)...)
	}
	return out
}

// docsAsksForCode is one finding for the Wanted section and one per open
// criterion that asks for code or a test.
func docsAsksForCode(issue *tracker.Issue) []Finding {
	finding := func(where, hit string) Finding {
		return Finding{
			Check:   "types",
			Where:   issue.Slug + " " + where,
			Message: fmt.Sprintf("names %q, but a docs issue changes no code and has no Test Plan", hit),
			Fix:     fmt.Sprintf(`issue-cli set-type %s tweak (at its first status; later the owner changes Type on the issue page), or reword it if it only quotes the name`, issue.Slug),
		}
	}
	var out []Finding
	if text, ok := tracker.SectionText(issue.BodyRaw, wantedSection); ok {
		if hit := asksForCode(text); hit != "" {
			out = append(out, finding("## "+wantedSection, hit))
		}
	}
	for _, it := range tracker.Criteria(issue.BodyRaw) {
		if it.Checked || it.ID == "" {
			continue
		}
		if _, na := tracker.NAClaim(it.Text); na {
			continue
		}
		if hit := asksForCode(it.Text); hit != "" {
			out = append(out, finding(it.ID, hit))
		}
	}
	return out
}
