package viewer

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// guardTestName is a repo-wide guard test: one that reads the whole repo and
// fails on a branch that adds a command, a rule or a doc number without its
// docs line. make guard-check runs these before the full suite.
var guardTestName = regexp.MustCompile(`^(TestEvery\w*|Test\w*ListedInDocs|TestDocNumbers\w*)$`)

// testFunc is a top-level test func in a _test.go file.
var testFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)

// The guard-check step's list (scripts/guard-tests.txt) names every guard
// test by its package, and names only tests that exist: a renamed test would
// leave go test -run matching nothing, which passes.
func TestGuardCheckListsEveryGuardTest(t *testing.T) {
	listed, err := readGuardList(repoPath("scripts", "guard-tests.txt"))
	if err != nil {
		t.Fatal(err)
	}
	found := repoTestFuncs(t)

	for line := range listed {
		pkg, name, _ := strings.Cut(line, " ")
		if !found[pkg][name] {
			t.Errorf("scripts/guard-tests.txt lists %q, but %s has no func %s: guard-check would pass on nothing; fix or drop the line", line, pkg, name)
		}
	}
	var missing []string
	for pkg, names := range found {
		for name := range names {
			if guardTestName.MatchString(name) && !listed[pkg+" "+name] {
				missing = append(missing, pkg+" "+name)
			}
		}
	}
	sort.Strings(missing)
	for _, line := range missing {
		t.Errorf("guard test missing from scripts/guard-tests.txt: add the line %q so make guard-check runs it on its own", line)
	}
}

// readGuardList reads the `<package> <TestName>` lines, skipping comments and
// blank lines.
func readGuardList(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out[strings.Join(strings.Fields(l), " ")] = true
	}
	return out, nil
}

// repoTestFuncs maps each package (./dir, as go test names it) to its test
// funcs, from every _test.go under the repo root outside hidden folders
// (.git, .worktrees) and testdata.
func repoTestFuncs(t *testing.T) map[string]map[string]bool {
	t.Helper()
	root := repoPath()
	out := map[string]map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "./" + filepath.ToSlash(rel)
		for _, m := range testFunc.FindAllSubmatch(b, -1) {
			if out[pkg] == nil {
				out[pkg] = map[string]bool{}
			}
			out[pkg][string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
