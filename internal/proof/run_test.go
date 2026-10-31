package proof

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestTestNamesDedupesInCriteriaOrder(t *testing.T) {
	r := Report{Criteria: []Criterion{
		{ID: "AC1", Links: []CheckedLink{{Link: Link{Kind: tracker.ProofTest, Ref: "TestB"}}, {Link: Link{Kind: tracker.ProofCommit, Ref: "abc"}}}},
		{ID: "AC2", Links: []CheckedLink{{Link: Link{Kind: tracker.ProofTest, Ref: "TestA"}}, {Link: Link{Kind: tracker.ProofTest, Ref: "TestB"}}}},
	}}
	if got, want := TestNames(r), []string{"TestB", "TestA"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TestNames = %v, want %v", got, want)
	}
}

func TestPlanGoRunFindsEachTestsPackage(t *testing.T) {
	dir := t.TempDir()
	gitAt(t, dir, t0, "init", "-q", "-b", "main")
	commitFile(t, dir, t0, "go.mod", "module x\n", "mod")
	commitFile(t, dir, t0, "a_test.go", "package x\n\nfunc TestRoot(t *testing.T) {}\n", "root test")
	commitFile(t, dir, t0, "pkg/b/b_test.go", "package b\n\nfunc TestB(t *testing.T) {}\nfunc TestBee(t *testing.T) {}\n", "b tests")
	commitFile(t, dir, t0, "pkg/b/helper.go", "package b\n\n// func TestFake(t *testing.T) {}\n", "not a test file")

	run := PlanGoRun(dir, []string{"TestB", "TestRoot", "TestB/case_one", "TestFake", "AC9-is-not-a-test"})
	if want := []string{".", "./pkg/b"}; !reflect.DeepEqual(run.Packages, want) {
		t.Errorf("Packages = %v, want %v", run.Packages, want)
	}
	if want := "go test -json -run ^(TestB|TestRoot)$ . ./pkg/b"; run.Command != want {
		t.Errorf("Command = %q, want %q", run.Command, want)
	}
	outcomes := map[string]string{}
	for _, r := range run.Results {
		outcomes[r.Name] = r.Outcome + " " + r.Package
	}
	want := map[string]string{
		"TestB":             "not run ./pkg/b",
		"TestRoot":          "not run .",
		"TestB/case_one":    "not run ./pkg/b",
		"TestFake":          "not found ",
		"AC9-is-not-a-test": "not found ",
	}
	if !reflect.DeepEqual(outcomes, want) {
		t.Errorf("outcomes = %v, want %v", outcomes, want)
	}
}

const goTestJSON = `{"Action":"start","Package":"x/pkg/b"}
{"Action":"run","Package":"x/pkg/b","Test":"TestB"}
{"Action":"run","Package":"x/pkg/b","Test":"TestB/case_one"}
{"Action":"output","Package":"x/pkg/b","Test":"TestB/case_one","Output":"    b_test.go:9: want 2, got 3\n"}
{"Action":"fail","Package":"x/pkg/b","Test":"TestB/case_one"}
{"Action":"output","Package":"x/pkg/b","Test":"TestB","Output":"--- FAIL: TestB (0.00s)\n"}
{"Action":"fail","Package":"x/pkg/b","Test":"TestB"}
{"Action":"run","Package":"x/pkg/b","Test":"TestBee"}
{"Action":"pass","Package":"x/pkg/b","Test":"TestBee"}
{"Action":"fail","Package":"x/pkg/b"}
{"ImportPath":"x/pkg/c [x/pkg/c.test]","Action":"build-output","Output":"pkg/c/c_test.go:3:1: syntax error\n"}
{"Action":"fail","Package":"x/pkg/c"}
{"Action":"run","Package":"x","Test":"TestSkipped"}
{"Action":"skip","Package":"x","Test":"TestSkipped"}
`

func TestReadGoTestJSON(t *testing.T) {
	run := TestRun{Results: []RunResult{
		{Name: "TestBee", Package: "./pkg/b", Outcome: RunNotRun},
		{Name: "TestB/case_one", Package: "./pkg/b", Outcome: RunNotRun},
		{Name: "TestB/case two", Package: "./pkg/b", Outcome: RunNotRun},
		{Name: "TestC", Package: "./pkg/c", Outcome: RunNotRun},
		{Name: "TestSkipped", Package: ".", Outcome: RunNotRun},
		{Name: "TestGone", Outcome: RunNotFound},
	}}
	ReadGoTestJSON(&run, []byte(goTestJSON))
	got := map[string]string{}
	for _, r := range run.Results {
		got[r.Name] = r.Outcome
	}
	want := map[string]string{
		"TestBee":        RunPass,
		"TestB/case_one": RunFail,
		"TestB/case two": RunFail, // no own result: its top-level test's
		"TestC":          RunNotRun,
		"TestSkipped":    RunSkip,
		"TestGone":       RunNotFound,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outcomes = %v, want %v", got, want)
	}
	if out := strings.Join(run.Results[1].Output, "\n"); !strings.Contains(out, "want 2, got 3") {
		t.Errorf("TestB/case_one output = %q, want its failure line", out)
	}
	if out := strings.Join(run.Results[3].Output, "\n"); !strings.Contains(out, "syntax error") {
		t.Errorf("TestC output = %q, want its package's build error", out)
	}
	if run.Passed() {
		t.Error("Passed() = true with failures")
	}
}

