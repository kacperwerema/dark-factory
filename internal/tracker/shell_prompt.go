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

// ProjectTests is how the project a worker works on runs its checks: the
// default-tier ci: command (empty without one) and whether it is a Go module.
// The briefing's Shell and Local CI sections name these, never another
// project's make validate or go test.
type ProjectTests struct {
	CI string // e.g. "make validate", "npm test"; "" when ci: has none
	Go bool   // go.mod in the work dir, or the CI command runs go test
}

// ProjectTestsFor reads ProjectTests from the workflow's ci: block and the
// work dir.
func ProjectTestsFor(workDir string, wf *WorkflowConfig) ProjectTests {
	var p ProjectTests
	if wf != nil && wf.CI != nil {
		var cmds []string
		for _, c := range wf.CI.Run {
			if cmd := strings.TrimSpace(c.Cmd); cmd != "" && c.TierName() == CIDefaultTier {
				cmds = append(cmds, cmd)
			}
		}
		p.CI = strings.Join(cmds, " && ")
	}
	if workDir != "" {
		if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err == nil {
			p.Go = true
		}
	}
	if strings.Contains(p.CI, "go test") {
		p.Go = true
	}
	return p
}

// validate reports whether the project's CI is make validate, whose verdict
// lines the notes may name.
func (p ProjectTests) validate() bool {
	return strings.Contains(p.CI, "make validate")
}

// glob is the quoted glob the notes use as an example.
func (p ProjectTests) glob() string {
	if p.Go {
		return "'*.go'"
	}
	return "'*.md'"
}

// ShellPromptSection is the "## Shell on this machine" part of a worker's
// briefing: the shell facts workers otherwise each lose a retry to, only the
// ones this machine has, with the project's own commands as examples. Empty
// when none apply.
func ShellPromptSection(f ShellFacts, p ProjectTests) string {
	notes := ShellNotes(f, p)
	if len(notes) == 0 {
		return ""
	}
	return "\n\n## Shell on this machine\n\n" + strings.Join(notes, "\n") + "\n"
}

// ShellNotes are the section's bullets for f and p. The bullets under Shell
// notes in docs/getting-started.md are these lines for this repo (zsh, macOS,
// rtk, make validate, Go: TestShellNotesDocMatchesBriefing).
func ShellNotes(f ShellFacts, p ProjectTests) []string {
	var lines []string
	if f.Shell == "zsh" {
		lines = append(lines, "- zsh: `$VAR` holding a command doesn't word-split (use a shell function), `$B:h` is a modifier, and unquoted globs fail: quote them (`"+p.glob()+"`)"+zshTraps)
	}
	if f.RTK {
		lines = append(lines,
			rtkNote(p),
			"- `rtk grep` is ripgrep, and a plain `grep` can be rewritten to it: `--include` fails and prints a false \"0 matches\". Use `-g "+p.glob()+"`.",
			pipeNote(p),
			"- A \"no verdict\" from auto mode means a hook rewrote a call sent beside it: send that call alone.",
		)
	}
	if f.BSD {
		lines = append(lines, "- macOS BSD tools: `sed -i ''`, `script` has no `-c`, no `timeout`, `cp` is `/bin/cp`.")
	}
	return lines
}

// rtkNote is the rtk bullet: what it shortens, how to get the raw output,
// and how to run the project's CI command so its verdict survives.
func rtkNote(p ProjectTests) string {
	var shortens []string
	shortens = append(shortens, "`git log`")
	if p.CI != "" {
		shortens = append(shortens, "`"+p.CI+"`")
	}
	what := strings.Join(shortens, ", ")
	proxyExample := ""
	if p.Go {
		what += "; `go test -v` loses its test names and `t.Log` lines"
		proxyExample = " (`rtk proxy go test -v …`)"
	}
	s := "- rtk shortens some output (" + what + "): when output feeds a commit message or a parser, or you need a test's log, use `/usr/bin/git`, `/usr/bin/grep` or `rtk proxy <cmd>`" + proxyExample + "."
	switch {
	case p.validate():
		s += " `make validate` loses its verdict line: run it in the background with `make validate > <log> 2>&1` and watch the log. A `validate: red so far: <step>` line means that step is red: stop and fix it instead of waiting for the suite. The verdict is the last `validate:` line, `validate: PASS` or `validate: FAIL (<step>, <step>)` (on red, make's own error line comes after it)."
	case p.CI != "":
		s += " Run `" + p.CI + " > <log> 2>&1` and read the log, not rtk's summary of it."
	}
	return s
}

// pipeNote is the bullet against piping a command whose exit code matters,
// with the project's CI command as the example.
func pipeNote(p ProjectTests) string {
	example := "a test command piped to `tail` shows tail's exit code"
	if p.CI != "" {
		example = "`" + p.CI + " | tail` shows tail's exit code"
	}
	return "- Never pipe or chain a command whose exit code decides the next step: " + example + ", and the next command runs on red."
}

// zshTraps ends the zsh bullet: the traps workers' retros keep naming
// (TestBriefingShellNotesCoverZshTraps). One bullet, so the section stays
// within its eight lines.
const zshTraps = ". Also: a word starting with `=` is a command lookup (`echo =====` fails), so print separators with `printf '%s\\n' '====='`; a loop over a list of commands needs a shell function, since `$t` stays one word; quote globs in a test command's arguments, or it fails or tests less than you think; put a heredoc in a file and run that instead of nesting heredocs; keep the working directory: use `git -C <dir>` or absolute paths, never `cd` in a compound command; write an `rm` target held in a variable as `\"${VAR:?}\"`."
