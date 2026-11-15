package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// driftHint ends the listings whose rows the sweep reports project-wide.
const driftHint = "every open issue citing replaced or expired refs, and more: issue-cli decisions drift"

type driftOutput struct {
	tracker.DriftReport
	Recorded bool   `json:"recorded"`
	Mark     string `json:"mark"`
}

// runDecisionsDrift is issue-cli decisions drift: the four kinds of drift
// (tracker.Drift). It reads only; --record also moves the last-sweep mark
// (<issues>/.decisions/drift.json) to main's head, which the
// decision-drift job's run does.
func runDecisionsDrift(ctx *Context, args []string) error {
	fs := newFlagSet("decisions drift", ctx)
	record := fs.Bool("record", false, "move the last-sweep mark to main's head after the sweep (the decision-drift job does)")
	since := fs.String("since", "", "<commit>: read main's commits after this one instead of after the mark (this run only)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if *record && strings.TrimSpace(*since) != "" {
		return fmt.Errorf("--since reads a window of your choosing; it doesn't go with --record (the mark moves only after a sweep from the mark)")
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot load issues: %w", err)
	}
	root := ctx.Project.TelemetryRoot()
	rep, err := tracker.Drift(tracker.DriftInput{
		Issues: issues, IsClosed: tracker.ClosedIssueFunc(ctx.Project), IssueDir: ctx.Project.IssueDir,
		Root: root, DocsDir: driftDocsDir(ctx.Project, root), Today: ctx.now(), Since: *since,
	})
	if err != nil {
		return err
	}
	out := driftOutput{DriftReport: rep, Mark: tracker.DriftMarkPath(ctx.Project.IssueDir)}
	if *record && rep.Window.To != "" {
		me := whoAmI(ctx.Project, issues)
		by := me.Name
		if by == "" {
			by = "owner"
		}
		if err := tracker.SaveDriftMark(ctx.Project.IssueDir, tracker.DriftMark{At: ctx.now(), Main: rep.Window.To, By: by}); err != nil {
			return fmt.Errorf("swept, but the mark was not written: %w", err)
		}
		out.Recorded = true
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	writeDriftMarkdown(ctx.Stdout, ctx.Project, out, *record)
	return nil
}

// driftDocsDir is the project's docs folder: docs: from projects.yaml
// (relative to the project root when it isn't found from here), else
// <root>/docs. "" when there is none.
func driftDocsDir(p *tracker.Project, root string) string {
	dir := strings.TrimSpace(p.DocsDir)
	if dir == "" {
		dir = "docs"
	}
	cands := []string{dir}
	if !filepath.IsAbs(dir) && root != "" {
		cands = []string{filepath.Join(root, dir), dir}
	}
	for _, c := range cands {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

var driftHeadings = map[string]string{
	tracker.DriftCitation:    "Open issues citing replaced or expired decisions",
	tracker.DriftPaths:       "Hard decisions' paths changed on main without a citation",
	tracker.DriftDocs:        "Settled rules pointing at replaced or expired decisions",
	tracker.DriftProvisional: "Provisional decisions older than 14 days that reach beyond their issue",
}

// writeDriftMarkdown prints the report as markdown, so the job's run can
// append it to an issue as it is.
func writeDriftMarkdown(w io.Writer, p *tracker.Project, out driftOutput, record bool) {
	rep := out.DriftReport
	fmt.Fprintf(w, "Decision drift · %s\n\n", driftWindowLine(rep.Window))
	if len(rep.Findings) == 0 {
		fmt.Fprintln(w, "No drift.")
	}
	for _, kind := range tracker.DriftKinds {
		n := rep.Count(kind)
		if n == 0 {
			continue
		}
		fmt.Fprintf(w, "## %s (%d)\n\n", driftHeadings[kind], n)
		for _, f := range rep.Findings {
			if f.Kind == kind {
				fmt.Fprintln(w, driftLine(f))
			}
		}
		fmt.Fprintln(w)
	}
	if rep.ProvisionalRest > 0 {
		fmt.Fprintf(w, "%d more provisional %s more than %d days old stay in their issue: %s\n", rep.ProvisionalRest,
			pluralWord(rep.ProvisionalRest, "decision", "decisions"), tracker.DriftProvisionalAge, tracker.InjectProject(p, "issue-cli decisions --state provisional"))
	}
	switch {
	case out.Recorded:
		fmt.Fprintf(w, "Recorded: the next sweep reads main after %s (%s).\n", gitdir.ShortSHA(rep.Window.To), out.Mark)
	case !record && rep.Window.To != "":
		fmt.Fprintf(w, "Not recorded: the next sweep reads the same window. The decision-drift job moves it: %s\n",
			tracker.InjectProject(p, "issue-cli decisions drift --record"))
	}
}

func driftWindowLine(w tracker.DriftWindow) string {
	var s string
	switch {
	case w.To == "":
		s = "path drift skipped"
	case w.From != "":
		s = fmt.Sprintf("main %s..%s", gitdir.ShortSHA(w.From), gitdir.ShortSHA(w.To))
	default:
		s = "main's last 14 days (no earlier sweep recorded)"
		if w.SinceAt != nil {
			s = fmt.Sprintf("main since %s (no earlier sweep recorded)", w.SinceAt.Format("2006-01-02"))
		}
	}
	if w.LastAt != nil {
		s += fmt.Sprintf(" · last sweep %s", w.LastAt.Format("2006-01-02 15:04"))
	}
	if w.Note != "" {
		s += " · " + w.Note
	}
	return s
}

func driftLine(f tracker.DriftFinding) string {
	var b strings.Builder
	switch f.Kind {
	case tracker.DriftCitation:
		fmt.Fprintf(&b, "- %s cites %s, %s", f.Issue, f.Ref, outOfForce(f))
	case tracker.DriftPaths:
		who := "no issue"
		if f.Issue != "" {
			who = f.Issue
		}
		files := f.Files
		more := ""
		if len(files) > 5 {
			more = fmt.Sprintf(" and %d more", len(files)-5)
			files = files[:5]
		}
		fmt.Fprintf(&b, "- %s · %s %q (%s) changed %s%s", f.Ref, gitdir.ShortSHA(f.SHA), oneLineText(f.Subject, 80), who, strings.Join(files, ", "), more)
	case tracker.DriftDocs:
		fmt.Fprintf(&b, "- %s:%d cites %s, %s", f.File, f.Line, f.Ref, outOfForce(f))
	case tracker.DriftProvisional:
		fmt.Fprintf(&b, "- %s · since %s · scope %s", f.Ref, f.Since, f.Scope)
	}
	if f.Text != "" {
		fmt.Fprintf(&b, "\n  %s", oneLineText(f.Text, 110))
	}
	fmt.Fprintf(&b, "\n  fix: %s", f.Fix)
	return b.String()
}

func outOfForce(f tracker.DriftFinding) string {
	if f.ReplacedBy != "" {
		return "replaced by " + f.ReplacedBy
	}
	return "expired after " + f.Expired
}
