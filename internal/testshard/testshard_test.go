package testshard

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

// TestMain keeps every tmux these tests start off the developer's server
// (internal/tmuxguard).
func TestMain(m *testing.M) { tmuxguard.Main(m) }

func TestListedTestsDropsGoTestsOwnLines(t *testing.T) {
	out := "TestA\nTestB\nBenchmarkC\nExampleD\nFuzzE\nTestA\nok  \tgithub.com/x/y\t0.5s\n"
	got := ListedTests([]byte(out))
	want := []string{"TestA", "TestB", "ExampleD", "FuzzE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListedTests = %v, want %v", got, want)
	}
}

func TestTimesReadsTopLevelTestsOfOnePackage(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Package":"m/a","Test":"TestA"}`,
		`{"Action":"pass","Package":"m/a","Test":"TestA/sub","Elapsed":9}`,
		`{"Action":"pass","Package":"m/a","Test":"TestA","Elapsed":2.5}`,
		`{"Action":"fail","Package":"m/a","Test":"TestB","Elapsed":1}`,
		`{"Action":"pass","Package":"m/b","Test":"TestC","Elapsed":7}`,
		`{"Action":"pass","Package":"m/a","Elapsed":30}`,
		`not json`,
	}, "\n")
	got := Times([]byte(stream), "m/a")
	want := map[string]float64{"TestA": 2.5, "TestB": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Times = %v, want %v", got, want)
	}
}

func TestPlanPutsEveryTestInExactlyOneShard(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for round := 0; round < 200; round++ {
		count := r.Intn(60)
		var tests []string
		times := map[string]float64{}
		for i := 0; i < count; i++ {
			// Names that are prefixes of each other: TestX, TestX1, TestX12...
			name := fmt.Sprintf("TestX%d", i)
			if i%3 == 0 {
				name = "TestX" + strings.Repeat("1", i/3)
			}
			tests = append(tests, name)
			if r.Intn(4) > 0 {
				times[name] = r.Float64() * 10
			}
		}
		tests = ListedTests([]byte(strings.Join(tests, "\n")))
		n := 1 + r.Intn(8)
		groups := Plan(tests, times, n)
		if len(groups) > n || (len(tests) > 0 && len(groups) == 0) {
			t.Fatalf("round %d: %d groups for n=%d, %d tests", round, len(groups), n, len(tests))
		}
		var regs []string
		for _, g := range groups {
			regs = append(regs, Regex(g))
		}
		if bad := Check(tests, regs); len(bad) > 0 {
			t.Fatalf("round %d: %v", round, bad)
		}
	}
}

