package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The guards' live check after the install: the commit-msg hook, the
// pre-push guard and the leak scan they run are the installed issue-cli,
// so a change to one is live only once the lead installs main, and no
// worker can run it before landing. The queue sweep of 2026-11-21 (C6)
// found such checks left as prose nobody ran. Once the installed build
// holds a commit that changed a guard, the tick says it, once per commit,
// with the check to run and the tests: comment to post: the lead's
// after-install task, there without anyone writing it down.

// guardWindow is how far back the tick looks for guard commits in the
// installed build: a commit older than that was installed and said long
// ago, or before this line existed.
const guardWindow = 14 * 24 * time.Hour

// guardMax is how many guard commits one tick names.
const guardMax = 5

// afterInstallGuard is one guard: the files whose change it carries, and
// its live check.
type afterInstallGuard struct {
	Name  string
	Paths []string
	Check string
}

// afterInstallGuards are the guards the installed issue-cli runs.
var afterInstallGuards = []afterInstallGuard{
	{"commit-msg", []string{"cmd/issue-cli/cmd_commit_msg.go"},
		"in a scratch clone with the hook (issue-cli workflow commit-msg --install), git commit --allow-empty -F <a message the change refuses> must refuse it, and one it allows must pass"},
	{"pre-push", []string{"cmd/issue-cli/cmd_privacy.go", "internal/privacy/hook.go", "internal/privacy/secrets.go"},
		"issue-cli privacy check shows the pre-push guard green; a push of a commit the change refuses to a scratch remote (git init --bare) must be refused"},
	{"leak-check", []string{"internal/privacy/leaks.go", "internal/privacy/staged.go"},
		"issue-cli workflow commit-msg <a message file holding a line make leak-check refuses> must warn about it"},
}

// guardCommit is one commit in the installed build that changed a guard.
type guardCommit struct {
	SHA     string
	Subject string
	// Refs is the issue slug its Refs: trailer names; "" without one.
	Refs   string
	Guards []string
}

// guardCommits lists the commits in version (the installed build) since
// since that touch a guard's files, newest first. A var so tests don't
// need the history.
var guardCommits = func(root, version string, since time.Time) []guardCommit {
	rev := strings.TrimSuffix(strings.TrimSpace(version), "-dirty")
	if rev == "" {
		return nil
	}
	var out []guardCommit
	at := map[string]int{}
	for _, g := range afterInstallGuards {
		args := append([]string{"-C", root, "log", "--no-merges", "--since=" + since.Format(time.RFC3339),
			"--format=%H%x1f%s%x1f%(trailers:key=Refs,valueonly,separator=%x2c)%x1e", rev + "^{commit}", "--"}, g.Paths...)
		raw, err := exec.Command("git", args...).Output()
		if err != nil {
			return nil
		}
		for _, rec := range strings.Split(string(raw), "\x1e") {
			f := strings.Split(strings.TrimSpace(rec), "\x1f")
			if len(f) < 2 || f[0] == "" {
				continue
			}
			if i, ok := at[f[0]]; ok {
				out[i].Guards = append(out[i].Guards, g.Name)
				continue
			}
			c := guardCommit{SHA: f[0], Subject: f[1], Guards: []string{g.Name}}
			if len(f) > 2 {
				c.Refs = strings.TrimSpace(strings.Split(f[2], ",")[0])
			}
			at[c.SHA] = len(out)
			out = append(out, c)
		}
	}
	return out
}

// leadTickGuards says each guard commit the installed build holds, once.
func leadTickGuards(ctx *Context, t *leadTicker) {
	for _, line := range guardLines(ctx.Project, t.now) {
		t.say(line.key, line.text)
	}
}

type guardLine struct{ key, text string }

// guardLines are the tick's lines, nil off the engine's repository.
func guardLines(proj *tracker.Project, now time.Time) []guardLine {
	root := gitmain.Root(projectRoot(proj))
	if root == "" {
		return nil
	}
	if fi, err := os.Stat(filepath.Join(root, "cmd", "issue-cli")); err != nil || !fi.IsDir() {
		return nil
	}
	commits := guardCommits(root, tracker.BuildVersion, now.Add(-guardWindow))
	if len(commits) > guardMax {
		commits = commits[:guardMax]
	}
	checks := map[string]string{}
	for _, g := range afterInstallGuards {
		checks[g.Name] = g.Check
	}
	var out []guardLine
	for _, c := range commits {
		short := c.SHA
		if len(short) > 8 {
			short = short[:8]
		}
		var how []string
		for _, g := range c.Guards {
			how = append(how, g+": "+checks[g])
		}
		post := "post tests: after install of " + tracker.BuildVersion + ": <guard> pass|fail: … on its issue"
		if c.Refs != "" {
			post = fmt.Sprintf("issue-cli comment %s --text \"tests: after install of %s: %s pass|fail: …\"", c.Refs, tracker.BuildVersion, strings.Join(c.Guards, ", "))
		}
		out = append(out, guardLine{
			key: "guard-after-install:" + c.SHA,
			text: fmt.Sprintf("after install: the installed issue-cli holds %s (%s), a change to the %s guard, live only now: run its check: %s; then %s (a failure is a bugfix issue)",
				short, c.Subject, strings.Join(c.Guards, ", "), strings.Join(how, "; "), post),
		})
	}
	return out
}
