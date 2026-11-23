package ship

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/testresults"
)

// Main fix hints: a red go test on a work branch can be one main already
// fixed after the branch was cut. MainFixHints names main's commits since
// the merge base that touch a failing test's file or package, one line
// each, so the worker merges main instead of debugging a test it never
// touched:
//
//	main has 422e749 fix(viewer): the budget tests unset … touching internal/viewer/budget_test.go: merge main and re-run

// maxMainFixHints caps the lines; the rest are one "(+n more)" line.
const maxMainFixHints = 3

// failTestLine is a top-level go test failure ("--- FAIL: TestX (0.01s)");
// an indented one is a subtest, its parent fails on its own line.
var failTestLine = regexp.MustCompile(`^--- FAIL: (Test[^\s/]*)`)

// failingTest is a failing test with the package its output came under.
type failingTest struct {
	Name, Pkg string
}

// failingTests reads go test's text output: each top-level failing test
// with the package whose `FAIL\t<pkg>` line follows it, module cut off.
// Lines a parallel validate step prefixed with `[<step>] ` are skipped.
func failingTests(output, module string) (tests []failingTest, pkgs []string) {
	var pending []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(output))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "[") {
			continue
		}
		if m := failTestLine.FindStringSubmatch(line); m != nil {
			pending = append(pending, m[1])
			continue
		}
		rt := testresults.ParseRedText(line, module)
		if len(rt.Packages) == 0 {
			continue
		}
		pkg := rt.Packages[0]
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
		for _, n := range pending {
			tests = append(tests, failingTest{Name: n, Pkg: pkg})
		}
		pending = nil
	}
	return tests, pkgs
}

// testFile is the _test.go file in dir/pkg that declares func name, as a
// slash path from dir; "" when none does.
func testFile(dir, pkg, name string) string {
	files, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pkg), "*_test.go"))
	decl := "func " + name + "("
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil || !strings.Contains(string(b), decl) {
			continue
		}
		return path.Join(pkg, filepath.Base(f))
	}
	return ""
}

// mainCommit is one non-merge commit on main with the files it touched.
type mainCommit struct {
	Short, Subject string
	Files          []string
}

// commitsAhead is main's non-merge commits since its merge base with HEAD
// in dir, newest first; nil when main is not ahead or git can't tell.
func commitsAhead(dir, mainRef string) []mainCommit {
	base, err := gitOut(dir, "merge-base", "HEAD", mainRef)
	if err != nil || base == "" {
		return nil
	}
	out, err := gitOut(dir, "log", "--no-merges", "--format=%x00%h %s", "--name-only", base+".."+mainRef)
	if err != nil {
		return nil
	}
	var cs []mainCommit
	for _, chunk := range strings.Split(out, "\x00") {
		lines := strings.Split(strings.TrimSpace(chunk), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		short, subject, _ := strings.Cut(lines[0], " ")
		c := mainCommit{Short: short, Subject: subject}
		for _, f := range lines[1:] {
			if f = strings.TrimSpace(f); f != "" {
				c.Files = append(c.Files, f)
			}
		}
		cs = append(cs, c)
	}
	return cs
}

// MainFixHints reads a red go test run's output in dir, a checkout on a
// work branch, and names each of mainRef's commits since the merge base
// that touches a failing test's file or a file of a failing package
// (directly in its folder). Empty when main is not ahead, no test failed
// or no commit touches one; a commit is named once, by the failing test's
// file when it touches it, else by its first file in the package.
func MainFixHints(dir, mainRef, output string) []string {
	tests, pkgs := failingTests(output, testresults.GoModule(dir))
	if len(pkgs) == 0 {
		return nil
	}
	commits := commitsAhead(dir, mainRef)
	if len(commits) == 0 {
		return nil
	}
	files := map[string]bool{}
	for _, t := range tests {
		if f := testFile(dir, t.Pkg, t.Name); f != "" {
			files[f] = true
		}
	}
	inPkg := map[string]bool{}
	for _, p := range pkgs {
		inPkg[p] = true
	}
	var hints []string
	for _, c := range commits {
		touched := ""
		for _, f := range c.Files {
			if files[f] {
				touched = f
				break
			}
			if touched == "" && inPkg[pkgDir(path.Dir(f))] {
				touched = f
			}
		}
		if touched != "" {
			hints = append(hints, fmt.Sprintf("%s has %s %s touching %s: merge %s and re-run", mainRef, c.Short, c.Subject, touched, mainRef))
		}
	}
	if len(hints) > maxMainFixHints {
		more := len(hints) - maxMainFixHints
		hints = append(hints[:maxMainFixHints], fmt.Sprintf("(+%d more on %s touching the failing tests' packages)", more, mainRef))
	}
	return hints
}

// pkgDir is a file's folder as go test's short package names it: "." for
// the module root.
func pkgDir(d string) string {
	if d == "" {
		return "."
	}
	return d
}
