package tracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	var lines []string
	if f.Shell == "zsh" {
		lines = append(lines, "- zsh: `$VAR` holding a command doesn't word-split (use a shell function), `$B:h` is a modifier, and unquoted globs fail: write `--include='*.go'`.")
	}
	if f.RTK {
		lines = append(lines, "- rtk shortens some output (`git log`, `go test -v` test names): when output feeds a commit message or a parser, use `/usr/bin/git`, `/usr/bin/grep` or `rtk proxy <cmd>`.")
	}
	if f.BSD {
		lines = append(lines, "- macOS BSD tools: `sed -i ''`, `script` has no `-c`, no `timeout`, `cp` is `/bin/cp`.")
	}
	if len(lines) == 0 {
		return ""
	}
	s := "\n\n## Shell on this machine\n\n"
	for _, l := range lines {
		s += l + "\n"
	}
	return s
}