func TestPlanBalancesByRecordedTime(t *testing.T) {
	tests := []string{"TestBig", "TestA", "TestB", "TestC", "TestD", "TestNew"}
	times := map[string]float64{"TestBig": 10, "TestA": 4, "TestB": 3, "TestC": 3, "TestD": 1}
	got := Plan(tests, times, 2)
	// TestNew has no recorded time and counts as the median, 3 s: 10+3 | 4+3+3+1.
	want := [][]string{{"TestBig", "TestNew"}, {"TestA", "TestB", "TestC", "TestD"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan = %v, want %v", got, want)
	}
	if again := Plan(tests, times, 2); !reflect.DeepEqual(again, got) {
		t.Fatalf("Plan is not stable: %v then %v", got, again)
	}
}

func TestPlanWithoutTimesSplitsByCount(t *testing.T) {
	got := Plan([]string{"TestA", "TestB", "TestC", "TestD"}, nil, 2)
	if len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 2 {
		t.Fatalf("Plan = %v, want two groups of two", got)
	}
	if one := Plan([]string{"TestA"}, nil, 4); len(one) != 1 {
		t.Fatalf("one test in %d groups, want 1", len(one))
	}
}

func TestCheckNamesMissingAndTwiceRunTests(t *testing.T) {
	bad := Check([]string{"TestA", "TestAB", "TestC"}, []string{"^(TestA|TestAB)$", "^(TestAB)$"})
	want := []string{"TestAB: in shards 1, 2", "TestC: in no shard"}
	if !reflect.DeepEqual(bad, want) {
		t.Fatalf("Check = %v, want %v", bad, want)
	}
}

func TestMergeProfilesKeepsOneModeLine(t *testing.T) {
	a := "mode: set\nm/a.go:1.1,2.2 1 1\n"
	b := "mode: set\nm/a.go:1.1,2.2 1 0\nm/b.go:3.1,4.2 2 1\n"
	got, err := MergeProfiles([][]byte{[]byte(a), nil, []byte(b)})
	if err != nil {
		t.Fatal(err)
	}
	want := "mode: set\nm/a.go:1.1,2.2 1 1\nm/a.go:1.1,2.2 1 0\nm/b.go:3.1,4.2 2 1\n"
	if string(got) != want {
		t.Fatalf("merged =\n%s\nwant\n%s", got, want)
	}
	if _, err := MergeProfiles([][]byte{[]byte(a), []byte("mode: count\n")}); err == nil {
		t.Fatal("mixed modes merged without an error")
	}
	if _, err := MergeProfiles(nil); err == nil {
		t.Fatal("no profiles merged without an error")
	}
}

// TestShardedPackagesRunEveryTestOnce is the guard on the real packages
// make test shards: the Makefile's TEST_SHARD_PKGS (cmd/issue-cli at
// TEST_SHARDS, at least 3), each package's listed tests against its shards'
// -run patterns.
func TestShardedPackagesRunEveryTestOnce(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^TEST_SHARDS \?= (\d+)$`).FindSubmatch(makefile)
	if m == nil {
		t.Fatal("no TEST_SHARDS ?= <n> line in the Makefile")
	}
	cliShards, _ := strconv.Atoi(string(m[1]))
	if cliShards < 3 {
		t.Fatalf("TEST_SHARDS ?= %d, want at least 3", cliShards)
	}
	line := regexp.MustCompile(`(?m)^TEST_SHARD_PKGS \?= (.+)$`).FindSubmatch(makefile)
	if line == nil {
		t.Fatal("no TEST_SHARD_PKGS ?= <pkg>=<n>... line in the Makefile")
	}
	counts := map[string]int{}
	for _, f := range strings.Fields(strings.ReplaceAll(string(line[1]), "$(TEST_SHARDS)", string(m[1]))) {
		pkg, nStr, _ := strings.Cut(f, "=")
		n, err := strconv.Atoi(nStr)
		if err != nil || n < 1 {
			t.Fatalf("TEST_SHARD_PKGS entry %q: want <pkg>=<n>", f)
		}
		counts[pkg] = n
	}
	// The packages the step waits on longest (factory/internalviewer-bench-
	// and-agentrun-tests-run-in-shards-too-so-the-test-step-ends-near-120-s).
	for pkg, min := range map[string]int{"./cmd/issue-cli": 3, "./internal/viewer": 2, "./internal/bench": 2, "./internal/agentrun": 2} {
		if counts[pkg] < min {
			t.Errorf("TEST_SHARD_PKGS runs %s in %d shards, want at least %d", pkg, counts[pkg], min)
		}
	}
	for pkg, n := range counts {
		t.Run(strings.TrimPrefix(pkg, "./"), func(t *testing.T) {
			cmd := exec.Command("go", "test", "-list=.", pkg)
			cmd.Dir = root
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go test -list: %v\n%s", err, out)
			}
			tests := ListedTests(out)
			if len(tests) < n*10 {
				t.Fatalf("listed %d tests in %s, want many more than its %d shards:\n%s", len(tests), pkg, n, out)
			}
			groups := Plan(tests, nil, n)
			if len(groups) != n {
				t.Fatalf("%d shards, want %d", len(groups), n)
			}
			var regs []string
			total := 0
			for _, g := range groups {
				regs = append(regs, Regex(g))
				total += len(g)
			}
			if bad := Check(tests, regs); len(bad) > 0 {
				t.Fatalf("shards don't run every test once:\n%s", strings.Join(bad, "\n"))
			}
			if total != len(tests) {
				t.Fatalf("shards hold %d tests, the package lists %d", total, len(tests))
			}
		})
	}
}
