package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// postmortemBriefRepo is a git repo holding an epic (x/epic) with two
// children: x/one keeps its work branch, x/gone's was merged and deleted.
// The epic's own branch exists. It returns the project and its root.
func postmortemBriefRepo(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	dir := realTempDir(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0",
			"-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	sys := filepath.Join(dir, "issues", "X")
	os.MkdirAll(sys, 0o755)
	wf := filepath.Join(dir, "workflow.yaml")
	os.WriteFile(wf, []byte("statuses:\n  - name: \"idea\"\n  - name: \"done\"\ntransitions:\n  - from: \"idea\"\n    to: \"done\"\n"), 0o644)
	write := func(name, front string) {
		os.WriteFile(filepath.Join(sys, name+".md"), []byte("---\ntitle: \""+name+"\"\nstatus: \"done\"\nsystem: \"X\"\n"+front+"---\n\nbody\n"), 0o644)
	}
	write("epic", "type: \"epic\"\n")
	write("one", "parent: \"x/epic\"\n")
	write("gone", "parent: \"x/epic\"\n")
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	git("branch", "work/x/epic")
	git("branch", "work/x/one")
	return &tracker.Project{Name: "test", Slug: "test", IssueDir: filepath.Join(dir, "issues"), WorkflowFile: wf}, dir
}

func postmortemBriefOf(t *testing.T, proj *tracker.Project, slug string) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runPostmortem(ctx, []string{"brief", slug}); err != nil {
		t.Fatalf("postmortem brief %s: %v", slug, err)
	}
	return stdout.String()
}

// AC1: a child whose work branch is gone gets its commits on main by slug
// and no branch.
func TestPostmortemBriefChildWithoutBranch(t *testing.T) {
	proj, dir := postmortemBriefRepo(t)
	got := postmortemBriefOf(t, proj, "x/epic")
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "- x/gone:") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no x/gone line:\n%s", got)
	}
	if strings.Contains(line, "branch") {
		t.Errorf("a gone branch is named: %s", line)
	}
	if want := `commits: git -C ` + dir + ` log --grep x/gone main`; !strings.Contains(line, want) {
		t.Errorf("line lacks %q: %s", want, line)
	}
	// A plain issue whose branch is gone: its commit line reads main.
	got = postmortemBriefOf(t, proj, "x/gone")
	if want := `- Commits: git -C ` + dir + ` log --stat --grep x/gone main`; !strings.Contains(got, want) {
		t.Errorf("brief lacks %q:\n%s", want, got)
	}
	if strings.Contains(got, "work/x/gone") {
		t.Errorf("brief names the gone branch:\n%s", got)
	}
}

// AC2: the epic's and the issue's commit line leaves out what merges of
// main brought in.
func TestPostmortemBriefFirstParentRange(t *testing.T) {
	proj, dir := postmortemBriefRepo(t)
	for _, slug := range []string{"x/epic", "x/one"} {
		got := postmortemBriefOf(t, proj, slug)
		if want := "- Commits: git -C " + dir + " log --first-parent --stat work/" + slug + " "; !strings.Contains(got, want) {
			t.Errorf("%s: brief lacks %q:\n%s", slug, want, got)
		}
	}
}

// AC3: a child whose branch still exists is named.
func TestPostmortemBriefChildWithBranch(t *testing.T) {
	proj, _ := postmortemBriefRepo(t)
	got := postmortemBriefOf(t, proj, "x/epic")
	if !strings.Contains(got, "· branch work/x/one\n") {
		t.Errorf("brief lacks x/one's branch:\n%s", got)
	}
}
