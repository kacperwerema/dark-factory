package tracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// engineRepo is a sandbox engine repository on main: cmd/issue-cli, v0.1.0
// tagged, then two commits. It returns the repo and HEAD's describe.
func engineRepo(t *testing.T) (root, head string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "tag.gpgSign=false", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "cmd", "issue-cli", name), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", name)
	}
	git("init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(root, "cmd", "issue-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit("main.go")
	git("tag", "-a", "v0.1.0", "-m", "v0.1.0")
	commit("a.go")
	commit("b.go")
	return root, git("describe", "--tags", "--always")
}

func setBuildVersion(t *testing.T, v string) {
	t.Helper()
	old := BuildVersion
	t.Cleanup(func() { BuildVersion = old })
	BuildVersion = v
}

// A briefing built while the installed build is behind main tells the
// worker to build its own issue-cli in the worktree and never to make
// install; one built at main, from a dev build or off the engine says
// nothing.
func TestBuildDispatchPrompt_BuildBehindMain(t *testing.T) {
	root, head := engineRepo(t)
	issue := &Issue{Slug: "factory/fix", Title: "Fix", Status: "in progress"}
	proj := &Project{Slug: "dark-factory"}

	setBuildVersion(t, "v0.1.0")
	p := BuildDispatchPrompt(proj, issue, nil, root, nil).Prompt
	for _, want := range []string{
		"## Your issue-cli build",
		"The installed issue-cli (v0.1.0) is 2 commits behind main",
		"`go build -o ./issue-cli ./cmd/issue-cli`",
		"`./issue-cli --project dark-factory <command>`",
		"`./issue-cli --project dark-factory check-script <slug> --yes` runs your guided check scripts with $ISSUE_CLI",
		"never make install",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("behind main: briefing missing %q:\n%s", want, BuildPromptSection(proj, root, nil))
		}
	}

	// A dirty build counts from its base commit.
	setBuildVersion(t, "v0.1.0-dirty")
	if s := BuildPromptSection(proj, root, nil); !strings.Contains(s, "2 commits behind main") {
		t.Errorf("dirty build: %q", s)
	}

	for name, v := range map[string]string{"at main": head, "dev build": "dev", "unset": "", "unknown describe": "v9.9.9"} {
		setBuildVersion(t, v)
		if p := BuildDispatchPrompt(proj, issue, nil, root, nil).Prompt; strings.Contains(p, "## Your issue-cli build") || strings.Contains(p, "make install") {
			t.Errorf("%s (%q): briefing has the build section:\n%s", name, v, p)
		}
	}

	// Not the engine's repository: nothing to build.
	setBuildVersion(t, "v0.1.0")
	if err := os.RemoveAll(filepath.Join(root, "cmd")); err != nil {
		t.Fatal(err)
	}
	if s := BuildPromptSection(proj, root, nil); s != "" {
		t.Errorf("a project without cmd/issue-cli: %q", s)
	}
}

// The branch the build is compared with is ci.branch when CI is on.
func TestBuildPromptSection_CIBranch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "issue-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := BuildBehind
	t.Cleanup(func() { BuildBehind = old })
	var asked string
	BuildBehind = func(_, branch, _ string) int { asked = branch; return 1 }
	setBuildVersion(t, "v0.1.0")
	wf := &WorkflowConfig{CI: &CIConfig{Branch: "trunk", Run: []CICommand{{Cmd: "true"}}}}
	s := BuildPromptSection(nil, root, wf)
	if asked != "trunk" || !strings.Contains(s, "1 commit behind trunk") || strings.Contains(s, "--project") {
		t.Fatalf("branch %q, section %q", asked, s)
	}
}
