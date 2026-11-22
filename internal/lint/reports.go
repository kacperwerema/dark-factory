package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkReports reads the issues whose path gates on has_output (the report
// type) and that have passed that gate: the shelf must still hold their
// output. One that is gone (never added since a hand edit, its file
// deleted, or rejected and not replaced) disagrees with the gate it passed,
// and the issue page shows no report. A done report whose output lists
// Follow-ups that nothing quotes is a warning too (unquotedFollowUps), and
// so is a report past the gate that cites vendor prices or API fields with
// no source URL and no UNVERIFIED list (unsourcedVendorFacts).
func checkReports(env *Env) []Finding {
	var list []*outputs.Output
	loaded := false
	var out []Finding
	scopes := env.Workflow.ScopeMemo()
	for _, issue := range env.Issues {
		wf := scopes.ForIssue(issue)
		if wf == nil {
			continue
		}
		to, kind, ok := wf.HasOutputRule()
		if !ok {
			continue
		}
		at, gate := wf.GetStatusIndex(issue.Status), wf.GetStatusIndex(to)
		if at < 0 || gate < 0 || at < gate || tracker.IsParkedStatus(issue.Status) {
			continue
		}
		if !loaded {
			list, _ = outputs.Load(env.Project)
			loaded = true
		}
		err := outputs.GateError(list, issue.Slug, kind)
		if err == nil {
			out = append(out, unsourcedVendorFacts(env, list, issue.Slug, kind)...)
			if wf.IsLastStatus(issue.Status) {
				out = append(out, unquotedFollowUps(env, list, issue.Slug, kind)...)
			}
			continue
		}
		msg, _, _ := strings.Cut(err.Error(), " — ")
		what := kind
		if what == "" {
			what = "<kind>"
		}
		out = append(out, Finding{
			Where:   issue.Slug,
			Message: fmt.Sprintf("at %s, past the has_output gate into %s, but %s", issue.Status, to, msg),
			Fix:     fmt.Sprintf(`issue-cli output add --issue %s --kind %s --title "..." --file <file>`, issue.Slug, what),
		})
	}
	return out
}

// unquotedFollowUps warns once on a done report whose good outputs list
// Follow-ups for the engine when no other issue and no queue item quotes the
// report. Items marked the owner's (outputs.AgentFollowUps) are left out: the
// owner decides those, nothing picks them up. A report put on the shelf more
// than once is still one report: the newest output listing work is named.
func unquotedFollowUps(env *Env, list []*outputs.Output, slug, kind string) []Finding {
	var newest *outputs.Output
	count, more := 0, 0
	for _, o := range outputs.Select(list, outputs.Filter{Issue: slug}) {
		if o.State == outputs.StateRejected || o.Missing || (kind != "" && !strings.EqualFold(strings.TrimSpace(o.Kind), kind)) {
			continue
		}
		text, ok := outputs.ReadMarkdown(env.Project, o)
		if !ok {
			continue
		}
		items := outputs.AgentFollowUps(text)
		if len(items) == 0 {
			continue
		}
		if reportQuoted(env, o) {
			return nil
		}
		if newest != nil {
			more++
		}
		if newest == nil || o.Added.After(newest.Added) {
			newest, count = o, len(items)
		}
	}
	if newest == nil {
		return nil
	}
	also := ""
	if more > 0 {
		also = fmt.Sprintf(" (as do %d more output(s) of this report)", more)
	}
	return []Finding{{
		Where:   slug,
		Message: fmt.Sprintf("done, but %s lists %d follow-up(s)%s and no issue or queue item quotes the report", newest.ID, count, also),
		Fix:     fmt.Sprintf(`issue-cli queue add --idea "<the follow-up> (from %s)", or file an issue that names %s, or mark the owner's items "(owner)" (one the owner does by hand, with exact text or a date: issue-cli owner-step add --title … --due <day> --body "…")`, slug, slug),
	}}
}

// reportQuoted reports whether another issue is a child of the report or
// names it in its body, or a queue item that isn't the report's own does: by
// the report's slug, its output id, or "output <short id>". A queue that
// can't be read counts as empty, as a warning is all that can come of it.
func reportQuoted(env *Env, o *outputs.Output) bool {
	names := []string{o.Issue, o.ID, "output " + o.ID[strings.LastIndex(o.ID, "-")+1:]}
	for _, issue := range env.Issues {
		if issue.Slug == o.Issue {
			continue
		}
		if tracker.ParentSlug(issue) == o.Issue || namesAny(issue.BodyRaw, names) {
			return true
		}
	}
	q, err := queue.Load(env.Project.IssueDir)
	if err != nil {
		return false
	}
	for _, it := range q.Items {
		if it.Slug != o.Issue && namesAny(it.Idea+"\n"+it.Quote+"\n"+it.Note, names) {
			return true
		}
	}
	return false
}

// namesAny reports whether text holds one of names as a whole name: a slug
// must not match the front of a longer slug.
func namesAny(text string, names []string) bool {
	for _, name := range names {
		for from := 0; ; {
			i := strings.Index(text[from:], name)
			if i < 0 {
				break
			}
			at := from + i
			end := at + len(name)
			if (at == 0 || !slugByte(text[at-1])) && (end == len(text) || !slugByte(text[end])) {
				return true
			}
			from = at + 1
		}
	}
	return false
}

func slugByte(b byte) bool {
	return b == '-' || b == '/' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// unsourcedVendorFacts warns once on a report past the has_output gate whose
// newest good output cites a vendor price or an API field but links no page
// and has no UNVERIFIED list (outputs.UnsourcedVendorFact): facts read from
// a search summary, which the report type's brief says to read from the
// vendor's own page instead. One the owner approved is left alone.
func unsourcedVendorFacts(env *Env, list []*outputs.Output, slug, kind string) []Finding {
	var newest *outputs.Output
	for _, o := range outputs.Select(list, outputs.Filter{Issue: slug}) {
		if o.State == outputs.StateRejected || o.Missing || (kind != "" && !strings.EqualFold(strings.TrimSpace(o.Kind), kind)) {
			continue
		}
		if newest == nil || o.Added.After(newest.Added) {
			newest = o
		}
	}
	// The owner read it and approved it as it stands: a warning now asks
	// for a change nobody will make.
	if newest == nil || newest.State == outputs.StateApproved || newest.State == outputs.StatePublished {
		return nil
	}
	text, ok := outputs.ReadMarkdown(env.Project, newest)
	if !ok {
		return nil
	}
	what, line := outputs.UnsourcedVendorFact(text)
	if what == "" {
		return nil
	}
	if len(line) > 80 {
		line = line[:77] + "..."
	}
	return []Finding{{
		Where:   slug,
		Message: fmt.Sprintf("%s cites %s (%q) with no source URL and no UNVERIFIED list", newest.ID, what, line),
		Fix:     fmt.Sprintf(`read the vendor's own page (the web-fetch agent), put its URL beside the fact and end the report with "## UNVERIFIED" (what no first-party page confirmed, or none); then issue-cli output revise %s --file <report.md>`, newest.ID),
	}}
}
