package main

import (
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const outputAddUsage = `usage: issue-cli output add --title "..." --kind <kind> (--url <link> | --file <file> | --path <repo path>) [--issue <slug>] [--department <name>] [--job <name>] [--note "..."]`

var outputCommand = &Command{
	Name:      "output",
	ShortHelp: "Record what a department made, and its review (output add|review|publish|show|revise|comments|reply)",
	LongHelp: `Put an output on the department's shelf: a post, design, page, site,
report or invoice an issue or a job made. It goes into
<issues>/.outputs/outputs.jsonl in review; the owner approves or rejects
it, and only an approved output is published.

  output add --title "..." --kind <kind> (--url <link> | --file <file> | --path <repo path>)
             [--issue <slug>] [--department <name>] [--job <name>] [--note "..."]
      --file copies the file onto the shelf (<issues>/.outputs/files/, up to
      25 MB); --path links a file in the repo; --url an http(s) link. The
      department is the issue's, else --department, else Product.
  output review <id> approve|reject --quote "<the owner's words>" [--how "..."]
      The owner's stamp, recorded by the owner or the lead; never a worker.
  output publish <id> [--url <where it went out>]
      Only once approved.
  output use <id> --by <department|issue>
      Another department (or one of its issues) uses the output: drawn as
      an output line between the two rooms on the company page.
  output show <id>
  output revise <id> --file <new version> [--note "..."]
      A new version of a file output (v2, v3…): copied onto the shelf next
      to the earlier ones, back in review. The reader shows what changed.
  output comments <id> [--all]
      The owner's comments on the output (from its reader page on the
      shelf): open threads, where each sits in the newest version, the
      quoted passage and the messages. --all adds resolved ones.
  output reply <id> <thread> --text "..."
      Answer a thread (say what the new version changed). Only the owner
      comments and resolves, from the page.

<id> is the full id (o-20261030-a1b2) or its last part (a1b2). Kinds with
an icon: post, design, site, page, report, invoice; any word works.

Examples:
  issue-cli output add --issue seo/acme-weekly --title "Acme: FAQ refresh" --kind page --url https://acme.example/faq
  issue-cli output add --department marketing --job weekly-pass --title "Hero banner" --kind design --file out/hero.png
  issue-cli output review a1b2 approve --quote "ok, ship it"
  issue-cli output publish a1b2 --url https://acme.example/faq
  issue-cli output use a1b2 --by sales
  issue-cli output comments ab00
  issue-cli output revise ab00 --file brand.md --note "answers the owner's comments"
  issue-cli output reply ab00 c-1a2b --text "v2 makes B the line"`,
	Run:         runOutput,
	Subcommands: []string{"add", "review", "publish", "use", "show", "revise", "comments", "reply"},
}

var outputsCommand = &Command{
	Name:      "outputs",
	ShortHelp: "List the departments' outputs shelf",
	LongHelp: `Print what each department made, newest first, grouped by department:
kind, title, where it is, the issue or job, who, when and its state
(review, approved, rejected, published).

Pass a department (marketing, product) to list only its shelf.

Examples:
  issue-cli outputs
  issue-cli outputs marketing --state review
  issue-cli outputs --issue seo/acme-weekly --json`,
	Run: runOutputs,
}

func init() {
	registerCommand(outputCommand)
	registerCommand(outputsCommand)
}

func runOutput(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: "usage: issue-cli output add|review|publish|use|show|revise|comments|reply ... (issue-cli help output)"}
	}
	switch args[0] {
	case "add":
		return runOutputAdd(ctx, args[1:])
	case "review":
		return runOutputReview(ctx, args[1:])
	case "publish":
		return runOutputPublish(ctx, args[1:])
	case "use":
		return runOutputUse(ctx, args[1:])
	case "show":
		return runOutputShow(ctx, args[1:])
	case "revise":
		return runOutputRevise(ctx, args[1:])
	case "comments":
		return runOutputComments(ctx, args[1:])
	case "reply":
		return runOutputReply(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown output command %q: add, review, publish, use, show, revise, comments or reply", args[0])}
}

func runOutputAdd(ctx *Context, args []string) error {
	fs := newFlagSet("output add", ctx)
	title := fs.String("title", "", "what the output is (required)")
	kind := fs.String("kind", "", "post, design, site, page, report, invoice or any word (required)")
	link := fs.String("url", "", "an http(s) link to the output")
	file := fs.String("file", "", "a file to copy onto the shelf (up to 25 MB)")
	path := fs.String("path", "", "a file in the repo, relative to the project root")
	slug := fs.String("issue", "", "the issue that made it")
	dept := fs.String("department", "", "the department, when there is no --issue (default Product)")
	job := fs.String("job", "", "the job that made it, when there is no issue")
	note := fs.String("note", "", "a line about it")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if *title == "" || *kind == "" {
		return &usageError{msg: outputAddUsage}
	}
	p := ctx.Project
	issues, _ := tracker.LoadIssues(p.IssueDir)
	in := outputs.AddInput{
		Title: normalizeEscapedText(*title), Kind: *kind, URL: *link, File: *file, Path: *path,
		Job: *job, Note: normalizeEscapedText(*note), By: whoAmI(p, issues).sender(),
	}
	var issue *tracker.Issue
	if *slug != "" {
		var err error
		if issue, _, err = findIssueOrErr(ctx, *slug); err != nil {
			return err
		}
		in.Issue = issue.Slug
	}
	name, err := outputDepartment(p, issue, *dept)
	if err != nil {
		return err
	}
	in.Department = outputKey(name)
	o, err := outputs.Add(p, in, ctx.now())
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, o)
	}
	fmt.Fprintf(ctx.Stdout, "✓ On %s's shelf: %s (%s)\n", o.Name, o.ID, o.Title)
	printOutput(ctx.Stdout, o, "  ")
	fmt.Fprintf(ctx.Stdout, "Shelf: %s\n", shelfURL(p, o.Department))
	fmt.Fprintf(ctx.Stdout, "It waits for the owner's review. Once approved: issue-cli output publish %s --url <where it went out>\n", shortID(o.ID))
	fmt.Fprintf(ctx.Stdout, "When another department uses it: issue-cli output use %s --by <department|issue>\n", shortID(o.ID))
	fmt.Fprintf(ctx.Stdout, "file: %s\n", outputs.LogPath(p))
	return nil
}

