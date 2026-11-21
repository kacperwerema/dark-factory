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
	for _, run := range jobs.Runs(plan, issues, ref.Key) {
		if !run.Open {
			continue
		}
		for _, c := range run.Issue.Comments() {
			text, ok := commentAfter(c, "report:")
			if !ok {
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
	var shelf []*outputs.Output
	loaded := false
	for _, issue := range issues {
		if proj.IssueClosed(issue) || outOfScope(proj, me, issue, "files reports for") != "" {
			continue
		}
		var lastReport time.Time
		for _, c := range issue.Comments() {
			text, ok := commentAfter(c, "report:")
			if !ok || askedByALead(tracker.CommentAuthor(c)) {
				continue
			}
			if at, ok := tracker.CommentTime(c.Date); ok && at.After(lastReport) {
				lastReport = at
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
		if !loaded {
			shelf, _ = outputs.Load(proj)
			loaded = true
		}
		for _, o := range outputs.Select(shelf, outputs.Filter{Issue: issue.Slug}) {
			if o.State != outputs.StateReview || o.Missing || !o.Added.After(lastReport) {
				continue
			}
			t.say("report-output:"+o.ID, fmt.Sprintf("report: %s's output %s is on the shelf: file its follow-ups (%s, then %s)",
				issue.Slug, agentrun.OutputLabel(o), create, add))
		}
	}
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
