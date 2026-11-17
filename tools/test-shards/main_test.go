package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/liveboard"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard).
func TestMain(m *testing.M) { tmuxguard.Main(m) }

func TestSplitArgsJoinsFlagValues(t *testing.T) {
	a := splitArgs([]string{"./a", "-count", "1", "-json", "-coverprofile=c.out", "./...", "-coverpkg", "./a/..."})
	if want := []string{"./a", "./..."}; !reflect.DeepEqual(a.patterns, want) {
		t.Fatalf("patterns = %v, want %v", a.patterns, want)
	}
	if want := []string{"-count=1", "-json", "-coverprofile=c.out", "-coverpkg=./a/..."}; !reflect.DeepEqual(a.flags, want) {
		t.Fatalf("flags = %v, want %v", a.flags, want)
	}
	if v, ok := a.flag("coverprofile"); !ok || v != "c.out" {
		t.Fatalf("coverprofile = %q %v", v, ok)
	}
	if got := a.without("json", "coverprofile"); !reflect.DeepEqual(got, []string{"-count=1", "-coverpkg=./a/..."}) {
		t.Fatalf("without = %v", got)
	}
}

// fixtureModule writes a module with a package of n tests (TestT0..) and one
// other package, and changes into it.
func fixtureModule(t *testing.T, n int, failing string) string {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fixture\n\ngo 1.21\n")
	write("big/big.go", "package big\n\nfunc Half(i int) int {\n\tif i%2 == 0 {\n\t\treturn i / 2\n\t}\n\treturn i\n}\n")
	var tests strings.Builder
	tests.WriteString("package big\n\nimport \"testing\"\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&tests, "\nfunc TestT%d(t *testing.T) {\n\tt.Run(\"sub\", func(t *testing.T) {})\n\tif Half(%d) < 0 || %q == t.Name() {\n\t\tt.Fatal(\"failed on purpose\")\n\t}\n}\n", i, i, failing)
	}
	write("big/big_test.go", tests.String())
	write("small/small.go", "package small\n\nfunc One() int { return 1 }\n")
	write("small/small_test.go", "package small\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {\n\tif One() != 1 {\n\t\tt.Fatal(1)\n\t}\n}\n")
	t.Chdir(dir)
	return dir
}

type event struct {
	Action, Package, Test string
}

func passedTests(t *testing.T, stream []byte) map[string]int {
	t.Helper()
	seen := map[string]int{}
	for _, line := range bytes.Split(stream, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("a line that isn't a whole go test -json event: %q", line)
		}
		if e.Test != "" && (e.Action == "pass" || e.Action == "fail") {
			seen[e.Package+" "+e.Test+" "+e.Action]++
		}
	}
	return seen
}

// TestShardedJSONRunListsEveryTestOnceAndMergesCoverage runs the tool the way
// make test does in local CI: -json, a coverage profile, one package in
// three shards beside the rest.
func TestShardedJSONRunListsEveryTestOnceAndMergesCoverage(t *testing.T) {
	dir := fixtureModule(t, 7, "")
	var out, errb bytes.Buffer
	code, err := run([]string{"./big=3"}, "none", "none", "",
		[]string{"./...", "-count=1", "-json", "-coverpkg=./big/...", "-coverprofile=cover.out"}, &out, &errb)
	if err != nil || code != 0 {
		t.Fatalf("run = %d, %v\nstdout:\n%s\nstderr:\n%s", code, err, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "fixture/big: 7 tests in 3 shards (no recorded times") {
		t.Errorf("stderr doesn't say how it sharded:\n%s", errb.String())
	}
	seen := passedTests(t, out.Bytes())
	var want []string
	for i := 0; i < 7; i++ {
		want = append(want, fmt.Sprintf("fixture/big TestT%d pass", i), fmt.Sprintf("fixture/big TestT%d/sub pass", i))
	}
	want = append(want, "fixture/small TestOne pass")
	sort.Strings(want)
	var got []string
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%s seen %d times, want once", k, n)
		}
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tests in the stream:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	prof, err := os.ReadFile(filepath.Join(dir, "cover.out"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(prof), "mode: ") || strings.Count(string(prof), "mode: ") != 1 || !strings.Contains(string(prof), "fixture/big/big.go:") {
		t.Fatalf("merged profile:\n%s", prof)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "cover.out.*"))
	if len(left) > 0 {
		t.Fatalf("per-process profiles left behind: %v", left)
	}
}

