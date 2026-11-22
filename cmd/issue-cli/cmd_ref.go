package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/docref"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var refCommand = &Command{
	Name:      "ref",
	ShortHelp: "A docs page in short: its outline, or one section (instead of reading the whole page)",
	LongHelp: `Print the compact form of a docs page, so you read the part you need
instead of a 100-200 KB page:

  issue-cli ref                       the pages workers read most, then the rest
  issue-cli ref <page>                the outline: each heading with its first
                                      sentence and its line
  issue-cli ref <page> <heading words>  that one section in full
  issue-cli ref --measure [--days 7]  which pages this project's workers read
                                      (from their Claude Code transcripts on
                                      this machine) and their median first-hour
                                      tokens

<page> is a short name (cli, departments, proof, lead-session: the file
name, or its folder's for an overview page), a path under docs/, or words
of its title. The docs come from your checkout (./docs), else the project
root's. Nothing is cached: the compact form is made from the page each time.

Examples:
  issue-cli ref cli
  issue-cli ref cli transition
  issue-cli ref docs/Workflow/proof.md "proof that waits"
  issue-cli ref --measure`,
	Run: runRef,
}

func init() { registerCommand(refCommand) }

func runRef(ctx *Context, args []string) error {
	fs := newFlagSet("ref", ctx)
	measure := fs.Bool("measure", false, "measure which docs pages workers read, and their first-hour tokens")
	days := fs.Int("days", 7, "with --measure: how many days of transcripts")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if *measure {
		return refMeasure(ctx, *days)
	}
	dir := refDocsDir(ctx)
	if dir == "" {
		return fmt.Errorf("no docs/ folder here or in the project root")
	}
	inject := func(s string) string { return tracker.InjectProject(ctx.Project, s) }
	w := ctx.Stdout
	if fs.NArg() == 0 {
		fmt.Fprintf(w, "Docs pages in %s: issue-cli ref <page> for the outline, ref <page> <heading words> for one section.\n\nRead most by workers (7 days to 2026-11-15):\n", dir)
		top := map[string]bool{}
		for _, rel := range docref.Top {
			if p, err := docref.Load(dir, rel); err == nil {
				top[rel] = true
				fmt.Fprintf(w, "  %-16s docs/%s · %s (%d lines)\n", docref.Key(rel), rel, p.Title, len(p.Lines))
			}
		}
		var rest []string
		for _, rel := range docref.Pages(dir) {
			if !top[rel] {
				rest = append(rest, docref.Key(rel))
			}
		}
		if len(rest) > 0 {
			fmt.Fprintf(w, "\nThe rest: %s\n", strings.Join(rest, ", "))
		}
		return nil
	}
	page, err := docref.Resolve(dir, fs.Arg(0))
	if err != nil {
		return err
	}
	if fs.NArg() == 1 {
		fmt.Fprint(w, page.Outline(inject(fmt.Sprintf("issue-cli ref %s <heading words>", docref.Key(page.Rel)))))
		return nil
	}
	out, err := page.Section(fs.Args()[1:])
	if err != nil {
		return refElsewhere(dir, page.Rel, fs.Args()[1:], err)
	}
	fmt.Fprint(w, out)
	return nil
}

// refDocsDir is ./docs when there is one (a worktree reads its own branch's
// docs), else the project root's docs folder.
func refDocsDir(ctx *Context) string {
	if st, err := os.Stat("docs"); err == nil && st.IsDir() {
		if abs, err := filepath.Abs("docs"); err == nil {
			return abs
		}
	}
	if ctx.Project != nil {
		d := filepath.Join(projectRoot(ctx.Project), "docs")
		if ctx.Project.DocsDir != "" {
			d = ctx.Project.DocsDir
			if !filepath.IsAbs(d) {
				d = filepath.Join(projectRoot(ctx.Project), d)
			}
		}
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

// refFolderKey is Claude Code's folder name for a path.
var refFolderKey = regexp.MustCompile(`[^A-Za-z0-9]`)

func refMeasure(ctx *Context, days int) error {
	if ctx.Project == nil {
		return fmt.Errorf("ref --measure needs a project: pass --project <slug>")
	}
	root := projectRoot(ctx.Project)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	prefix := refFolderKey.ReplaceAllString(root, "-") + "--worktrees-"
	rep := docref.Measure(spend.DefaultProjectsDir(), prefix, ctx.now().AddDate(0, 0, -days))
	w := ctx.Stdout
	fmt.Fprintf(w, "Worker sessions in %s's worktrees, last %d days: %d · first-hour tokens median %d\n", ctx.Project.Slug, days, rep.Sessions, rep.Median())
	if len(rep.Pages) == 0 {
		fmt.Fprintln(w, "No docs page read.")
		return nil
	}
	fmt.Fprintln(w, "\nDocs pages read, by sessions:")
	for i, r := range rep.Pages {
		if i == 15 {
			break
		}
		fmt.Fprintf(w, "  %4d sessions %5d reads  docs/%s\n", r.Sessions, r.Reads, r.Rel)
	}
	return nil
}

// refElsewhere adds to a missed section the other pages whose heading holds
// the words ("ref testing catalog" names "ref cli catalog", the
// workflow catalog-check section), up to three, so the guess lands on the
// real page. err comes back unchanged when no other page has one.
func refElsewhere(dir, rel string, words []string, err error) error {
	var hits []string
	for _, other := range docref.Pages(dir) {
		if other == rel {
			continue
		}
		p, lerr := docref.Load(dir, other)
		if lerr != nil {
			continue
		}
		sec, serr := p.Section(words)
		if serr != nil {
			continue
		}
		heading := ""
		if _, after, ok := strings.Cut(sec, "\n\n"); ok {
			heading, _, _ = strings.Cut(after, "\n")
		}
		hits = append(hits, fmt.Sprintf("  issue-cli ref %s %s    %s", docref.Key(other), strings.Join(words, " "), strings.TrimSpace(heading)))
		if len(hits) == 3 {
			break
		}
	}
	if len(hits) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\nOn other pages:\n%s", err, strings.Join(hits, "\n"))
}
