package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// overlapStartProject is a git project where work/cli/sibling changes a file
// the CLI system owns and the issue's own branch is one commit behind main.
func overlapStartProject(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	proj, slug := makeDispatchPromptProject(t)
	root := t.TempDir()
	proj.WorkDir = root
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commitFile := func(path string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(path+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", path)
	}
	git("init", "-q", "-b", "main")
	commitFile("README.md")
	git("branch", "work/"+slug)
	git("checkout", "-q", "-b", "work/cli/sibling")
	commitFile("cmd/x.go")
	git("checkout", "-q", "main")
	commitFile("moved.go")

	text, err := os.ReadFile(proj.WorkflowFile)
	if err != nil {
		t.Fatal(err)
	}
	text = append(text, []byte("  - name: \"done\"\nworkbench:\n  systems:\n    CLI:\n      paths:\n        - cmd/\n")...)
	if err := os.WriteFile(proj.WorkflowFile, text, 0o644); err != nil {
		t.Fatal(err)
	}
	sibling := "---\ntitle: \"sibling\"\nstatus: \"in progress\"\nsystem: \"CLI\"\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "CLI", "sibling.md"), []byte(sibling), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
	return proj, slug
}

func TestStartAndBriefingNameBranchesOnTheSameFiles(t *testing.T) {
	proj, slug := overlapStartProject(t)
	wants := []string{
		"Your branch is 1 commit behind main. Merge it before editing anything: git merge main",
		"cli/sibling (in progress): 1 file: cmd/x.go",
		"--project " + proj.Slug + " ask cli/sibling",
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDispatchPrompt(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "## Open branches") {
		t.Errorf("briefing has no section:\n%s", stdout)
	}
	for _, want := range wants {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("briefing misses %q:\n%s", want, stdout)
		}
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runStart(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "== Open branches ==") {
		t.Errorf("start has no block:\n%s", stdout)
	}
	for _, want := range wants {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("start misses %q:\n%s", want, stdout)
		}
	}
}
