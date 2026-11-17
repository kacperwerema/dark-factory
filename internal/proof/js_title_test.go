package proof

import (
	"strings"
	"testing"
	"time"
)

// jsTitle is a vitest it() title the way a Polish project writes one:
// spaces, a colon, parentheses and letters outside ASCII.
const jsTitle = "lista: ukryte przepisy (ryba w piwnym cieście) poza poleceniami"

// TestAQuotedJSTitleIsOneTestName: a Proves: trailer reads a quoted it()
// title as one test name, and the lookup finds it beside a shorter name
// that sits inside the same line (git grep -o printed only the longer one).
func TestAQuotedJSTitleIsOneTestName(t *testing.T) {
	f := newFixture(t, "TestFoo")
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "src/katalog.test.ts",
		"describe('katalog', () => {\n  it('"+jsTitle+"', () => {})\n})\n",
		"test(katalog): hidden recipes\n\nProves: AC1 \""+jsTitle+"\"\nProves: AC3 lista")
	gitAt(t, f.root, t0, "checkout", "-q", "main")

	r := Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	var refs []string
	for _, l := range criterion(t, r, "AC1").Links {
		refs = append(refs, l.Ref)
		if l.Ref == jsTitle && !l.Check.OK {
			t.Errorf("AC1 %q: %s (%s)", l.Ref, l.Check.State, l.Check.Fix)
		}
	}
	if !strings.Contains(strings.Join(refs, "|"), jsTitle) {
		t.Errorf("AC1 links %q, want the whole title as one link", refs)
	}
	for _, l := range criterion(t, r, "AC3").Links {
		if !l.Check.OK {
			t.Errorf("AC3 %q: %s, want found (it sits inside the title's line)", l.Ref, l.Check.State)
		}
	}
}

func TestSplitTrailerKeepsAQuotedTitleWhole(t *testing.T) {
	ids, tests := splitTrailer(`AC3, AC5 "`+jsTitle+`" TestB 'it: drugi (b)'`, "AC")
	if strings.Join(ids, ",") != "AC3,AC5" {
		t.Errorf("ids %q", ids)
	}
	if want := []string{jsTitle, "TestB", "it: drugi (b)"}; strings.Join(tests, "|") != strings.Join(want, "|") {
		t.Errorf("tests %q, want %q", tests, want)
	}
	if p := CheckProves(`AC3 "`+jsTitle+`"`, "AC"); len(p) != 0 {
		t.Errorf("CheckProves on a quoted title: %q, want none", p)
	}
	if p := CheckProves(`AC3 lista: ukryte (ryba)`, "AC"); len(p) == 0 || !strings.Contains(strings.Join(p, "\n"), `"`) {
		t.Errorf("CheckProves on an unquoted title: %q, want a hint to quote it", p)
	}
}

func TestHasWholeWordMatchesLikeGitGrepW(t *testing.T) {
	for _, c := range []struct {
		line, name string
		want       bool
	}{
		{"  it('" + jsTitle + "', () => {})", jsTitle, true},
		{"  it('" + jsTitle + "', () => {})", "lista", true},
		{"listy i lista", "lista", true},
		{"listaX", "lista", false},
		{"func TestFooBar(t", "TestFoo", false},
		{"func TestFoo(t", "TestFoo", true},
		// git grep -w counts a byte outside ASCII as no word byte, as here.
		{"cieście)", "cie", true},
	} {
		if got := hasWholeWord(c.line, c.name); got != c.want {
			t.Errorf("hasWholeWord(%q, %q) = %v, want %v", c.line, c.name, got, c.want)
		}
	}
}
