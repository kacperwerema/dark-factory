package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/leaks"
	"github.com/kacperwerema/dark-factory/internal/restrace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var doctorCommand = &Command{
	Name:      "doctor",
	ShortHelp: "Look for trouble on this machine: doctor leaks lists what the factory left running",
	LongHelp: `doctor leaks lists what is still alive that should not be: what the
factory started and nothing stopped. Each one comes with its pid, its age
and the command that stops it. Nothing is stopped without --stop.

It needs no tracing: it matches the shapes leaks have taken on this
machine,
  - a playtest Chrome whose runner is gone (profile
    dark-factory-playtest-<pid>-*, or an old chromedp-runner* one whose
    parent has been pid 1 for 10 minutes or more);
  - a process whose CODEX_HOME or CLAUDE_CONFIG_DIR is a temp or
    scratchpad folder, detached (parent pid 1) or with that folder gone:
    codex app-server-daemon out of a test's or a sandbox's home;
  - the tmux server of a test run that was killed (/tmp/tmg-*, tmx-*,
    ckt-*, older than a run lasts);
  - a per_run: tool the engine started that no run holds;
  - a registered git worktree of a project that nothing uses (none live
    inside): one outside the main checkout in a temp or scratchpad folder,
    gone or untouched for an hour, or a subagent's under .claude/worktrees
    whose branch is on main, untouched for a day. Its stop is git worktree
    remove (unlock first when locked); --stop never forces, so a dirty
    tree stays. The branch stays. Trees under .worktrees/ are worktrees
    prune's.
With a resource trace (DF_TRACE=resources, or --debug, wrote one) it also
lists what was started, never stopped, and is still alive; those may still
be in use (a live worker, a CI run), so --stop leaves them alone: stop
them with their command if nothing needs them.

  --stop          stop each leak listed (not the traced ones)
  --under <dir>   only processes whose agent CLI home, or command line, is
                  inside <dir>, at any parent (a check script's sandbox at
                  its exit runs: doctor leaks --under <sandbox> --stop)
  --quiet         print nothing when nothing is found
  --json          the findings as JSON

The trace is $XDG_STATE_HOME/ai-workflow-engine/trace/resources.jsonl.
docs/getting-started.md#troubleshooting.`,
	Run:         runDoctor,
	Subcommands: []string{"leaks"},
}

func init() { registerCommand(doctorCommand) }

// findLeaks is the look; a seam for tests.
var findLeaks = leaks.Find

func runDoctor(ctx *Context, args []string) error {
	if len(args) == 0 || args[0] != "leaks" {
		return fmt.Errorf("usage: issue-cli doctor leaks [--stop] [--under <dir>] [--quiet] [--json]")
	}
	fs := newFlagSet("doctor leaks", ctx)
	stop := fs.Bool("stop", false, "stop each leak listed (not the traced ones)")
	under := fs.String("under", "", "only processes whose agent CLI home or command line is inside this folder")
	quiet := fs.Bool("quiet", false, "print nothing when nothing is found")
	if err := parseFlagsArgs(ctx, fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return unexpectedArgError(fs, fs.Arg(0))
	}
	var projects []*tracker.Project
	for i := range ctx.AllProjects {
		projects = append(projects, &ctx.AllProjects[i])
	}
	if len(projects) == 0 && ctx.Project != nil {
		projects = append(projects, ctx.Project)
	}
	rep := findLeaks(leaks.Options{Now: ctx.now(), Projects: projects, Under: *under})
	var stopped, failed []string
	if *stop {
		for _, f := range rep.Leaks {
			if err := f.Stop(); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", f.What, err))
			} else {
				stopped = append(stopped, f.What)
			}
		}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, struct {
			leaks.Report
			Stopped []string `json:"stopped,omitempty"`
			Failed  []string `json:"failed,omitempty"`
		}{rep, stopped, failed})
	}
	w := ctx.Stdout
	if len(rep.Leaks) == 0 && len(rep.Traced) == 0 {
		if !*quiet {
			fmt.Fprintln(w, "✓ doctor leaks: nothing left running")
			if !rep.TraceRead && *under == "" {
				fmt.Fprintln(w, "  (known shapes only: no resource trace; DF_TRACE=resources records one)")
			}
		}
		return nil
	}
	if len(rep.Leaks) > 0 {
		verb := "nothing stopped; --stop stops them"
		if *stop {
			verb = fmt.Sprintf("stopped %d", len(stopped))
		}
		fmt.Fprintf(w, "doctor leaks: %d left running (%s)\n", len(rep.Leaks), verb)
		printFindings(w, rep.Leaks)
	}
	if len(rep.Traced) > 0 {
		fmt.Fprintf(w, "From the trace (%s): %d started, never stopped, still alive; may be in use, --stop leaves them\n", restrace.Path(), len(rep.Traced))
		printFindings(w, rep.Traced)
	}
	for _, f := range failed {
		fmt.Fprintln(ctx.Stderr, "✗ did not stop:", f)
	}
	if len(failed) > 0 {
		return &exitCodeError{Code: 1}
	}
	return nil
}

func printFindings(w interface{ Write([]byte) (int, error) }, fs []leaks.Finding) {
	for _, f := range fs {
		pid := "-"
		if f.PID > 0 {
			pid = fmt.Sprint(f.PID)
		}
		fmt.Fprintf(w, "  %-12s pid %-7s %-7s %s\n", f.Kind, pid, leaks.Age(f.Age), f.What)
		fmt.Fprintf(w, "  %-12s %s\n", "", f.Why)
		fmt.Fprintf(w, "  %-12s stop: %s\n", "", strings.TrimSpace(f.StopCommand))
	}
}