// outputDepartment is the department an output is recorded under: the
// issue's (a --department that disagrees is refused), else --department,
// else Product (""). The name is as configured.
func outputDepartment(p *tracker.Project, issue *tracker.Issue, flag string) (string, error) {
	named, err := departmentFlag(p, flag)
	if err != nil {
		return "", err
	}
	if issue == nil {
		return named, nil
	}
	own := p.DepartmentOf(issue)
	if strings.TrimSpace(flag) != "" && own != named {
		return "", fmt.Errorf("--department %s, but %s is %s's issue: drop --department or record it without --issue", deptLabel(named), issue.Slug, deptLabel(own))
	}
	return own, nil
}

func deptLabel(name string) string {
	if name == "" {
		return tracker.ProductName
	}
	return name
}

// outputKey is a department name's key in the log; "" is Product.
func outputKey(name string) string {
	if name == "" {
		return goals.ProductKey
	}
	return outputs.DeptKey(name)
}

func runOutputReview(ctx *Context, args []string) error {
	fs := newFlagSet("output review", ctx)
	quote := fs.String("quote", "", "the owner's words (required)")
	how := fs.String("how", "", "how the words reached you (default: \"in the CEO chat\")")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	usage := &usageError{msg: `usage: issue-cli output review <id> approve|reject --quote "<the owner's words>"`}
	if fs.NArg() != 2 {
		return usage
	}
	state := map[string]string{"approve": outputs.StateApproved, "reject": outputs.StateRejected}[fs.Arg(1)]
	if state == "" {
		return usage
	}
	q := strings.TrimSpace(normalizeEscapedText(*quote))
	if q == "" {
		return fmt.Errorf("output review needs --quote, the owner's words\n\nExample:\n  issue-cli output review %s %s --quote \"ok, ship it\"", fs.Arg(0), fs.Arg(1))
	}
	p := ctx.Project
	issues, _ := tracker.LoadIssues(p.IssueDir)
	me := whoAmI(p, issues)
	if me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers never review outputs: the owner approves or rejects on the shelf (%s), or tells the CEO", shelfURL(p, ""))
	}
	h := strings.TrimSpace(*how)
	if h == "" {
		h = "in the CEO chat"
	}
	o, err := outputs.Review(p, fs.Arg(0), state, q, h, me.sender(), ctx.now())
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, o)
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s %s: %s (%q, %s)\n", strings.ToUpper(state[:1])+state[1:], o.ID, o.Title, q, h)
	printOutput(ctx.Stdout, o, "  ")
	return nil
}

