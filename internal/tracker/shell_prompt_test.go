package tracker

import (
	"strings"
	"testing"
)

// The briefing carries the shell facts the machine has, five lines at most,
// and nothing on a machine that has none of them.
func TestShellPromptSection(t *testing.T) {
	all := ShellPromptSection(ShellFacts{Shell: "zsh", BSD: true, RTK: true})
	if !strings.HasPrefix(all, "\n\n## Shell on this machine\n\n") {
		t.Fatalf("missing heading: %q", all)
	}
	for _, want := range []string{"doesn't word-split", "`--include='*.go'`", "`/usr/bin/git`", "rtk proxy", "`sed -i ''`", "no `-c`", "`/bin/cp`"} {
		if !strings.Contains(all, want) {
			t.Errorf("section missing %q: %q", want, all)
		}
	}
	if n := len(strings.Split(strings.TrimSpace(all), "\n")); n > 5 {
		t.Errorf("section is %d lines, want at most 5: %q", n, all)
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

func TestBuildDispatchPrompt_ShellSection(t *testing.T) {
	old := DetectShell
	t.Cleanup(func() { DetectShell = old })
	issue := &Issue{Slug: "api/fix", Title: "Fix", Status: "in progress"}

	DetectShell = func() ShellFacts { return ShellFacts{Shell: "zsh", BSD: true, RTK: true} }
	if p := BuildDispatchPrompt(&Project{Slug: "proj"}, issue, nil, "/w", nil).Prompt; !strings.Contains(p, ShellPromptSection(DetectShell())) {
		t.Fatalf("prompt missing the shell section: %q", p)
	}
	DetectShell = func() ShellFacts { return ShellFacts{Shell: "bash"} }
	if p := BuildDispatchPrompt(&Project{Slug: "proj"}, issue, nil, "/w", nil).Prompt; strings.Contains(p, "## Shell on this machine") {
		t.Fatalf("prompt has a shell section on a machine with no facts: %q", p)
	}
}
