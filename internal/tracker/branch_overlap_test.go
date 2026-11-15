package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overlapProject is a project over the repo whose CLI system owns cli/.
func overlapProject(t *testing.T, r *gitRepo) *Project {
	t.Helper()
	wf := filepath.Join(t.TempDir(), "workflow.yaml")
	text := "statuses:\n  - name: \"in progress\"\n  - name: \"done\"\nworkbench:\n  systems:\n    CLI:\n      paths:\n        - cli/\n"
	if err := os.WriteFile(wf, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Project{Slug: "p", WorkflowFile: wf, WorkDir: r.root}
}

// branchWith cuts branch from start and commits the files on it.
func (r *gitRepo) branchWith(branch, start string, files ...string) {
	r.t.Helper()
	r.git("checkout", "-q", "-b", branch, start)
	for _, f := range files {
		r.write(f, branch+"\n")
	}
	r.commit(branch)
	r.git("checkout", "-q", "main")
}

func TestOverlapsNameBranchesOnTheSystemsFiles(t *testing.T) {
	r := newGitRepo(t)
	r.branchWith("work/cli/sibling", "main", "cli/x.go", "cli/CHANGELOG.md", "elsewhere.go")
	r.branchWith("work/api/other", "main", "api/y.go")
	r.branchWith("work/cli/shipped", "main", "cli/z.go")
	r.worktree("work/cli/mine", "main") // no commits yet: a fresh dispatch

	mine := &Issue{Slug: "cli/mine", System: "CLI", Status: "in progress"}
	issues := []*Issue{mine,
		{Slug: "cli/sibling", Status: "in progress"},
		{Slug: "api/other", Status: "in progress"},
		{Slug: "cli/shipped", Status: "done"},
	}
	o := IssueBranchOverlaps(overlapProject(t, r), r.root, mine, issues)
	if o == nil || len(o.Others) != 1 {
		t.Fatalf("overlaps = %+v", o)
	}
	got := o.Others[0]
	if got.Branch != "work/cli/sibling" || got.Status != "in progress" || got.Own != 0 || strings.Join(got.Files, ",") != "cli/x.go" {
		t.Errorf("overlap = %+v", got)
	}
	text := strings.Join(o.Lines(mine.Slug), "\n")
	for _, want := range []string{"1 open branch changes the same files:", "cli/sibling (in progress): 1 file: cli/x.go", `issue-cli ask cli/sibling "..."`} {
		if !strings.Contains(text, want) {
			t.Errorf("lines miss %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "behind") {
		t.Errorf("a branch level with main got a merge line:\n%s", text)
	}
}

func TestOverlapsNameBranchesOnAResumedBranchsFiles(t *testing.T) {
	r := newGitRepo(t)
	// No system maps these files: only the resumed branch's diff links them.
	r.branchWith("work/ui/mine", "main", "page.go", "only-mine.go", "CHANGELOG.md")
	r.branchWith("work/ui/sibling", "main", "page.go", "only-sibling.go", "CHANGELOG.md")

	mine := &Issue{Slug: "ui/mine", System: "UI", Status: "in progress"}
	issues := []*Issue{mine, {Slug: "ui/sibling", Status: "in progress"}}
	o := IssueBranchOverlaps(overlapProject(t, r), r.root, mine, issues)
	if o == nil || len(o.Others) != 1 {
		t.Fatalf("overlaps = %+v", o)
	}
	if got := o.Others[0]; got.Own != 1 || strings.Join(got.Files, ",") != "page.go" {
		t.Errorf("overlap = %+v", got)
	}
	if text := strings.Join(o.Lines(mine.Slug), "\n"); !strings.Contains(text, "1 file, 1 also on your branch: page.go") {
		t.Errorf("lines:\n%s", text)
	}
}

func TestAResumedBranchBehindMainIsToldToMergeFirst(t *testing.T) {
	r := newGitRepo(t)
	r.branchWith("work/ui/mine", "main", "page.go")
	r.write("moved.go", "m\n")
	r.commit("main moves")
	r.write("moved2.go", "m\n")
	r.commit("main moves again")

	mine := &Issue{Slug: "ui/mine", System: "UI", Status: "in progress"}
	o := IssueBranchOverlaps(overlapProject(t, r), r.root, mine, []*Issue{mine})
	if o == nil || o.Behind != 2 {
		t.Fatalf("overlaps = %+v", o)
	}
	want := `Your branch is 2 commits behind main. Merge it before editing anything: git merge main -m "chore(merge): main into ui/mine"`
	if lines := o.Lines(mine.Slug); len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q", lines)
	}
}

func TestNoOverlapSaysNothing(t *testing.T) {
	r := newGitRepo(t)
	r.branchWith("work/ui/mine", "main", "page.go")
	r.branchWith("work/ui/sibling", "main", "other.go")
	mine := &Issue{Slug: "ui/mine", System: "UI", Status: "in progress"}
	issues := []*Issue{mine, {Slug: "ui/sibling", Status: "in progress"}}
	proj := overlapProject(t, r)
	o := IssueBranchOverlaps(proj, r.root, mine, issues)
	if o != nil {
		t.Fatalf("overlaps = %+v", o)
	}
	if got := BranchOverlapPromptSection(o, mine.Slug); got != "" {
		t.Errorf("section = %q", got)
	}
	if got := IssueBranchOverlaps(proj, t.TempDir(), mine, issues); got != nil {
		t.Errorf("no git: %+v", got)
	}
}

func TestOverlapsShowTheNearestAndCount(t *testing.T) {
	o := &BranchOverlaps{Main: "main"}
	for i := range overlapsShown + 2 {
		o.Others = append(o.Others, BranchOverlap{Branch: fmt.Sprintf("work/cli/b%d", i), IssueSlug: fmt.Sprintf("cli/b%d", i), Files: []string{"a", "b", "c", "d", "e"}})
	}
	text := strings.Join(o.Lines("cli/mine"), "\n")
	for _, want := range []string{"10 open branches change", "5 files: a, b, c, +2 more", "… 2 more: see the Branches tab"} {
		if !strings.Contains(text, want) {
			t.Errorf("lines miss %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "cli/b9:") {
		t.Errorf("past the cap:\n%s", text)
	}
}
