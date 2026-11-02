package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

type touchedRow struct {
	Ref        string   `json:"ref"`
	Text       string   `json:"text"`
	File       string   `json:"file"`
	Paths      []string `json:"paths"`
	Files      []string `json:"files"`
	Cited      bool     `json:"cited"`
	Challenged bool     `json:"challenged"`
}

type touchedOutput struct {
	Slug    string       `json:"slug"`
	Base    string       `json:"base,omitempty"`
	Head    string       `json:"head,omitempty"`
	Changed int          `json:"changed"`
	Warning string       `json:"warning,omitempty"`
	Touched []touchedRow `json:"touched"`
}

// runDecisionsTouched prints the decisions_respected audit for one issue:
// the hard decisions whose paths: its branch changes, the files, and
// whether each is cited or challenged by the issue. It only reads.
func runDecisionsTouched(ctx *Context, slug string) error {
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	out, err := touchedFor(ctx.Project, issue, issues, ctx)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	if out.Warning != "" {
		fmt.Fprintf(ctx.Stdout, "⚠ %s\n", out.Warning)
		return nil
	}
	if len(out.Touched) == 0 {
		fmt.Fprintf(ctx.Stdout, "The branch of %s (%d files changed) touches no hard decision's paths:.\n", issue.Slug, out.Changed)
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Hard decisions the branch of %s touches (%d files changed, %s...%s) ==\n", issue.Slug, out.Changed, shortHex(out.Base), shortHex(out.Head))
	held := 0
	for _, r := range out.Touched {
		state := "cited"
		if !r.Cited {
			state = "NOT cited"
		}
		if r.Challenged {
			state += ", this issue's challenge open"
		}
		if !r.Cited || r.Challenged {
			held++
		}
		fmt.Fprintf(ctx.Stdout, "%s · %s\n    %s\n    files: %s  (paths: %s)\n", r.Ref, state, oneLineText(r.Text, 100), strings.Join(r.Files, ", "), strings.Join(r.Paths, ", "))
	}
	if held > 0 {
		fmt.Fprintf(ctx.Stdout, "decisions_respected holds %d: cite each in the body (%s)\n", held,
			tracker.InjectProject(ctx.Project, fmt.Sprintf(`issue-cli append %s --section Design --body "Follows <ref>: …"`, issue.Slug)))
	}
	return nil
}

// touchedFor runs the audit the decisions_respected rule runs, against the
// project's checkout, for the CLI and the critic's brief.
func touchedFor(proj *tracker.Project, issue *tracker.Issue, issues []*tracker.Issue, ctx *Context) (touchedOutput, error) {
	out := touchedOutput{Slug: issue.Slug, Touched: []touchedRow{}}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(proj))
	r, err := tracker.CheckRespected(index, proj.TelemetryRoot(), proj.IssueDir, issue, ctx.now())
	if err != nil {
		return out, err
	}
	out.Base, out.Head, out.Changed, out.Warning = r.Diff.Base, r.Diff.Head, len(r.Diff.Files), r.Diff.Warn
	held := map[string]tracker.RespectedHold{}
	for _, h := range r.Holds {
		held[h.Entry.Ref] = h
	}
	for _, t := range r.Touched {
		h, isHeld := held[t.Entry.Ref]
		out.Touched = append(out.Touched, touchedRow{
			Ref: t.Entry.Ref, Text: t.Entry.Text, File: t.Entry.Issue.FilePath, Paths: t.Entry.Paths, Files: t.Files,
			Cited: !isHeld || !h.Uncited, Challenged: isHeld && h.Challenged,
		})
	}
	return out, nil
}

func shortHex(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func oneLineText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