func TestTestRunCommentNamesTheHeadCommandAndTests(t *testing.T) {
	run := TestRun{Command: "go test -json -run ^(TestA|TestB)$ ./a", Head: "abc1234", Results: []RunResult{{Name: "TestA", Outcome: RunPass}, {Name: "TestB", Outcome: RunPass}}}
	if !run.Passed() {
		t.Fatal("Passed() = false, want true")
	}
	want := "tests: prove --run at abc1234: go test -json -run ^(TestA|TestB)$ ./a — 2 passed: TestA, TestB"
	if got := run.Comment(); got != want {
		t.Errorf("Comment() = %q, want %q", got, want)
	}
	if (TestRun{}).Passed() {
		t.Error("an empty run passed")
	}
}

func TestDirtyCodeIgnoresNonCodeFiles(t *testing.T) {
	dir := t.TempDir()
	gitAt(t, dir, t0, "init", "-q", "-b", "main")
	commitFile(t, dir, t0, "main.go", "package main\n", "init")
	for name, content := range map[string]string{"docs/a.md": "x", "playtest/sessions/s.yaml": "x", "notes.md": "x"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if dirty, err := DirtyCode(dir); err != nil || len(dirty) != 0 {
		t.Fatalf("DirtyCode with only non-code changes = %v, %v; want none", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, _ := DirtyCode(dir); !reflect.DeepEqual(dirty, []string{"main.go"}) {
		t.Errorf("DirtyCode = %v, want [main.go]", dirty)
	}
}

func TestSupersededNeedsANewerFoundLinkOnEveryCriterion(t *testing.T) {
	tl := func(ref, src, from string) CheckedLink {
		return CheckedLink{Link: Link{Kind: tracker.ProofTest, Ref: ref, Source: src, From: from}}
	}
	found := func(n string) bool { return n == "TestNew" }
	for _, tc := range []struct {
		name     string
		criteria [][]CheckedLink
		want     string
	}{
		{"newer trailer", [][]CheckedLink{{tl("TestNew", SourceTrailer, "bbb"), tl("TestOld", SourceTrailer, "aaa")}}, "TestNew (trailer on bbb)"},
		{"prove link", [][]CheckedLink{{tl("TestOld", SourceTrailer, "aaa"), tl("TestNew", SourceProve, "")}}, "TestNew (prove)"},
		{"older trailer only", [][]CheckedLink{{tl("TestOld", SourceTrailer, "bbb"), tl("TestNew", SourceTrailer, "aaa")}}, ""},
		{"same commit", [][]CheckedLink{{tl("TestNew", SourceTrailer, "aaa"), tl("TestOld", SourceTrailer, "aaa")}}, ""},
		{"prove link to the old name", [][]CheckedLink{{tl("TestNew", SourceTrailer, "bbb"), tl("TestOld", SourceProve, "")}}, ""},
		{"one criterion without a newer link", [][]CheckedLink{
			{tl("TestNew", SourceTrailer, "bbb"), tl("TestOld", SourceTrailer, "aaa")},
			{tl("TestOld", SourceTrailer, "aaa")},
		}, ""},
	} {
		var r Report
		for i, links := range tc.criteria {
			r.Criteria = append(r.Criteria, Criterion{ID: fmt.Sprintf("AC%d", i+1), Links: links})
		}
		got, ok := Superseded(r, "TestOld", found)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: Superseded = %q, %v; want %q", tc.name, got, ok, tc.want)
		}
	}
}

func TestPassedAndCommentWithSupersededNames(t *testing.T) {
	run := TestRun{Command: "go test", Results: []RunResult{{Name: "TestNew", Outcome: RunPass}, {Name: "TestOld", Outcome: RunSuperseded}}}
	if !run.Passed() {
		t.Error("Passed = false, want true: a superseded name doesn't block")
	}
	if got := run.Comment(); got != "tests: prove --run: go test — 1 passed: TestNew; 1 superseded: TestOld" {
		t.Errorf("Comment = %q", got)
	}
	if (TestRun{Results: []RunResult{{Name: "TestOld", Outcome: RunSuperseded}}}).Passed() {
		t.Error("Passed with only superseded names = true, want false")
	}
}
