package tracker

import (
	"strings"
	"testing"
)

// The briefing carries the shell facts the machine has, eight lines at most,
// and nothing on a machine that has none of them.
func TestShellPromptSection(t *testing.T) {
	all := ShellPromptSection(ShellFacts{Shell: "zsh", BSD: true, RTK: true})
	if !strings.HasPrefix(all, "\n\n## Shell on this machine\n\n") {
		t.Fatalf("missing heading: %q", all)
	}
	for _, want := range []string{"doesn't word-split", "quote them (`'*.go'`)", "`/usr/bin/git`", "rtk proxy", "`t.Log` lines", "`rtk proxy go test -v …`", "`make validate > <log> 2>&1`", "watch the log", "`validate: red so far: <step>`", "stop and fix it", "The verdict is the last `validate:` line", "`rtk grep` is ripgrep", "`--include` fails", "false \"0 matches\"", "`-g '*.go'`", "Never pipe or chain a command whose exit code decides the next step", "\"no verdict\" from auto mode", "send that call alone", "`sed -i ''`", "no `-c`", "`/bin/cp`"} {
		if !strings.Contains(all, want) {
			t.Errorf("section missing %q: %q", want, all)
		}
	}
	if n := len(strings.Split(strings.TrimSpace(all), "\n")); n > 8 {
		t.Errorf("section is %d lines, want at most 8: %q", n, all)
	}

	// A machine with zsh and no rtk has real grep: the glob note must not
	// recommend a flag of either tool.
	zshOnly := ShellPromptSection(ShellFacts{Shell: "zsh"})
	if strings.Contains(zshOnly, "--include") || strings.Contains(zshOnly, "-g '") {
		t.Errorf("zsh without rtk names a grep flag: %q", zshOnly)
	}

	linux := ShellPromptSection(ShellFacts{Shell: "bash"})
	if linux != "" {
		t.Errorf("bash on Linux without rtk: got %q, want no section", linux)
	}
	bashMac := ShellPromptSection(ShellFacts{Shell: "bash", BSD: true})
	if strings.Contains(bashMac, "zsh") || strings.Contains(bashMac, "rtk") || !strings.Contains(bashMac, "`sed -i ''`") {
		t.Errorf("bash on macOS: %q", bashMac)
	}
}
