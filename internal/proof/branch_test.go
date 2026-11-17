package proof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// moveBranch renames the fixture's work/<slug> to name and checks it out in
// root/.worktrees/<folder>, the way a worker in a project without
// worktree: true does by hand. It returns the worktree.
func moveBranch(t *testing.T, f fixture, name, folder string) string {
	t.Helper()
	gitAt(t, f.root, t0, "branch", "-m", "work/"+f.issue.Slug, name)
	wt := filepath.Join(f.root, ".worktrees", folder)
	gitAt(t, f.root, t0, "worktree", "add", "-q", wt, name)
	return wt
}

func writeHandoff(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sameDir(t *testing.T, got, want string) bool {
	t.Helper()
	g, err1 := filepath.EvalSymlinks(got)
	w, err2 := filepath.EvalSymlinks(want)
	return err1 == nil && err2 == nil && g == w
}

// Workers without worktree: true make their own worktree under a short name: prove reads
// its commits when the worktree's HANDOFF.md names the issue, and the
// worktree's folder is the one prove --run and the shot lookup use.
func TestProveReadsAWorktreeOnAShortBranch(t *testing.T) {
	f := newFixture(t, "TestFoo")
	wt := moveBranch(t, f, "work/short-name", "short-name")
	writeHandoff(t, wt, "# HANDOFF: "+f.issue.Slug+"\n\n## Done\n- the test\n")

	r := Collect(f.root, f.issue, testsComment(t0.Add(150*time.Minute)))
	if r.RefName != "work/short-name" || r.Ref != gitAt(t, f.root, t0, "rev-parse", "work/short-name") {
		t.Fatalf("ref = %q (%s), want work/short-name's head", r.RefName, r.Ref)
	}
	if len(r.Missing) != 0 {
		t.Errorf("missing = %q, want none", r.Missing)
	}
	if c := criterion(t, r, "AC1"); !c.Proven() {
		t.Errorf("AC1 = %+v, want the test trailer on work/short-name proven", c.Links)
	}
	if c := criterion(t, r, "AC2"); !c.Proven() {
		t.Errorf("AC2 = %+v, want the commit trailer on work/short-name proven", c.Links)
	}
	at := FindIssueRef(f.root, f.issue, nil)
	if !sameDir(t, at.Worktree, wt) {
		t.Errorf("worktree = %q, want %s", at.Worktree, wt)
	}
	if sha, name, err := Reference(f.root, f.issue); err != nil || name != "work/short-name" || sha != "" {
		t.Errorf("Reference = %q, %q, %v; want the walk on work/short-name (no commit references the issue)", sha, name, err)
	}
}

// The lookup's order: the branch: field, then a worktree branch ending in
// the slug, which beats a HANDOFF.md that names it.
func TestIssueBranchOrder(t *testing.T) {
	t.Run("branch ends in the slug", func(t *testing.T) {
		f := newFixture(t, "TestFoo")
		wt := moveBranch(t, f, "feature/prove-me", "pm")
		other := filepath.Join(f.root, ".worktrees", "other")
		gitAt(t, f.root, t0, "worktree", "add", "-q", "-b", "work/other", other, "main")
		writeHandoff(t, other, "next: "+f.issue.Slug+"\n")
		b := checks.IssueBranch(f.root, f.issue)
		if b.Name != "feature/prove-me" || b.How != checks.HowWorktree || !sameDir(t, b.Worktree, wt) {
			t.Errorf("IssueBranch = %+v, want feature/prove-me by its name, not work/other by HANDOFF.md", b)
		}
	})
	t.Run("branch: field", func(t *testing.T) {
		f := newFixture(t, "TestFoo")
		gitAt(t, f.root, t0, "branch", "-m", "work/"+f.issue.Slug, "anything")
		f.issue.ExtraFields = append(f.issue.ExtraFields, tracker.ExtraField{Key: "branch", Value: "anything"})
		at := FindIssueRef(f.root, f.issue, nil)
		if at.Name != "anything" || at.Ref == "" {
			t.Errorf("FindIssueRef = %+v, want the branch: field's branch", at)
		}
	})
	t.Run("several matches: none used", func(t *testing.T) {
		f := newFixture(t, "TestFoo")
		moveBranch(t, f, "a/prove-me", "a")
		gitAt(t, f.root, t0, "worktree", "add", "-q", "-b", "b/prove-me", filepath.Join(f.root, ".worktrees", "b"), "main")
		b := checks.IssueBranch(f.root, f.issue)
		if b.Found() || len(b.Ambiguous) != 2 || !strings.Contains(b.Miss(), "several worktrees match") {
			t.Errorf("IssueBranch = %+v, miss %q; want no pick and both listed", b, b.Miss())
		}
	})
}

// With nothing to find, the proof says what it looked for and how to fix
// it, not "not checked: no work branch".
func TestIssueBranchMissNamesWhatItTried(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "branch", "-m", "work/"+f.issue.Slug, "elsewhere")
	if err := tracker.SaveData(f.issue.FilePath, tracker.DataStore{Proofs: map[string][]tracker.ProofLink{
		"AC1": {{Kind: tracker.ProofTest, Ref: "TestFoo", Criterion: "The test proves it"}},
	}}); err != nil {
		t.Fatal(err)
	}
	r := Collect(f.root, f.issue, nil)
	if r.Ref != "" {
		t.Fatalf("ref = %q, want none", r.Ref)
	}
	miss := strings.Join(r.Missing, "\n")
	for _, want := range []string{
		"no branch for " + f.issue.Slug,
		"a branch: field (unset)",
		"work/" + f.issue.Slug + " (no such branch)",
		"a worktree whose branch ends in prove-me or whose HANDOFF.md names " + f.issue.Slug,
		"git branch -m <your-branch> work/" + f.issue.Slug,
		"issue-cli set-meta " + f.issue.Slug + " --key branch --value <your-branch>",
	} {
		if !strings.Contains(miss, want) {
			t.Errorf("missing lacks %q:\n%s", want, miss)
		}
	}
	ac1 := criterion(t, r, "AC1")
	if len(ac1.Links) != 1 {
		t.Fatalf("AC1 links = %+v, want the stored test link", ac1.Links)
	}
	for _, l := range ac1.Links {
		if l.Kind == tracker.ProofTest && (l.Check.State != "not checked: no branch found" || !strings.Contains(l.Check.Fix, "set-meta")) {
			t.Errorf("test link = %+v, want the miss as its fix", l.Check)
		}
	}
	if err := ReferenceError(f.root, f.issue); err == nil || !strings.Contains(err.Error(), "no branch for "+f.issue.Slug) {
		t.Errorf("ReferenceError = %v, want the miss", err)
	}
}
