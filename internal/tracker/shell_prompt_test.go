package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The briefing carries the shell facts the machine has, eight lines at most,
// and nothing on a machine that has none of them.
func TestShellPromptSection(t *testing.T) {
	all := ShellPromptSection(ShellFacts{Shell: "zsh", BSD: true, RTK: true}, ProjectTests{CI: "make validate", Go: true})
	if !strings.HasPrefix(all, "\n\n## Shell on this machine\n\n") {
		t.Fatalf("missing heading: %q", all)
	}
	for _, want := range []string{"doesn't word-split", "quote them (`'*.go'`)", "`/usr/bin/git`", "rtk proxy", "`t.Log` lines", "`rtk proxy go test -v …`", "`make validate > \"$(git rev-parse --git-path validate.log)\" 2>&1`", "watch that file", "the worktree's own git dir", "a shared /tmp name", "`validate: red so far: <step>`", "stop and fix it", "The verdict is the last `validate:` line", "`rtk grep` is ripgrep", "`--include` fails", "false \"0 matches\"", "`-g '*.go'`", "Never pipe or chain a command whose exit code decides the next step", "\"no verdict\" from auto mode", "send that call alone", "`sed -i ''`", "no `-c`", "`/bin/cp`"} {
		if !strings.Contains(all, want) {
			t.Errorf("section missing %q: %q", want, all)
		}
	}
	if n := len(strings.Split(strings.TrimSpace(all), "\n")); n > 8 {
		t.Errorf("section is %d lines, want at most 8: %q", n, all)
	}

	// A machine with zsh and no rtk has real grep: the glob note must not
	// recommend a flag of either tool.
	zshOnly := ShellPromptSection(ShellFacts{Shell: "zsh"}, ProjectTests{})
	if strings.Contains(zshOnly, "--include") || strings.Contains(zshOnly, "-g '") {
		t.Errorf("zsh without rtk names a grep flag: %q", zshOnly)
	}

	linux := ShellPromptSection(ShellFacts{Shell: "bash"}, ProjectTests{})
	if linux != "" {
		t.Errorf("bash on Linux without rtk: got %q, want no section", linux)
	}
	bashMac := ShellPromptSection(ShellFacts{Shell: "bash", BSD: true}, ProjectTests{})
	if strings.Contains(bashMac, "zsh") || strings.Contains(bashMac, "rtk") || !strings.Contains(bashMac, "`sed -i ''`") {
		t.Errorf("bash on macOS: %q", bashMac)
	}
}

// A project whose CI is npm test and that has no go.mod gets its own command
// in the examples and none of this repo's make validate or Go advice.
func TestShellPromptSectionNamesTheProjectsCI(t *testing.T) {
	dir := t.TempDir()
	wf := &WorkflowConfig{CI: &CIConfig{Run: []CICommand{{Cmd: "npm test"}, {Cmd: "npm run e2e", Tier: "slow"}}}}
	p := ProjectTestsFor(dir, wf)
	if p != (ProjectTests{CI: "npm test"}) {
		t.Fatalf("ProjectTestsFor = %+v, want CI npm test, not Go", p)
	}
	s := ShellPromptSection(ShellFacts{Shell: "zsh", BSD: true, RTK: true}, p)
	for _, want := range []string{"`npm test`", "`npm test > <log> 2>&1`", "`npm test | tail` shows tail's exit code"} {
		if !strings.Contains(s, want) {
			t.Errorf("section missing %q: %s", want, s)
		}
	}
	for _, not := range []string{"make validate", "go test", "validate:", "'*.go'"} {
		if strings.Contains(s, not) {
			t.Errorf("section names %q for a non-Go npm project: %s", not, s)
		}
	}

	// No ci: command: generic wording, still no make validate.
	none := ShellPromptSection(ShellFacts{RTK: true}, ProjectTestsFor(dir, &WorkflowConfig{}))
	if strings.Contains(none, "make validate") || !strings.Contains(none, "a test command piped to `tail`") {
		t.Errorf("no ci: command: %s", none)
	}

	// go.mod makes it a Go project whatever its CI command.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ProjectTestsFor(dir, wf); !p.Go {
		t.Errorf("go.mod present, but Go = false")
	}
}

// A zsh machine's notes name the traps the retros keep hitting: printf for
// separators, a function over a loop, quoted globs, heredocs in files, no cd
// in a compound and a guarded rm target. A bash machine gets none of them.
func TestBriefingShellNotesCoverZshTraps(t *testing.T) {
	s := ShellPromptSection(ShellFacts{Shell: "zsh"}, ProjectTests{})
	for _, want := range []string{"`echo =====` fails", "`printf '%s\\n' '====='`", "a loop over a list of commands needs a shell function", "quote globs in a test command", "put a heredoc in a file", "`git -C <dir>`", "never `cd` in a compound command", "`\"${VAR:?}\"`"} {
		if !strings.Contains(s, want) {
			t.Errorf("zsh notes missing %q: %s", want, s)
		}
	}
	if bash := ShellPromptSection(ShellFacts{Shell: "bash", BSD: true}, ProjectTests{}); strings.Contains(bash, "`echo =====`") {
		t.Errorf("bash machine got the zsh traps: %s", bash)
	}
}
