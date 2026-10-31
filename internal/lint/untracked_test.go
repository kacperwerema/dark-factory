package lint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestUntrackedDepartmentFiles(t *testing.T) {
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	env := lintProject(t, minimalWorkflow, nil)
	root := filepath.Dir(env.Project.IssueDir)
	env.Project.WorkDir = root
	d := deptMarketing()
	d.Prompt = "departments/marketing/role.md"
	d.Jobs = []tracker.Job{{ID: "w", Every: "week", Brief: "departments/marketing/jobs/w.md"}, {ID: "m", Every: "week", Brief: "builtin:memory-consolidate"}}
	env.Project.Departments = []tracker.Department{d}
	for _, f := range []string{"workflows/marketing.yaml", "departments/marketing/role.md", "departments/marketing/jobs/w.md", "departments/marketing/brand.md", "departments/marketing/role.template.md", "departments/marketing/email.template.md"} {
		p := filepath.Join(root, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x\n"), 0o644)
	}

	// Not a git repository: nothing to commit to, no finding.
	if rep, _ := Run(env, "untracked"); rep.Warnings != 0 {
		t.Fatalf("outside git: %+v", rep.Findings)
	}

	gitRun := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init", "-q", "-b", "main")
	rep, err := Run(env, "untracked")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Warnings != 2 || !strings.Contains(rep.Findings[1].Message, "comparison copies from department update --side left in place: departments/marketing/role.template.md") || rep.Findings[1].Fix != "rm -- "+filepath.ToSlash(filepath.Join(root, "departments/marketing/role.template.md")) {
		t.Fatalf("untracked: %+v", rep.Findings)
	}
	f := rep.Findings[0]
	if f.Check != "untracked" || !strings.Contains(f.Message, "5 department file(s) git does not track: departments/marketing/brand.md, departments/marketing/email.template.md, departments/marketing/jobs/w.md, departments/marketing/role.md, workflows/marketing.yaml") || !strings.Contains(f.Fix, "git -C") {
		t.Fatalf("finding = %+v", f)
	}

	os.Remove(filepath.Join(root, "departments", "marketing", "role.template.md"))
	gitRun("add", "departments", "workflows")
	gitRun("commit", "-q", "-m", "chore: departments")
	if rep, _ := Run(env, "untracked"); rep.Warnings != 0 {
		t.Fatalf("after commit: %+v", rep.Findings)
	}
}
