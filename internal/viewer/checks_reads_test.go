package viewer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestPagesReadEachCheckScriptOnce: the pages that count owner checks ask,
// for every issue in owner-test, which worktree holds its scripts and which
// are due. On a board with N such issues and M worktrees, two identical
// requests read each script at most once (it used to be once per issue per
// worktree per request).
func TestPagesReadEachCheckScriptOnce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	const issuesN, worktreesM = 6, 4
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, old, old)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write("workflow.yaml", "statuses:\n  - name: \"testing\"\n  - name: \"human-testing\"\n  - name: \"done\"\n")
	for i := range issuesN {
		write(fmt.Sprintf("issues/CLI/i%d.md", i), fmt.Sprintf("---\ntitle: \"I%d\"\nstatus: \"human-testing\"\n---\n\nBody.\n", i))
	}
	git("init", "-q", "-b", "main")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	write("scripts/checks/main-only.sh", "#!/usr/bin/env bash\n# issue: cli/elsewhere\n")
	for w := range worktreesM {
		rel := fmt.Sprintf(".worktrees/w%d", w)
		git("worktree", "add", "-q", "-b", fmt.Sprintf("cli/w%d", w), filepath.Join(root, rel))
		// Each worktree holds one issue's script and two that name no
		// issue on this board.
		write(rel+fmt.Sprintf("/scripts/checks/i%d.sh", w), fmt.Sprintf("#!/usr/bin/env bash\n# issue: cli/i%d\n", w))
		write(rel+"/scripts/checks/other-a.sh", "#!/usr/bin/env bash\n# issue: cli/gone-a\n")
		write(rel+"/scripts/checks/other-b.sh", "#!/usr/bin/env bash\n# issue: cli/gone-b\n")
	}
	proj := tracker.Project{Name: "Checks", Slug: "chk", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}

	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	var body string
	for range 2 {
		_, body = getResp(t, ts.URL+"/p/chk/needs-you.json")
	}
	for w := range worktreesM {
		if !strings.Contains(body, fmt.Sprintf(`"cli/i%d"`, w)) {
			t.Errorf("needs-you.json has no checks row for cli/i%d:\n%s", w, body)
		}
	}
	reads := checks.ScriptReads(root)
	if want := 1 + 3*worktreesM; len(reads) != want {
		t.Errorf("scripts read = %d, want every one of the %d once: %v", len(reads), want, reads)
	}
	for path, n := range reads {
		if n > 1 {
			t.Errorf("%s read %d times over two requests, want at most once", path, n)
		}
	}
}
