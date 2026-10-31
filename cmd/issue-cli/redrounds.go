package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/redrounds"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// redRoundsRun follows one transition's checks for the red-round cap: it
// refuses before anything runs while a cap question is open, hears each
// check's result through wf.CheckDone, records them and raises the question
// when a check reaches the cap.
type redRoundsRun struct {
	ctx     *Context
	wf      *tracker.WorkflowConfig
	issue   *tracker.Issue
	state   redrounds.State
	results []tracker.RoundResult
}

// redRoundsCIRoot is where local CI keeps its runs for the project, "" when
// CI is off or there is no project.
func redRoundsCIRoot(ctx *Context, wf *tracker.WorkflowConfig) string {
	if ctx.Project == nil || wf == nil || !wf.CI.Enabled() {
		return ""
	}
	return ciRoot(ctx.Project)
}

// loadRedRounds reads issue's red rounds; a read error leaves the cap out
// of the way (an unreadable sidecar is reported, never blocking by itself).
func loadRedRounds(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue) (redrounds.State, error) {
	return redrounds.Load(issue.FilePath, issue.Slug, redRoundsCIRoot(ctx, wf), redRoundsIssuesDir(ctx), wf.RedRoundCap())
}

// startRedRounds sets up the cap for a transition. A non-nil error is the
// refusal: a cap question is open (raised now, for a streak that reached
// the cap since, e.g. from local CI), and nothing is run again.
func startRedRounds(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue) (*redRoundsRun, error) {
	state, err := loadRedRounds(ctx, wf, issue)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: red rounds not counted: %v\n", err)
		return nil, nil
	}
	r := &redRoundsRun{ctx: ctx, wf: wf, issue: issue, state: state}
	if err := r.raise(); err != nil {
		return nil, err
	}
	if err := r.state.Hold(issue.Slug); err != nil {
		return nil, fmt.Errorf("failed to transition: %w", err)
	}
	wf.CheckDone = func(key string, err error) {
		res := tracker.RoundResult{Check: key, Red: err != nil, At: ctx.now()}
		if err != nil {
			res.Why = firstErrLine(err)
		}
		r.results = append(r.results, res)
	}
	return r, nil
}

// raise posts a cap question for every streak at the cap without one, and
// reloads the open questions.
func (r *redRoundsRun) raise() error {
	need := r.state.NeedQuestion()
	if len(need) == 0 {
		return nil
	}
	author := commentAuthor(r.ctx.Project, r.issue)
	for _, s := range need {
		if err := tracker.AddCommentBy(r.issue.FilePath, 0, redrounds.QuestionText(s, r.state.Max), "cli", author); err != nil {
			return err
		}
	}
	comments, err := tracker.LoadComments(r.issue.FilePath)
	if err != nil {
		return err
	}
	r.state.Open = redrounds.Open(redrounds.Questions(comments))
	return nil
}

// finish records the checks the transition ran and, when the transition
// failed on a check, returns its error with the count added, or the cap
// refusal when this round reached the cap. A nil r passes err through.
func (r *redRoundsRun) finish(err error) error {
	if r == nil {
		return err
	}
	r.wf.CheckDone = nil
	if len(r.results) == 0 {
		return err
	}
	head := r.wf.WorkBranchHead(r.issue.FilePath, r.issue.Slug)
	for i := range r.results {
		r.results[i].Head = head
	}
	if rerr := tracker.AppendRoundResults(r.issue.FilePath, r.results...); rerr != nil {
		fmt.Fprintf(r.ctx.Stderr, "warning: red rounds not recorded: %v\n", rerr)
		return err
	}
	if err == nil {
		return nil
	}
	state, lerr := loadRedRounds(r.ctx, r.wf, r.issue)
	if lerr != nil {
		return err
	}
	r.state = state
	last := r.results[len(r.results)-1]
	if !last.Red {
		return err
	}
	if qerr := r.raise(); qerr != nil {
		fmt.Fprintf(r.ctx.Stderr, "warning: red-round question not posted: %v\n", qerr)
	}
	s := r.state.Streak(last.Check)
	if _, open := r.state.Open[last.Check]; open {
		return fmt.Errorf("%w\n\n%v", err, r.state.Hold(r.issue.Slug))
	}
	return fmt.Errorf("%w\n\n%s is %s (max_red_rounds); at %d it becomes a question for the owner. Can't fix it? issue-cli rounds %s --cannot-fix %q --why \"...\"",
		err, last.Check, s.Label(r.state.Max), r.state.Max, r.issue.Slug, last.Check)
}

