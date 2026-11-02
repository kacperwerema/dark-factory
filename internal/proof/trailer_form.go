package proof

import (
	"fmt"
	"regexp"
	"strings"
)

// ProvesForm is the form of a Proves: line the proof check reads.
const ProvesForm = "Proves: AC1[, AC2] [TestName ...]"

var (
	// testWord is a test name the proof check can run: a Go identifier, a
	// dotted name (another runner's), or a Go test with a subtest after /.
	testWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
	goTop    = regexp.MustCompile(`^(Test|Benchmark|Example|Fuzz)[A-Za-z0-9_]*$`)
	// boxID is a checkbox or playtest step id (TP4, I2, AC3); a test named
	// TestFoo2 is not one.
	boxID = regexp.MustCompile(`^[A-Z]{1,4}\d+$`)
)

// CheckProves returns what is wrong with one Proves: value, read the way
// Collect reads it (splitTrailer with the criteria prefix): each problem
// names the word and the form that works. Empty when the line links what
// it says. issue-cli workflow commit-msg asks it before the commit exists,
// since a word Collect can't use is otherwise read as a test name, or the
// line is dropped, without a word.
func CheckProves(value, prefix string) []string {
	if prefix == "" {
		prefix = "AC"
	}
	ids, words := splitTrailer(value, prefix)
	var out []string
	for _, w := range words {
		if p := provesWordProblem(w, prefix); p != "" {
			out = append(out, p)
		}
	}
	if len(ids) == 0 && len(out) == 0 {
		out = append(out, fmt.Sprintf("names no criterion: the criterion id comes first (%s); without one the line links nothing", ProvesForm))
	}
	return out
}

// provesWordProblem is what is wrong with a word splitTrailer reads as a
// test name, "" when it is one.
func provesWordProblem(w, prefix string) string {
	stepHint := func(step string) string {
		return fmt.Sprintf("a playtest step proves through proves: [%s1] on step %s in its session file, never a trailer (or issue-cli prove <slug> %s1 --step %s)", prefix, step, prefix, step)
	}
	switch {
	case strings.Contains(w, "#"):
		_, id, _ := strings.Cut(w, "#")
		return fmt.Sprintf("%q: the issue slug doesn't go in a Proves: line (the commit is on the issue's branch): write Proves: %s <TestName>", w, id)
	case strings.Contains(w, "/"):
		top, _, _ := strings.Cut(w, "/")
		if goTop.MatchString(top) {
			return "" // a subtest: prove --run runs its top test
		}
		segs := strings.Split(w, "/")
		if last := segs[len(segs)-1]; boxID.MatchString(last) && len(segs) == 2 && !strings.ContainsAny(segs[0], ".") {
			return fmt.Sprintf("%q is a <session>/<step>, read as a test name: %s", w, stepHint(last))
		}
		return fmt.Sprintf("%q is a path, read as a test name: a Proves: line names tests only (Proves: %s1 TestX); prove finds each test's package itself", w, prefix)
	case boxID.MatchString(w):
		return fmt.Sprintf("%q is a box or playtest step id, read as a test name: %s", w, stepHint(strings.ToUpper(w)))
	case !testWord.MatchString(w):
		return fmt.Sprintf("%q is not a test name (a Go test function such as TestX): the form is %s", w, ProvesForm)
	}
	return ""
}
