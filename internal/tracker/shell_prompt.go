package tracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ShellFacts is what the machine running the dispatch says about the shell a
// worker gets: the login shell's name, whether the tools are BSD's (macOS)
// and whether rtk, which rewrites some commands' output, is on PATH.
type ShellFacts struct {
	Shell string // basename of $SHELL, e.g. "zsh"
	BSD   bool
	RTK   bool
}

// DetectShell reads ShellFacts from this machine. Tests replace it.
var DetectShell = func() ShellFacts {
	_, err := exec.LookPath("rtk")
	return ShellFacts{
		Shell: filepath.Base(os.Getenv("SHELL")),
		BSD:   runtime.GOOS == "darwin",
		RTK:   err == nil,
	}
}

// ShellPromptSection is the "## Shell on this machine" part of a worker's
// briefing: the shell facts workers otherwise each lose a retry to, only the
// ones this machine has. Empty when none apply.
func ShellPromptSection(f ShellFacts) string {
	notes := ShellNotes(f)
	if len(notes) == 0 {
		return ""
	}
	return "\n\n## Shell on this machine\n\n" + strings.Join(notes, "\n") + "\n"
}

// ShellNotes are the section's bullets for f. The bullets under Shell notes
// in docs/getting-started.md are these lines (TestShellNotesDocMatches).
func ShellNotes(f ShellFacts) []string {
	var lines []string
	if f.Shell == "zsh" {
		lines = append(lines, "- zsh: `$VAR` holding a command doesn't word-split (use a shell function), `$B:h` is a modifier, and unquoted globs fail: quote them (`'*.go'`).")
	}
	if f.RTK {
		lines = append(lines,
			"- rtk shortens some output (`git log`, `make validate`; `go test -v` loses its test names and `t.Log` lines): when output feeds a commit message or a parser, or you need a test's log, use `/usr/bin/git`, `/usr/bin/grep` or `rtk proxy <cmd>` (`rtk proxy go test -v …`). `make validate` loses its verdict line: run it in the background with `make validate > <log> 2>&1` and watch the log. A `validate: red so far: <step>` line means that step is red: stop and fix it instead of waiting for the suite. The verdict is the last `validate:` line, `validate: PASS` or `validate: FAIL (<step>, <step>)` (on red, make's own error line comes after it).",
			"- `rtk grep` is ripgrep, and a plain `grep` can be rewritten to it: `--include` fails and prints a false \"0 matches\". Use `-g '*.go'`.",
			"- Never pipe or chain a command whose exit code decides the next step: `make validate | tail` shows tail's exit code, and the next command runs on red.",
			"- A \"no verdict\" from auto mode means a hook rewrote a call sent beside it: send that call alone.",
		)
	}
	if f.BSD {
		lines = append(lines, "- macOS BSD tools: `sed -i ''`, `script` has no `-c`, no `timeout`, `cp` is `/bin/cp`.")
	}
	return lines
}
