package agentrun

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// agentWrapperMark is what AgentCommand's wrapper shell carries in its
// arguments while the agent runs. When the agent exits the wrapper execs a
// plain login shell, and the mark is gone from the pane's process.
const agentWrapperMark = "trap : INT; "

// shells are the pane commands that count as a shell.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true}

// LaunchSettle is how long a launch is watched for an agent that exits at
// once; launchSettlePoll how often. An agent that dies later is past this
// check: the Crew pages, push and Reprompt see it (BareShell).
var (
	LaunchSettle     = 3 * time.Second
	launchSettlePoll = 250 * time.Millisecond
)

// diedAtLaunch reports whether target's session is gone or its pane turns
// into a bare shell within settle; settle 0 is one look.
func diedAtLaunch(target string, settle time.Duration) bool {
	deadline := time.Now().Add(settle)
	for {
		if !HasSession(target) || BareShell(target) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(launchSettlePoll)
	}
}

// paneTailLines and paneTailChars cap what a dead launch's report quotes.
const (
	paneTailLines = 3
	paneTailChars = 300
)

// paneTail is the last non-empty lines of target's pane on one line; the
// shell's own error for an agent that never started is among them. "" when
// the pane can't be read.
func paneTail(target string) string {
	pane, err := CapturePane(target)
	if err != nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(pane, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > paneTailLines {
		lines = lines[len(lines)-paneTailLines:]
	}
	tail := []rune(strings.Join(lines, " | "))
	if len(tail) > paneTailChars {
		tail = tail[len(tail)-paneTailChars:]
	}
	return string(tail)
}

// BareShell reports whether target's pane runs only a shell: no agent
// wrapper (AgentCommand's, which holds while the agent starts and runs)
// and no child process. That is what a launch leaves when its agent
// exited at once or never started (an empty command, "permission
// denied"), and what an agent leaves that dies any time later: the session
// is there but nothing works in it. A page over many agents asks
// Snapshot.BareShell instead. A var so tests stub it.
var BareShell = defaultBareShell

func defaultBareShell(target string) bool {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, Format("pane_pid", "pane_current_command")).Output()
	if err != nil {
		return false
	}
	f := SplitFields(strings.TrimSpace(string(out)), 2)
	if len(f) != 2 {
		return false
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil || !shellCommand(f[1]) {
		return false
	}
	for _, p := range ProcessTable() {
		if p.PPID == pid {
			return false
		}
	}
	return bareShellPane(pid, f[1], false)
}

// shellCommand reports whether a pane's current command is a shell.
func shellCommand(command string) bool {
	return shells[strings.TrimPrefix(filepath.Base(command), "-")]
}

// bareShellPane is the rule both looks share: the pane's process is a shell,
// has no child and is not AgentCommand's wrapper. The arguments are read last
// and only for a pane the rest leaves standing, so a page over many agents
// runs ps for almost none of them.
func bareShellPane(pid int, command string, hasChild bool) bool {
	if pid <= 0 || hasChild || !shellCommand(command) {
		return false
	}
	args, err := PaneArgs(pid)
	return err == nil && !strings.Contains(args, agentWrapperMark)
}

// PaneArgs reads a process's command line; a var so tests can stub it.
var PaneArgs = func(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	return string(out), err
}

// paneWorkingMark is what Claude Code and Codex print on their working line.
const paneWorkingMark = "esc to interrupt"

// PaneWorking reports whether target's agent is in the middle of a turn: the
// working line sits in the pane's last lines non-empty lines. A pane that
// can't be read is not working.
func PaneWorking(target string, lines int) bool {
	out, err := CapturePane(target)
	if err != nil {
		return false
	}
	var tail []string
	all := strings.Split(out, "\n")
	for i := len(all) - 1; i >= 0 && len(tail) < lines; i-- {
		if l := strings.TrimSpace(all[i]); l != "" {
			tail = append(tail, l)
		}
	}
	return strings.Contains(strings.Join(tail, "\n"), paneWorkingMark)
}
