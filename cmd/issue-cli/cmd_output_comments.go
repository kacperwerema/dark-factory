package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The owner's comments on an output, for agents: output comments lists
// them, output reply answers one, output revise adds the version that
// answers them. The owner writes and resolves comments on the output's
// reader page (shelf_read.go in the viewer).

// commentsLine is output show's line about the output's comments, "" for
// none.
func commentsLine(p *tracker.Project, o *outputs.Output) string {
	talk, _ := outputs.LoadComments(p)
	all := talk[o.ID]
	if len(all) == 0 {
		return ""
	}
	return fmt.Sprintf("Comments: %d open, %d resolved · issue-cli output comments %s", o.OpenComments, len(all)-o.OpenComments, shortID(o.ID))
}

// readerURL is the output's reader page in the viewer.
func readerURL(p *tracker.Project, o *outputs.Output) string {
	return fmt.Sprintf("%s/p/%s/shelf/read/%s", viewerBaseURL(p), p.Slug, o.ID)
}

// newestDoc is the newest version rendered, ok false when it can't be read
// (a link, or the file is gone).
func newestDoc(p *tracker.Project, o *outputs.Output) (outputs.Doc, bool) {
	var path string
	switch {
	case o.File != "":
		path = filepath.Join(outputs.FilesDir(p), filepath.FromSlash(o.File))
	case o.Path != "":
		path = filepath.Join(p.Root(), filepath.FromSlash(o.Path))
	default:
		return outputs.Doc{}, false
	}
	src, err := os.ReadFile(path)
	if err != nil || !o.IsMarkdown() {
		return outputs.Doc{}, false
	}
	return outputs.Render(src), true
}

// threadPlace is where a thread sits in the newest version, in words.
func threadPlace(t *outputs.Thread, doc outputs.Doc, ok bool, version int) string {
	if !ok {
		return ""
	}
	switch place, _ := outputs.Locate(t, doc.Text, doc.Headings); place {
	case outputs.PlaceWhole:
		return "whole document"
	case outputs.PlaceHere:
		if where := outputs.HeadingPath(doc.Toc, t.Anchor.Heading); where != "" {
			return "under " + where
		}
		return "passage"
	case outputs.PlaceHeading:
		where := outputs.HeadingPath(doc.Toc, t.Anchor.Heading)
		if t.Anchor.Quote == "" {
			return "heading " + where
		}
		return fmt.Sprintf("under %s; the passage is not in v%d", where, version)
	default:
		return fmt.Sprintf("not in v%d (was under %q)", version, t.Anchor.HeadingText)
	}
}

func runOutputComments(ctx *Context, args []string) error {
	fs := newFlagSet("output comments", ctx)
	all := fs.Bool("all", false, "also list resolved threads")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli output comments <id> [--all]"}
	}
	p := ctx.Project
	list, _ := outputs.Load(p)
	o, err := outputs.Find(list, fs.Arg(0))
	if err != nil {
		return err
	}
	talk, _ := outputs.LoadComments(p)
	var threads []*outputs.Thread
	for _, t := range talk[o.ID] {
		if *all || !t.Resolved {
			threads = append(threads, t)
		}
	}
	if ctx.JSONOutput {
		if threads == nil {
			threads = []*outputs.Thread{}
		}
		return writeJSON(ctx.Stdout, threads)
	}
	resolved := len(talk[o.ID]) - o.OpenComments
	fmt.Fprintf(ctx.Stdout, "== Comments on %s v%d: %s · %d open, %d resolved ==\n", shortID(o.ID), o.Version(), o.Title, o.OpenComments, resolved)
	if len(threads) == 0 {
		if len(talk[o.ID]) == 0 {
			fmt.Fprintf(ctx.Stdout, "No comments yet. The owner comments on the reader page: %s\n", readerURL(p, o))
		} else {
			fmt.Fprintf(ctx.Stdout, "Nothing open (--all lists the %d resolved).\n", resolved)
		}
		return nil
	}
	doc, ok := newestDoc(p, o)
	for _, t := range threads {
		state := "open"
		if t.Resolved {
			state = "resolved"
		}
		fmt.Fprintf(ctx.Stdout, "\n%s [%s] v%d", t.ID, state, t.Version)
		if where := threadPlace(t, doc, ok, o.Version()); where != "" {
			fmt.Fprintf(ctx.Stdout, " · %s", where)
		}
		fmt.Fprintln(ctx.Stdout)
		if t.Anchor.Quote != "" {
			fmt.Fprintf(ctx.Stdout, "  > %s\n", t.Anchor.Quote)
		}
		for _, m := range t.Messages {
			who := m.By
			if who == tracker.OwnerAuthor {
				who = "owner"
			}
			fmt.Fprintf(ctx.Stdout, "  %s, %s: %s\n", who, m.Date.Local().Format("2006-01-02 15:04"), strings.Join(strings.Fields(m.Text), " "))
		}
	}
	fmt.Fprintf(ctx.Stdout, "\nAnswer a thread: issue-cli output reply %s <thread> --text \"...\"\n", shortID(o.ID))
	if o.File != "" && o.State != outputs.StatePublished {
		fmt.Fprintf(ctx.Stdout, "Add the version that answers them: issue-cli output revise %s --file <file> --note \"...\"\n", shortID(o.ID))
	}
	fmt.Fprintf(ctx.Stdout, "Page: %s\n", readerURL(p, o))
	return nil
}

func runOutputReply(ctx *Context, args []string) error {
	fs := newFlagSet("output reply", ctx)
	text := fs.String("text", "", "the answer (required)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*text) == "" {
		return &usageError{msg: `usage: issue-cli output reply <id> <thread> --text "..."`}
	}
	p := ctx.Project
	list, _ := outputs.Load(p)
	o, err := outputs.Find(list, fs.Arg(0))
	if err != nil {
		return err
	}
	issues, _ := tracker.LoadIssues(p.IssueDir)
	me := whoAmI(p, issues).sender()
	if me == "" || me == tracker.OwnerAuthor {
		me = "agent"
	}
	t, err := outputs.Reply(p, o.ID, fs.Arg(1), normalizeEscapedText(*text), me, o.Version(), ctx.now())
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, t)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Replied on %s (%s) as %s\n", t.ID, shortID(o.ID), me)
	fmt.Fprintf(ctx.Stdout, "The owner sees it in the margin: %s#thread-%s\n", readerURL(p, o), t.ID)
	return nil
}

func runOutputRevise(ctx *Context, args []string) error {
	fs := newFlagSet("output revise", ctx)
	file := fs.String("file", "", "the new version (required)")
	note := fs.String("note", "", "what changed")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*file) == "" {
		return &usageError{msg: `usage: issue-cli output revise <id> --file <new version> [--note "..."]`}
	}
	p := ctx.Project
	issues, _ := tracker.LoadIssues(p.IssueDir)
	o, err := outputs.Revise(p, fs.Arg(0), *file, normalizeEscapedText(*note), whoAmI(p, issues).sender(), ctx.now())
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, o)
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s is at v%d, back in review: %s\n", shortID(o.ID), o.Version(), o.Title)
	printOutput(ctx.Stdout, o, "  ")
	if o.OpenComments > 0 {
		fmt.Fprintf(ctx.Stdout, "💬 %d open %s: answer each with issue-cli output reply %s <thread> --text \"...\" (issue-cli output comments %s lists them)\n", o.OpenComments, pluralWord(o.OpenComments, "comment", "comments"), shortID(o.ID), shortID(o.ID))
	}
	fmt.Fprintf(ctx.Stdout, "Changes: %s?view=changes\n", readerURL(p, o))
	return nil
}