func runOutputPublish(ctx *Context, args []string) error {
	fs := newFlagSet("output publish", ctx)
	link := fs.String("url", "", "where it went out (http/https)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "usage: issue-cli output publish <id> [--url <where it went out>]"}
	}
	p := ctx.Project
	issues, _ := tracker.LoadIssues(p.IssueDir)
	o, err := outputs.Publish(p, fs.Arg(0), *link, whoAmI(p, issues).sender(), ctx.now())
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, o)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Published %s: %s\n", o.ID, o.Title)
	printOutput(ctx.Stdout, o, "  ")
	return nil
}

const outputUseUsage = "usage: issue-cli output use <id> --by <department|issue>"

// runOutputUse records that a department, or one of its issues, uses an
// output: the company page draws it from the maker's room to theirs.
func runOutputUse(ctx *Context, args []string) error {
	fs := newFlagSet("output use", ctx)
	by := fs.String("by", "", "the department (marketing, product, hq) or issue slug that uses it")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*by) == "" {
		return &usageError{msg: outputUseUsage}
	}
	p := ctx.Project
	issues, _ := tracker.LoadIssues(p.IssueDir)
	forWhat, to := "", ""
	if box := connections.ResolveBox(p, *by); box != "" {
		forWhat, to = box, box
	} else if issue := tracker.FindIssue(issues, strings.TrimSpace(*by)); issue != nil {
		forWhat, to = issue.Slug, connections.BoxOf(p, issue)
	} else {
		return fmt.Errorf("output use --by %q: no department or issue by that name (departments: %s)", *by, strings.Join(connections.Boxes(p), ", "))
	}
	list, _ := outputs.Load(p)
	if o, err := outputs.Find(list, fs.Arg(0)); err == nil && o.Department == to {
		return fmt.Errorf("output use: %s is %s's own output; a use is by another department", shortID(o.ID), o.Name)
	}
	o, err := outputs.MarkUsed(p, fs.Arg(0), forWhat, whoAmI(p, issues).sender(), ctx.now())
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, o)
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s uses %s: %s\n", connections.BoxName(p, to), shortID(o.ID), o.Title)
	fmt.Fprintf(ctx.Stdout, "Drawn as an output %s → %s on the company page (issue-cli connections).\n", o.Name, connections.BoxName(p, to))
	if connections.DeclaresKind(p, connections.Output) && !connections.IsDeclared(p, o.Department, to, connections.Output) {
		fmt.Fprintf(ctx.Stdout, "No declared feed %s → %s (projects.yaml connections: feeds:): the line is marked not declared until the owner adds - {from: %s, to: %s}.\n", o.Name, connections.BoxName(p, to), o.Department, to)
	}
	fmt.Fprintf(ctx.Stdout, "file: %s\n", outputs.LogPath(p))
	return nil
}

