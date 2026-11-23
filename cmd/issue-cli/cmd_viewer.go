package main

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// viewer restart: the viewer runs as the start command of a tmux pane, and
// every install of main ends with restarting it. By hand that went wrong
// twice on 2026-11-22 (killing the process closed its session; a pgrep
// wait matched itself). respawn-pane -k reruns the pane's own start command
// and keeps the session; then we wait until the new viewer answers.

const viewerUsage = `Usage:
  issue-cli viewer restart [--timeout 60s]

The board itself is its own binary: issue-viewer --help (docs/getting-started.md).`

var viewerCommand = &Command{
	Name:      "viewer",
	ShortHelp: "Restart the running viewer in its own tmux pane and wait until it answers (restart)",
	LongHelp: `Restart the running issue-viewer after an install, the safe way.

  issue-cli viewer restart [--timeout 60s]

Finds the tmux pane the viewer runs in: the pane holding the pid viewer.json
records (the pane's own process or an ancestor of it), else the one live pane
whose start command runs issue-viewer. Then tmux respawn-pane -k reruns that
pane's own start command, so the session stays, and it waits until the new
viewer answers: viewer.json names a new live pid and an HTTP request to its
URL gets an answer (a viewer that records no viewer.json: the pane's process
changed and the old URL answers). It prints one line with the old and the new
version and pid:

  viewer restart: v0.65.0-1-gabc (pid 101) → v0.65.0-9-gdef (pid 202), answering at http://127.0.0.1:8090 after 1.4s (pane %3, session issue-viewer)

The version comes from viewer.json; a viewer started before viewers recorded
it reads "unknown".

Refused, exit 1, with one line: no tmux pane runs the viewer (it names what it
looked for), several panes do (it names them), or a dispatched worker runs it
(restarting the viewer is the lead's or the owner's call). The pane dying or
--timeout passing before the viewer answers is exit 1 with the pane's last
lines.

After an install of main: cd <repo> && make install && issue-cli viewer restart`,
	Run:         runViewer,
	Subcommands: []string{"restart"},
}

func init() {
	registerCommand(viewerCommand)
}

func runViewer(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: "viewer requires a subcommand\n\n" + viewerUsage}
	}
	switch args[0] {
	case "restart":
		return runViewerRestart(ctx, args[1:])
	default:
		return &usageError{msg: fmt.Sprintf("unknown viewer subcommand: %s%s\n\n%s", args[0], subcommandHint("viewer", args[0]), viewerUsage)}
	}
}

// Seams for tests: how a pid's parent is read, how the viewer is asked
// whether it answers, and how often the wait looks.
var (
	viewerParentPID = parentPID
	viewerAnswers   = httpAnswers
	viewerPoll      = 200 * time.Millisecond
)

// viewerPane is one tmux pane as list-panes prints it.
type viewerPane struct {
	ID, Session, Start string
	PID                int
	Dead               bool
}

