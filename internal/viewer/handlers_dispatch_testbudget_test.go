package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/testbudget"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// startBudgetAgent starts a stand-in agent for proj (a lead when leadScope is
// set) on a machine with 14 cores and returns the environment it received.
func startBudgetAgent(t *testing.T, proj *tracker.Project, leadScope string) string {
	t.Helper()
	out := installFakeAgents(t)
	unsetTestBudget(t)
	t.Setenv("GOFLAGS", "-mod=mod")
	isolateTmux(t)
	standInAgentRuns(t)
	withPromptDeliveryTimeout(t, 15*time.Second)
	cpu := agentrun.NumCPU
	agentrun.NumCPU = func() int { return 14 }
	t.Cleanup(func() { agentrun.NumCPU = cpu })
	workDir, _ := filepath.EvalSymlinks(t.TempDir())
	proj.Slug, proj.Terminal, proj.WorkDir = "test", "none", workDir
	spec := agentrun.Spec{Session: "agent-x", Prompt: "briefing\n", AgentType: "claude", IssueSlug: "x", WorkDir: workDir, LeadScope: leadScope, Headless: true}
	if leadScope != "" {
		spec.Session, spec.IssueSlug = "lead-test", ""
	}
	if resp := agentrun.Start(proj, spec); resp.Status != "dispatched" {
		t.Fatalf("Status = %q; steps=%+v", resp.Status, resp.Steps)
	}
	return "\n" + waitForFile(t, out+".env")
}

// unsetTestBudget takes the budget of the worker running the tests out of
// the test's environment: the private tmux server, and every pane it starts,
// would inherit it, and the stand-in lead would carry the worker's own budget.
func unsetTestBudget(t *testing.T) {
	t.Helper()
	for _, kv := range testbudget.Env(1, "") {
		k, _, _ := strings.Cut(kv, "=")
		t.Setenv(k, "") // restores the worker's value after the test
		os.Unsetenv(k)
	}
}

func envHas(env, kv string) bool { return strings.Contains(env, "\n"+kv+"\n") }

// A worker on 14 cores under the factory's cap of 4 gets 3 test threads,
// in every variable the projects' runners read; test_threads: wins.
func TestDispatchGivesAWorkerItsTestBudget(t *testing.T) {
	for _, c := range []struct {
		name    string
		setting int
		want    string
	}{{"14 cores, cap 4", 0, "3"}, {"test_threads: 5", 5, "5"}} {
		t.Run(c.name, func(t *testing.T) {
			env := startBudgetAgent(t, &tracker.Project{TestThreads: c.setting}, "")
			for _, kv := range []string{
				"ISSUE_VIEWER_TEST_THREADS=" + c.want, "VITEST_MAX_THREADS=" + c.want, "VITEST_MAX_FORKS=" + c.want,
				"VITEST_MAX_WORKERS=" + c.want, "VITEST_MIN_THREADS=1", "VITEST_MIN_FORKS=1", "GOFLAGS=-mod=mod -p=" + c.want,
			} {
				if !envHas(env, kv) {
					t.Errorf("worker env is missing %q:\n%s", kv, env)
				}
			}
		})
	}
}

// A lead keeps the machine's full parallelism: its env has no budget.
func TestLeadSessionGetsNoTestBudget(t *testing.T) {
	env := startBudgetAgent(t, &tracker.Project{}, "lead")
	if strings.Contains(env, "ISSUE_VIEWER_TEST_THREADS=") || strings.Contains(env, "VITEST_MAX_") || !envHas(env, "GOFLAGS=-mod=mod") {
		t.Errorf("lead env carries a test budget:\n%s", env)
	}
}
