// Package testbudget gives a worker session its share of the machine's cores
// for test runs: vitest and go test start one worker per core, so three
// suites running at once in three worktrees asked for three machines
// (2026-11-22: load 77-90 on 14 cores). A kernel leaf: agentrun sets the
// budget on a worker's environment, local CI strips it from its commands.
package testbudget

import (
	"strconv"
	"strings"
)

// Var is the budget itself, for a project's own config to read (a vitest
// config that computes its pool size, jest's --maxWorkers).
const Var = "ISSUE_VIEWER_TEST_THREADS"

// MachineWorkers is the factory's live workers across all projects on one
// machine (~/CLAUDE.md: up to 4): workers of different projects share the
// same cores, so the divisor never goes below it.
const MachineWorkers = 4

// Min is the smallest budget: one thread per worker would serialise a suite.
const Min = 2

// runnerVars are the variables Env sets from the budget besides Var and
// GOFLAGS. vitest 2.x reads the THREADS and FORKS pairs (its default pool is
// forks; the env overrides the config's poolOptions max); newer vitest reads
// VITEST_MAX_WORKERS. The minimums go to 1 so a config minimum above the new
// maximum can't make the pool refuse to start.
var runnerVars = []string{"VITEST_MAX_THREADS", "VITEST_MAX_FORKS", "VITEST_MAX_WORKERS"}
var minVars = []string{"VITEST_MIN_THREADS", "VITEST_MIN_FORKS"}

// Threads is a worker's budget: setting (projects.yaml test_threads:) when
// above 0, else cores divided by the larger of workers and MachineWorkers,
// never below Min.
func Threads(cores, workers, setting int) int {
	if setting > 0 {
		return setting
	}
	if workers < MachineWorkers {
		workers = MachineWorkers
	}
	if n := cores / workers; n > Min {
		return n
	}
	return Min
}

// Env is the environment a worker with budget n gets. goflags is the GOFLAGS
// the session would inherit: kept, with -p=n after it.
func Env(n int, goflags string) []string {
	v := strconv.Itoa(n)
	env := []string{Var + "=" + v}
	for _, k := range runnerVars {
		env = append(env, k+"="+v)
	}
	for _, k := range minVars {
		env = append(env, k+"=1")
	}
	return append(env, "GOFLAGS="+strings.TrimSpace(goflags+" "+pFlag(v)))
}

func pFlag(n string) string { return "-p=" + n }

// Strip takes a worker's budget out of env, for a run that keeps the
// machine's full parallelism (local CI): every variable Env set, and the -p=
// it added to GOFLAGS (a GOFLAGS left empty goes). An env without Var is
// returned as it is: its vitest or GOFLAGS lines are the owner's own.
func Strip(env []string) []string {
	n := ""
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == Var {
			n = v
		}
	}
	if n == "" {
		return env
	}
	drop := map[string]bool{Var: true}
	for _, k := range append(append([]string{}, runnerVars...), minVars...) {
		drop[k] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if drop[k] {
			continue
		}
		if k == "GOFLAGS" {
			var keep []string
			for _, f := range strings.Fields(v) {
				if f != pFlag(n) {
					keep = append(keep, f)
				}
			}
			if len(keep) == 0 {
				continue
			}
			kv = "GOFLAGS=" + strings.Join(keep, " ")
		}
		out = append(out, kv)
	}
	return out
}