func runViewerRestart(ctx *Context, args []string) error {
	fs := newFlagSet("viewer restart", ctx)
	timeout := fs.Duration("timeout", 60*time.Second, "how long to wait for the new viewer to answer")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if ctx.Project != nil {
		issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
		if me := guardWho(ctx, issues, "viewer restart"); me.Role == tracker.RoleWorker {
			guardNever(ctx, "viewer restart")
			return errors.New("viewer restart: workers never restart the viewer: it is the lead's or the owner's call. Ask on your issue trail (issue-cli comment <slug> \"question: …\")")
		}
	}

	old, recErr := viewerstate.Read()
	if recErr != nil || old.PID <= 0 || !viewerstate.Alive(old.PID) {
		old = viewerstate.State{}
	}
	panes, listErr := listPanes()
	pane, err := findViewerPane(panes, old.PID)
	if err != nil {
		return err
	}
	if listErr != nil || pane == nil {
		return errors.New(noViewerPaneLine(old.PID, listErr))
	}

	url := strings.TrimRight(old.URL, "/")
	if url == "" {
		configured, config := "", ""
		if ctx.Project != nil {
			configured, config = ctx.Project.ViewerURL, ctx.ConfigPath
		}
		url = viewerstate.BaseURL(configured, config)
	}
	oldVersion := valueOr(old.Version, "unknown")
	oldPID := old.PID
	if oldPID == 0 {
		oldPID = pane.PID
	}

	start := time.Now()
	if out, err := tmuxRun("respawn-pane", "-k", "-t", pane.ID); err != nil {
		return fmt.Errorf("viewer restart: tmux respawn-pane -k -t %s failed: %v %s", pane.ID, err, strings.TrimSpace(out))
	}

	deadline := start.Add(*timeout)
	for {
		if rec, err := viewerstate.Read(); err == nil && rec.PID > 0 && rec.PID != oldPID && viewerstate.Alive(rec.PID) {
			if u := strings.TrimRight(rec.URL, "/"); u != "" && viewerAnswers(u) {
				fmt.Fprintf(ctx.Stdout, "viewer restart: %s (pid %d) → %s (pid %d), answering at %s after %s (pane %s, session %s)\n",
					oldVersion, oldPID, valueOr(rec.Version, "unknown"), rec.PID, u, roundSeconds(time.Since(start)), pane.ID, pane.Session)
				return nil
			}
		}
		now, gone := paneNow(pane.ID)
		if gone || now.Dead {
			return fmt.Errorf("viewer restart: the viewer's pane %s %s after the respawn; its last lines:\n%s", pane.ID, map[bool]string{true: "is gone", false: "died"}[gone], paneTail(pane.ID))
		}
		// A viewer that records no viewer.json: the pane runs a new process,
		// the old one is gone and the URL answers.
		if now.PID != pane.PID && !viewerstate.Alive(oldPID) && viewerAnswers(url) {
			if rec, err := viewerstate.Read(); err != nil || rec.PID == oldPID || rec.PID <= 0 || !viewerstate.Alive(rec.PID) {
				fmt.Fprintf(ctx.Stdout, "viewer restart: %s (pid %d) → unknown (pane pid %d), answering at %s after %s (pane %s, session %s)\n",
					oldVersion, oldPID, now.PID, url, roundSeconds(time.Since(start)), pane.ID, pane.Session)
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("viewer restart: the viewer in pane %s did not answer at %s within %s; its last lines:\n%s", pane.ID, url, *timeout, paneTail(pane.ID))
		}
		time.Sleep(viewerPoll)
	}
}

// findViewerPane picks the viewer's pane: the one holding pid (its process
// or an ancestor of it), else the one live pane whose start command runs
// issue-viewer. nil, nil when none does; an error when several do.
func findViewerPane(panes []viewerPane, pid int) (*viewerPane, error) {
	if pid > 0 {
		byPID := map[int]int{}
		for i, p := range panes {
			byPID[p.PID] = i
		}
		for cur, hops := pid, 0; cur > 1 && hops < 16; hops++ {
			if i, ok := byPID[cur]; ok {
				return &panes[i], nil
			}
			cur = viewerParentPID(cur)
		}
	}
	var found []int
	for i, p := range panes {
		if !p.Dead && startRunsViewer(p.Start) {
			found = append(found, i)
		}
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return &panes[found[0]], nil
	}
	var names []string
	for _, i := range found {
		names = append(names, panes[i].ID+" (session "+panes[i].Session+")")
	}
	return nil, fmt.Errorf("viewer restart: %d tmux panes run issue-viewer (%s) and none holds the pid viewer.json records; restart the right one with tmux respawn-pane -k -t <pane>", len(found), strings.Join(names, ", "))
}

// startRunsViewer reports whether a pane's start command runs issue-viewer:
// its first word, or the word after a leading exec, is a file of that name.
func startRunsViewer(start string) bool {
	s := strings.TrimSpace(start)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	words := strings.Fields(s)
	if len(words) > 1 && words[0] == "exec" {
		words = words[1:]
	}
	return len(words) > 0 && filepath.Base(strings.Trim(words[0], `"'`)) == "issue-viewer"
}

// noViewerPaneLine is the one-line refusal when no pane runs the viewer.
func noViewerPaneLine(pid int, listErr error) string {
	looked := "a pane whose start command runs issue-viewer"
	if pid > 0 {
		looked = fmt.Sprintf("the pane holding pid %d from %s and %s", pid, viewerstate.Path(), looked)
	} else {
		looked = fmt.Sprintf("%s (%s names no live viewer)", looked, viewerstate.Path())
	}
	line := "viewer restart: no tmux pane runs the viewer (looked for " + looked + ")"
	if listErr != nil {
		line += "; tmux list-panes: " + strings.TrimSpace(listErr.Error())
	}
	return line
}

// listPanes reads every pane of the tmux server this process talks to.
func listPanes() ([]viewerPane, error) {
	out, err := tmuxRun("list-panes", "-a", "-F", agentrun.Format("pane_id", "pane_pid", "pane_dead", "session_name", "pane_start_command"))
	if err != nil {
		if msg := strings.TrimSpace(out); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	var panes []viewerPane
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := agentrun.SplitFields(line, 5)
		if len(f) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[1])
		p := viewerPane{ID: f[0], PID: pid, Dead: f[2] == "1", Session: f[3]}
		if len(f) == 5 {
			p.Start = f[4]
		}
		panes = append(panes, p)
	}
	return panes, nil
}

// paneNow reads one pane's pid and dead flag; gone when tmux no longer has it.
func paneNow(id string) (viewerPane, bool) {
	out, err := tmuxRun("display-message", "-p", "-t", id, agentrun.Format("pane_id", "pane_pid", "pane_dead"))
	if err != nil {
		return viewerPane{}, true
	}
	f := agentrun.SplitFields(out, 3)
	if len(f) < 3 || f[0] != id {
		return viewerPane{}, true
	}
	pid, _ := strconv.Atoi(f[1])
	return viewerPane{ID: id, PID: pid, Dead: f[2] == "1"}, false
}

// paneTail is the pane's last 15 non-empty lines, indented.
func paneTail(id string) string {
	out, err := tmuxRun("capture-pane", "-p", "-t", id)
	if err != nil {
		return "  (pane not readable)"
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, "  "+l)
		}
	}
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	if len(lines) == 0 {
		return "  (pane is empty)"
	}
	return strings.Join(lines, "\n")
}

func tmuxRun(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return string(out), err
}

// parentPID is pid's parent from ps; 0 when ps can't say.
func parentPID(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// httpAnswers reports whether the viewer at url answers HTTP below 500.
func httpAnswers(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url + "/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// roundSeconds prints d as seconds with one decimal.
func roundSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
}
