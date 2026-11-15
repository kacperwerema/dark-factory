package agentrun

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Takeover is a live worker stopped at its usage limit: To takes its branch
// over, or Hold says why nothing starts.
type Takeover struct {
	Slug    string
	Session string
	From    string
	To      string
	Words   string
	Hold    string
	Limit   routing.Limit
}

// takeoverPaneLines is how many of a pane's last lines are read for the
// working line.
const takeoverPaneLines = dialogLines

// Takeovers lists the workers of proj stopped at a usage limit (root scopes
// standalone sessions, as ForProject). A pane counts when it belongs to an
// open issue, shows its own CLI's limit, isn't in a turn and doesn't say it
// goes on by itself: a takeover on anything less puts two writers in one
// worktree. It only reads panes and comments.
func Takeovers(proj *tracker.Project, root string, issues []*tracker.Issue, yoloOn bool, now time.Time) []Takeover {
	var out []Takeover
	limits := ScanLimits(proj, root, now)
	if len(limits) == 0 {
		return nil
	}
	bySession := map[string]*tracker.Issue{}
	for _, issue := range issues {
		bySession[tracker.AgentSessionName(issue.Slug)] = issue
	}
	st := Subscriptions()
	for _, l := range limits {
		issue := bySession[l.Session]
		if issue == nil || proj.IssueClosed(issue) || strings.Contains(strings.ToLower(l.Limit.Line), "continuing automatically") {
			continue
		}
		if agent := SessionAgent(proj, l.Session); agent != "" && agent != l.Limit.Agent {
			continue
		}
		if PaneWorking(Target(proj, l.Session), takeoverPaneLines) {
			continue
		}
		t := Takeover{Slug: issue.Slug, Session: l.Session, From: l.Limit.Agent, Limit: l.Limit}
		comments, _ := tracker.LoadComments(issue.FilePath)
		t.Words = tracker.DispatchWords(comments)
		switch {
		case tracker.TakenOver(comments) || IsLive(proj, tracker.LimitedSessionName(issue.Slug)):
			t.Hold = "taken over once already: no third worker; the owner decides"
		case t.Words == "":
			t.Hold = "no dispatch: words on the issue to quote; the owner decides"
		case !yoloOn && tracker.WaitingOnGate(proj.LoadWorkflowForIssue(issue), issue, comments) != "":
			t.Hold = "it only waits on the owner's approval"
		default:
			to, why := routing.Other(st, t.From, now)
			if to == "" {
				t.Hold = "no other agent to take it over: " + why
			}
			t.To = to
		}
		out = append(out, t)
	}
	return out
}

// TakeoverOf is issue's line in Takeovers; ok is false when it has none.
func TakeoverOf(proj *tracker.Project, root string, issue *tracker.Issue, yoloOn bool, now time.Time) (Takeover, bool) {
	for _, t := range Takeovers(proj, root, []*tracker.Issue{issue}, yoloOn, now) {
		return t, true
	}
	return Takeover{}, false
}

// TakeoverSpec is the launch of t: the issue's normal briefing, opened by
// the takeover's section, on the same worktree and the same session name.
func TakeoverSpec(proj *tracker.Project, issue *tracker.Issue, t Takeover, workDir string) Spec {
	spec, _ := DispatchSpec(proj, issue, t.To, workDir, true)
	dir := spec.Worktree.Path
	if dir == "" {
		dir = spec.WorkDir
	}
	spec.Prompt = tracker.TakeoverPromptSection(t.From, dir, spec.Worktree.Branch) + spec.Prompt
	if spec.Route != nil {
		spec.Route.Why = "takes over from " + t.From + " at its usage limit"
	}
	return spec
}

// defaultRenameSession renames the agent called from to to: its session, or
// its window in the project's shared session. It sends no keys.
func defaultRenameSession(proj *tracker.Project, from, to string) error {
	verb := "rename-session"
	if SharedSession(proj) != "" {
		verb = "rename-window"
	}
	return exec.Command("tmux", verb, "-t", Target(proj, from), to).Run()
}

// LimitedSessions names the sessions of proj left running after a takeover.
func LimitedSessions(proj *tracker.Project, root string) []string {
	var out []string
	for _, s := range ForProject(proj, root, ListSessions()) {
		if name := BareName(s.Name); strings.HasPrefix(name, tracker.LimitedSessionPrefix) {
			out = append(out, name)
		}
	}
	return out
}

// CloseCmd is the tmux command that closes the session called name.
func CloseCmd(proj *tracker.Project, name string) string {
	verb := "kill-session"
	if SharedSession(proj) != "" {
		verb = "kill-window"
	}
	return fmt.Sprintf("%stmux %s -t %s", tmuxEnvPrefix(), verb, shell.Quote(Target(proj, name)))
}

// ExcludeHandoff keeps HANDOFF.md out of git in the checkout dir, through the
// repository's info/exclude (shared by its worktrees), so it reaches every
// project and a worker's git add can't commit it. Best effort: a failure
// leaves the file untracked, which the worker's commit discipline covers.
func ExcludeHandoff(dir string) {
	path, err := RunGitOutput("-C", dir, "rev-parse", "--git-path", "info/exclude")
	if err != nil || path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "/"+tracker.HandoffFile {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		fmt.Fprintln(f)
	}
	fmt.Fprintln(f, "/"+tracker.HandoffFile)
}
