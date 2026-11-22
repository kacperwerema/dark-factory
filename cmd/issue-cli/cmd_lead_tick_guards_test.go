package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func guardTickLines(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "after install: the installed issue-cli holds") {
			got = append(got, line)
		}
	}
	return got
}

// A commit in the installed build that changed a guard (commit-msg,
// pre-push, leak-check) leaves the lead its after-install task on its
// own: the tick names the commit, the guards, each guard's live check and
// the tests: comment to post on the issue its Refs: names, once; a commit
// touching no guard file and one the build doesn't hold say nothing.
func TestLeadTickLeavesTheLeadAnAfterInstallTaskForAGuardChange(t *testing.T) {
	behind, fixes := 0, []string(nil)
	proj := installLagProject(t, &behind, &fixes)
	stubStart(t)
	root := filepath.Dir(proj.IssueDir)
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cmd/issue-cli/other.go", "package main\n")
	queueGit(t, root, "add", "-A")
	queueGit(t, root, "commit", "-q", "-m", "feat(cli): not a guard")
	write("cmd/issue-cli/cmd_commit_msg.go", "package main\n")
	write("internal/privacy/leaks.go", "package privacy\n")
	queueGit(t, root, "add", "-A")
	queueGit(t, root, "commit", "-q", "-m", "feat(guard): the hook refuses more\n\nRefs: workflow/the-guard-issue")
	installed := strings.TrimSpace(queueGit(t, root, "rev-parse", "HEAD"))
	write("internal/privacy/hook.go", "package privacy\n")
	queueGit(t, root, "add", "-A")
	queueGit(t, root, "commit", "-q", "-m", "fix(guard): not installed yet")
	tracker.BuildVersion = installed

	now := time.Now()
	out, err := leadCmd(t, proj, now, "tick")
	if err != nil {
		t.Fatalf("tick: %v\n%s", err, out)
	}
	got := guardTickLines(out)
	if len(got) != 1 {
		t.Fatalf("want one after-install line for the installed guard commit:\n%s", out)
	}
	line := got[0]
	for _, want := range []string{installed[:8], "(feat(guard): the hook refuses more)", "the commit-msg, leak-check guard",
		"commit-msg: in a scratch clone", "leak-check: issue-cli workflow commit-msg",
		`issue-cli comment workflow/the-guard-issue --text "tests: after install of ` + installed + `: commit-msg, leak-check pass|fail: …"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("line lacks %q:\n%s", want, line)
		}
	}
	if strings.Contains(out, "not installed yet") || strings.Contains(out, "not a guard") {
		t.Fatalf("a commit the build lacks or one touching no guard was named:\n%s", out)
	}

	out, _ = leadCmd(t, proj, now.Add(time.Minute), "tick")
	if got := guardTickLines(out); len(got) != 0 {
		t.Fatalf("second tick repeated the task:\n%s", out)
	}
}