func firstErrLine(err error) string {
	msg := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

// printRedRoundsLine prints "Red rounds: …" for show and start when a check
// is red; nothing otherwise.
func printRedRoundsLine(w io.Writer, ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue) {
	state, err := loadRedRounds(ctx, wf, issue)
	if err != nil || len(state.Streaks) == 0 {
		return
	}
	var parts []string
	for _, s := range state.Streaks {
		p := s.Check + " " + s.Label(state.Max)
		if _, open := state.Open[s.Check]; open {
			p += " (question open)"
		}
		parts = append(parts, p)
	}
	fmt.Fprintf(w, "Red rounds: %s — at %d a check becomes a question for the owner (issue-cli rounds %s)\n",
		strings.Join(parts, "; "), state.Max, issue.Slug)
}

var roundsCommand = &Command{
	Name:      "rounds",
	ShortHelp: "Red rounds per check; say a check can't be fixed",
	LongHelp: `Show each check that is red on the issue and how many fix rounds it has
been red: a gate's command or the proof check run by transition, a local CI
command run on a merge that named the issue, the critic (each held
issue-cli critique run is a round; a failed run is not). Reds at one branch
head are one round; a green ends the streak.

At max_red_rounds (workflow.yaml, 4 when unset) the check becomes a
question: comment for the owner, and transition refuses without running
anything again until the owner resolves it. YOLO doesn't pass it.

--cannot-fix <check> --why "..." says you can't fix a check (one shown here,
or a new name such as test:TestFoo): it raises the question at once.

Examples:
  issue-cli rounds <slug>
  issue-cli rounds <slug> --json
  issue-cli rounds <slug> --cannot-fix "gate:criteria_proven" --why "AC2 needs a real Chrome"`,
	Run: runRounds,
}

func init() {
	registerCommand(roundsCommand)
}

type roundsJSON struct {
	Slug    string            `json:"slug"`
	Max     int               `json:"max_red_rounds"`
	Streaks []roundsJSONEntry `json:"streaks"`
}

type roundsJSONEntry struct {
	redrounds.Streak
	Label    string `json:"label"`
	Question int    `json:"question,omitempty"`
}

func runRounds(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "rounds")
	if err != nil {
		return err
	}
	fs := newFlagSet("rounds", ctx)
	cannot := fs.String("cannot-fix", "", "the check you can't fix: raises the question for the owner now")
	why := fs.String("why", "", "why you can't fix it (with --cannot-fix)")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	wf, err := issueWorkflow(ctx, issue)
	if err != nil {
		return err
	}
	if *cannot != "" {
		if strings.TrimSpace(*why) == "" {
			return errors.New("--cannot-fix needs --why \"...\": the owner reads it in the question")
		}
		if err := ownIssueOnly(ctx, issues, issue, "rounds --cannot-fix"); err != nil {
			return err
		}
		res := tracker.RoundResult{Check: strings.TrimSpace(*cannot), Red: true, At: ctx.now(), Why: strings.TrimSpace(*why), CannotFix: true,
			Head: wf.WorkBranchHead(issue.FilePath, issue.Slug)}
		if err := tracker.AppendRoundResults(issue.FilePath, res); err != nil {
			return err
		}
	}
	state, err := loadRedRounds(ctx, wf, issue)
	if err != nil {
		return err
	}
	r := &redRoundsRun{ctx: ctx, wf: wf, issue: issue, state: state}
	if err := r.raise(); err != nil {
		return err
	}
	state = r.state
	if ctx.JSONOutput {
		out := roundsJSON{Slug: issue.Slug, Max: state.Max, Streaks: []roundsJSONEntry{}}
		for _, s := range state.Streaks {
			e := roundsJSONEntry{Streak: s, Label: s.Label(state.Max)}
			if q, ok := state.Open[s.Check]; ok {
				e.Question = q.CommentID
			}
			out.Streaks = append(out.Streaks, e)
		}
		return writeJSON(ctx.Stdout, out)
	}
	if len(state.Streaks) == 0 {
		fmt.Fprintf(ctx.Stdout, "No red checks on %s (max_red_rounds: %d).\n", issue.Slug, state.Max)
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Red rounds (max_red_rounds: %d) ==\n", state.Max)
	for _, s := range state.Streaks {
		line := fmt.Sprintf("  %s · %s", s.Label(state.Max), s.Check)
		if w := strings.TrimSpace(s.Last.Why); w != "" {
			line += " · " + w
		}
		fmt.Fprintln(ctx.Stdout, line)
		if q, ok := state.Open[s.Check]; ok {
			fmt.Fprintf(ctx.Stdout, "    question for the owner: comment #%d. Stop working on this check; transition is refused until it is resolved.\n", q.CommentID)
		}
	}
	return nil
}

// redRoundsIssuesDir is where the project's critique runs live, "" without
// a project.
func redRoundsIssuesDir(ctx *Context) string {
	if ctx.Project == nil {
		return ""
	}
	return ctx.Project.IssueDir
}
