package briefing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// heldRepo is a git repo whose main changed TestStub on 2026-11-16, after
// the issue's created day.
func heldRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(date, body, msg string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, "v"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "v", "stub_test.go"), []byte("package v\n\nimport \"testing\"\n\nfunc TestStub(t *testing.T) {\n\t"+body+"\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_COMMITTER_DATE", date+"T12:00:00")
		t.Setenv("GIT_AUTHOR_DATE", date+"T12:00:00")
		git("add", "-A")
		git("commit", "-q", "-m", msg)
	}
	git("init", "-q", "-b", "main")
	commit("2026-11-01", "t.Log(1)", "add the stub test")
	commit("2026-11-16", "t.Log(2)", "test(v): the stub goes through the helper")
	return root
}

func TestBriefingsSayWhatMainAlreadyDidToTheNamedTest(t *testing.T) {
	root := heldRepo(t)
	for _, fresh := range []bool{false, true} {
		wf := tracker.DefaultWorkflow()
		if fresh {
			wf = freshWorkflow(t, "in progress", 0)
		}
		proj, issue := stageIssue(t, "in progress")
		issue.Title, issue.Created = "TestStub flakes under load", "2026-11-15"
		p := BuildDispatchPrompt(proj, issue, wf, root, nil).Prompt
		for _, want := range []string{"## Already on main?", "test(v): the stub goes through the helper", "go test -count=1 -run '^TestStub$' ./v/"} {
			if !strings.Contains(p, want) {
				t.Errorf("fresh=%v briefing misses %q:\n%s", fresh, want, p)
			}
		}

		issue.Title = "Stub flakes under load"
		if p := BuildDispatchPrompt(proj, issue, wf, root, nil).Prompt; strings.Contains(p, "## Already on main?") {
			t.Errorf("fresh=%v: a brief naming no test got the section", fresh)
		}
	}
}

// Folded, the stage part keeps the first commit and points at start.
func TestMainHoldsPartFoldsToItsSummary(t *testing.T) {
	root := heldRepo(t)
	proj, issue := stageIssue(t, "in progress")
	issue.Title, issue.Created = "TestStub flakes under load", "2026-11-15"
	p := mainHoldsPart(stageInput{proj: proj, issue: issue, workDir: root})
	if p.text == "" || !strings.Contains(p.folded, "main may already hold it: ") || !strings.Contains(p.folded, "issue-cli --project proj start prints") {
		t.Errorf("part = %+v", p)
	}
}