func runOutputShow(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "output show", args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return &usageError{msg: "usage: issue-cli output show <id>"}
	}
	list, _ := outputs.Load(ctx.Project)
	o, err := outputs.Find(list, pos[0])
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, o)
	}
	fmt.Fprintf(ctx.Stdout, "== %s ==\n", o.Title)
	printOutput(ctx.Stdout, o, "")
	if o.Note != "" {
		fmt.Fprintf(ctx.Stdout, "Note: %s\n", o.Note)
	}
	if !o.Reviewed.IsZero() {
		fmt.Fprintf(ctx.Stdout, "Reviewed: %s by %s, %q\n", o.Reviewed.Local().Format("2006-01-02 15:04"), o.ReviewedBy, o.Quote)
	}
	if !o.Published.IsZero() {
		fmt.Fprintf(ctx.Stdout, "Published: %s\n", o.Published.Local().Format("2006-01-02 15:04"))
	}
	for _, u := range o.Uses {
		fmt.Fprintf(ctx.Stdout, "Used by: %s, %s%s\n", u.For, u.Date.Local().Format("2006-01-02 15:04"), byWho(u.By))
	}
	if line := commentsLine(ctx.Project, o); line != "" {
		fmt.Fprintln(ctx.Stdout, line)
	}
	fmt.Fprintf(ctx.Stdout, "Shelf: %s\n", shelfURL(ctx.Project, o.Department))
	return nil
}

func runOutputs(ctx *Context, args []string) error {
	fs := newFlagSet("outputs", ctx)
	state := fs.String("state", "", "only outputs in this state: review, approved, rejected, published")
	slug := fs.String("issue", "", "only the outputs this issue made")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return unexpectedArgError(fs, fs.Arg(1))
	}
	p := ctx.Project
	f := outputs.Filter{State: strings.ToLower(strings.TrimSpace(*state))}
	if f.State != "" && !slices.Contains(outputs.States, f.State) {
		return fmt.Errorf("--state %q: one of %s", *state, strings.Join(outputs.States, ", "))
	}
	if fs.NArg() == 1 {
		name, err := departmentFlag(p, fs.Arg(0))
		if err != nil {
			return err
		}
		f.Department = outputKey(name)
	}
	if *slug != "" {
		issue, _, err := findIssueOrErr(ctx, *slug)
		if err != nil {
			return err
		}
		f.Issue = issue.Slug
	}
	all, _ := outputs.Load(p)
	list := outputs.Select(all, f)
	if ctx.JSONOutput {
		if list == nil {
			list = []*outputs.Output{}
		}
		return writeJSON(ctx.Stdout, list)
	}
	if len(all) == 0 {
		fmt.Fprintln(ctx.Stdout, "The shelf is empty. Record what an issue or job made:\n  "+outputAddExample("<slug>"))
		return nil
	}
	if len(list) == 0 {
		fmt.Fprintln(ctx.Stdout, "No outputs match.")
		return nil
	}
	// One block per department, in projects.yaml order (Product first),
	// then any department no longer configured.
	counts := outputs.Counts(list, ctx.now())
	order := outputs.Keys(p)
	for _, o := range list {
		if !slices.Contains(order, o.Department) {
			order = append(order, o.Department)
		}
	}
	first := true
	for _, dept := range order {
		mine := outputs.Select(list, outputs.Filter{Department: dept})
		if len(mine) == 0 {
			continue
		}
		if !first {
			fmt.Fprintln(ctx.Stdout)
		}
		first = false
		c := counts[dept]
		fmt.Fprintf(ctx.Stdout, "== %s · %d to review · %d this week ==\n", mine[0].Name, c.ToReview, c.Week)
		for _, o := range mine {
			printOutput(ctx.Stdout, o, "  ")
		}
	}
	return nil
}

