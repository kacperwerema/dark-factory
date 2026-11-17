package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// lead watch: a probe (a job's check, an image download) run in the shell
// until its output changes or matches, so the lead waits on a background
// shell instead of waking its whole context on a timer. Quiet runs print
// nothing; the one exit prints what changed. See docs/patterns/lead-session.md#waiting.

// leadWatchMinEvery is the shortest --every: a probe costs no tokens, but
// a tight loop is still a busy machine.
const leadWatchMinEvery = 10 * time.Second

// leadWatchShowLines caps the output printed on exit.
const leadWatchShowLines = 40

// leadWatchSleep waits d between runs and reports whether to go on; a var
// so tests run with a pinned clock and no real sleep.
var leadWatchSleep = func(d time.Duration) bool {
	time.Sleep(d)
	return true
}

// leadWatchRun runs cmd with /bin/sh -c in dir and returns its stdout and
// stderr together, with "exit N" appended when it failed: a probe that
// starts failing is a change too. A var for tests.
var leadWatchRun = func(dir, cmd string) string {
	c := exec.Command("/bin/sh", "-c", cmd)
	c.Dir = dir
	c.Env = os.Environ()
	out, err := c.CombinedOutput()
	s := strings.TrimRight(string(out), "\n")
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		s += fmt.Sprintf("\nexit %d", exit.ExitCode())
	case err != nil:
		s += "\nerror: " + err.Error()
	}
	return strings.TrimLeft(s, "\n")
}

func runLeadWatch(ctx *Context, args []string) error {
	fs := newFlagSet("lead watch", ctx)
	run := fs.String("run", "", "the probe: a shell command (run with /bin/sh -c in the project root)")
	every := fs.Duration("every", 10*time.Minute, "time between runs (at least 10s)")
	untilChange := fs.Bool("until-change", false, "exit when the output differs from the first run's (the default)")
	until := fs.String("until", "", "exit when the output matches this regular expression (the first run included)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*run) == "" {
		return &usageError{msg: "lead watch needs --run '<cmd>'\n\nUsage:\n  issue-cli lead watch --run '<cmd>' [--every 10m] [--until-change | --until '<regex>']"}
	}
	if *untilChange && *until != "" {
		return &usageError{msg: "--until-change and --until don't go together: pick one"}
	}
	if *every < leadWatchMinEvery {
		return &usageError{msg: fmt.Sprintf("--every must be at least %s, got %s", leadWatchMinEvery, *every)}
	}
	var re *regexp.Regexp
	if *until != "" {
		var err error
		if re, err = regexp.Compile(*until); err != nil {
			return &usageError{msg: fmt.Sprintf("--until: %v", err)}
		}
	}
	dir, _ := os.Getwd()
	if proj := ctx.Project; proj != nil {
		issues, err := proj.LoadIssues()
		if err != nil {
			return fmt.Errorf("cannot load issues: %w", err)
		}
		if me := guardWho(ctx, issues, "lead watch"); me.Role == tracker.RoleWorker {
			guardNever(ctx, "lead watch")
			return fmt.Errorf("workers don't wait on probes (you are %s): finish your step or post a question: comment, and stop", valueOr(me.Name, "a worker"))
		}
		dir = projectRoot(proj)
		defer registerLeadWatch(ctx, whoAmI(proj, issues))()
	}
	return leadWatch(ctx, dir, *run, *every, re)
}

// leadWatch runs cmd every every until its output changes from the first
// run's (re nil) or matches re, then prints the header and the output.
func leadWatch(ctx *Context, dir, cmd string, every time.Duration, re *regexp.Regexp) error {
	start := ctx.now()
	first := leadWatchRun(dir, cmd)
	for runs := 1; ; runs++ {
		out := first
		if runs > 1 {
			out = leadWatchRun(dir, cmd)
		}
		var head string
		switch {
		case re != nil && re.MatchString(out):
			head = fmt.Sprintf("watch: output matched /%s/", re.String())
		case re == nil && out != first:
			head = "watch: output changed"
		}
		if head != "" {
			fmt.Fprintf(ctx.Stdout, "%s after %s (%s): %s\n", head, roundWatch(ctx.now().Sub(start)), countOf(runs, "run", "runs"), cmd)
			fmt.Fprintln(ctx.Stdout, capLines(out, leadWatchShowLines))
			return nil
		}
		if !leadWatchSleep(every) {
			return nil
		}
	}
}

// roundWatch is d to the second under a minute, else to the minute.
func roundWatch(d time.Duration) string {
	if d < time.Minute {
		return d.Truncate(time.Second).String()
	}
	return roundHours(d)
}

// capLines is s cut to n lines, with how many were left out.
func capLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… %d more line(s)", len(lines)-n)
}
