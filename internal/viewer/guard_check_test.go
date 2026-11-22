package viewer

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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

// guardRow is one row of docs/testing.md's "Guard tests" table.
type guardRow struct {
	line             int
	name, rule, more string
	cells            int
}

// guardTableRows reads the table's rows: each line starting "| `Test".
func guardTableRows(t *testing.T) []guardRow {
	t.Helper()
	b, err := os.ReadFile(repoPath("docs", "testing.md"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []guardRow
	for i, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "| `Test") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(l), "|"), "|"), "|")
		r := guardRow{line: i + 1, cells: len(cells), name: strings.Trim(strings.TrimSpace(cells[0]), "`")}
		if len(cells) == 3 {
			r.rule, r.more = strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2])
		}
		rows = append(rows, r)
	}
	return rows
}

// Every guard test has one row in docs/testing.md's "Guard tests" table,
// with its rule and a link to the page that says more: guard-check prints
// that row when the test fails. A row names only a listed test.
func TestGuardTestsTableNamesEveryGuardTest(t *testing.T) {
	listed, err := readGuardList(repoPath("scripts", "guard-tests.txt"))
	if err != nil {
		t.Fatal(err)
	}
	names := nameSet(listed)
	link := regexp.MustCompile(`\]\([^)]+\)`)
	seen := map[string]int{}
	for _, r := range guardTableRows(t) {
		where := fmt.Sprintf("docs/testing.md:%d", r.line)
		switch {
		case r.cells != 3:
			t.Errorf("%s: the Guard tests row for %s has %d cells, want 3 (test, rule, more) and no | inside a cell", where, r.name, r.cells)
		case r.rule == "":
			t.Errorf("%s: the Guard tests row for %s states no rule", where, r.name)
		case !link.MatchString(r.more):
			t.Errorf("%s: the Guard tests row for %s links no page in its More cell: [page](path#anchor)", where, r.name)
		}
		if !names[r.name] {
			t.Errorf("%s: the Guard tests table has a row for %s, which scripts/guard-tests.txt doesn't list: drop the row or list the test", where, r.name)
		}
		if prev, ok := seen[r.name]; ok {
			t.Errorf("%s: a second Guard tests row for %s (the first is line %d)", where, r.name, prev)
		}
		seen[r.name] = r.line
	}
	var missing []string
	for name := range names {
		if _, ok := seen[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("%s is in scripts/guard-tests.txt but has no row in docs/testing.md's Guard tests table: add | `%s` | <the rule it holds> | [<page>](<path#anchor>) |, which guard-check prints when it fails", name, name)
	}
}

// A stand-in change that forgot its test-cache line: scripts/test-cache.txt
// without internal/viewerstate's line. guard-check goes red on it, and after go
// test's own message names the rule, its row in docs/testing.md and the
// page that says more. The same run on the real list is green and quiet.
func TestGuardCheckNamesTheRuleOfAFailingGuard(t *testing.T) {
	const guard = "TestEveryTestPackageIsInTheCacheList"
	// Read in process what the script reads, so go test's cache sees them.
	for _, p := range [][]string{{"scripts", "guard-check.sh"}, {"tools", "test-shards", "cachelist_test.go"}} {
		if _, err := os.ReadFile(repoPath(p...)); err != nil {
			t.Fatal(err)
		}
	}
	cache, err := os.ReadFile(repoPath("scripts", "test-cache.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var row guardRow
	for _, r := range guardTableRows(t) {
		if r.name == guard {
			row = r
		}
	}
	if row.line == 0 {
		t.Fatalf("docs/testing.md's Guard tests table has no row for %s", guard)
	}
	const drop = "cached ./internal/viewerstate\n"
	if !strings.Contains(string(cache), drop) {
		t.Fatalf("scripts/test-cache.txt has no line %q to leave out", drop)
	}
	dir := t.TempDir()
	list := filepath.Join(dir, "guard-tests.txt")
	standin := filepath.Join(dir, "test-cache.txt")
	if err := os.WriteFile(list, []byte("./tools/test-shards "+guard+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(standin, []byte(strings.Replace(string(cache), drop, "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(extra ...string) (string, error) {
		t.Helper()
		cmd := exec.Command("bash", repoPath("scripts", "guard-check.sh"))
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GUARD_") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, append([]string{"GUARD_LIST=" + list}, extra...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("GUARD_STANDIN_TEST_CACHE=" + standin)
	if err == nil {
		t.Fatalf("guard-check passed on a cache list without internal/viewerstate's line:\n%s", out)
	}
	for _, want := range []string{
		"./internal/viewerstate has tests but no line in scripts/test-cache.txt",
		"guard-check: " + guard + " failed. Rule: " + row.rule,
		fmt.Sprintf("(docs/testing.md:%d; more: docs/testing.md#make-validate)", row.line),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("guard-check's output lacks %q:\n%s", want, out)
		}
	}

	if out, err := run(); err != nil || strings.Contains(out, "guard-check:") {
		t.Errorf("guard-check on the real cache list: err=%v, want green with no rule line:\n%s", err, out)
	}
}
