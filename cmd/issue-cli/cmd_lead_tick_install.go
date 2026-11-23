package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The install lag: a fix to what the lead or a worker sees does nothing
// until someone installs it, and installing is the lead's or the owner's
// call. Once a local day, on the engine's own repository, the tick says
// how far the running issue-cli is behind main and which shipped fixes
// wait on an install, with the command. It is held, never news: under
// --exit-on-news it goes out with the next news. Nothing runs the install.

// installLagLine is how many commits behind main the build may be before
// the tick says so.
const installLagLine = 10

// installLagFixMax is how many fix subjects the line names.
const installLagFixMax = 5

// installLagFixPaths are the paths a fix must touch to change what issue-cli
// prints: the viewer's own code needs a viewer restart, not this line.
var installLagFixPaths = []string{"cmd/issue-cli", "internal", ":(exclude)internal/viewer"}

// installLagFixes are the subjects of the fixes on branch that version
// lacks, newest first. A var so tests don't need the history.
var installLagFixes = func(root, version, branch string) []string {
	rev := strings.TrimSuffix(strings.TrimSpace(version), "-dirty")
	args := append([]string{"-C", root, "log", "--no-merges", "--format=%s", rev + "^{commit}.." + branch, "--"}, installLagFixPaths...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	var fixes []string
	for _, s := range strings.Split(string(out), "\n") {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "fix:") || strings.HasPrefix(s, "fix(") {
			fixes = append(fixes, s)
		}
	}
	return fixes
}

// installLagExe is the running issue-cli's path, links resolved; a var so
// tests name one.
var installLagExe = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return exe
}

// leadTickInstallLag says the install lag once a local day.
func leadTickInstallLag(ctx *Context, t *leadTicker) {
	t.installLagOn = t.prev.InstallLagOn
	today := t.now.Local().Format("2006-01-02")
	if t.prev.InstallLagOn == today {
		return
	}
	line := installLagNote(ctx.Project)
	if line == "" {
		return
	}
	t.installLagOn = today
	t.held(line)
}

// installLagNote is the line, "" when the build is not more than
// installLagLine commits behind or the project is not the engine's.
func installLagNote(proj *tracker.Project) string {
	root := gitmain.Root(projectRoot(proj))
	if root == "" {
		return ""
	}
	if fi, err := os.Stat(filepath.Join(root, "cmd", "issue-cli")); err != nil || !fi.IsDir() {
		return ""
	}
	branch := "main"
	if wf := proj.LoadWorkflow(); wf != nil && wf.CI.Enabled() && strings.TrimSpace(wf.CI.Branch) != "" {
		branch = strings.TrimSpace(wf.CI.Branch)
	}
	n := tracker.BuildBehind(root, branch, tracker.BuildVersion)
	if n <= installLagLine {
		return ""
	}
	line := fmt.Sprintf("note: the installed issue-cli (%s) is %d commits behind %s; ", tracker.BuildVersion, n, branch)
	fixes := installLagFixes(root, tracker.BuildVersion, branch)
	if len(fixes) == 0 {
		line += "no shipped fix to issue-cli among them"
	} else {
		shown := fixes
		if len(shown) > installLagFixMax {
			shown = shown[:installLagFixMax]
		}
		verb := "waits"
		if len(fixes) > 1 {
			verb = "wait"
		}
		line += fmt.Sprintf("%s %s on an install: %s", countOf(len(fixes), "shipped fix", "shipped fixes"), verb, strings.Join(shown, "; "))
		if more := len(fixes) - len(shown); more > 0 {
			line += fmt.Sprintf(" (+%d more)", more)
		}
	}
	return line + "; install: " + installCommand(root)
}

// installCommand is the install over the running build: make install in
// the repository with GOBIN at the running issue-cli's folder, home as ~,
// then the viewer's restart in its own pane. The tick only prints it.
func installCommand(root string) string {
	cmd := "cd " + tildeHome(root) + " && "
	if exe := installLagExe(); exe != "" {
		cmd += "GOBIN=" + tildeHome(filepath.Dir(exe)) + " "
	}
	return cmd + "make install && issue-cli viewer restart"
}

// tildeHome is path with the home folder as ~.
func tildeHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return path
}
