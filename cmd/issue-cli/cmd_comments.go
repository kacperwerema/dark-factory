package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var commentsCommand = &Command{
	Name:      "comments",
	ShortHelp: "Print an issue's comment trail alone, numbered as the page numbers it",
	LongHelp: `Print an issue's comments without the body: each as #<id>, the number the
issue page, comment edit and question: replies use, with its day, author,
kind and whether it is done.

  --since <date>   only comments made on or after this day (2006-01-02, or
                   an RFC3339 time)
  --author <name>  only comments by this author (lead-<project>,
                   agent-<slug>, owner, …)
  --kind <k>       only comments whose text starts with "<k>:" (question,
                   progress, tests, approval, …); repeatable or comma-separated
  --open           only comments not marked done
  --json           the comments as a JSON array

Examples:
  issue-cli comments <slug>
  issue-cli comments <slug> --kind question --open
  issue-cli comments <slug> --since 2026-10-30 --author lead-dark-factory --json`,
	Run: runComments,
}

func init() {
	registerCommand(commentsCommand)
}

// commentRow is one comment as comments prints it.
type commentRow struct {
	tracker.Comment
	Author string `json:"author"`
	Kind   string `json:"kind"`
	Open   bool   `json:"open"`
}

func runComments(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "comments")
	if err != nil {
		return err
	}
	fs := newFlagSet("comments", ctx)
	sinceFlag := fs.String("since", "", "only comments on or after this day (2006-01-02 or RFC3339)")
	authorFlag := fs.String("author", "", "only comments by this author")
	var kinds []string
	fs.Func("kind", "only comments of this kind (the word before ':'); repeatable or comma-separated", func(v string) error {
		for _, k := range strings.Split(v, ",") {
			if k = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(k), ":")); k != "" {
				kinds = append(kinds, k)
			}
		}
		return nil
	})
	openFlag := fs.Bool("open", false, "only comments not marked done")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	var since time.Time
	if s := strings.TrimSpace(*sinceFlag); s != "" {
		t, ok := tracker.CommentTime(s)
		if !ok {
			return &usageError{msg: fmt.Sprintf("--since %q: want a day (2006-01-02) or an RFC3339 time", *sinceFlag)}
		}
		since = t
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	comments, err := tracker.LoadComments(issue.FilePath)
	if err != nil {
		return err
	}
	rows := filterComments(comments, since, strings.TrimSpace(*authorFlag), kinds, *openFlag)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rows)
	}
	if len(rows) == 0 {
		if len(comments) == 0 {
			fmt.Fprintf(ctx.Stdout, "No comments on %s.\n", issue.Slug)
		} else {
			fmt.Fprintf(ctx.Stdout, "No comments on %s match these filters (%d in all).\n", issue.Slug, len(comments))
		}
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Comments on %s (%d of %d) ==\n", issue.Slug, len(rows), len(comments))
	for _, r := range rows {
		head := []string{fmt.Sprintf("#%d", r.ID), tracker.CommentDay(r.Date)}
		if r.Author != "" {
			head = append(head, r.Author)
		}
		if r.Kind != "" {
			head = append(head, r.Kind)
		}
		if !r.Open {
			head = append(head, "done")
		}
		if len(r.Edits) > 0 {
			head = append(head, "edited")
		}
		fmt.Fprintln(ctx.Stdout, strings.Join(head, " · "))
		for _, line := range strings.Split(strings.TrimRight(r.Text, "\n"), "\n") {
			fmt.Fprintf(ctx.Stdout, "    %s\n", line)
		}
	}
	return nil
}

// filterComments keeps the comments that pass every filter, in file order.
// A zero since, empty author or no kinds don't filter.
func filterComments(comments []tracker.Comment, since time.Time, author string, kinds []string, onlyOpen bool) []commentRow {
	rows := []commentRow{}
	for _, c := range comments {
		r := commentRow{Comment: c, Author: tracker.CommentAuthor(c), Kind: commentKind(c.Text), Open: c.Status != "done"}
		if onlyOpen && !r.Open {
			continue
		}
		if author != "" && !strings.EqualFold(r.Author, author) {
			continue
		}
		if len(kinds) > 0 && !containsFold(kinds, r.Kind) {
			continue
		}
		if !since.IsZero() {
			at, ok := tracker.CommentTime(c.Date)
			if !ok || at.Before(since) {
				continue
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// commentKind is the word a comment starts with before ':' ("question",
// "progress", "tests"), lowercased, or "" when it starts with none.
func commentKind(text string) string {
	text = strings.TrimSpace(text)
	i := strings.Index(text, ":")
	if i <= 0 || i >= 20 {
		return ""
	}
	k := strings.ToLower(text[:i])
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return k
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
