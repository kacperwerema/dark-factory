package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clibug"
	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var reportBugCommand = &Command{
	Name:      "report-bug",
	ShortHelp: "Report a bug in issue-cli or the workflow engine",
	LongHelp: `Report a bug in issue-cli or the workflow engine itself (not in your
project: those go on your own board with 'issue-cli create').

The report becomes a cli-bug issue on the engine's board (the issue-viewer
project): engine_project: in projects.yaml, else the project whose repo is
kacperwerema/dark-factory. If an open cli-bug issue has the same title, or
its report quotes the same error (a quoted span of 3+ words that is not a
command), your report is added to it as a report: comment instead. The
output names the issue, so you can cite it.

When your build is older than the engine's main, report-bug searches main's
commits since your build for each error you quote (in backticks or quotes)
and lists the ones that touch it under "Maybe fixed since"; the issue gets
the maybe-fixed label. Try again on a current build before working around it.

The issue gets the first sentence as its title and the full text, your
project, issue slug, agent, issue-cli version and the last issue-cli command
as context. Pass --cmd "<command line>" to name the command that failed.

Without an engine project on this machine, the report is saved locally in
${XDG_DATA_HOME:-~/.local/share}/issue-cli/bug-reports/ and sent by the next
report-bug that finds one, or by 'issue-cli report-bug --send'. A queued
report that fails again stays queued; the others are still sent.

Examples:
  issue-cli report-bug "transition rejects a valid status name with a trailing space"
  issue-cli report-bug --cmd "issue-cli check foo D3" "check ticks the wrong box when ids repeat"
  issue-cli report-bug --send
  issue-cli report-bug --import ~/Work/bugs/*.json`,
	Run: runReportBug,
}

func init() {
	registerCommand(reportBugCommand)
}

