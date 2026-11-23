package slowtier

import (
	"os"
	"regexp"
	"testing"
)

func TestOnReadsTheVariable(t *testing.T) {
	t.Setenv(Var, "")
	if On() {
		t.Error("On with the variable unset")
	}
	t.Setenv(Var, "1")
	if !On() {
		t.Error("not On with the variable at 1")
	}
}

func TestOnlySkipsOutsideTheSlowTier(t *testing.T) {
	t.Setenv(Var, "")
	ran := false
	t.Run("fast", func(t *testing.T) {
		Only(t, "test")
		ran = true
	})
	if ran {
		t.Error("Only let a test run outside the slow tier")
	}
	t.Setenv(Var, "1")
	t.Run("slow", func(t *testing.T) {
		Only(t, "test")
		ran = true
	})
	if !ran {
		t.Error("Only skipped a test in the slow tier")
	}
}

// TestTheSlowTierSetsTheVariable: local CI's slow tier passes SLOW_TESTS=1
// to make test and the Makefile exports Var from it, or every slowtier.Only
// test would skip in both tiers and run nowhere.
func TestTheSlowTierSetsTheVariable(t *testing.T) {
	wf, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\s*- cmd: make test .*\bSLOW_TESTS=1\b.*\n\s*tier: slow$`).Match(wf) {
		t.Error("workflow.yaml's slow tier has no `make test ... SLOW_TESTS=1` command")
	}
	mk, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^ifeq \(\$\(SLOW_TESTS\),1\)\nexport ` + Var + ` := 1\nendif$`).Match(mk) {
		t.Errorf("the Makefile does not export %s=1 under SLOW_TESTS=1", Var)
	}
	if regexp.MustCompile(`(?m)^SLOW_TESTS \?= 1`).Match(mk) {
		t.Error("SLOW_TESTS defaults to 1: make validate would run the slow tier's tests")
	}
}
