package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/jobs"
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
