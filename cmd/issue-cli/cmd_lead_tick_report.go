package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Comments the lead has to act on, besides the owner's: a queue-low
// sweep's report: (its proposals go on the queue before it runs dry) and a
// worker's open question: (it holds a slot until someone answers). Both
// are news, said once per comment. 2026-11-18: a sweep's report landed at
// 02:01 and a worker asked at 00:31; the tick said neither, the queue ran
// dry at 02:35 and the next start was 07:30.

// leadTickCommentMax is how many characters of a comment a tick line quotes.
const leadTickCommentMax = 100

// leadTickReports says each report: comment on an open run of the
// queue-low sweep's job.
func leadTickReports(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	proj := ctx.Project
	plan, ref := jobs.QueueSweepRef(proj)
	add := tracker.InjectProject(proj, `issue-cli queue add <slug> --quote "<owner's words>"`)
	filed := newFiledFollowUps(proj, issues)
	for _, run := range jobs.Runs(plan, issues, ref.Key) {
		if !run.Open {
			continue
		}
		comments := run.Issue.Comments()
		for i, c := range comments {
			text, ok := commentAfter(c, "report:")
			if !ok || filed.comment(run.Issue, comments, i) {
				continue
			}
			t.say(fmt.Sprintf("report:%s:%d", run.Issue.Slug, c.ID),
				fmt.Sprintf("report: %s: %s: file its proposals (%s, or create them first) before the queue runs dry",
					run.Issue.Slug, text, add))
		}
	}
	leadTickWorkerReports(ctx, t, issues)
}

// leadTickWorkerReports says every other worker's report: comment on an
// open issue in the lead's scope, once per comment, with how many
// follow-ups it proposes; and a report output on the shelf, in review, that
// no report: comment came after, once per output. 2026-11-20: two reports
// landed by 22:20 with follow-ups "for the lead to file"; neither was news
// and the queue stayed empty all night.
func leadTickWorkerReports(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	proj := ctx.Project
	me := whoAmI(proj, issues)
	create := tracker.InjectProject(proj, `issue-cli create`)
	add := tracker.InjectProject(proj, `issue-cli queue add <slug> --quote "<owner's words>"`)
	filed := newFiledFollowUps(proj, issues)
	for _, issue := range issues {
		if proj.IssueClosed(issue) || outOfScope(proj, me, issue, "files reports for") != "" {
			continue
		}
		var lastReport time.Time
		comments := issue.Comments()
		for i, c := range comments {
			text, ok := commentAfter(c, "report:")
			if !ok || askedByALead(tracker.CommentAuthor(c)) {
				continue
			}
			if at, ok := tracker.CommentTime(c.Date); ok && at.After(lastReport) {
				lastReport = at
			}
			if filed.comment(issue, comments, i) {
				continue
			}
			line := fmt.Sprintf("report: %s reported: %s", issue.Slug, text)
			if n := followUpCount(c.Text); n > 0 {
				line = fmt.Sprintf("report: %s's report has %s to file: %s: %s, then %s", issue.Slug, countOf(n, "follow-up", "follow-ups"), text, create, add)
			}
			t.say(fmt.Sprintf("report:%s:%d", issue.Slug, c.ID), line)
		}
		wf := proj.LoadWorkflowForIssue(issue)
		if _, _, ok := wf.HasOutputRule(); !ok {
			continue
		}
		for _, o := range outputs.Select(filed.shelf(), outputs.Filter{Issue: issue.Slug}) {
			if o.State != outputs.StateReview || o.Missing || !o.Added.After(lastReport) || filed.output(issue, comments, o) {
				continue
			}
			t.say("report-output:"+o.ID, fmt.Sprintf("report: %s's output %s is on the shelf: file its follow-ups (%s, then %s)",
				issue.Slug, agentrun.OutputLabel(o), create, add))
		}
	}
}

// A report whose follow-ups are filed is no news. 2026-11-21 09:40: the
// first tick after 5313b48 said "has N follow-ups to file" for six
// reports, though one's had been filed minutes before (two issues and a
// lead comment) and another's that morning; a fresh lead would have filed
// them again. A report's follow-ups are filed when another issue, in any
// status, names its output id (o-YYYYMMDD-xxxx) in its title or body, or a
// lead's (or the owner's) comment after it on the same issue starts with
// leadFollowUpsFiled.

// leadFollowUpsFiled is the one shape of the lead's comment that says a
// report's follow-ups are filed: `lead: follow-ups filed: 464, 465`.
const leadFollowUpsFiled = "lead: follow-ups filed:"

// reportOutputID is an output id as a report or a follow-up names it.
var reportOutputID = regexp.MustCompile(`\bo-\d{8}-[0-9a-f]{4}\b`)

// filedFollowUps is what a tick reads once to tell a filed report: which
// issues name which output ids, and the shelf, loaded when first asked.
type filedFollowUps struct {
	proj   *tracker.Project
	citers map[string][]string // output id → slugs of the issues naming it
	list   []*outputs.Output
	loaded bool
}

func newFiledFollowUps(proj *tracker.Project, issues []*tracker.Issue) *filedFollowUps {
	f := &filedFollowUps{proj: proj, citers: map[string][]string{}}
	for _, is := range issues {
		for _, id := range reportOutputID.FindAllString(is.Title+"\n"+is.BodyRaw, -1) {
			f.citers[id] = append(f.citers[id], is.Slug)
		}
	}
	return f
}

func (f *filedFollowUps) shelf() []*outputs.Output {
	if !f.loaded {
		f.list, _ = outputs.Load(f.proj)
		f.loaded = true
	}
	return f.list
}

