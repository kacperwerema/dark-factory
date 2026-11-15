package lint

import (
	"fmt"
	"regexp"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// showFooterLine matches a line issue-cli show prints around the body and
// no one writes by hand: its == blocks and the hints under them, whole and
// at the start of the line, so a quote in a code span, a blockquote or an
// indented example doesn't match. TestShowOutputIsTheFooterLintReads keeps
// it in step with show.
var showFooterLine = regexp.MustCompile(`^(?:` +
	`== (?:Body|Test Plan|Decisions in force|Checklist \(\d+/\d+\)|Comments \(\d+\)|Shared files \(\d+, with \d+ other branch(?:es)?\)) ==$` +
	`|Tick done boxes by id, several at once: issue-cli ` +
	`|Box text wrong\? issue-cli check-edit ` +
	`|Disagree with one\? Challenge it: issue-cli ` +
	`|Merge main often and coordinate on these files` +
	`)`)

// IsShowFooterLine reports whether line is one show prints around the body.
func IsShowFooterLine(line string) bool { return showFooterLine.MatchString(line) }

// checkShowFooter names each section holding show's footer: a section
// rebuilt from show's output runs on past the last heading, and the stale
// boxes and comments pasted in read as the record while no gate notices.
// Closed issues count too, unlike the other checks on bodies: this is junk,
// not the words an issue was written with, and it can land an hour before
// the close, between two lint runs.
func checkShowFooter(env *Env) []Finding {
	var out []Finding
	for _, issue := range env.Issues {
		for _, sec := range tracker.SplitSections(issue.BodyRaw) {
			var hits []string
			for _, line := range tracker.LinesOutsideFences(sec.Text) {
				if IsShowFooterLine(line) {
					hits = append(hits, line)
				}
			}
			if len(hits) == 0 {
				continue
			}
			out = append(out, Finding{
				Where:   issue.Slug + " ## " + sec.Title,
				Message: fmt.Sprintf("holds %d line(s) of issue-cli show's output, first %q: boxes and comments pasted here are stale copies, not the record", len(hits), hits[0]),
				Fix:     fmt.Sprintf(`issue-cli replace %s --section %s --body-file <the section without them> (read it from the issue file, not from show)`, issue.Slug, shell.Quote(sec.Title)),
			})
		}
	}
	return out
}
