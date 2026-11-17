package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/issuetrace"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var traceCommand = &Command{
	Name:      "trace",
	ShortHelp: "Show an issue's trace: one span per status visit",
	LongHelp: `Show an issue's trace: one span per status visit (a status entered twice
is two spans), built from what is already recorded.

Each span has the agent and model, the session ids, the briefing sent in
the visit (bytes, estimated tokens, the memory ids and decision refs it
carried), the tokens its messages spent (in, out, cached), the wall time,
its issue-cli calls and refused calls, the gate out (tries, first try) and
whether the issue went back or came back.

Sources: the stats sidecar (transitions, dispatches, briefings), the
clilogs, and the Claude Code transcripts of sessions in the issue's
worktree. A value nothing recorded shows as —: no transcript (a Codex
worker, or another machine) leaves tokens unknown, not zero.

The trace file, <issues>/.traces/<slug>.jsonl, is rewritten after every
transition. It never leaves the machine and holds sizes, counts, times and
ids only, never a briefing's or a transcript's text.

  --json    the spans as JSON
  --write   also rewrite the trace file

Examples:
  issue-cli trace ui/board-filters
  issue-cli trace ui/board-filters --json`,
	Run: runTrace,
}

func init() { registerCommand(traceCommand) }

// traceProjectsDir is where Claude Code keeps transcripts; "" is
// ~/.claude/projects. Tests point it at a fixture.
var traceProjectsDir = ""

type traceOutput struct {
	Slug    string       `json:"slug"`
	File    string       `json:"file"`
	Spans   []trace.Span `json:"spans"`
	Read    []string     `json:"read"`
	Missing []string     `json:"missing,omitempty"`
}

func runTrace(ctx *Context, args []string) error {
	slug, rest, err := issueSlug(ctx, args, "trace")
	if err != nil {
		return err
	}
	fs := newFlagSet("trace", ctx)
	asJSON := fs.Bool("json", false, "the spans as JSON")
	write := fs.Bool("write", false, "also rewrite the trace file")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return &usageError{msg: "usage: issue-cli trace <slug> [--json] [--write]"}
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	t := issuetrace.Load(ctx.Project, issue, nil, traceProjectsDir, ctx.now())
	path := trace.Path(ctx.Project.IssueDir, issue.Slug)
	if *write {
		if err := trace.Write(path, t.Spans); err != nil {
			return fmt.Errorf("writing the trace: %w", err)
		}
	}
	out := traceOutput{Slug: issue.Slug, File: path, Spans: t.Spans, Read: t.Read}
	for _, m := range t.Missing {
		out.Missing = append(out.Missing, m.Name+": "+m.Detail)
	}
	if *asJSON {
		if out.Spans == nil {
			out.Spans = []trace.Span{}
		}
		return writeJSON(ctx.Stdout, out)
	}
	fmt.Fprintf(ctx.Stdout, "== Trace: %s ==\n", issue.Title)
	if len(t.Spans) == 0 {
		fmt.Fprintln(ctx.Stdout, "Nothing recorded yet.")
	} else {
		trace.Table(ctx.Stdout, t.Spans)
	}
	fmt.Fprintln(ctx.Stdout)
	read := "nothing"
	if len(t.Read) > 0 {
		read = strings.Join(t.Read, "; ")
	}
	fmt.Fprintf(ctx.Stdout, "Read: %s\n", read)
	for _, m := range out.Missing {
		fmt.Fprintf(ctx.Stdout, "Not read: %s\n", m)
	}
	if *write {
		fmt.Fprintf(ctx.Stdout, "✓ Wrote %s\n", path)
	} else {
		fmt.Fprintf(ctx.Stdout, "File: %s (rewritten after every transition; --write now)\n", path)
	}
	return nil
}

// writeIssueTrace rewrites issue's trace file after a transition. A failure
// is a warning: the transition already happened.
func writeIssueTrace(ctx *Context, w io.Writer, issue *tracker.Issue) {
	if ctx.Project == nil || ctx.Project.IssueDir == "" {
		return
	}
	path, err := issuetrace.Write(ctx.Project, issue, traceProjectsDir, ctx.now())
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: the trace was not written: %v\n", err)
		return
	}
	fmt.Fprintf(w, "Trace: %s (%s)\n", path, tracker.InjectProject(ctx.Project, "issue-cli trace "+issue.Slug))
}
