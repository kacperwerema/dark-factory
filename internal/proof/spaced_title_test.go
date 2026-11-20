package proof

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// plTitle is the vitest it() title of the report (a project's
// packages/core/test/themes.test.ts): plain Polish words, no quotes.
const plTitle = "kuchnia z daniami prawie tylko na obiad nie ma kafelka i jest w zdaniu o ukrytych kuchniach"

const plTestFile = "describe('kuchnia', () => {\n  it('" + plTitle + "', () => {})\n  it('kuchnia z daniami na kolacje ma kafelek', () => {})\n})\n"

// TestAnUnquotedTitleTrailerIsOneTestName: a commit wrote
// "Proves: AC1 <title>" without quotes. When the whole rest of the line is
// a test in a test file, it is one link, not one link per word.
func TestAnUnquotedTitleTrailerIsOneTestName(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "packages/core/test/themes.test.ts", plTestFile,
		"test(regresja): themeDishesBySlot\n\nProves: AC1 "+plTitle)
	gitAt(t, f.root, t0, "checkout", "-q", "main")

	r := Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	var refs []string
	for _, l := range criterion(t, r, "AC1").Links {
		if l.Kind != tracker.ProofTest || l.Ref == "TestFoo" {
			continue
		}
		refs = append(refs, l.Ref)
		if !l.Check.OK {
			t.Errorf("AC1 %q: %s (%s)", l.Ref, l.Check.State, l.Check.Fix)
		}
	}
	if len(refs) != 1 || refs[0] != plTitle {
		t.Errorf("AC1 test links %q, want the one title %q", refs, plTitle)
	}
}

// TestUnquotedWordsStaySeparateTestsWhenNoTitleMatches: "Proves: AC1 TestA
// TestB" keeps reading as two tests; the whole-line reading only applies
// when that line is a test.
func TestUnquotedWordsStaySeparateTestsWhenNoTitleMatches(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "two_test.go",
		"package x\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n", "test: two\n\nProves: AC1 TestA TestB")
	gitAt(t, f.root, t0, "checkout", "-q", "main")

	r := Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	var refs []string
	for _, l := range criterion(t, r, "AC1").Links {
		if l.Kind == tracker.ProofTest && l.Ref != "TestFoo" {
			refs = append(refs, l.Ref)
		}
	}
	if strings.Join(refs, " ") != "TestA TestB" {
		t.Errorf("AC1 test links %q, want TestA and TestB", refs)
	}
}

// TestPlanSuiteRunFindsATitleBesideAShorterName: prove --run in a project
// without go.mod looked names up with git grep -o, which prints only the
// longest match on a line: a describe name sharing the it() title's line
// read not found, and whether a name was found hung on the other names.
func TestPlanSuiteRunFindsATitleBesideAShorterName(t *testing.T) {
	dir := t.TempDir()
	gitAt(t, dir, t0, "init", "-q", "-b", "main")
	commitFile(t, dir, t0, "packages/core/test/themes.test.ts", plTestFile, "tests")
	run := PlanSuiteRun(dir, []string{plTitle, "prawie tylko na obiad"}, "npm test")
	for _, res := range run.Results {
		if res.Outcome == RunNotFound {
			t.Errorf("%q not found, want found in packages/core/test/themes.test.ts", res.Name)
		}
	}
}

// TestAProveTestLinkWithSpacesIsFoundAfterTheReport: prove AC2 --test
// "<title>" stays found once the tests: comment is posted.
func TestAProveTestLinkWithSpacesIsFoundAfterTheReport(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "packages/core/test/themes.test.ts", plTestFile, "test(regresja): kafelki")
	gitAt(t, f.root, t0, "checkout", "-q", "main")
	store := tracker.DataStore{Proofs: map[string][]tracker.ProofLink{"AC2": {{Kind: tracker.ProofTest, Ref: plTitle}}}}
	if err := tracker.SaveData(f.issue.FilePath, store); err != nil {
		t.Fatal(err)
	}
	for _, comments := range [][]tracker.Comment{nil, testsComment(t0.Add(6 * time.Hour))} {
		r := Collect(f.root, f.issue, comments)
		for _, l := range criterion(t, r, "AC2").Links {
			if l.Ref == plTitle && l.Check.State == "not found" {
				t.Errorf("with %d comments: %q not found (%s)", len(comments), l.Ref, l.Check.Fix)
			}
		}
	}
}