// printOutput is an output on two lines: id, state, kind and title; then
// where it is, what made it, who and when.
func printOutput(w io.Writer, o *outputs.Output, indent string) {
	ver := ""
	if o.Version() > 1 {
		ver = fmt.Sprintf(" v%d", o.Version())
	}
	fmt.Fprintf(w, "%s%s %s%s [%s] %s · %s\n", indent, o.Icon(), shortID(o.ID), ver, o.State, o.Kind, o.Title)
	where := o.Where()
	if o.Missing {
		where += " (missing)"
	}
	parts := []string{where}
	if n := o.OpenComments; n > 0 {
		parts = append(parts, fmt.Sprintf("💬 %d open (issue-cli output comments %s)", n, shortID(o.ID)))
	}
	switch {
	case o.Issue != "":
		parts = append(parts, "issue "+o.Issue)
	case o.Job != "":
		parts = append(parts, "job "+o.Job)
	}
	if o.By != "" {
		parts = append(parts, "by "+o.By)
	}
	parts = append(parts, o.Added.Local().Format("2006-01-02 15:04"))
	if n := len(o.Uses); n > 0 {
		var who []string
		for _, u := range o.Uses {
			if !slices.Contains(who, u.For) {
				who = append(who, u.For)
			}
		}
		parts = append(parts, "used by "+strings.Join(who, ", "))
	}
	fmt.Fprintf(w, "%s    %s\n", indent, strings.Join(parts, " · "))
}

// shortID is the id's last part (a1b2), what the commands accept.
func shortID(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// shelfURL is the viewer's shelf page, filtered to a department key.
func shelfURL(p *tracker.Project, dept string) string {
	u := fmt.Sprintf("%s/p/%s/shelf", viewerBaseURL(p), p.Slug)
	if dept != "" {
		u += "?dept=" + url.QueryEscape(dept)
	}
	return u
}

func outputAddExample(slug string) string {
	return fmt.Sprintf("issue-cli output add --issue %s --title \"...\" --kind <post|design|page|report|...> --url <link> (or --file/--path)", slug)
}

// outputsLine is the line start and show print about the issue's outputs:
// their counts, or for an issue of a department (or on a content
// workflow) that made none yet, how to record one. "" otherwise.
func outputsLine(p *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig) string {
	list, _ := outputs.Load(p)
	mine := outputs.Select(list, outputs.Filter{Issue: issue.Slug})
	if len(mine) > 0 {
		review := len(outputs.Select(mine, outputs.Filter{State: outputs.StateReview}))
		line := fmt.Sprintf("Outputs: %d (%d to review) · issue-cli outputs --issue %s", len(mine), review, issue.Slug)
		for _, o := range mine {
			if n := o.OpenComments; n > 0 {
				line += fmt.Sprintf("\n  💬 %d open %s from the owner on %s: issue-cli output comments %s", n, pluralWord(n, "comment", "comments"), shortID(o.ID), shortID(o.ID))
			}
		}
		return line
	}
	if !wantsOutputs(p, issue, wf) {
		return ""
	}
	return "Outputs: none yet · record what this makes: " + outputAddExample(issue.Slug)
}

// wantsOutputs: the issue belongs to a department other than Product,
// walks the content workflow, or its path gates on has_output (the report
// type), so what it makes belongs on a shelf.
func wantsOutputs(p *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig) bool {
	if p.DepartmentOf(issue) != "" {
		return true
	}
	if _, _, ok := wf.HasOutputRule(); ok {
		return true
	}
	return wf != nil && strings.HasPrefix(wf.Template, "content@")
}

// outputsTransitionHint is the hint a transition into owner review,
// publish or done prints for an issue that should have outputs and has
// none; "" otherwise. A hint, not a gate.
func outputsTransitionHint(p *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig, to string) string {
	switch strings.ToLower(strings.TrimSpace(to)) {
	case "owner review", "publish", "done":
	default:
		return ""
	}
	if !wantsOutputs(p, issue, wf) {
		return ""
	}
	list, _ := outputs.Load(p)
	if len(outputs.Select(list, outputs.Filter{Issue: issue.Slug})) > 0 {
		return ""
	}
	return "Made something? Put it on the shelf: " + outputAddExample(issue.Slug)
}

// byWho is " (by x)" or "".
func byWho(by string) string {
	if by == "" {
		return ""
	}
	return " (by " + by + ")"
}
