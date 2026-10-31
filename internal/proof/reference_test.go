package proof

import (
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestNamesSlug(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"chore(merge): main into sys/prove-me", true},
		{"chore(merge): main into the prove branch (sys/prove-me)", true},
		{"sys/prove-me.", true},
		{"sys/prove-me-more", false},
		{"other/sys/prove-me", false},
		{"chore(merge): main into the prove branch", false},
	}
	for _, c := range cases {
		if got := NamesSlug(c.text, "sys/prove-me"); got != c.want {
			t.Errorf("NamesSlug(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// The fixture's branch merged main as "Merge main" and names the issue
// nowhere; a slug in a commit main brought in doesn't count either.
func TestReferenceErrorWithoutAReference(t *testing.T) {
	f := newFixture(t, "TestFoo")
	commitFile(t, f.root, t0.Add(5*time.Hour), "main.go", "x", "fix: on main\n\nRefs: "+f.issue.Slug)
	branch := "work/" + f.issue.Slug
	gitAt(t, f.root, t0, "checkout", "-q", branch)
	gitAt(t, f.root, t0.Add(6*time.Hour), "merge", "-q", "--no-ff", "-m", "chore(merge): main into the prove branch", "main")
	gitAt(t, f.root, t0, "checkout", "-q", "main")

	err := ReferenceError(f.root, f.issue)
	if err == nil {
		t.Fatal("no error, want one: no commit of the branch's own references the issue")
	}
	for _, want := range []string{
		"no commit on " + branch + " references the issue",
		`git merge main -m "chore(merge): main into ` + f.issue.Slug + `"`,
		`-m "Refs: ` + f.issue.Slug + `"`,
		"don't reword one",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestReferenceBySubjectOrRefsLine(t *testing.T) {
	for name, msg := range map[string]string{
		"merge subject": "chore(merge): main into sys/prove-me",
		"refs line":     "docs(changelog): entry\n\nRefs: sys/prove-me\n\nCo-Authored-By: t <t@t>",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "TestFoo")
			gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
			sha := commitFile(t, f.root, t0.Add(5*time.Hour), "CHANGELOG.md", "x", msg)
			gitAt(t, f.root, t0, "checkout", "-q", "main")
			got, _, err := Reference(f.root, f.issue)
			if err != nil || got != sha {
				t.Errorf("Reference = %q, %v; want %s", got, err, sha)
			}
			if err := ReferenceError(f.root, f.issue); err != nil {
				t.Errorf("ReferenceError = %v, want nil", err)
			}
		})
	}
}

func TestReferenceErrorWithoutABranch(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "branch", "-D", "work/"+f.issue.Slug)
	if err := ReferenceError(f.root, f.issue); err == nil || !strings.Contains(err.Error(), "no branch work/"+f.issue.Slug) {
		t.Errorf("ReferenceError = %v, want the missing branch named", err)
	}
}

// After the fast-forward the branch is on main; the walk from the issue's
// dispatch still finds the merge that names it, so done passes.
func TestReferenceAfterTheFastForward(t *testing.T) {
	f := newFixture(t, "TestFoo")
	branch := "work/" + f.issue.Slug
	if err := tracker.SaveStats(f.issue.FilePath, tracker.StatsStore{Dispatches: []tracker.Dispatch{{At: t0.Add(30 * time.Minute)}}}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, f.root, t0.Add(5*time.Hour), "main.go", "x", "fix: on main")
	gitAt(t, f.root, t0, "checkout", "-q", branch)
	gitAt(t, f.root, t0.Add(5*time.Hour), "merge", "-q", "--no-ff", "-m", "chore(merge): main into "+f.issue.Slug, "main")
	commitFile(t, f.root, t0.Add(5*time.Hour), "README", "y", "chore(checks): remove scripts/checks/x.sh")
	gitAt(t, f.root, t0, "checkout", "-q", "main")
	gitAt(t, f.root, t0, "merge", "-q", "--ff-only", branch)
	if err := ReferenceError(f.root, f.issue); err != nil {
		t.Errorf("ReferenceError after the fast-forward = %v, want nil", err)
	}
}
