package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The lead adds scope after dispatch as a "lead:" comment. show and start
// print comments last, where a worker's head or tail cuts them, so transition
// (every worker's call at each step) prints the ones posted since the issue's
// last move first, before its own result. It prints only: nothing refuses.

// leadCommentsHeading is the line the block starts with; docs and tests
// quote it.
const leadCommentsHeading = "== From the lead since your last move =="

// leadComment is one "lead:" comment the worker hasn't been shown at a move.
type leadComment struct {
	ID     int    `json:"id"`
	Date   string `json:"date"`
	Author string `json:"author,omitempty"`
	Text   string `json:"text"`
	// Answered is true when a later comment by someone else follows it.
	Answered bool `json:"answered"`
}

// isLeadComment is the one test for a lead's scope note: its text starts
// with "lead:" (any case).
func isLeadComment(c tracker.Comment) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Text)), "lead:")
}

// lastMove is when the issue last changed status, from its stats sidecar;
// ok is false when no transition is recorded.
func lastMove(store tracker.StatsStore) (time.Time, bool) {
	var last time.Time
	for _, tr := range store.Transitions {
		if tr.TS.After(last) {
			last = tr.TS
		}
	}
	return last, !last.IsZero()
}

// newLeadComments are the issue's "lead:" comments posted after since (every
// one when hasSince is false), minus the caller's own. A comment dated by
// day only (before v0.54) is newer only when its day is after since's day.
func newLeadComments(comments []tracker.Comment, since time.Time, hasSince bool, caller string) []leadComment {
	var out []leadComment
	for i, c := range comments {
		if !isLeadComment(c) {
			continue
		}
		author := tracker.CommentAuthor(c)
		if caller != "" && author == caller {
			continue
		}
		if hasSince && !postedAfter(c.Date, since) {
			continue
		}
		lc := leadComment{ID: c.ID, Date: c.Date, Author: author, Text: strings.TrimSpace(c.Text)}
		for _, later := range comments[i+1:] {
			if a := tracker.CommentAuthor(later); a != author {
				lc.Answered = true
				break
			}
		}
		out = append(out, lc)
	}
	return out
}

func postedAfter(date string, since time.Time) bool {
	t, ok := tracker.CommentTime(date)
	if !ok {
		return false
	}
	if len(strings.TrimSpace(date)) == len("2006-01-02") {
		return tracker.CommentDay(date) > since.In(time.Local).Format("2006-01-02")
	}
	return t.After(since)
}

// leadCommentsFor reads issue's new lead comments for the calling session.
// A sidecar that can't be read counts as no move recorded: the comments
// print rather than hide.
func leadCommentsFor(ctx *Context, issue *tracker.Issue) []leadComment {
	store, _ := tracker.LoadStats(issue.FilePath)
	since, ok := lastMove(store)
	return newLeadComments(issue.Comments(), since, ok, commentAuthor(ctx.Project, issue))
}

// printLeadComments writes the block; nothing when there are none.
func printLeadComments(w io.Writer, comments []leadComment) {
	if len(comments) == 0 {
		return
	}
	fmt.Fprintln(w, leadCommentsHeading)
	for _, c := range comments {
		who := c.Author
		if who == "" {
			who = "lead"
		}
		lines := strings.Split(c.Text, "\n")
		fmt.Fprintf(w, "  #%d %s %s: %s\n", c.ID, tracker.CommentDay(c.Date), who, lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(w, "      %s\n", l)
		}
	}
	fmt.Fprintln(w)
}

// leadCommentWarnings are the real transition's reminders for the lead
// comments nobody answered yet: warnings, never a refusal.
func leadCommentWarnings(ctx *Context, slug string, comments []leadComment) []string {
	var out []string
	for _, c := range comments {
		if c.Answered {
			continue
		}
		out = append(out, fmt.Sprintf("lead comment #%d has no reply after it: answer it with %s", c.ID,
			tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"progress: …\"", slug))))
	}
	return out
}
