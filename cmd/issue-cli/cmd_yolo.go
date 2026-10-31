package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

var yoloCommand = &Command{
	Name:      "yolo",
	ShortHelp: "YOLO mode: gates pass without the owner for a window, reviewed after the fact (sub: status|on|extend|off|queue|review)",
	LongHelp: `YOLO mode: for a window the owner chose, require_human_approval gates on the
issue's forward path pass without the owner. Each pass writes
  approval: "YOLO" (yolo · <agent> · from <status>, <date>) → <status>
on the issue and waits in the "after the fact" queue for the owner's review.
CI, tests, review, retros and the agents' permission prompts stay on. YOLO
never passes backwards steps, optional statuses (parked), Human Testing
approval itself, an experiment's gates, a gate marked owner_only (it spends
money or publishes outside the machine), or release publish (unless
unlocked with --allow-releases).
Safety valve: CI red for more than 30 min turns YOLO off by itself.

A box only the owner can check (a playtest judgement step, a guided check
step left for the owner, a criterion marked "(owner)") doesn't block the
proof gate under YOLO and is never ticked for them: it stays unticked and
waits in the queue as "AC6: owner check, not done" until it is ticked or
reviewed.

Subcommands:
  status
      On or off, until when, passes so far, valves, the gates it never
      passes, queue size.
  on --until 08:00|4h|morning|off --quote "<owner's words>" [--how ...]
     [--no-ci-valve] [--allow-releases]
      Turn YOLO on. Always with an end; "off" means until turned off and must
      be spelled out. Only the owner or the project lead (with the owner's
      words); never a worker.
  extend --until ...
      Move the end of the window that is on.
  off [--reason "..."]
      Turn YOLO off now.
  queue
      Gates passed in YOLO, and owner checks, the owner has not reviewed yet.
  review <slug> --agree --quote "<owner's words>" [--status <status>|<box>]
      Record the owner's agreement with a YOLO pass or owner check. "Back to <status>" is
      the owner's, in the viewer.

See docs/Workflow/yolo.md.`,
	Run:         runYOLO,
	Subcommands: []string{"status", "on", "extend", "off", "queue", "review"},
}

func init() {
	registerCommand(yoloCommand)
}

