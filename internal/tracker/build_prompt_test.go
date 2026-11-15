package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setBuildVersion(t *testing.T, v string) {
	t.Helper()
	old := BuildVersion
	t.Cleanup(func() { BuildVersion = old })
	BuildVersion = v
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