// cited is whether an issue other than the report's own names id.
func (f *filedFollowUps) cited(issue *tracker.Issue, id string) bool {
	for _, slug := range f.citers[id] {
		if slug != issue.Slug {
			return true
		}
	}
	return false
}

// comment is whether the report: comment comments[i] on issue has its
// follow-ups filed: a lead's leadFollowUpsFiled comment after it, or
// another issue naming an output id it names (else, when it names none,
// one of the issue's outputs shelved by then).
func (f *filedFollowUps) comment(issue *tracker.Issue, comments []tracker.Comment, i int) bool {
	for _, c := range comments[i+1:] {
		if leadSaysFiled(c) {
			return true
		}
	}
	ids := reportOutputID.FindAllString(comments[i].Text, -1)
	if len(ids) == 0 && len(f.citers) > 0 {
		at, ok := tracker.CommentTime(comments[i].Date)
		for _, o := range outputs.Select(f.shelf(), outputs.Filter{Issue: issue.Slug}) {
			if !ok || !o.Added.After(at) {
				ids = append(ids, o.ID)
			}
		}
	}
	for _, id := range ids {
		if f.cited(issue, id) {
			return true
		}
	}
	return false
}

// output is whether shelf output o of issue has its follow-ups filed:
// another issue names its id, or a lead's leadFollowUpsFiled comment on
// issue came after it was shelved.
func (f *filedFollowUps) output(issue *tracker.Issue, comments []tracker.Comment, o *outputs.Output) bool {
	if f.cited(issue, o.ID) {
		return true
	}
	for _, c := range comments {
		if at, ok := tracker.CommentTime(c.Date); ok && !at.Before(o.Added) && leadSaysFiled(c) {
			return true
		}
	}
	return false
}

// leadSaysFiled is whether c is a lead's or the owner's
// leadFollowUpsFiled comment.
func leadSaysFiled(c tracker.Comment) bool {
	return askedByALead(tracker.CommentAuthor(c)) &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Text)), leadFollowUpsFiled)
}

// followUpCounted is "Follow-ups (11 proposed issues …):": the count named.
var followUpCounted = regexp.MustCompile(`(?i)follow[- ]?ups?\b[^:\n]*?\b(\d+)\b[^:\n]*:`)

// followUpHead is a follow-ups lead-in: "Follow-ups:", "follow-ups for the lead to file:".
var followUpHead = regexp.MustCompile(`(?i)follow[- ]?ups?\b[^:\n]*:`)

// listItem is a markdown list line.
var listItem = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+\S`)

// followUpCount is how many follow-ups a report: comment proposes: the
// number its "Follow-ups (N …):" names, else the list lines under a
// follow-ups line, else the items after it up to the sentence's end split
// on ";"; 0 when it names no follow-ups.
func followUpCount(text string) int {
	if m := followUpCounted.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	loc := followUpHead.FindStringIndex(text)
	if loc == nil {
		return 0
	}
	rest := text[loc[1]:]
	lines := strings.Split(rest, "\n")
	n := 0
	for _, l := range lines[1:] {
		if listItem.MatchString(l) {
			n++
		} else if strings.TrimSpace(l) != "" && n > 0 {
			break
		}
	}
	if n > 0 {
		return n
	}
	first := lines[0]
	if i := strings.Index(first, ". "); i >= 0 {
		first = first[:i]
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(first)), "none") {
		return 0
	}
	for _, part := range strings.Split(first, ";") {
		if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), ".")) != "" {
			n++
		}
	}
	return n
}

// leadTickQuestions says each open issue whose newest comment is a
// worker's question:, in the lead's scope. The owner's and leads' own
// questions are not the lead's to answer here.
func leadTickQuestions(ctx *Context, t *leadTicker, issues []*tracker.Issue) {
	proj := ctx.Project
	me := whoAmI(proj, issues)
	for _, issue := range issues {
		if proj.IssueClosed(issue) || outOfScope(proj, me, issue, "answers questions for") != "" {
			continue
		}
		comments := issue.Comments()
		if len(comments) == 0 {
			continue
		}
		c := comments[len(comments)-1]
		text, ok := commentAfter(c, "question:")
		if !ok || askedByALead(tracker.CommentAuthor(c)) {
			continue
		}
		t.say(fmt.Sprintf("question:%s:%d", issue.Slug, c.ID),
			fmt.Sprintf("question: %s: %s: answer it (%s) or relay it to the owner; the worker holds a slot until then",
				issue.Slug, text, tracker.InjectProject(proj, `issue-cli comment `+issue.Slug+` "answer: …"`)))
	}
}

// commentAfter is c's text after prefix (case-insensitive), on one line
// and cut to leadTickCommentMax; ok false when c doesn't start with it.
func commentAfter(c tracker.Comment, prefix string) (string, bool) {
	text := strings.TrimSpace(c.Text)
	if !strings.HasPrefix(strings.ToLower(text), prefix) {
		return "", false
	}
	text = strings.Join(strings.Fields(text[len(prefix):]), " ")
	if r := []rune(text); len(r) > leadTickCommentMax {
		text = string(r[:leadTickCommentMax]) + "…"
	}
	return text, true
}

// askedByALead is whether author is the owner or a lead: every lead's
// session name (the project's, a department's, a group's) starts with
// tracker.LeadSessionName's "lead-". Their questions are not a worker's.
func askedByALead(author string) bool {
	return author == tracker.OwnerAuthor || strings.HasPrefix(author, "lead-")
}
