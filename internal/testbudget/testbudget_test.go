package testbudget

import (
	"reflect"
	"slices"
	"testing"
)

func TestThreads(t *testing.T) {
	for _, c := range []struct{ cores, workers, setting, want int }{
		{14, 4, 0, 3},  // the brief's machine: 14 cores, cap 4
		{14, 2, 0, 3},  // a project cap below the machine's 4 still shares with the others
		{14, 7, 0, 2},  // a larger cap divides further
		{4, 4, 0, Min}, // never below Min
		{14, 4, 6, 6},  // test_threads: wins
	} {
		if got := Threads(c.cores, c.workers, c.setting); got != c.want {
			t.Errorf("Threads(%d, %d, %d) = %d, want %d", c.cores, c.workers, c.setting, got, c.want)
		}
	}
}

func TestEnvSetsEveryRunnerVariable(t *testing.T) {
	got := Env(3, "")
	want := []string{"ISSUE_VIEWER_TEST_THREADS=3", "VITEST_MAX_THREADS=3", "VITEST_MAX_FORKS=3", "VITEST_MAX_WORKERS=3", "VITEST_MIN_THREADS=1", "VITEST_MIN_FORKS=1", "GOFLAGS=-p=3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Env(3) = %q, want %q", got, want)
	}
	if got := Env(3, "-mod=mod"); !slices.Contains(got, "GOFLAGS=-mod=mod -p=3") {
		t.Errorf("inherited GOFLAGS not kept: %q", got)
	}
}

func TestStripRemovesTheBudgetOnly(t *testing.T) {
	env := append([]string{"PATH=/bin", "VITEST_POOL_ID=1"}, Env(3, "-mod=mod")...)
	got := Strip(env)
	want := []string{"PATH=/bin", "VITEST_POOL_ID=1", "GOFLAGS=-mod=mod"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Strip = %q, want %q", got, want)
	}
	if got := Strip(append([]string{"PATH=/bin"}, Env(3, "")...)); !reflect.DeepEqual(got, []string{"PATH=/bin"}) {
		t.Errorf("an empty GOFLAGS should go: %q", got)
	}
	own := []string{"VITEST_MAX_THREADS=8", "GOFLAGS=-p=8"}
	if got := Strip(own); !reflect.DeepEqual(got, own) {
		t.Errorf("no budget set: the owner's own lines must stay, got %q", got)
	}
}