// reportOutcome is what happened to one report.
type reportOutcome struct {
	clibug.Result
	// Queued is the queue file when the report was not sent.
	Queued string `json:"queued,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Source is the imported file (--import only).
	Source string `json:"source,omitempty"`
}

type reportBugOutput struct {
	*reportOutcome
	Imported      []reportOutcome `json:"imported,omitempty"`
	SentFromQueue []string        `json:"sent_from_queue,omitempty"`
	StillQueued   int             `json:"still_queued"`
	QueueReason   string          `json:"queue_reason,omitempty"`
}

func runReportBug(ctx *Context, args []string) error {
	fs := newFlagSet("report-bug", ctx)
	cmdFlag := fs.String("cmd", "", "the issue-cli command line that hit the bug (default: your last issue-cli call)")
	sendFlag := fs.Bool("send", false, "send the locally queued reports, nothing else")
	importFlag := fs.Bool("import", false, "turn old bugs/*.json reports (the files given) into issues")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	rest := fs.Args()
	switch {
	case *sendFlag && (*importFlag || len(rest) > 0):
		return fmt.Errorf("--send takes no description or files\n\nExample:\n  issue-cli report-bug --send")
	case *importFlag && len(rest) == 0:
		return fmt.Errorf("--import needs the report files\n\nExample:\n  issue-cli report-bug --import ~/Work/bugs/*.json")
	case !*sendFlag && len(rest) == 0:
		return fmt.Errorf("report-bug requires a description\n\nExample:\n  issue-cli report-bug \"transition command rejects valid status name with trailing space\"")
	}

	engine, why := findEngineProject(ctx)
	var sender clibug.Sender
	if engine != nil {
		sender = &boardSender{ctx: ctx, engine: engine}
	}

	out := reportBugOutput{}
	switch {
	case *importFlag:
		for _, path := range rest {
			o := importReport(ctx, sender, why, path)
			out.Imported = append(out.Imported, o)
		}
	case !*sendFlag:
		r := newReport(ctx, strings.Join(rest, " "), *cmdFlag)
		o := deliverReport(sender, why, r)
		out.reportOutcome = &o
	}
	if sender != nil {
		out.SentFromQueue, out.QueueReason = drainQueue(sender, out.reportOutcome)
	} else if *sendFlag {
		out.QueueReason = why
	}
	out.StillQueued = clibug.PendingCount()

	if ctx.JSONOutput {
		enc := json.NewEncoder(ctx.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	printReportBug(ctx, out, *sendFlag)
	return nil
}

// findEngineProject resolves the engine project from projects.yaml. Without
// one it returns why, for the "saved locally" line.
func findEngineProject(ctx *Context) (*tracker.Project, string) {
	if ctx.ConfigPath == "" {
		return nil, ctx.Config.notFound().Error()
	}
	cfg, err := tracker.LoadProjectsConfig(ctx.ConfigPath)
	if err != nil {
		return nil, fmt.Sprintf("cannot load %s (%s): %v", ctx.ConfigPath, ctx.Config.Why, err)
	}
	engine := clibug.FindEngine(cfg)
	if engine != nil {
		return engine, ""
	}
	if cfg.EngineProject != "" {
		return nil, fmt.Sprintf("engine_project %q is not a project in %s", cfg.EngineProject, ctx.ConfigPath)
	}
	return nil, fmt.Sprintf("no engine project in %s (set engine_project: <slug>, or a project with repo %s)", ctx.ConfigPath, clibug.EngineRepos[0])
}

// newReport fills a report with what this session knows about itself.
func newReport(ctx *Context, text, command string) clibug.Report {
	r := clibug.Report{
		At:        ctx.now(),
		Text:      strings.TrimSpace(text),
		Tool:      "issue-cli",
		IssueSlug: strings.TrimSpace(os.Getenv("ISSUE_VIEWER_ISSUE_SLUG")),
		Version:   Version,
		OS:        runtime.GOOS,
		Command:   strings.TrimSpace(command),
	}
	if ctx.Project != nil {
		r.Project = ctx.Project.Slug
		issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
		r.Agent = whoAmI(ctx.Project, issues).Name
		if r.Command == "" {
			r.Command = lastCommand(ctx, issues, r.IssueSlug)
		}
	}
	return r
}

// lastCommandWindow is how old the previous issue-cli call may be to count
// as the command the report is about.
const lastCommandWindow = 10 * time.Minute

// lastCommand is the agent's previous issue-cli call from its clilog, when
// recent: the call that most likely hit the bug.
func lastCommand(ctx *Context, issues []*tracker.Issue, slug string) string {
	path := strings.TrimSpace(os.Getenv("ISSUE_CLI_LOG"))
	if path == "" && slug != "" {
		if issue := tracker.FindIssue(issues, slug); issue != nil {
			path = agentClilogPath(ctx.Project, issue)
		}
	}
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var e struct {
			TS   time.Time `json:"ts"`
			Args []string  `json:"args"`
		}
		if json.Unmarshal([]byte(lines[i]), &e) != nil {
			continue
		}
		stripped := stripCLIGlobalFlags(e.Args)
		if len(stripped) > 0 && stripped[0] == "report-bug" {
			continue
		}
		if ctx.now().Sub(e.TS) > lastCommandWindow {
			return ""
		}
		return "issue-cli " + quoteArgs(e.Args)
	}
	return ""
}

func quoteArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n\"'") {
			a = fmt.Sprintf("%q", a)
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// deliverReport sends r, or queues it when there is no sender or the send
// fails.
func deliverReport(sender clibug.Sender, why string, r clibug.Report) reportOutcome {
	if sender != nil {
		var res clibug.Result
		err := clibug.WithLock(func() error {
			var err error
			res, err = sender.Send(r)
			return err
		})
		if err == nil {
			return reportOutcome{Result: res}
		}
		why = err.Error()
	}
	o := reportOutcome{Reason: why}
	path, err := clibug.Enqueue(r)
	if err != nil {
		o.Reason = fmt.Sprintf("%s; and queueing failed: %v", why, err)
		o.Result.Kind = "lost"
		return o
	}
	o.Result.Kind = "queued"
	o.Queued = path
	return o
}

// importReport files one old bugs/*.json report and moves the file into
// imported/ next to it, so a re-run does not report it twice.
func importReport(ctx *Context, sender clibug.Sender, why, path string) reportOutcome {
	r, err := clibug.ReadReport(path)
	if err != nil {
		return reportOutcome{Result: clibug.Result{Kind: "skipped"}, Reason: err.Error(), Source: path}
	}
	if r.Project == "" && r.IssueSlug != "" {
		r.Project = projectOfIssue(ctx, r.IssueSlug)
	}
	if r.Tool == "" {
		r.Tool = "issue-cli"
	}
	o := deliverReport(sender, why, r)
	o.Source = path
	if o.Kind == "lost" {
		return o
	}
	dir := filepath.Join(filepath.Dir(path), "imported")
	if err := os.MkdirAll(dir, 0755); err == nil {
		if err := os.Rename(path, filepath.Join(dir, filepath.Base(path))); err != nil {
			o.Reason = strings.TrimPrefix(o.Reason+"; ", "; ") + "not moved to imported/: " + err.Error()
		}
	}
	return o
}

// projectOfIssue finds which configured project has an issue with slug.
func projectOfIssue(ctx *Context, slug string) string {
	cfg, err := tracker.LoadProjectsConfig(ctx.ConfigPath)
	if err != nil {
		return ""
	}
	for _, p := range cfg.Projects {
		issues, err := tracker.LoadIssues(p.IssueDir)
		if err == nil && tracker.FindIssue(issues, slug) != nil {
			return p.Slug
		}
	}
	return ""
}

// drainQueue sends the queued reports, oldest first, except the one this
// call just queued. A report that fails stays queued and the rest are still
// tried: one report never holds back the others. why names the failures.
func drainQueue(sender clibug.Sender, current *reportOutcome) ([]string, string) {
	var sent, failed []string
	for _, q := range clibug.Pending() {
		if current != nil && current.Queued != "" && q.Path == current.Queued {
			continue
		}
		var res clibug.Result
		err := clibug.WithLock(func() error {
			var err error
			res, err = sender.Send(q.Report)
			return err
		})
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", filepath.Base(q.Path), err))
			continue
		}
		os.Remove(q.Path)
		sent = append(sent, res.Slug)
	}
	return sent, strings.Join(failed, "; ")
}

func printReportBug(ctx *Context, out reportBugOutput, send bool) {
	w := ctx.Stdout
	if out.reportOutcome != nil {
		printOutcome(ctx, *out.reportOutcome)
	}
	for _, o := range out.Imported {
		fmt.Fprintf(w, "%s:\n  ", o.Source)
		printOutcome(ctx, o)
	}
	if len(out.SentFromQueue) > 0 {
		fmt.Fprintf(w, "✓ Sent %d queued report(s): %s\n", len(out.SentFromQueue), strings.Join(out.SentFromQueue, ", "))
	}
	switch {
	case out.StillQueued > 0 && out.QueueReason != "":
		fmt.Fprintf(w, "%d report(s) still queued in %s: %s\n", out.StillQueued, clibug.QueueDir(), out.QueueReason)
	case send && out.StillQueued == 0 && len(out.SentFromQueue) == 0:
		fmt.Fprintf(w, "No queued reports in %s.\n", clibug.QueueDir())
	}
}

func printOutcome(ctx *Context, o reportOutcome) {
	w := ctx.Stdout
	switch o.Kind {
	case "created":
		fmt.Fprintf(w, "✓ Reported as new cli-bug issue %s on the %s board\n", o.Slug, o.Project)
		fmt.Fprintf(w, "file: %s\n", o.File)
		printFoundIn(w, o)
		printMaybeFixed(w, o)
	case "commented":
		how := ""
		if o.Matched == "error" {
			how = " (same quoted error)"
		}
		fmt.Fprintf(w, "✓ Already reported%s: added your report to cli-bug issue %s on the %s board (%d reports)\n", how, o.Slug, o.Project, o.Reports)
		fmt.Fprintf(w, "file: %s\n", o.File)
		printFoundIn(w, o)
		printMaybeFixed(w, o)
	case "queued":
		fmt.Fprintf(w, "saved locally, not sent: %s\n", o.Queued)
		// A multi-line why (the config lookup's) stays under "why:".
		why := strings.ReplaceAll(strings.ReplaceAll(o.Reason, "\n\n", "\n"), "\n", "\n  ")
		fmt.Fprintf(w, "  why: %s\n", why)
		fmt.Fprintln(w, "  It is sent by the next report-bug that finds the engine project, or run: issue-cli report-bug --send")
	case "skipped":
		fmt.Fprintf(w, "skipped: %s\n", o.Reason)
	default:
		fmt.Fprintf(ctx.Stderr, "report lost: %s\n", o.Reason)
	}
}

// printMaybeFixed lists the commits since the reporter's build that touch
// the quoted error: the bug may be gone on main.
func printMaybeFixed(w io.Writer, o reportOutcome) {
	if len(o.MaybeFixed) == 0 {
		return
	}
	fmt.Fprintf(w, "Maybe fixed since your build: %s has %d commit(s) since it that touch the quoted error (label %s):\n", o.Branch, len(o.MaybeFixed), clibug.MaybeFixedLabel)
	for _, c := range o.MaybeFixed {
		fmt.Fprintf(w, "  - %s %s\n", c.SHA, c.Subject)
	}
	fmt.Fprintln(w, "  Try again on a build of "+o.Branch+" (in the engine repo: go build -o ./issue-cli ./cmd/issue-cli) before working around it.")
}

// printFoundIn says where a report from a release candidate build went.
func printFoundIn(w io.Writer, o reportOutcome) {
	switch {
	case o.Milestone != "":
		fmt.Fprintf(w, "Added to milestone %s (found in release candidate %s): the release waits for its fix\n", o.Milestone, o.FoundIn)
	case o.FoundIn != "":
		fmt.Fprintf(w, "Filed against release candidate %s; the issue already has a version, so it stays there\n", o.FoundIn)
	}
}

// boardSender files reports on the engine project's board on this machine.
type boardSender struct {
	ctx    *Context
	engine *tracker.Project
}

func (b *boardSender) Send(r clibug.Report) (clibug.Result, error) {
	proj := b.engine
	if info, err := os.Stat(proj.IssueDir); err != nil || !info.IsDir() {
		return clibug.Result{}, fmt.Errorf("engine project %s has no issues directory %s", proj.Slug, proj.IssueDir)
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return clibug.Result{}, fmt.Errorf("reading the %s board: %w", proj.Slug, err)
	}
	closed := tracker.ClosedIssueFunc(proj)
	title := r.Title()
	key := clibug.NormalizeTitle(title)
	again := clibug.NormalizeTitle(title + " (again)")
	var closedMatch *tracker.Issue
	for _, issue := range issues {
		if !hasLabel(issue, clibug.Label) {
			continue
		}
		k := clibug.NormalizeTitle(issue.Title)
		if k != key && k != again {
			continue
		}
		if closed(issue) {
			if k == key {
				closedMatch = issue
			}
			continue
		}
		res, err := b.comment(issue, r)
		res.Matched = "title"
		return res, err
	}
	if quotes := clibug.QuotedErrors(r.Text); len(quotes) > 0 {
		for _, issue := range issues {
			if !hasLabel(issue, clibug.Label) || closed(issue) {
				continue
			}
			if clibug.SameError(quotes, clibug.QuotedErrors(clibug.ReportSection(issue.BodyRaw))) {
				res, err := b.comment(issue, r)
				res.Matched = "error"
				return res, err
			}
		}
	}
	if closedMatch != nil {
		title += " (again)"
	}
	title, inTheWay := freeTitle(proj, issues, title, r.Title())
	if inTheWay != nil {
		closedMatch = inTheWay
	}
	return b.create(title, r, closedMatch)
}

// freeTitle is title, or the next of base " (again)", " (again 2)", …
// when an issue file already sits at the slug create would give it: a
// closed match, or the bare issue an earlier report left. It returns the
// last issue in the way (nil when title was free), so the new issue names
// it.
func freeTitle(proj *tracker.Project, issues []*tracker.Issue, title, base string) (string, *tracker.Issue) {
	candidates := []string{base, base + " (again)"}
	for n := 2; n < 100; n++ {
		candidates = append(candidates, fmt.Sprintf("%s (again %d)", base, n))
	}
	from := 0
	for i, c := range candidates {
		if c == title {
			from = i
		}
	}
	var inTheWay *tracker.Issue
	for _, c := range candidates[from:] {
		_, _, file := newIssueFile(proj, "CLI", c)
		if _, err := os.Stat(file); err != nil {
			return c, inTheWay
		}
		inTheWay = issueAtFile(issues, file)
	}
	return title, inTheWay
}

// issueAtFile is the loaded issue whose file is path; a stand-in naming the
// file when it does not parse.
func issueAtFile(issues []*tracker.Issue, path string) *tracker.Issue {
	for _, issue := range issues {
		if sameFile(issue.FilePath, path) {
			return issue
		}
	}
	return &tracker.Issue{Slug: "cli/" + strings.TrimSuffix(filepath.Base(path), ".md"), FilePath: path}
}

func (b *boardSender) comment(issue *tracker.Issue, r clibug.Report) (clibug.Result, error) {
	fixed, branch := b.maybeFixed(r)
	text := "report: " + r.Text + "\n\n" + r.Context()
	if block := clibug.MaybeFixedBlock(r.Version, branch, fixed); block != "" {
		text += "\n" + block
	}
	if err := tracker.AddComment(issue.FilePath, 0, strings.TrimSpace(text), "issue-cli"); err != nil {
		return clibug.Result{}, err
	}
	var milestone string
	update := tracker.IssueUpdate{}
	changed := false
	if issue.Version == "" {
		if milestone = b.rcMilestone(r); milestone != "" {
			update.Version, changed = &milestone, true
		}
	}
	if len(fixed) > 0 && !hasLabel(issue, clibug.MaybeFixedLabel) {
		update.Labels, changed = append(append([]string{}, issue.Labels...), clibug.MaybeFixedLabel), true
	}
	if changed {
		if err := tracker.UpdateIssueFrontmatter(issue.FilePath, update); err != nil {
			return clibug.Result{}, err
		}
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	n := 1
	for _, c := range comments {
		if strings.HasPrefix(c.Text, "report:") {
			n++
		}
	}
	res := b.withMilestone(clibug.Result{Kind: "commented", Project: b.engine.Slug, Slug: issue.Slug, File: issue.FilePath, Reports: n}, r, milestone)
	res.MaybeFixed, res.Branch = fixed, branch
	return res, nil
}

// maybeFixed lists the engine's commits on its main branch (ci.branch when
// set) since the reporter's build that touch an error the report quotes,
// and names that branch.
func (b *boardSender) maybeFixed(r clibug.Report) ([]clibug.Commit, string) {
	branch := "main"
	if wf := b.engine.LoadWorkflow(); wf != nil && wf.CI.Enabled() && strings.TrimSpace(wf.CI.Branch) != "" {
		branch = strings.TrimSpace(wf.CI.Branch)
	}
	quotes := clibug.QuotedErrors(r.Text)
	if len(quotes) == 0 {
		return nil, branch
	}
	return clibug.FixedSince(b.engine.Root(), branch, r.Version, quotes), branch
}

// rcMilestone is the engine board's version: label for the release a
// release candidate build is a candidate for: a bug found in the soak
// belongs to that release, not the next one. "" for any other build.
func (b *boardSender) rcMilestone(r clibug.Report) string {
	rc, ok := milestone.ParseRC(r.Version)
	if !ok {
		return ""
	}
	issues, _ := tracker.LoadIssues(b.engine.IssueDir)
	var inUse []string
	for _, is := range issues {
		inUse = append(inUse, is.Version)
	}
	return milestone.IssueVersion(rc.Cut, inUse)
}

// withMilestone notes on res the milestone the report put the issue in and
// the candidate it was found in.
func (b *boardSender) withMilestone(res clibug.Result, r clibug.Report, version string) clibug.Result {
	if _, ok := milestone.ParseRC(r.Version); ok {
		res.Milestone, res.FoundIn = version, r.Version
	}
	return res
}

// create files the issue through `create` (as CI's fileRedIssue does), so
// the engine workflow's template applies, then adds the label, the reporter
// fields and the report.
func (b *boardSender) create(title string, r clibug.Report, previous *tracker.Issue) (clibug.Result, error) {
	proj := b.engine
	const system = "CLI"
	spec := newIssueSpec{Title: title, System: system}
	if wf := proj.LoadWorkflow(); wf.HasTypes() {
		if _, ok := wf.Types["bugfix"]; ok {
			spec.Type = "bugfix"
		}
	}
	var out bytes.Buffer
	sub := *b.ctx
	sub.Project, sub.Stdout, sub.JSONOutput = proj, &out, false
	made, err := writeNewIssue(&sub, spec)
	if err != nil {
		return clibug.Result{}, fmt.Errorf("creating the issue on the %s board: %w", proj.Slug, err)
	}
	// create may leave a private name out of the slug: find the issue by
	// the file it wrote, not by a slug worked out again from the title.
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	var issue *tracker.Issue
	for _, is := range issues {
		if sameFile(is.FilePath, made.File) {
			issue = is
			break
		}
	}
	if issue == nil {
		return clibug.Result{}, fmt.Errorf("created %s but cannot read it back", made.File)
	}

	var body strings.Builder
	body.WriteString("## Report\n\n")
	body.WriteString(r.Text)
	body.WriteString("\n\n")
	body.WriteString(r.Context())
	switch {
	case previous != nil && tracker.ClosedIssueFunc(proj)(previous):
		fmt.Fprintf(&body, "\nReported again after `%s` was closed.\n", previous.Slug)
	case previous != nil:
		fmt.Fprintf(&body, "\nFiled beside `%s`, which already had this report's slug.\n", previous.Slug)
	}
	fixed, branch := b.maybeFixed(r)
	if block := clibug.MaybeFixedBlock(r.Version, branch, fixed); block != "" {
		body.WriteString("\n" + block)
	}
	if rest := strings.TrimSpace(issue.BodyRaw); rest != "" {
		body.WriteString("\n" + rest + "\n")
	}
	newBody := strings.TrimSpace(body.String())
	labels := []string{clibug.Label}
	if len(fixed) > 0 {
		labels = append(labels, clibug.MaybeFixedLabel)
	}
	update := tracker.IssueUpdate{
		Labels:      labels,
		Body:        &newBody,
		ExtraFields: map[string]string{},
	}
	version := b.rcMilestone(r)
	if version != "" {
		update.Version = &version
	}
	if from := r.ReportedFrom(); from != "" {
		update.ExtraFields["reported_from"] = from
	}
	if r.Agent != "" {
		update.ExtraFields["reported_by"] = r.Agent
	}
	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, update); err != nil {
		return clibug.Result{}, err
	}
	res := b.withMilestone(clibug.Result{Kind: "created", Project: proj.Slug, Slug: issue.Slug, File: issue.FilePath, Reports: 1}, r, version)
	res.MaybeFixed, res.Branch = fixed, branch
	return res, nil
}

func hasLabel(issue *tracker.Issue, label string) bool {
	for _, l := range issue.Labels {
		if strings.EqualFold(l, label) {
			return true
		}
	}
	return false
}