// TestAPlanIsKeptWhileThePackageListsTheSameTests: the second run uses the
// first run's shards (their -run patterns are go test's cache key) even when
// recorded times now say otherwise; another test count, or another shard
// count, makes a new plan.
func TestAPlanIsKeptWhileThePackageListsTheSameTests(t *testing.T) {
	fixtureModule(t, 6, "")
	plans := t.TempDir()
	var out, errb bytes.Buffer
	if code, err := run([]string{"./big=2"}, "none", plans, "", []string{"./big", "-count=1"}, &out, &errb); err != nil || code != 0 {
		t.Fatalf("run = %d, %v\n%s%s", code, err, out.String(), errb.String())
	}
	first, kept := keptPlan(plans, "fixture/big", []string{"TestT0", "TestT1", "TestT2", "TestT3", "TestT4", "TestT5"}, 2)
	if kept == "" || len(first) != 2 {
		t.Fatalf("no plan kept after the first run: %v %q\n%s", first, kept, errb.String())
	}
	// Times that would put TestT0 alone in a shard of its own.
	times := filepath.Join(t.TempDir(), "unit.json")
	var rec strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&rec, `{"Action":"pass","Package":"fixture/big","Test":"TestT%d","Elapsed":%d}`+"\n", i, map[bool]int{true: 50, false: 1}[i == 0])
	}
	if err := os.WriteFile(times, []byte(rec.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code, err := run([]string{"./big=2"}, times, plans, "", []string{"./big", "-count=1"}, &out, &errb); err != nil || code != 0 {
		t.Fatalf("run = %d, %v\n%s%s", code, err, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "6 tests in 2 shards (plan kept from ") {
		t.Fatalf("second run didn't keep the plan:\n%s", errb.String())
	}
	if again, _ := keptPlan(plans, "fixture/big", []string{"TestT0", "TestT1", "TestT2", "TestT3", "TestT4", "TestT5"}, 2); !reflect.DeepEqual(again, first) {
		t.Fatalf("plan changed: %v, was %v", again, first)
	}
	if p, _ := keptPlan(plans, "fixture/big", []string{"TestT0", "TestT1", "TestT2", "TestT3", "TestT4", "TestT5", "TestT6"}, 2); p != nil {
		t.Fatalf("a plan kept for another set of tests: %v", p)
	}
	if p, _ := keptPlan(plans, "fixture/big", []string{"TestT0", "TestT1", "TestT2", "TestT3", "TestT4", "TestT5"}, 3); p != nil {
		t.Fatalf("a plan kept for another shard count: %v", p)
	}
	errb.Reset()
	if code, err := run([]string{"./big=3"}, times, plans, "", []string{"./big", "-count=1"}, &out, &errb); err != nil || code != 0 {
		t.Fatalf("run = %d, %v\n%s%s", code, err, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "6 tests in 3 shards (times from "+times) {
		t.Fatalf("a new shard count didn't plan anew:\n%s", errb.String())
	}
	if p, _ := keptPlan(plans, "fixture/big", []string{"TestT0", "TestT1", "TestT2", "TestT3", "TestT4", "TestT5"}, 3); len(p) != 3 || !reflect.DeepEqual(p[0], []string{"TestT0"}) {
		t.Fatalf("new plan not kept or not by the times: %v", p)
	}
}

func TestAFailingShardFailsTheRunAndShowsItsOutput(t *testing.T) {
	fixtureModule(t, 5, "TestT3")
	var out, errb bytes.Buffer
	code, err := run([]string{"./big=2"}, "none", "none", "", []string{"./...", "-count=1"}, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 {
		t.Fatalf("a failing test exited 0:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "--- FAIL: TestT3") || !strings.Contains(out.String(), "ok  \tfixture/small") {
		t.Fatalf("output lacks the failure or the other package:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "fixture/big shard ") || !strings.Contains(errb.String(), " exited 1") {
		t.Fatalf("stderr doesn't name the red shard:\n%s", errb.String())
	}
}

func TestARunFlagRunsGoTestUnsharded(t *testing.T) {
	fixtureModule(t, 3, "")
	var out, errb bytes.Buffer
	code, err := run([]string{"./big=3"}, "none", "none", "", []string{"./big", "-count=1", "-run=^TestT1$", "-v"}, &out, &errb)
	if err != nil || code != 0 {
		t.Fatalf("run = %d, %v\n%s%s", code, err, out.String(), errb.String())
	}
	if strings.Contains(errb.String(), "shards") || !strings.Contains(out.String(), "--- PASS: TestT1 (") || strings.Contains(out.String(), "TestT0") || strings.Contains(out.String(), "TestT2") {
		t.Fatalf("-run was sharded or ran the wrong tests:\n%s\n%s", out.String(), errb.String())
	}
}

// cacheFixture is fixtureModule plus a cache list: big (sharded, reading
// testdata/input.txt and LIVE_BOARD_DEFER) cached, small fresh, and big's
// TestT1 a fresh-test. A nonce in big keeps an earlier run's cache out.
func cacheFixture(t *testing.T) string {
	dir := fixtureModule(t, 4, "")
	write := func(rel, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour) // go test won't cache a file written in the last seconds
		if err := os.Chtimes(filepath.Join(dir, rel), old, old); err != nil {
			t.Fatal(err)
		}
	}
	write("big/testdata/input.txt", "one\n")
	write("big/input_test.go", fmt.Sprintf(`package big

import (
	"os"
	"strings"
	"testing"
)

const nonce = %q

func TestInput(t *testing.T) {
	b, err := os.ReadFile("testdata/input.txt")
	if err != nil || strings.TrimSpace(string(b)) != "one" {
		t.Fatalf("input.txt = %%q, %%v", b, err)
	}
}

func TestDeferEnv(t *testing.T) {
	t.Logf("defer=%%q", os.Getenv("LIVE_BOARD_DEFER"))
}
`, t.Name()+fmt.Sprint(os.Getpid())+fmt.Sprint(time.Now().UnixNano())))
	write("cache.txt", "cached ./big\nfresh ./small reads this machine\nfresh-test ./big TestT1 reads the live board\n")
	return dir
}

// A run with a cache list and no -count: the second run takes the cached
// package (each shard) from go test's cache and reruns the fresh one and
// the fresh-test; a changed input file of the cached package reruns it, and
// -count=1 in go test's flags (make test TEST_CACHE=0) runs everything.
func TestCacheListRunsCachedPackagesFromTheCache(t *testing.T) {
	t.Setenv(liveboard.DeferEnv, "") // this test may itself run in make test's cached run
	dir := cacheFixture(t)
	runOnce := func(args ...string) (string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code, err := run([]string{"./big=2"}, "none", "none", "cache.txt", append([]string{"./..."}, args...), &out, &errb)
		if err != nil || code != 0 {
			t.Fatalf("run = %d, %v\nstdout:\n%s\nstderr:\n%s", code, err, out.String(), errb.String())
		}
		return out.String(), errb.String()
	}
	lines := func(out, prefix string) (cached, ran int) {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, prefix+"\t") || l == prefix {
				if strings.Contains(l, "(cached)") {
					cached++
				} else {
					ran++
				}
			}
		}
		return
	}

	out, errb := runOnce()
	if !strings.Contains(errb, "test-shards: 1 packages may come from go test's cache, 1 always run (cache.txt)") {
		t.Errorf("stderr doesn't say what may be cached:\n%s", errb)
	}
	if c, r := lines(out, "ok  \tfixture/big"); c != 0 || r != 3 {
		t.Errorf("first run: big cached %d, ran %d; want 2 shards and the fresh-test run:\n%s", c, r, out)
	}

	out, _ = runOnce()
	if c, r := lines(out, "ok  \tfixture/big"); c != 2 || r != 1 {
		t.Errorf("second run: big cached %d, ran %d; want both shards cached, the fresh-test run:\n%s", c, r, out)
	}
	if c, r := lines(out, "ok  \tfixture/small"); c != 0 || r != 1 {
		t.Errorf("second run: small cached %d, ran %d; a fresh package always runs:\n%s", c, r, out)
	}

	input := filepath.Join(dir, "big", "testdata", "input.txt")
	if err := os.WriteFile(input, []byte("one \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(input, old, old); err != nil {
		t.Fatal(err)
	}
	out, _ = runOnce()
	if c, _ := lines(out, "ok  \tfixture/big"); c != 1 {
		t.Errorf("input.txt changed: big cached in %d shards, want only the shard without TestInput:\n%s", c, out)
	}

	out, errb = runOnce("-count=1", "-v")
	if strings.Contains(out, "(cached)") || strings.Contains(errb, "may come from") || !strings.Contains(out, `defer=""`) {
		t.Errorf("-count=1: want every package run, no cache list, no LIVE_BOARD_DEFER:\n%s\n%s", out, errb)
	}
	out, _ = runOnce("-v")
	if !strings.Contains(out, "defer=\""+filepath.Join(dir, "cache.txt")+"\"") {
		t.Errorf("the cached run's tests don't get LIVE_BOARD_DEFER=<list>:\n%s", out)
	}
}
