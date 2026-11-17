package proof

import (
	"reflect"
	"testing"
)

// rustRepo is a Cargo workspace whose tests live inline in src/*.rs (a
// #[cfg(test)] mod tests), beside a src file that only mentions a name.
func rustRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitAt(t, dir, t0, "init", "-q", "-b", "main")
	commitFile(t, dir, t0, "packages/solver/src/main.rs",
		"fn plan() {}\n\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn rotation_keeps_two_of_a_kind_a_week() {}\n}\n", "solver")
	commitFile(t, dir, t0, "packages/solver/src/lib.rs",
		"// see not_a_test_just_a_word\npub fn not_a_test_just_a_word() {}\n", "lib")
	commitFile(t, dir, t0, "packages/solver/src/attr.rs",
		"#[tokio::test]\nasync fn async_inline_test() {}\n", "attr")
	return dir
}

func TestProofFindsRustInlineTests(t *testing.T) {
	dir := rustRepo(t)
	c := &checker{root: dir, ref: "main", tests: map[string]Check{}}
	names := []string{"rotation_keeps_two_of_a_kind_a_week", "async_inline_test", "not_a_test_just_a_word"}
	c.findTests(names)
	for _, n := range names[:2] {
		if !c.tests[n].OK {
			t.Errorf("%s = %+v, want found in a src/*.rs file with inline tests", n, c.tests[n])
		}
	}
	if c.tests["not_a_test_just_a_word"].OK {
		t.Errorf("not_a_test_just_a_word found, want not found: lib.rs holds no #[test]")
	}
}

func TestPlanSuiteRunFindsRustInlineTests(t *testing.T) {
	dir := rustRepo(t)
	run := PlanSuiteRun(dir, []string{"rotation_keeps_two_of_a_kind_a_week", "not_a_test_just_a_word"}, "cargo test")
	if want := []string{"packages/solver/src/main.rs"}; !reflect.DeepEqual(run.Packages, want) {
		t.Errorf("Packages = %v, want %v", run.Packages, want)
	}
	if got := run.Results[1].Outcome; got != RunNotFound {
		t.Errorf("not_a_test_just_a_word = %q, want %q", got, RunNotFound)
	}
}