func runYOLO(ctx *Context, args []string) error {
	if ctx.Project == nil {
		return fmt.Errorf("yolo needs a project — pass --project <slug> or run from a project root")
	}
	if len(args) == 0 {
		return runYOLOStatus(ctx, nil)
	}
	switch args[0] {
	case "status":
		return runYOLOStatus(ctx, args[1:])
	case "on":
		return runYOLOOn(ctx, args[1:])
	case "extend":
		return runYOLOExtend(ctx, args[1:])
	case "off":
		return runYOLOOff(ctx, args[1:])
	case "queue":
		return runYOLOQueue(ctx, args[1:])
	case "review":
		return runYOLOReview(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown yolo subcommand %q (status, on, extend, off, queue, review)", args[0])}
}

// yoloNotWorker refuses a worker: switching YOLO and reviewing passes are
// the owner's, relayed by the lead.
func yoloNotWorker(ctx *Context, what string) error {
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	if me := whoAmI(ctx.Project, issues); me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers never %s: YOLO is the owner's switch. Ask the CEO on your issue trail with a question: comment", what)
	}
	return nil
}

type yoloStatusJSON struct {
	On        bool        `json:"on"`
	Since     time.Time   `json:"since,omitempty"`
	Until     string      `json:"until,omitempty"`
	Valves    yolo.Valves `json:"valves"`
	Passed    int         `json:"passed"`
	Queue     []yolo.Pass `json:"queue"`
	EndReason string      `json:"end_reason,omitempty"`
	// NeverPasses is the gates YOLO never passes (yolo.OwnerGates).
	NeverPasses []yolo.OwnerGate `json:"never_passes"`
}

func runYOLOStatus(ctx *Context, args []string) error {
	fs := newFlagSet("yolo status", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	now := ctx.now()
	st, on, err := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, now)
	if err != nil {
		return err
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	queue := yolo.Queue(issues)
	out := yoloStatusJSON{On: on, Valves: st.Valves, Queue: queue, EndReason: st.EndReason,
		NeverPasses: yolo.OwnerGates(ctx.Project, st.Valves)}
	if queue == nil {
		out.Queue = []yolo.Pass{}
	}
	if on {
		out.Since, out.Until = st.Since, st.EndLabel(now)
		out.Passed = yoloPassedSince(issues, st.Since)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	w := ctx.Stdout
	if !on {
		fmt.Fprintln(w, "YOLO: off")
		if st.EndReason != "" {
			fmt.Fprintf(w, "  last window: %s → %s (%s)\n", st.Since.Format("Mon 15:04"), st.Ended.Format("Mon 15:04"), st.EndReason)
		}
	} else {
		fmt.Fprintf(w, "YOLO: on %s · since %s · passed: %d\n", yoloEndPhrase(st, now), st.Since.Format("15:04"), out.Passed)
		fmt.Fprintf(w, "  turned on by %s (%s): %q\n", st.By, st.How, st.Words)
		fmt.Fprintf(w, "  valves: %s\n", yoloValvesLine(st.Valves))
	}
	printYOLONeverPasses(w, out.NeverPasses)
	fmt.Fprintf(w, "To review after the fact: %d\n", len(queue))
	if len(queue) > 0 {
		fmt.Fprintln(w, "  "+tracker.InjectProject(ctx.Project, "issue-cli yolo queue"))
	}
	return nil
}

// yoloPassedSince counts YOLO passes written since the window opened.
func yoloPassedSince(issues []*tracker.Issue, since time.Time) int {
	n := 0
	for _, issue := range issues {
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil {
			continue
		}
		for _, p := range yolo.Passes(issue, comments) {
			if at, ok := tracker.CommentTime(p.At); ok && !at.Before(since) {
				n++
			} else if !ok && tracker.CommentDay(p.At) >= since.Format("2006-01-02") {
				n++
			}
		}
	}
	return n
}

func yoloValvesLine(v yolo.Valves) string {
	ciValve := "no CI valve"
	if v.CIRedMinutes > 0 {
		ciValve = fmt.Sprintf("off when CI is red > %d min", v.CIRedMinutes)
	}
	releases := "no releases"
	if v.Releases {
		releases = "releases unlocked"
	}
	return ciValve + " · " + releases
}

func runYOLOOn(ctx *Context, args []string) error {
	fs := newFlagSet("yolo on", ctx)
	untilFlag := fs.String("until", "", "end: 08:00, 4h, morning, or off (until turned off)")
	quoteFlag := fs.String("quote", "", "the owner's words turning YOLO on (required)")
	howFlag := fs.String("how", "", "how the words reached you (default: \"in the CEO chat\")")
	noCI := fs.Bool("no-ci-valve", false, "don't turn off when CI is red > 30 min")
	allowReleases := fs.Bool("allow-releases", false, "let release publish run while YOLO is on")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if err := yoloNotWorker(ctx, "turn YOLO on"); err != nil {
		return err
	}
	now := ctx.now()
	until, forever, err := yolo.ParseUntil(*untilFlag, now)
	if err != nil {
		return &usageError{msg: err.Error() + "\n\nExample:\n  issue-cli yolo on --until 08:00 --quote \"go yolo till morning\""}
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if quote == "" {
		return &usageError{msg: "yolo on needs --quote: the owner's words turning it on\n\nExample:\n  issue-cli yolo on --until 08:00 --quote \"go yolo till morning\""}
	}
	how := strings.TrimSpace(*howFlag)
	if how == "" {
		how = "in the CEO chat"
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	by := whoAmI(ctx.Project, issues).Name
	if by == "" {
		by = "owner"
	}
	valves := yolo.Valves{CIRedMinutes: yolo.DefaultCIRedMinutes, Releases: *allowReleases}
	wf := ctx.Project.LoadWorkflow()
	if *noCI || !wf.CI.Enabled() {
		valves.CIRedMinutes = 0
	}
	root := yoloRoot(ctx.Project)
	if _, on, _ := yolo.Active(root, ctx.Project.Slug, now); on {
		return fmt.Errorf("YOLO is already on. Move its end instead:\n  %s", tracker.InjectProject(ctx.Project, "issue-cli yolo extend --until <08:00|4h|off>"))
	}
	st := yolo.State{Until: until, Forever: forever, By: by, Words: quote, How: how, Valves: valves}
	if err := yolo.TurnOn(root, ctx.Project.Slug, st, now); err != nil {
		return err
	}
	st.Since = now
	fmt.Fprintf(ctx.Stdout, "✓ YOLO on %s: gates pass without the owner and wait for review after the fact\n", yoloEndPhrase(&st, now))
	fmt.Fprintf(ctx.Stdout, "  valves: %s\n", yoloValvesLine(valves))
	if !wf.CI.Enabled() {
		fmt.Fprintln(ctx.Stdout, "  no local CI: the CI valve is off (docs/workflow.md, \"Local CI\")")
	}
	for _, g := range yolo.Gates(wf) {
		fmt.Fprintf(ctx.Stdout, "  passes: %s → %s\n", g.From, g.To)
	}
	printYOLONeverPasses(ctx.Stdout, yolo.OwnerGates(ctx.Project, valves))
	return nil
}

// printYOLONeverPasses lists the gates YOLO never passes: they wait for the
// owner with YOLO on (docs/Workflow/yolo.md, "Gates YOLO never passes").
func printYOLONeverPasses(w io.Writer, gates []yolo.OwnerGate) {
	fmt.Fprintln(w, "Never passes (the owner's, even in YOLO):")
	for _, g := range gates {
		if g.From == "" {
			fmt.Fprintf(w, "  - %s\n", g.Why)
			continue
		}
		fmt.Fprintf(w, "  - %s: %s\n", g.Label(), g.Why)
	}
}

func runYOLOExtend(ctx *Context, args []string) error {
	fs := newFlagSet("yolo extend", ctx)
	untilFlag := fs.String("until", "", "new end: 08:00, 4h, morning, or off (until turned off)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if err := yoloNotWorker(ctx, "extend YOLO"); err != nil {
		return err
	}
	now := ctx.now()
	until, forever, err := yolo.ParseUntil(*untilFlag, now)
	if err != nil {
		return &usageError{msg: err.Error()}
	}
	st, err := yolo.Extend(yoloRoot(ctx.Project), ctx.Project.Slug, until, forever, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ YOLO on %s\n", yoloEndPhrase(st, now))
	return nil
}

func runYOLOOff(ctx *Context, args []string) error {
	fs := newFlagSet("yolo off", ctx)
	reasonFlag := fs.String("reason", "", "why (default: \"turned off\")")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	now := ctx.now()
	root := yoloRoot(ctx.Project)
	st, on, err := yolo.Active(root, ctx.Project.Slug, now)
	if err != nil {
		return err
	}
	if !on {
		fmt.Fprintln(ctx.Stdout, "YOLO is already off")
		return nil
	}
	reason := strings.TrimSpace(*reasonFlag)
	if reason == "" {
		reason = "turned off"
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	if me := whoAmI(ctx.Project, issues); me.Name != "" {
		reason += " by " + me.Name
	}
	if err := yolo.TurnOff(root, ctx.Project.Slug, st, now, reason); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ YOLO off (%s). Gates wait for the owner again.\n", reason)
	return nil
}

func runYOLOQueue(ctx *Context, args []string) error {
	fs := newFlagSet("yolo queue", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	queue := yolo.Queue(issues)
	if ctx.JSONOutput {
		if queue == nil {
			queue = []yolo.Pass{}
		}
		return writeJSON(ctx.Stdout, queue)
	}
	if len(queue) == 0 {
		fmt.Fprintln(ctx.Stdout, "Nothing to review after the fact.")
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== To review after YOLO (%d) ==\n", len(queue))
	for _, p := range queue {
		from := p.From
		if from == "" {
			from = "?"
		}
		if p.Check != "" {
			fmt.Fprintf(ctx.Stdout, "  %s  %s (%s) %q  from %s  %s\n", p.Slug, p.Label(), p.Why, p.What, from, tracker.CommentDay(p.At))
			continue
		}
		fmt.Fprintf(ctx.Stdout, "  %s  %s → %s  by %s  %s\n", p.Slug, from, p.Status, p.By, tracker.CommentDay(p.At))
	}
	fmt.Fprintln(ctx.Stdout, "The owner reviews each on the issue page (Agree / Back to <status>), or:")
	fmt.Fprintln(ctx.Stdout, "  "+tracker.InjectProject(ctx.Project, "issue-cli yolo review <slug> --agree --quote \"<owner's words>\""))
	return nil
}

func runYOLOReview(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "yolo review")
	if err != nil {
		return err
	}
	fs := newFlagSet("yolo review", ctx)
	agree := fs.Bool("agree", false, "the owner agrees with the YOLO pass")
	quoteFlag := fs.String("quote", "", "the owner's words (required)")
	howFlag := fs.String("how", "", "how the words reached you (default: \"in the CEO chat\")")
	statusFlag := fs.String("status", "", "which pass, when the issue has several (the approved status, or an owner check's box: AC6)")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	if !*agree {
		return &usageError{msg: "yolo review records agreement: --agree --quote \"<owner's words>\". Sending an issue back is the owner's, from the issue page (Back to <status>)."}
	}
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if quote == "" {
		return &usageError{msg: "yolo review needs --quote: the owner's words"}
	}
	if err := yoloNotWorker(ctx, "review YOLO passes"); err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	pending := yolo.Unreviewed(issue, comments)
	if status := strings.ToLower(strings.TrimSpace(*statusFlag)); status != "" {
		var only []yolo.Pass
		for _, p := range pending {
			if strings.EqualFold(p.Key(), status) {
				only = append(only, p)
			}
		}
		pending = only
	}
	if len(pending) == 0 {
		return fmt.Errorf("%s has no YOLO pass waiting for review", issue.Slug)
	}
	how := strings.TrimSpace(*howFlag)
	if how == "" {
		how = "in the CEO chat"
	}
	date := ctx.now().Format("2006-01-02")
	for _, p := range pending {
		comment := tracker.YOLOReviewComment("agreed", quote, how, date, p.Key())
		if err := tracker.AddCommentBy(issue.FilePath, 0, comment, "cli", commentAuthor(ctx.Project, issue)); err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "✓ Agreed: %s → %s\n", issue.Slug, p.Label())
	}
	return nil
}
